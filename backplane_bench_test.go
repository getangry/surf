package surf

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// --- Local backplane (the default; only touched when you call it) ----------

func BenchmarkLocalKV(b *testing.B) {
	ctx := context.Background()
	val := []byte("a-typical-session-value-of-modest-size")

	b.Run("Set", func(b *testing.B) {
		bp := NewLocal()
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = bp.Set(ctx, "k", val, time.Minute)
		}
	})

	b.Run("Get", func(b *testing.B) {
		bp := NewLocal()
		_ = bp.Set(ctx, "k", val, time.Minute)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _, _ = bp.Get(ctx, "k")
		}
	})

	b.Run("GetParallel", func(b *testing.B) {
		bp := NewLocal()
		_ = bp.Set(ctx, "k", val, time.Minute)
		b.ReportAllocs()
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_, _, _ = bp.Get(ctx, "k")
			}
		})
	})
}

func BenchmarkLocalLease(b *testing.B) {
	ctx := context.Background()
	bp := NewLocal()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l, _, _ := bp.TryLease(ctx, "k", time.Second)
		_ = l.Release(ctx)
	}
}

// --- Crypto (the cost the cluster backend pays; zero for Local) ------------

func BenchmarkCryptoAtRest(b *testing.B) {
	kr := mustKeyringB(b)
	val := []byte("a-typical-session-value-of-modest-size")
	b.Run("Seal", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = kr.sealAtRest(val)
		}
	})
	b.Run("Open", func(b *testing.B) {
		blob, _ := kr.sealAtRest(val)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _ = kr.openAtRest(blob)
		}
	})
}

func BenchmarkCryptoHandshake(b *testing.B) {
	kr := mustKeyringB(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c, s := newBufferedConnPair("c", "s")
		done := make(chan struct{})
		go func() {
			_, _ = serverHandshake(s, kr, "server")
			close(done)
		}()
		_, _ = clientHandshake(c, kr, "client")
		<-done
		c.Close()
		s.Close()
	}
}

func BenchmarkCryptoFrameRoundTrip(b *testing.B) {
	kr := mustKeyringB(b)
	c, s := newBufferedConnPair("c", "s")
	defer c.Close()
	defer s.Close()
	done := make(chan *secureConn, 1)
	go func() {
		sc, _ := serverHandshake(s, kr, "server")
		done <- sc
	}()
	client, _ := clientHandshake(c, kr, "client")
	server := <-done

	msg := []byte("a-typical-session-value-of-modest-size")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		errc := make(chan error, 1)
		go func() { errc <- client.WriteMsg(msg) }()
		_, _ = server.ReadMsg()
		<-errc
	}
}

// --- Per-request storage: reqState path vs global fallback -----------------

func BenchmarkRequestStorageReqState(b *testing.B) {
	// A request carrying a reqState, as it would after passing through
	// ServeHTTP. This is the path surf-handled requests take.
	app := NewApp()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	st := newReqState(app, req.Context())
	req = req.WithContext(st)

	b.Run("Store", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			Store(req, "key", "value")
		}
	})
	b.Run("Get", func(b *testing.B) {
		Store(req, "key", "value")
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			Get(req, "key")
		}
	})
}

// --- Single-node cluster (shows the seal + bookkeeping cost; replication to
// peers is async and off the caller's path) --------------------------------

func benchClusterNode(b *testing.B) *clusterBackplane {
	b.Helper()
	netw := newMemNetwork()
	bp := newClusterBackplaneWithKeyring(netw.transportFor("solo:1"),
		[]byte("bench-secret"), StaticPeers("solo:1"),
		WithClusterAdvertiseAddr("solo:1"), WithClusterNodeID("solo"))
	bp.SetLogger(quietLogger())
	if err := bp.start(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = bp.Close() })
	return bp
}

func BenchmarkClusterKV(b *testing.B) {
	ctx := context.Background()
	val := []byte("a-typical-session-value-of-modest-size")
	bp := benchClusterNode(b)

	b.Run("Set", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = bp.Set(ctx, "k", val, time.Minute)
		}
	})
	b.Run("Get", func(b *testing.B) {
		_ = bp.Set(ctx, "k", val, time.Minute)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, _, _ = bp.Get(ctx, "k")
		}
	})
}

func BenchmarkClusterLeaseSelfArbiter(b *testing.B) {
	ctx := context.Background()
	bp := benchClusterNode(b) // single node => always its own arbiter, no RPC
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l, ok, _ := bp.TryLease(ctx, "k", time.Second)
		if ok {
			_ = l.Release(ctx)
		}
	}
}

func mustKeyringB(b *testing.B) *keyring {
	b.Helper()
	kr, err := newKeyring(epochSecret{Epoch: 0, Secret: []byte("bench-secret")})
	if err != nil {
		b.Fatal(err)
	}
	return kr
}
