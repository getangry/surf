package redisbackplane

import (
	"context"
	"os"
	"testing"
	"time"
)

// These tests run only when SURF_TEST_REDIS_ADDR points at a real Redis (e.g.
// "localhost:6379"). They validate the hand-rolled RESP client and the Lua
// scripts against an actual server:
//
//	SURF_TEST_REDIS_ADDR=localhost:6379 go test ./redisbackplane/ -run Integration -v
func integrationBackplane(t *testing.T) *Backplane {
	t.Helper()
	addr := os.Getenv("SURF_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set SURF_TEST_REDIS_ADDR to run Redis integration tests")
	}
	// Unique prefix per run so repeated runs don't collide.
	bp, err := New(addr, WithKeyPrefix("surftest:"+randomHex(4)+":"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bp.Close() })
	return bp
}

func TestIntegration_KV(t *testing.T) {
	bp := integrationBackplane(t)
	ctx := context.Background()

	if err := bp.Set(ctx, "k", []byte("hello"), 0); err != nil {
		t.Fatal(err)
	}
	v, ok, err := bp.Get(ctx, "k")
	if err != nil || !ok || string(v) != "hello" {
		t.Fatalf("Get = %q ok=%v err=%v", v, ok, err)
	}
	if err := bp.Delete(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := bp.Get(ctx, "k"); ok {
		t.Fatal("present after delete")
	}

	// TTL.
	_ = bp.Set(ctx, "tk", []byte("v"), 200*time.Millisecond)
	if _, ok, _ := bp.Get(ctx, "tk"); !ok {
		t.Fatal("missing before expiry")
	}
	time.Sleep(400 * time.Millisecond)
	if _, ok, _ := bp.Get(ctx, "tk"); ok {
		t.Fatal("present after TTL expiry")
	}
}

func TestIntegration_LeaseAndFencing(t *testing.T) {
	bp := integrationBackplane(t)
	ctx := context.Background()

	l1, ok, err := bp.TryLease(ctx, "res", 5*time.Second)
	if err != nil || !ok {
		t.Fatalf("acquire ok=%v err=%v", ok, err)
	}
	if _, ok, _ := bp.TryLease(ctx, "res", time.Second); ok {
		t.Fatal("acquired while held (real Redis SET NX failed to exclude)")
	}
	t1 := l1.Token()
	if err := l1.Renew(ctx, 5*time.Second); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if err := l1.Release(ctx); err != nil {
		t.Fatalf("release: %v", err)
	}
	l2, ok, err := bp.TryLease(ctx, "res", 5*time.Second)
	if err != nil || !ok {
		t.Fatalf("reacquire ok=%v err=%v", ok, err)
	}
	if l2.Token() <= t1 {
		t.Fatalf("Redis INCR fencing token not monotonic: %d then %d", t1, l2.Token())
	}
	_ = l2.Release(ctx)
}

func TestIntegration_EncryptedValues(t *testing.T) {
	addr := os.Getenv("SURF_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set SURF_TEST_REDIS_ADDR to run Redis integration tests")
	}
	prefix := "surftest:" + randomHex(4) + ":"
	enc, err := New(addr, WithKeyPrefix(prefix), WithEncryption([]byte("integration-secret")))
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	ctx := context.Background()
	plain := []byte("secret-session-value")
	if err := enc.Set(ctx, "s", plain, time.Minute); err != nil {
		t.Fatal(err)
	}

	// Read the raw bytes with a non-encrypting client: must be ciphertext.
	raw, err := New(addr, WithKeyPrefix(prefix))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	got, ok, err := raw.Get(ctx, "s")
	if err != nil || !ok {
		t.Fatalf("raw Get ok=%v err=%v", ok, err)
	}
	if string(got) == string(plain) {
		t.Fatal("value stored in plaintext on the server")
	}
	// Encrypting client round-trips.
	back, ok, err := enc.Get(ctx, "s")
	if err != nil || !ok || string(back) != string(plain) {
		t.Fatalf("decrypt round-trip = %q ok=%v err=%v", back, ok, err)
	}
	_ = enc.Delete(ctx, "s")
}
