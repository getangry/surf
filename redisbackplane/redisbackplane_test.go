package redisbackplane

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	surf "github.com/getangry/surf"
)

// fakeStore is an in-memory stand-in for Redis honoring the subset of semantics
// the Backplane relies on: TTL expiry, SET-NX acquire, INCR fencing, and
// owner-checked renew/release.
type fakeStore struct {
	mu      sync.Mutex
	entries map[string]fakeEntry
	fences  map[string]uint64
	closed  bool
}

type fakeEntry struct {
	val      []byte
	expireAt time.Time // zero = no expiry
}

func newFakeStore() *fakeStore {
	return &fakeStore{entries: map[string]fakeEntry{}, fences: map[string]uint64{}}
}

func (f *fakeStore) live(key string) (fakeEntry, bool) {
	e, ok := f.entries[key]
	if !ok {
		return fakeEntry{}, false
	}
	if !e.expireAt.IsZero() && !time.Now().Before(e.expireAt) {
		delete(f.entries, key)
		return fakeEntry{}, false
	}
	return e, true
}

func (f *fakeStore) get(ctx context.Context, key string) ([]byte, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.live(key)
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), e.val...), true, nil
}

func (f *fakeStore) set(ctx context.Context, key string, val []byte, ttlMs int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := fakeEntry{val: append([]byte(nil), val...)}
	if ttlMs > 0 {
		e.expireAt = time.Now().Add(time.Duration(ttlMs) * time.Millisecond)
	}
	f.entries[key] = e
	return nil
}

func (f *fakeStore) del(ctx context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.entries, key)
	return nil
}

func (f *fakeStore) acquire(ctx context.Context, lockKey, fenceKey, holder string, ttlMs int64) (uint64, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, held := f.live(lockKey); held {
		return 0, false, nil
	}
	e := fakeEntry{val: []byte(holder)}
	if ttlMs > 0 {
		e.expireAt = time.Now().Add(time.Duration(ttlMs) * time.Millisecond)
	}
	f.entries[lockKey] = e
	f.fences[fenceKey]++
	return f.fences[fenceKey], true, nil
}

func (f *fakeStore) renew(ctx context.Context, lockKey, holder string, ttlMs int64) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.live(lockKey)
	if !ok || string(e.val) != holder {
		return false, nil
	}
	if ttlMs > 0 {
		e.expireAt = time.Now().Add(time.Duration(ttlMs) * time.Millisecond)
	}
	f.entries[lockKey] = e
	return true, nil
}

func (f *fakeStore) release(ctx context.Context, lockKey, holder string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.live(lockKey); ok && string(e.val) == holder {
		delete(f.entries, lockKey)
	}
	return nil
}

func (f *fakeStore) close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func newFakeBackplane(t *testing.T, secret []byte) (*Backplane, *fakeStore) {
	t.Helper()
	fs := newFakeStore()
	bp, err := newWithStore(fs, "surf:", secret)
	if err != nil {
		t.Fatal(err)
	}
	return bp, fs
}

func TestRedis_KVRoundTrip(t *testing.T) {
	ctx := context.Background()
	bp, _ := newFakeBackplane(t, nil)

	if _, ok, _ := bp.Get(ctx, "k"); ok {
		t.Fatal("expected miss")
	}
	if err := bp.Set(ctx, "k", []byte("v"), 0); err != nil {
		t.Fatal(err)
	}
	v, ok, err := bp.Get(ctx, "k")
	if err != nil || !ok || string(v) != "v" {
		t.Fatalf("Get = %q ok=%v err=%v", v, ok, err)
	}
	if err := bp.Delete(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := bp.Get(ctx, "k"); ok {
		t.Fatal("present after delete")
	}
}

func TestRedis_TTLExpires(t *testing.T) {
	ctx := context.Background()
	bp, _ := newFakeBackplane(t, nil)
	_ = bp.Set(ctx, "k", []byte("v"), 30*time.Millisecond)
	if _, ok, _ := bp.Get(ctx, "k"); !ok {
		t.Fatal("missing before expiry")
	}
	time.Sleep(60 * time.Millisecond)
	if _, ok, _ := bp.Get(ctx, "k"); ok {
		t.Fatal("present after expiry")
	}
}

func TestRedis_Encryption(t *testing.T) {
	ctx := context.Background()
	bp, fs := newFakeBackplane(t, []byte("kv-secret"))
	plain := []byte("PLAINTEXT-SESSION")
	if err := bp.Set(ctx, "sess", plain, 0); err != nil {
		t.Fatal(err)
	}
	// Raw stored bytes must not contain the plaintext.
	fs.mu.Lock()
	raw := fs.entries["surf:kv:sess"].val
	fs.mu.Unlock()
	if len(raw) == 0 {
		t.Fatal("nothing stored under namespaced key")
	}
	if bytes.Contains(raw, plain) {
		t.Fatal("plaintext stored in Redis")
	}
	// But Get round-trips.
	got, ok, err := bp.Get(ctx, "sess")
	if err != nil || !ok || !bytes.Equal(got, plain) {
		t.Fatalf("Get = %q ok=%v err=%v", got, ok, err)
	}
}

func TestRedis_KeyNamespacing(t *testing.T) {
	ctx := context.Background()
	bp, fs := newFakeBackplane(t, nil)
	_ = bp.Set(ctx, "x", []byte("1"), 0)
	l, _, _ := bp.TryLease(ctx, "x", time.Second)
	_ = l
	fs.mu.Lock()
	_, kvOK := fs.entries["surf:kv:x"]
	_, lockOK := fs.entries["surf:lock:x"]
	fenceVal := fs.fences["surf:fence:x"]
	fs.mu.Unlock()
	if !kvOK || !lockOK {
		t.Fatalf("expected distinct kv and lock keys: kv=%v lock=%v", kvOK, lockOK)
	}
	if fenceVal == 0 {
		t.Fatal("fence counter not incremented")
	}
}

func TestRedis_LeaseExclusionAndFencing(t *testing.T) {
	ctx := context.Background()
	bp, _ := newFakeBackplane(t, nil)

	l1, ok, err := bp.TryLease(ctx, "res", time.Second)
	if err != nil || !ok {
		t.Fatalf("first acquire ok=%v err=%v", ok, err)
	}
	// Held: second acquire fails.
	if _, ok, _ := bp.TryLease(ctx, "res", time.Second); ok {
		t.Fatal("acquired while held")
	}
	t1 := l1.Token()
	if err := l1.Release(ctx); err != nil {
		t.Fatal(err)
	}
	// Re-acquire: fencing token strictly increases (reliable, unlike gossip).
	l2, ok, _ := bp.TryLease(ctx, "res", time.Second)
	if !ok {
		t.Fatal("re-acquire failed")
	}
	if l2.Token() <= t1 {
		t.Fatalf("fencing token not monotonic: %d then %d", t1, l2.Token())
	}
	_ = l2.Release(ctx)
}

func TestRedis_RenewAndLoss(t *testing.T) {
	ctx := context.Background()
	bp, _ := newFakeBackplane(t, nil)
	l, _, _ := bp.TryLease(ctx, "r", 40*time.Millisecond)
	if err := l.Renew(ctx, 40*time.Millisecond); err != nil {
		t.Fatalf("renew: %v", err)
	}
	time.Sleep(80 * time.Millisecond) // lease lapses
	// Another holder takes it.
	l2, ok, _ := bp.TryLease(ctx, "r", time.Second)
	if !ok {
		t.Fatal("could not reacquire after lapse")
	}
	if err := l.Renew(ctx, time.Second); err != surf.ErrLeaseLost {
		t.Fatalf("renew after loss = %v, want ErrLeaseLost", err)
	}
	_ = l2.Release(ctx)
}

func TestRedis_ImplementsBackplane(t *testing.T) {
	var _ surf.Backplane = (*Backplane)(nil)
	bp, err := New("localhost:6379")
	if err != nil {
		t.Fatal(err)
	}
	_ = bp.Close()
	if _, err := New(""); err == nil {
		t.Fatal("expected error for empty address")
	}
}
