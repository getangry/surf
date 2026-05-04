package surf

import (
	"compress/gzip"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// CORSConfig defines the configuration for CORS middleware
type CORSConfig struct {
	// AllowOrigins is a list of origins that may access the resource.
	// Use "*" to allow any origin, or specify exact origins.
	AllowOrigins []string

	// AllowMethods is a list of methods the client is allowed to use.
	AllowMethods []string

	// AllowHeaders is a list of headers the client is allowed to use.
	AllowHeaders []string

	// ExposeHeaders is a list of headers exposed to the client.
	ExposeHeaders []string

	// AllowCredentials indicates whether the request can include credentials.
	AllowCredentials bool

	// MaxAge indicates how long the results of a preflight request can be cached.
	MaxAge int
}

// DefaultCORSConfig returns a permissive CORS configuration that allows any
// origin without credentials. Override AllowOrigins explicitly for production.
func DefaultCORSConfig() CORSConfig {
	return CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
		AllowHeaders: []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With"},
		MaxAge:       86400, // 24 hours
	}
}

// CORS creates a CORS middleware with the given configuration.
//
// Behavior:
//   - Empty AllowOrigins is fail-closed: no CORS headers are emitted, so
//     browsers reject cross-origin requests. Set AllowOrigins explicitly.
//   - AllowOrigins{"*"} combined with AllowCredentials=true panics at
//     construction. Browsers reject this combination, but a non-browser
//     client could be misled. Use an explicit allowlist with credentials.
//   - When the request Origin matches an allowlist entry, the response
//     echoes that origin and adds Vary: Origin so caches don't conflate
//     cross-origin clients.
func CORS(config CORSConfig) Middleware {
	if config.AllowCredentials {
		for _, o := range config.AllowOrigins {
			if o == "*" {
				panic("surf: CORS AllowOrigins=\"*\" with AllowCredentials=true is unsafe and rejected by browsers; use an explicit origin allowlist")
			}
		}
	}

	allowMethods := strings.Join(config.AllowMethods, ", ")
	allowHeaders := strings.Join(config.AllowHeaders, ", ")
	exposeHeaders := strings.Join(config.ExposeHeaders, ", ")

	wildcard := len(config.AllowOrigins) == 1 && config.AllowOrigins[0] == "*"

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Empty allowlist: fail-closed. No CORS headers.
			if len(config.AllowOrigins) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			allowed := false
			switch {
			case wildcard:
				allowed = true
				w.Header().Set("Access-Control-Allow-Origin", "*")
			default:
				for _, o := range config.AllowOrigins {
					if o == origin && origin != "" {
						allowed = true
						w.Header().Set("Access-Control-Allow-Origin", origin)
						w.Header().Add("Vary", "Origin")
						break
					}
				}
			}

			if !allowed {
				next.ServeHTTP(w, r)
				return
			}

			if allowMethods != "" {
				w.Header().Set("Access-Control-Allow-Methods", allowMethods)
			}
			if allowHeaders != "" {
				w.Header().Set("Access-Control-Allow-Headers", allowHeaders)
			}
			if exposeHeaders != "" {
				w.Header().Set("Access-Control-Expose-Headers", exposeHeaders)
			}
			if config.AllowCredentials {
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			if config.MaxAge > 0 {
				w.Header().Set("Access-Control-Max-Age", fmt.Sprintf("%d", config.MaxAge))
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// CORSWithDefaults creates a CORS middleware with default configuration
func CORSWithDefaults() Middleware {
	return CORS(DefaultCORSConfig())
}

// RecoveryConfig defines the configuration for panic recovery middleware
type RecoveryConfig struct {
	// Logger for logging panic information
	Logger *slog.Logger

	// StackSize is the maximum size of the stack trace to log (default 4KB)
	StackSize int

	// DisableStackAll disables capturing all goroutines stack trace
	DisableStackAll bool

	// DisablePrintStack disables printing the stack trace
	DisablePrintStack bool

	// RecoveryHandler is called when a panic is recovered
	// If nil, returns 500 Internal Server Error
	RecoveryHandler func(w http.ResponseWriter, r *http.Request, err interface{})
}

// DefaultRecoveryConfig returns a default recovery configuration
func DefaultRecoveryConfig() RecoveryConfig {
	return RecoveryConfig{
		Logger:          slog.Default(),
		StackSize:       4 << 10, // 4KB
		DisableStackAll: false,
	}
}

// Recovery creates a panic recovery middleware with the given configuration
func Recovery(config RecoveryConfig) Middleware {
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.StackSize == 0 {
		config.StackSize = 4 << 10
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					// Get stack trace
					var stack []byte
					if !config.DisablePrintStack {
						stack = debug.Stack()
						if len(stack) > config.StackSize {
							stack = stack[:config.StackSize]
						}
					}

					// Log the panic
					config.Logger.Error("panic recovered",
						"error", err,
						"method", r.Method,
						"path", r.URL.Path,
						"remote_addr", r.RemoteAddr,
						"stack", string(stack),
					)

					// Call recovery handler or return 500
					if config.RecoveryHandler != nil {
						config.RecoveryHandler(w, r, err)
					} else {
						http.Error(w, "Internal Server Error", http.StatusInternalServerError)
					}
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// RecoveryWithDefaults creates a recovery middleware with default configuration
func RecoveryWithDefaults() Middleware {
	return Recovery(DefaultRecoveryConfig())
}

// RateLimitConfig defines the configuration for rate limiting middleware
type RateLimitConfig struct {
	// RequestsPerSecond is the maximum number of requests per second
	RequestsPerSecond float64

	// Burst is the maximum burst size
	Burst int

	// KeyFunc returns a key to identify the client. Default: client IP from
	// r.RemoteAddr. Forwarded-for headers are NOT trusted by default; use
	// XForwardedForKeyFunc with an explicit trusted-proxy allowlist.
	KeyFunc func(r *http.Request) string

	// ExceededHandler is called when the rate limit is exceeded
	// If nil, returns 429 Too Many Requests
	ExceededHandler func(w http.ResponseWriter, r *http.Request)

	// SkipFunc returns true if the request should skip rate limiting
	SkipFunc func(r *http.Request) bool

	// MaxKeys caps the number of distinct client buckets retained. When the
	// store grows past this, idle buckets (no traffic in IdleEvictAfter)
	// are evicted opportunistically. Defaults to 100_000 if zero.
	MaxKeys int

	// IdleEvictAfter is the duration of inactivity after which a bucket is
	// eligible for eviction. Defaults to 10 minutes if zero.
	IdleEvictAfter time.Duration
}

// tokenBucket implements a simple token bucket rate limiter.
// lastUpdate doubles as the last-seen timestamp for eviction.
type tokenBucket struct {
	tokens     float64
	lastUpdate time.Time
	rate       float64 // tokens per second
	burst      float64 // max tokens
	mu         sync.Mutex
}

func newTokenBucket(rate float64, burst int) *tokenBucket {
	return &tokenBucket{
		tokens:     float64(burst),
		lastUpdate: time.Now(),
		rate:       rate,
		burst:      float64(burst),
	}
}

func (tb *tokenBucket) allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(tb.lastUpdate).Seconds()
	tb.tokens += elapsed * tb.rate
	if tb.tokens > tb.burst {
		tb.tokens = tb.burst
	}
	tb.lastUpdate = now

	if tb.tokens >= 1 {
		tb.tokens--
		return true
	}
	return false
}

func (tb *tokenBucket) lastSeen() time.Time {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	return tb.lastUpdate
}

// rateLimiterStore stores rate limiters per client with bounded growth.
type rateLimiterStore struct {
	limiters       map[string]*tokenBucket
	mu             sync.RWMutex
	rate           float64
	burst          int
	maxKeys        int
	idleEvictAfter time.Duration
}

func newRateLimiterStore(rate float64, burst, maxKeys int, idleEvictAfter time.Duration) *rateLimiterStore {
	return &rateLimiterStore{
		limiters:       make(map[string]*tokenBucket),
		rate:           rate,
		burst:          burst,
		maxKeys:        maxKeys,
		idleEvictAfter: idleEvictAfter,
	}
}

func (s *rateLimiterStore) get(key string) *tokenBucket {
	s.mu.RLock()
	limiter, exists := s.limiters[key]
	s.mu.RUnlock()
	if exists {
		return limiter
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if limiter, exists = s.limiters[key]; exists {
		return limiter
	}

	if len(s.limiters) >= s.maxKeys {
		s.evictLocked()
	}

	limiter = newTokenBucket(s.rate, s.burst)
	s.limiters[key] = limiter
	return limiter
}

// evictLocked removes idle buckets. Caller must hold the write lock.
// If no buckets are idle (high churn), drops the oldest tenth to bound growth.
func (s *rateLimiterStore) evictLocked() {
	cutoff := time.Now().Add(-s.idleEvictAfter)
	for k, b := range s.limiters {
		if b.lastSeen().Before(cutoff) {
			delete(s.limiters, k)
		}
	}
	// If pure-idle eviction freed nothing, drop a slice of arbitrary keys to
	// bound the worst case. Map iteration order is randomized, so this is a
	// random-eviction policy under sustained adversarial growth.
	if len(s.limiters) >= s.maxKeys {
		toDrop := s.maxKeys / 10
		if toDrop < 1 {
			toDrop = 1
		}
		for k := range s.limiters {
			if toDrop == 0 {
				break
			}
			delete(s.limiters, k)
			toDrop--
		}
	}
}

// defaultKeyFunc returns the client IP from r.RemoteAddr. It does NOT trust
// any forwarded-for header; spoofed XFF would let any client bypass the
// rate limit. Use XForwardedForKeyFunc to opt in behind trusted proxies.
func defaultKeyFunc(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr may be unset in tests, or already host-only.
		return r.RemoteAddr
	}
	return host
}

// XForwardedForKeyFunc returns a KeyFunc that honors X-Forwarded-For only
// when the immediate peer is in trustedProxies. It walks XFF right-to-left,
// skipping every hop that is itself a trusted proxy, and returns the first
// non-trusted address. If the peer is not trusted, falls back to the peer.
//
// trustedProxies are CIDRs (e.g. "10.0.0.0/8", "::1/128").
func XForwardedForKeyFunc(trustedProxies []string) (func(r *http.Request) string, error) {
	prefixes := make([]netip.Prefix, 0, len(trustedProxies))
	for _, cidr := range trustedProxies {
		p, err := netip.ParsePrefix(cidr)
		if err != nil {
			return nil, fmt.Errorf("surf: invalid trusted proxy CIDR %q: %w", cidr, err)
		}
		prefixes = append(prefixes, p)
	}

	isTrusted := func(host string) bool {
		addr, err := netip.ParseAddr(host)
		if err != nil {
			return false
		}
		for _, p := range prefixes {
			if p.Contains(addr) {
				return true
			}
		}
		return false
	}

	return func(r *http.Request) string {
		peer := defaultKeyFunc(r)
		if !isTrusted(peer) {
			return peer
		}
		xff := r.Header.Get("X-Forwarded-For")
		if xff == "" {
			return peer
		}
		// Walk right-to-left; the rightmost non-trusted hop is the real client.
		hops := strings.Split(xff, ",")
		for i := len(hops) - 1; i >= 0; i-- {
			h := strings.TrimSpace(hops[i])
			if h == "" {
				continue
			}
			if !isTrusted(h) {
				return h
			}
		}
		return peer
	}, nil
}

// DefaultRateLimitConfig returns a default rate limit configuration.
// The default KeyFunc uses the connection peer IP only and does not trust
// any forwarded-for header.
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		RequestsPerSecond: 10,
		Burst:             20,
		KeyFunc:           defaultKeyFunc,
		MaxKeys:           100_000,
		IdleEvictAfter:    10 * time.Minute,
	}
}

// RateLimit creates a rate limiting middleware with the given configuration
func RateLimit(config RateLimitConfig) Middleware {
	if config.KeyFunc == nil {
		config.KeyFunc = defaultKeyFunc
	}
	if config.RequestsPerSecond <= 0 {
		config.RequestsPerSecond = 10
	}
	if config.Burst <= 0 {
		config.Burst = int(config.RequestsPerSecond * 2)
	}
	if config.MaxKeys <= 0 {
		config.MaxKeys = 100_000
	}
	if config.IdleEvictAfter <= 0 {
		config.IdleEvictAfter = 10 * time.Minute
	}

	store := newRateLimiterStore(config.RequestsPerSecond, config.Burst, config.MaxKeys, config.IdleEvictAfter)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if config.SkipFunc != nil && config.SkipFunc(r) {
				next.ServeHTTP(w, r)
				return
			}

			key := config.KeyFunc(r)
			limiter := store.get(key)

			if !limiter.allow() {
				if config.ExceededHandler != nil {
					config.ExceededHandler(w, r)
				} else {
					w.Header().Set("Retry-After", "1")
					http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
				}
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RateLimitWithDefaults creates a rate limiting middleware with default configuration
func RateLimitWithDefaults() Middleware {
	return RateLimit(DefaultRateLimitConfig())
}

// TimeoutConfig defines the configuration for timeout middleware
type TimeoutConfig struct {
	// Timeout is the maximum duration before timing out
	Timeout time.Duration

	// TimeoutHandler is called when the request times out
	// If nil, returns 504 Gateway Timeout
	TimeoutHandler func(w http.ResponseWriter, r *http.Request)
}

// DefaultTimeoutConfig returns a default timeout configuration
func DefaultTimeoutConfig() TimeoutConfig {
	return TimeoutConfig{
		Timeout: 30 * time.Second,
	}
}

// Timeout creates a timeout middleware with the given configuration.
//
// Caveats:
//   - The middleware buffers the response in memory and writes it only when
//     the handler returns. Streaming endpoints (SSE, NDJSON, large file
//     downloads) will appear unresponsive to clients; do not wrap them.
//   - When the timeout fires, the handler goroutine is not killed — Go has
//     no safe way to do that. The request context is cancelled, so handlers
//     must observe r.Context().Done() to release resources promptly.
//     Otherwise the goroutine continues until the handler returns naturally,
//     producing a bounded (but real) goroutine leak under sustained timeouts.
func Timeout(config TimeoutConfig) Middleware {
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Second
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), config.Timeout)
			defer cancel()

			r = r.WithContext(ctx)

			done := make(chan struct{})
			tw := &timeoutWriter{
				ResponseWriter: w,
				h:              make(http.Header),
			}

			go func() {
				next.ServeHTTP(tw, r)
				close(done)
			}()

			select {
			case <-done:
				// Handler returned. close(done) happens-before this receive,
				// so reads of tw fields are safe without re-locking.
				for k, v := range tw.h {
					w.Header()[k] = v
				}
				if tw.code != 0 {
					w.WriteHeader(tw.code)
				}
				w.Write(tw.buf)
			case <-ctx.Done():
				// Mark timed-out under lock so any in-flight handler Write
				// returns http.ErrHandlerTimeout and the handler can unwind.
				tw.mu.Lock()
				tw.timedOut = true
				tw.mu.Unlock()

				if config.TimeoutHandler != nil {
					config.TimeoutHandler(w, r)
				} else {
					http.Error(w, "Gateway Timeout", http.StatusGatewayTimeout)
				}
			}
		})
	}
}

// timeoutWriter buffers the response until we know if we timed out.
// It does not implement http.Hijacker: hijacking would defeat the buffering
// strategy the timeout depends on. Flush is a deliberate no-op for the
// same reason — bytes do not reach the wire until the handler returns.
type timeoutWriter struct {
	http.ResponseWriter
	h        http.Header
	buf      []byte
	code     int
	timedOut bool
	mu       sync.Mutex
}

func (tw *timeoutWriter) Header() http.Header {
	return tw.h
}

func (tw *timeoutWriter) Write(b []byte) (int, error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.timedOut {
		return 0, http.ErrHandlerTimeout
	}
	tw.buf = append(tw.buf, b...)
	return len(b), nil
}

func (tw *timeoutWriter) WriteHeader(code int) {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	if tw.timedOut {
		return
	}
	tw.code = code
}

// Flush is a no-op. The timeout middleware buffers the entire response
// until the handler returns, so flushing has no effect.
func (tw *timeoutWriter) Flush() {}

// TimeoutWithDefaults creates a timeout middleware with default configuration
func TimeoutWithDefaults() Middleware {
	return Timeout(DefaultTimeoutConfig())
}

// GzipConfig defines the configuration for gzip compression middleware
type GzipConfig struct {
	// Level is the compression level (1-9, or gzip.DefaultCompression)
	Level int

	// MinSize is the minimum response size to trigger compression
	MinSize int

	// ContentTypes is a list of content types to compress
	// If empty, all content types are compressed
	ContentTypes []string

	// SkipFunc returns true if the request should skip compression
	SkipFunc func(r *http.Request) bool
}

// DefaultGzipConfig returns a default gzip configuration
func DefaultGzipConfig() GzipConfig {
	return GzipConfig{
		Level:   gzip.DefaultCompression,
		MinSize: 1024, // 1KB minimum
		ContentTypes: []string{
			"text/html",
			"text/css",
			"text/plain",
			"text/javascript",
			"application/javascript",
			"application/json",
			"application/xml",
			"text/xml",
		},
	}
}

// Gzip creates a gzip compression middleware with the given configuration
func Gzip(config GzipConfig) Middleware {
	if config.Level == 0 {
		config.Level = gzip.DefaultCompression
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check if client accepts gzip
			if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				next.ServeHTTP(w, r)
				return
			}

			// Check if we should skip
			if config.SkipFunc != nil && config.SkipFunc(r) {
				next.ServeHTTP(w, r)
				return
			}

			gz := &gzipResponseWriter{
				ResponseWriter: w,
				config:         config,
			}
			defer gz.Close()

			// Set Vary header
			w.Header().Set("Vary", "Accept-Encoding")

			next.ServeHTTP(gz, r)
		})
	}
}

// gzipResponseWriter wraps http.ResponseWriter with gzip compression
type gzipResponseWriter struct {
	http.ResponseWriter
	writer     *gzip.Writer
	config     GzipConfig
	buf        []byte
	statusCode int
	headerSent bool
}

func (gz *gzipResponseWriter) WriteHeader(code int) {
	gz.statusCode = code
}

func (gz *gzipResponseWriter) Write(b []byte) (int, error) {
	gz.buf = append(gz.buf, b...)
	return len(b), nil
}

func (gz *gzipResponseWriter) Close() error {
	// Check content type
	contentType := gz.Header().Get("Content-Type")
	if contentType == "" {
		contentType = http.DetectContentType(gz.buf)
	}

	shouldCompress := len(gz.buf) >= gz.config.MinSize
	if shouldCompress && len(gz.config.ContentTypes) > 0 {
		shouldCompress = false
		for _, ct := range gz.config.ContentTypes {
			if strings.HasPrefix(contentType, ct) {
				shouldCompress = true
				break
			}
		}
	}

	if shouldCompress {
		gz.Header().Set("Content-Encoding", "gzip")
		gz.Header().Del("Content-Length")

		if gz.statusCode != 0 {
			gz.ResponseWriter.WriteHeader(gz.statusCode)
		}

		writer, err := gzip.NewWriterLevel(gz.ResponseWriter, gz.config.Level)
		if err != nil {
			return err
		}
		defer writer.Close()
		_, err = writer.Write(gz.buf)
		return err
	}

	// Write uncompressed
	if gz.statusCode != 0 {
		gz.ResponseWriter.WriteHeader(gz.statusCode)
	}
	_, err := gz.ResponseWriter.Write(gz.buf)
	return err
}

// GzipWithDefaults creates a gzip compression middleware with default configuration
func GzipWithDefaults() Middleware {
	return Gzip(DefaultGzipConfig())
}
