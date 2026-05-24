package surf

import (
	"compress/gzip"
	"context"
	"fmt"
	"log/slog"
	"net/http"
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

// DefaultCORSConfig returns a permissive CORS configuration
func DefaultCORSConfig() CORSConfig {
	return CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
		AllowHeaders: []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Requested-With"},
		MaxAge:       86400, // 24 hours
	}
}

// CORS creates a CORS middleware with the given configuration
func CORS(config CORSConfig) Middleware {
	allowMethods := strings.Join(config.AllowMethods, ", ")
	allowHeaders := strings.Join(config.AllowHeaders, ", ")
	exposeHeaders := strings.Join(config.ExposeHeaders, ", ")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Check if origin is allowed
			allowed := false
			if len(config.AllowOrigins) == 0 || (len(config.AllowOrigins) == 1 && config.AllowOrigins[0] == "*") {
				allowed = true
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				for _, o := range config.AllowOrigins {
					if o == origin {
						allowed = true
						w.Header().Set("Access-Control-Allow-Origin", origin)
						break
					}
				}
			}

			if !allowed {
				next.ServeHTTP(w, r)
				return
			}

			// Set CORS headers
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

			// Handle preflight request
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

	// KeyFunc returns a key to identify the client (default: IP address).
	// When nil, a key function derived from TrustedProxies is used.
	KeyFunc func(r *http.Request) string

	// TrustedProxies is a list of proxy CIDR blocks or addresses. When set and
	// KeyFunc is nil, the client IP is taken from X-Forwarded-For only for
	// requests arriving through these proxies. When empty, X-Forwarded-For is
	// ignored and the connecting peer address is used.
	TrustedProxies []string

	// ExceededHandler is called when the rate limit is exceeded
	// If nil, returns 429 Too Many Requests
	ExceededHandler func(w http.ResponseWriter, r *http.Request)

	// SkipFunc returns true if the request should skip rate limiting
	SkipFunc func(r *http.Request) bool
}

// tokenBucket implements a simple token bucket rate limiter
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

// rateLimiterStore stores rate limiters per client
type rateLimiterStore struct {
	limiters map[string]*tokenBucket
	mu       sync.RWMutex
	rate     float64
	burst    int
}

func newRateLimiterStore(rate float64, burst int) *rateLimiterStore {
	return &rateLimiterStore{
		limiters: make(map[string]*tokenBucket),
		rate:     rate,
		burst:    burst,
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

	// Double-check after acquiring write lock
	if limiter, exists = s.limiters[key]; exists {
		return limiter
	}

	limiter = newTokenBucket(s.rate, s.burst)
	s.limiters[key] = limiter
	return limiter
}

// DefaultRateLimitConfig returns a default rate limit configuration
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		RequestsPerSecond: 10,
		Burst:             20,
		KeyFunc: func(r *http.Request) string {
			// Extract IP from RemoteAddr or X-Forwarded-For
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				parts := strings.Split(xff, ",")
				return strings.TrimSpace(parts[0])
			}
			// Remove port from RemoteAddr
			addr := r.RemoteAddr
			if idx := strings.LastIndex(addr, ":"); idx != -1 {
				addr = addr[:idx]
			}
			return addr
		},
	}
}

// RateLimit creates a rate limiting middleware with the given configuration
func RateLimit(config RateLimitConfig) Middleware {
	if config.KeyFunc == nil {
		if len(config.TrustedProxies) > 0 {
			config.KeyFunc = KeyByIP(config.TrustedProxies...)
		} else {
			config.KeyFunc = DefaultRateLimitConfig().KeyFunc
		}
	}
	if config.RequestsPerSecond <= 0 {
		config.RequestsPerSecond = 10
	}
	if config.Burst <= 0 {
		config.Burst = int(config.RequestsPerSecond * 2)
	}

	store := newRateLimiterStore(config.RequestsPerSecond, config.Burst)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check if request should skip rate limiting
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
					setKnownHeader(w.Header(), headerRetryAfter, "1")
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

// Timeout creates a timeout middleware with the given configuration
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
				// Request completed successfully
				tw.mu.Lock()
				defer tw.mu.Unlock()
				if !tw.timedOut {
					// Copy headers
					for k, v := range tw.h {
						w.Header()[k] = v
					}
					if tw.code != 0 {
						w.WriteHeader(tw.code)
					}
					w.Write(tw.buf)
				}
			case <-ctx.Done():
				// Request timed out
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

// timeoutWriter buffers the response until we know if we timed out
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
		return 0, context.DeadlineExceeded
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
			setKnownHeader(w.Header(), headerVary, "Accept-Encoding")

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
		setKnownHeader(gz.Header(), headerContentEncoding, "gzip")
		gz.Header().Del(headerContentLength)

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
