package surf

import (
	"context"
	"sync"
	"testing"
	"time"
)

// backplaneConformance exercises the Backplane contract. Every backend
// (Local and the clustered backend) must pass it. Backends with eventual
// consistency should run it against a single node, where reads-after-writes
// are immediate.
func backplaneConformance(t *testing.T, newBP func(t *testing.T) Backplane) {
	t.Helper()
	ctx := context.Background()

	t.Run("get-missing", func(t *testing.T) {
		bp := newBP(t)
		_, ok, err := bp.Get(ctx, "nope")
		if err != nil {
			t.Fatalf("Get error: %v", err)
		}
		if ok {
			t.Fatal("expected missing key to report ok=false")
		}
	})

	t.Run("set-get-delete", func(t *testing.T) {
		bp := newBP(t)
		if err := bp.Set(ctx, "k", []byte("v"), 0); err != nil {
			t.Fatalf("Set: %v", err)
		}
		got, ok, err := bp.Get(ctx, "k")
		if err != nil || !ok {
			t.Fatalf("Get: ok=%v err=%v", ok, err)
		}
		if string(got) != "v" {
			t.Fatalf("Get = %q, want %q", got, "v")
		}
		if err := bp.Delete(ctx, "k"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, ok, _ := bp.Get(ctx, "k"); ok {
			t.Fatal("key present after Delete")
		}
		// Deleting a missing key is not an error.
		if err := bp.Delete(ctx, "k"); err != nil {
			t.Fatalf("Delete missing: %v", err)
		}
	})

	t.Run("set-overwrites", func(t *testing.T) {
		bp := newBP(t)
		_ = bp.Set(ctx, "k", []byte("one"), 0)
		_ = bp.Set(ctx, "k", []byte("two"), 0)
		got, _, _ := bp.Get(ctx, "k")
		if string(got) != "two" {
			t.Fatalf("Get = %q, want %q", got, "two")
		}
	})

	t.Run("ttl-expires", func(t *testing.T) {
		bp := newBP(t)
		if err := bp.Set(ctx, "k", []byte("v"), 30*time.Millisecond); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if _, ok, _ := bp.Get(ctx, "k"); !ok {
			t.Fatal("key missing before expiry")
		}
		time.Sleep(60 * time.Millisecond)
		if _, ok, _ := bp.Get(ctx, "k"); ok {
			t.Fatal("key present after expiry")
		}
	})

	t.Run("get-returns-copy", func(t *testing.T) {
		bp := newBP(t)
		_ = bp.Set(ctx, "k", []byte("abc"), 0)
		got, _, _ := bp.Get(ctx, "k")
		got[0] = 'X' // mutate caller's copy
		again, _, _ := bp.Get(ctx, "k")
		if string(again) != "abc" {
			t.Fatalf("stored value mutated through returned slice: %q", again)
		}
	})

	t.Run("lock-mutual-exclusion", func(t *testing.T) {
		bp := newBP(t)
		l, err := bp.Lease(ctx, "x", time.Second)
		if err != nil {
			t.Fatalf("Lock: %v", err)
		}
		// Second TryLock must fail while held.
		if _, ok, _ := bp.TryLease(ctx, "x", time.Second); ok {
			t.Fatal("TryLock succeeded while lock held")
		}
		if err := l.Release(ctx); err != nil {
			t.Fatalf("Unlock: %v", err)
		}
		// After unlock it is acquirable again.
		l2, ok, err := bp.TryLease(ctx, "x", time.Second)
		if err != nil || !ok {
			t.Fatalf("TryLock after unlock: ok=%v err=%v", ok, err)
		}
		_ = l2.Release(ctx)
	})

	t.Run("lock-blocks-until-released", func(t *testing.T) {
		bp := newBP(t)
		l, _ := bp.Lease(ctx, "y", time.Second)
		acquired := make(chan Lease, 1)
		go func() {
			l2, err := bp.Lease(ctx, "y", time.Second)
			if err == nil {
				acquired <- l2
			}
		}()
		select {
		case <-acquired:
			t.Fatal("second Lock returned while first still held")
		case <-time.After(40 * time.Millisecond):
		}
		_ = l.Release(ctx)
		select {
		case l2 := <-acquired:
			_ = l2.Release(ctx)
		case <-time.After(time.Second):
			t.Fatal("second Lock did not acquire after release")
		}
	})

	t.Run("lock-fencing-token-monotonic", func(t *testing.T) {
		bp := newBP(t)
		l1, _ := bp.Lease(ctx, "z", time.Second)
		t1 := l1.Token()
		_ = l1.Release(ctx)
		l2, _ := bp.Lease(ctx, "z", time.Second)
		t2 := l2.Token()
		_ = l2.Release(ctx)
		if !(t2 > t1) {
			t.Fatalf("fencing token not monotonic: t1=%d t2=%d", t1, t2)
		}
	})

	t.Run("lock-lease-expires", func(t *testing.T) {
		bp := newBP(t)
		l, err := bp.Lease(ctx, "w", 30*time.Millisecond)
		if err != nil {
			t.Fatalf("Lock: %v", err)
		}
		_ = l
		time.Sleep(60 * time.Millisecond)
		// Lease elapsed without refresh: lock should be acquirable.
		l2, ok, err := bp.TryLease(ctx, "w", time.Second)
		if err != nil || !ok {
			t.Fatalf("TryLock after lease expiry: ok=%v err=%v", ok, err)
		}
		_ = l2.Release(ctx)
	})

	t.Run("refresh-extends-lease", func(t *testing.T) {
		bp := newBP(t)
		l, _ := bp.Lease(ctx, "r", 50*time.Millisecond)
		for i := 0; i < 3; i++ {
			time.Sleep(25 * time.Millisecond)
			if err := l.Renew(ctx, 50*time.Millisecond); err != nil {
				t.Fatalf("Refresh: %v", err)
			}
		}
		// Still held: a TryLock must fail.
		if _, ok, _ := bp.TryLease(ctx, "r", time.Second); ok {
			t.Fatal("lock acquirable despite active refresh")
		}
		_ = l.Release(ctx)
	})

	t.Run("refresh-after-loss-errors", func(t *testing.T) {
		bp := newBP(t)
		l, _ := bp.Lease(ctx, "rl", 20*time.Millisecond)
		time.Sleep(50 * time.Millisecond)
		// Someone else takes the lock after the lease elapses.
		l2, ok, _ := bp.TryLease(ctx, "rl", time.Second)
		if !ok {
			t.Fatal("could not reacquire after lease expiry")
		}
		if err := l.Renew(ctx, time.Second); err != ErrLeaseLost {
			t.Fatalf("Refresh after loss = %v, want ErrLeaseLost", err)
		}
		_ = l2.Release(ctx)
	})

	t.Run("ctx-cancel-aborts-lock", func(t *testing.T) {
		bp := newBP(t)
		l, _ := bp.Lease(ctx, "c", time.Second)
		cctx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			_, err := bp.Lease(cctx, "c", time.Second)
			done <- err
		}()
		time.Sleep(20 * time.Millisecond)
		cancel()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("expected error from cancelled Lock")
			}
		case <-time.After(time.Second):
			t.Fatal("cancelled Lock did not return")
		}
		_ = l.Release(ctx)
	})
}

func TestLocalBackplane_Conformance(t *testing.T) {
	backplaneConformance(t, func(t *testing.T) Backplane {
		bp := NewLocal()
		t.Cleanup(func() { _ = bp.Close() })
		return bp
	})
}

func TestLocalBackplane_ClosedErrors(t *testing.T) {
	ctx := context.Background()
	bp := NewLocal()
	_ = bp.Close()
	if err := bp.Set(ctx, "k", []byte("v"), 0); err != ErrBackplaneClosed {
		t.Fatalf("Set after close = %v, want ErrBackplaneClosed", err)
	}
	if _, _, err := bp.Get(ctx, "k"); err != ErrBackplaneClosed {
		t.Fatalf("Get after close = %v, want ErrBackplaneClosed", err)
	}
	// Close is idempotent.
	if err := bp.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestLocalBackplane_ConcurrentLock(t *testing.T) {
	ctx := context.Background()
	bp := NewLocal()
	defer bp.Close()

	const goroutines = 20
	var counter int
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			l, err := bp.Lease(ctx, "counter", time.Second)
			if err != nil {
				t.Errorf("Lock: %v", err)
				return
			}
			// Critical section: non-atomic increment is safe only under
			// real mutual exclusion.
			counter++
			_ = l.Release(ctx)
		}()
	}
	wg.Wait()
	if counter != goroutines {
		t.Fatalf("counter = %d, want %d (lost updates => no mutual exclusion)", counter, goroutines)
	}
}

func TestKV_TypedRoundTrip(t *testing.T) {
	ctx := context.Background()
	bp := NewLocal()
	defer bp.Close()

	type session struct {
		UserID string
		Roles  []string
	}
	kv := NewKV[session](bp, "sess:")

	want := session{UserID: "u1", Roles: []string{"admin", "user"}}
	if err := kv.Set(ctx, "abc", want, time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, ok, err := kv.Get(ctx, "abc")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.UserID != want.UserID || len(got.Roles) != 2 {
		t.Fatalf("Get = %+v, want %+v", got, want)
	}
	// Prefix isolation: the raw key is namespaced.
	if raw, ok, _ := bp.Get(ctx, "sess:abc"); !ok || len(raw) == 0 {
		t.Fatal("expected value under namespaced key sess:abc")
	}
	if _, ok, _ := bp.Get(ctx, "abc"); ok {
		t.Fatal("value leaked outside prefix")
	}

	if err := kv.Delete(ctx, "abc"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok, _ := kv.Get(ctx, "abc"); ok {
		t.Fatal("present after Delete")
	}
}

func TestApp_BackplaneDefaultsToLocal(t *testing.T) {
	app := NewApp()
	if app.Backplane() == nil {
		t.Fatal("Backplane() is nil; expected default Local")
	}
	if _, ok := app.Backplane().(*Local); !ok {
		t.Fatalf("default backplane = %T, want *Local", app.Backplane())
	}
}

func TestApp_WithBackplane(t *testing.T) {
	custom := NewLocal()
	app := NewApp(WithBackplane(custom))
	if app.Backplane() != custom {
		t.Fatal("WithBackplane not honored")
	}
}
