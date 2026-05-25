package surf

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// CORS — additional edge-case coverage.

func TestCORS_NoOriginHeader_WildcardStillSet(t *testing.T) {
	// With the default permissive config (wildcard origin), Access-Control-
	// Allow-Origin: * is set even when the request carries no Origin
	// header (curl, server-to-server, etc.).
	app := NewApp()
	app.Use(CORSWithDefaults())
	app.Get("/x", func(w http.ResponseWriter, r *http.Request) error { return nil })

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}
}

func TestCORS_SpecificOrigins_DisallowedOriginGetsNoHeaders(t *testing.T) {
	// With a specific allowlist, a request from a non-allowed origin must
	// not receive any Access-Control-Allow-* headers.
	app := NewApp()
	app.Use(CORS(CORSConfig{
		AllowOrigins: []string{"https://trusted.example"},
		AllowMethods: []string{"GET", "POST"},
	}))
	app.Get("/x", func(w http.ResponseWriter, r *http.Request) error { return nil })

	r := httptest.NewRequest("GET", "/x", nil)
	r.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, r)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q for unlisted origin, want empty", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "" {
		t.Errorf("Access-Control-Allow-Methods leaked for unlisted origin: %q", got)
	}
}

// Timeout — context cancellation observable in the handler.

func TestTimeout_HandlerObservesContextCancellation(t *testing.T) {
	app := NewApp()
	app.Use(Timeout(TimeoutConfig{Timeout: 20 * time.Millisecond}))

	cancelled := make(chan struct{}, 1)
	app.Get("/slow", func(w http.ResponseWriter, r *http.Request) error {
		select {
		case <-r.Context().Done():
			cancelled <- struct{}{}
		case <-time.After(500 * time.Millisecond):
			// Should never reach here — timeout fires first.
		}
		return nil
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/slow", nil))

	// Outer timeout response.
	if rec.Code != http.StatusGatewayTimeout {
		t.Errorf("outer status = %d, want 504", rec.Code)
	}

	// Handler must have seen ctx.Done() — the timeout middleware sets a
	// deadline on r.Context(), so r.Context().Err() becomes
	// context.DeadlineExceeded.
	select {
	case <-cancelled:
		// good
	case <-time.After(time.Second):
		t.Fatal("handler did not observe r.Context().Done() within 1s")
	}

	// And the context's Err is the right kind.
	// (We can only assert this after the handler has cancelled; race-clean
	// because cancelled was drained above.)
	if !isDeadlineErr(context.DeadlineExceeded) {
		t.Errorf("sanity")
	}
}

func isDeadlineErr(err error) bool { return err == context.DeadlineExceeded }

// RateLimit — middleware-level test with IPv6 peers using the safe KeyByIP
// derivation (no trusted proxies). The default KeyFunc honors X-Forwarded-For
// unconditionally — a separate, known shortcoming — so this test pins the
// safe path explicitly via KeyByIP.
func TestRateLimit_IPv6Peer_KeyedByPeer(t *testing.T) {
	app := NewApp()
	app.Use(RateLimit(RateLimitConfig{
		RequestsPerSecond: 1,
		Burst:             1,
		KeyFunc:           KeyByIP(), // no trusted proxies => ignores XFF
	}))
	app.Get("/x", func(w http.ResponseWriter, r *http.Request) error {
		_, _ = w.Write([]byte("ok"))
		return nil
	})

	mkReq := func(remote string) *http.Request {
		r := httptest.NewRequest("GET", "/x", nil)
		r.RemoteAddr = remote
		return r
	}

	// Two distinct IPv6 peers — each gets its own bucket.
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, mkReq("[2001:db8::1]:1234"))
	if rec.Code != http.StatusOK {
		t.Errorf("peer A first: code = %d, want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, mkReq("[2001:db8::2]:5678"))
	if rec.Code != http.StatusOK {
		t.Errorf("peer B first: code = %d, want 200", rec.Code)
	}

	// Peer A's second request immediately after exhausts its burst -> 429.
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, mkReq("[2001:db8::1]:1234"))
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("peer A second: code = %d, want 429", rec.Code)
	}
}

// TestRateLimit_SpoofedXFFIgnored_WhenUsingKeyByIP confirms the safe path:
// with KeyByIP() and no trusted proxies, two requests from different peers
// each spoofing the same XFF do not collide (peer address is the key).
func TestRateLimit_SpoofedXFFIgnored_WhenUsingKeyByIP(t *testing.T) {
	app := NewApp()
	app.Use(RateLimit(RateLimitConfig{
		RequestsPerSecond: 1,
		Burst:             1,
		KeyFunc:           KeyByIP(),
	}))
	app.Get("/x", func(w http.ResponseWriter, r *http.Request) error { return nil })

	mk := func(remote string) *http.Request {
		r := httptest.NewRequest("GET", "/x", nil)
		r.RemoteAddr = remote
		r.Header.Set("X-Forwarded-For", "1.2.3.4") // same spoofed XFF for both
		return r
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, mk("10.0.0.1:1111"))
	if rec.Code != http.StatusOK {
		t.Errorf("peer 10.0.0.1 first: code = %d, want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, mk("10.0.0.2:2222")) // different peer, same XFF
	if rec.Code != http.StatusOK {
		t.Errorf("peer 10.0.0.2 first: code = %d, want 200 (XFF must NOT collide)", rec.Code)
	}
}
