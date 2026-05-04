package surf

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCORSMiddleware(t *testing.T) {
	app := NewApp()
	app.Use(CORSWithDefaults())

	app.Get("/test", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("ok"))
		return nil
	})

	t.Run("preflight request", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "/test", nil)
		req.Header.Set("Origin", "http://example.com")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusNoContent)
		}
		if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Error("Access-Control-Allow-Origin not set")
		}
		if rec.Header().Get("Access-Control-Allow-Methods") == "" {
			t.Error("Access-Control-Allow-Methods not set")
		}
	})

	t.Run("regular request", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Origin", "http://example.com")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Error("Access-Control-Allow-Origin not set")
		}
		if rec.Body.String() != "ok" {
			t.Errorf("body = %q, want %q", rec.Body.String(), "ok")
		}
	})
}

func TestCORSSpecificOrigins(t *testing.T) {
	app := NewApp()
	app.Use(CORS(CORSConfig{
		AllowOrigins:     []string{"http://allowed.com", "http://also-allowed.com"},
		AllowMethods:     []string{"GET", "POST"},
		AllowCredentials: true,
	}))

	app.Get("/test", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("ok"))
		return nil
	})

	t.Run("allowed origin", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Origin", "http://allowed.com")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Header().Get("Access-Control-Allow-Origin") != "http://allowed.com" {
			t.Errorf("origin = %q, want %q", rec.Header().Get("Access-Control-Allow-Origin"), "http://allowed.com")
		}
		if rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
			t.Error("credentials header not set")
		}
	})

	t.Run("disallowed origin", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Origin", "http://evil.com")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Error("origin should not be set for disallowed origin")
		}
	})
}

func TestCORSEmptyOriginsFailsClosed(t *testing.T) {
	app := NewApp()
	app.Use(CORS(CORSConfig{
		AllowOrigins: []string{},
		AllowMethods: []string{"GET"},
	}))
	app.Get("/test", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("ok"))
		return nil
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("expected no CORS headers with empty AllowOrigins, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSWildcardWithCredentialsPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when combining AllowOrigins=\"*\" with AllowCredentials=true")
		}
	}()
	CORS(CORSConfig{
		AllowOrigins:     []string{"*"},
		AllowCredentials: true,
	})
}

func TestCORSVaryOnAllowlistMatch(t *testing.T) {
	app := NewApp()
	app.Use(CORS(CORSConfig{
		AllowOrigins: []string{"http://allowed.com"},
		AllowMethods: []string{"GET"},
	}))
	app.Get("/test", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("ok"))
		return nil
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "http://allowed.com")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	vary := rec.Header().Get("Vary")
	if !strings.Contains(vary, "Origin") {
		t.Errorf("expected Vary to include Origin, got %q", vary)
	}
}

func TestCORSEmptyOriginHeaderNotEchoed(t *testing.T) {
	app := NewApp()
	app.Use(CORS(CORSConfig{
		AllowOrigins: []string{"http://allowed.com"},
		AllowMethods: []string{"GET"},
	}))
	app.Get("/test", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("ok"))
		return nil
	})

	// Request without Origin header should not get any CORS response headers.
	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("missing Origin header should not produce ACAO header")
	}
}

func TestRecoveryMiddleware(t *testing.T) {
	app := NewApp()
	app.Use(RecoveryWithDefaults())

	app.Get("/panic", func(w http.ResponseWriter, r *http.Request) error {
		panic("test panic")
	})

	app.Get("/ok", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("ok"))
		return nil
	})

	t.Run("recovers from panic", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/panic", nil)
		rec := httptest.NewRecorder()

		// Should not panic
		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
		}
	})

	t.Run("normal request works", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/ok", nil)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if rec.Body.String() != "ok" {
			t.Errorf("body = %q, want %q", rec.Body.String(), "ok")
		}
	})
}

func TestRecoveryCustomHandler(t *testing.T) {
	app := NewApp()
	app.Use(Recovery(RecoveryConfig{
		RecoveryHandler: func(w http.ResponseWriter, r *http.Request, err interface{}) {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("custom error"))
		},
	}))

	app.Get("/panic", func(w http.ResponseWriter, r *http.Request) error {
		panic("test panic")
	})

	req := httptest.NewRequest("GET", "/panic", nil)
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if rec.Body.String() != "custom error" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "custom error")
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	app := NewApp()
	app.Use(RateLimit(RateLimitConfig{
		RequestsPerSecond: 2,
		Burst:             2,
	}))

	app.Get("/test", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("ok"))
		return nil
	})

	// First 2 requests should succeed (burst)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("request %d: status = %d, want %d", i+1, rec.Code, http.StatusOK)
		}
	}

	// Third request should be rate limited
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("rate limited request: status = %d, want %d", rec.Code, http.StatusTooManyRequests)
	}
	if rec.Header().Get("Retry-After") != "1" {
		t.Error("Retry-After header not set")
	}
}

func TestRateLimitDifferentClients(t *testing.T) {
	app := NewApp()
	app.Use(RateLimit(RateLimitConfig{
		RequestsPerSecond: 1,
		Burst:             1,
	}))

	app.Get("/test", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("ok"))
		return nil
	})

	// Different IPs should have separate limits
	clients := []string{"192.168.1.1:12345", "192.168.1.2:12345", "192.168.1.3:12345"}

	for _, client := range clients {
		req := httptest.NewRequest("GET", "/test", nil)
		req.RemoteAddr = client
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("client %s: status = %d, want %d", client, rec.Code, http.StatusOK)
		}
	}
}

func TestRateLimitSkipFunc(t *testing.T) {
	app := NewApp()
	app.Use(RateLimit(RateLimitConfig{
		RequestsPerSecond: 1,
		Burst:             1,
		SkipFunc: func(r *http.Request) bool {
			return r.Header.Get("X-Skip-Rate-Limit") == "true"
		},
	}))

	app.Get("/test", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("ok"))
		return nil
	})

	// First request exhausts the limit
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	// Second request with skip header should succeed
	req = httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	req.Header.Set("X-Skip-Rate-Limit", "true")
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("skipped request: status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestRateLimitDefaultIgnoresXForwardedFor(t *testing.T) {
	app := NewApp()
	app.Use(RateLimit(RateLimitConfig{
		RequestsPerSecond: 1,
		Burst:             1,
	}))
	app.Get("/test", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("ok"))
		return nil
	})

	// Both requests come from the same peer IP. Spoofed XFF must not
	// create a separate bucket — the second request must be limited.
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	req.Header.Set("X-Forwarded-For", "10.0.0.1")
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first request: status = %d, want 200", rec.Code)
	}

	req = httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	req.Header.Set("X-Forwarded-For", "10.0.0.2") // try to look like a different client
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("spoofed XFF should not bypass rate limit; status = %d, want 429", rec.Code)
	}
}

func TestRateLimitIPv6PeerAddr(t *testing.T) {
	app := NewApp()
	app.Use(RateLimit(RateLimitConfig{
		RequestsPerSecond: 1,
		Burst:             1,
	}))
	app.Get("/test", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("ok"))
		return nil
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "[2001:db8::1]:54321"
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first IPv6 request: status = %d, want 200", rec.Code)
	}

	req = httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "[2001:db8::1]:54322" // same host, different port
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("IPv6 same host different port should hit same bucket; status = %d, want 429", rec.Code)
	}
}

func TestXForwardedForKeyFuncTrustedProxy(t *testing.T) {
	keyFunc, err := XForwardedForKeyFunc([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("trusted peer honors XFF", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		req.Header.Set("X-Forwarded-For", "203.0.113.5")
		if got := keyFunc(req); got != "203.0.113.5" {
			t.Errorf("got %q, want %q", got, "203.0.113.5")
		}
	})

	t.Run("untrusted peer ignores XFF", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "203.0.113.99:1234"
		req.Header.Set("X-Forwarded-For", "10.0.0.1")
		if got := keyFunc(req); got != "203.0.113.99" {
			t.Errorf("got %q, want %q", got, "203.0.113.99")
		}
	})

	t.Run("walks past trusted hops", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		req.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.2, 10.0.0.3")
		if got := keyFunc(req); got != "203.0.113.5" {
			t.Errorf("got %q, want %q", got, "203.0.113.5")
		}
	})

	t.Run("invalid CIDR returns error", func(t *testing.T) {
		_, err := XForwardedForKeyFunc([]string{"not-a-cidr"})
		if err == nil {
			t.Error("expected error for invalid CIDR")
		}
	})
}

func TestRateLimitStoreEvictsIdleBuckets(t *testing.T) {
	store := newRateLimiterStore(1, 1, 3, 50*time.Millisecond)

	// Fill to capacity with three keys.
	store.get("a")
	store.get("b")
	store.get("c")
	if got := len(store.limiters); got != 3 {
		t.Fatalf("expected 3 buckets, got %d", got)
	}

	// Wait past idle window, then insert a new key. The store should
	// evict idle buckets before adding.
	time.Sleep(80 * time.Millisecond)
	store.get("d")

	store.mu.RLock()
	size := len(store.limiters)
	_, hasD := store.limiters["d"]
	store.mu.RUnlock()

	if size > 3 {
		t.Errorf("store size = %d, want <= 3 after eviction", size)
	}
	if !hasD {
		t.Error("newly inserted key 'd' should be present")
	}
}

func TestTimeoutMiddleware(t *testing.T) {
	app := NewApp()
	app.Use(Timeout(TimeoutConfig{
		Timeout: 50 * time.Millisecond,
	}))

	app.Get("/slow", func(w http.ResponseWriter, r *http.Request) error {
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte("slow response"))
		return nil
	})

	app.Get("/fast", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("fast response"))
		return nil
	})

	t.Run("request times out", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/slow", nil)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusGatewayTimeout {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusGatewayTimeout)
		}
	})

	t.Run("fast request succeeds", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/fast", nil)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if rec.Body.String() != "fast response" {
			t.Errorf("body = %q, want %q", rec.Body.String(), "fast response")
		}
	})
}

func TestTimeoutHandlerObservesContextCancellation(t *testing.T) {
	app := NewApp()
	app.Use(Timeout(TimeoutConfig{Timeout: 30 * time.Millisecond}))

	cancelled := make(chan struct{})
	app.Get("/respect-ctx", func(w http.ResponseWriter, r *http.Request) error {
		// Well-behaved handler that observes context cancellation.
		select {
		case <-r.Context().Done():
			close(cancelled)
		case <-time.After(500 * time.Millisecond):
		}
		return nil
	})

	req := httptest.NewRequest("GET", "/respect-ctx", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want 504", rec.Code)
	}
	select {
	case <-cancelled:
		// good
	case <-time.After(200 * time.Millisecond):
		t.Error("handler did not observe context cancellation within 200ms after timeout fired")
	}
}

func TestTimeoutWriteAfterTimeoutReturnsHandlerTimeout(t *testing.T) {
	tw := &timeoutWriter{
		ResponseWriter: httptest.NewRecorder(),
		h:              make(http.Header),
	}
	tw.timedOut = true

	_, err := tw.Write([]byte("late"))
	if err != http.ErrHandlerTimeout {
		t.Errorf("Write after timeout: err = %v, want http.ErrHandlerTimeout", err)
	}
}

func TestGzipMiddleware(t *testing.T) {
	app := NewApp()
	app.Use(GzipWithDefaults())

	largeBody := strings.Repeat("Hello, World! ", 100) // >1KB

	app.Get("/large", func(w http.ResponseWriter, r *http.Request) error {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(largeBody))
		return nil
	})

	app.Get("/small", func(w http.ResponseWriter, r *http.Request) error {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("small"))
		return nil
	})

	t.Run("compresses large response", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/large", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Header().Get("Content-Encoding") != "gzip" {
			t.Error("Content-Encoding should be gzip")
		}

		// Decompress and verify
		reader, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
		if err != nil {
			t.Fatalf("failed to create gzip reader: %v", err)
		}
		defer reader.Close()

		decompressed, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("failed to decompress: %v", err)
		}

		if string(decompressed) != largeBody {
			t.Error("decompressed content doesn't match original")
		}
	})

	t.Run("does not compress small response", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/small", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Header().Get("Content-Encoding") == "gzip" {
			t.Error("small response should not be compressed")
		}
		if rec.Body.String() != "small" {
			t.Errorf("body = %q, want %q", rec.Body.String(), "small")
		}
	})

	t.Run("does not compress without Accept-Encoding", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/large", nil)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Header().Get("Content-Encoding") == "gzip" {
			t.Error("should not compress without Accept-Encoding")
		}
	})
}

func TestGzipContentTypes(t *testing.T) {
	app := NewApp()
	app.Use(Gzip(GzipConfig{
		MinSize:      0, // Compress everything
		ContentTypes: []string{"application/json"},
	}))

	app.Get("/json", func(w http.ResponseWriter, r *http.Request) error {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"message": "hello"}`))
		return nil
	})

	app.Get("/text", func(w http.ResponseWriter, r *http.Request) error {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("hello"))
		return nil
	})

	t.Run("compresses JSON", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/json", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Header().Get("Content-Encoding") != "gzip" {
			t.Error("JSON should be compressed")
		}
	})

	t.Run("does not compress text", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/text", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Header().Get("Content-Encoding") == "gzip" {
			t.Error("text should not be compressed")
		}
	})
}

func TestDefaultConfigs(t *testing.T) {
	// Ensure defaults don't panic
	_ = DefaultCORSConfig()
	_ = DefaultRecoveryConfig()
	_ = DefaultRateLimitConfig()
	_ = DefaultTimeoutConfig()
	_ = DefaultGzipConfig()
}

func TestTokenBucket(t *testing.T) {
	tb := newTokenBucket(10, 5) // 10 per second, burst of 5

	// Should allow burst
	for i := 0; i < 5; i++ {
		if !tb.allow() {
			t.Errorf("request %d should be allowed (burst)", i+1)
		}
	}

	// Should deny after burst exhausted
	if tb.allow() {
		t.Error("request after burst should be denied")
	}

	// Wait for refill
	time.Sleep(200 * time.Millisecond) // Should refill ~2 tokens

	if !tb.allow() {
		t.Error("request after refill should be allowed")
	}
}
