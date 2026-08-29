package surf

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Pre-compiled regex for log template parsing (avoids compilation on every log call)
var logTemplateRegex = regexp.MustCompile(`\{([^}]+)\}`)

// Logger context helpers
//
// Request-scoped values live in the request's own context.Context, keyed by the
// unexported contextKey type so no other package can collide with them.
//
// This used to be a package-level map[*http.Request]map[string]interface{}, and
// keying by the request POINTER made it lose values silently: r.WithContext
// returns a NEW *http.Request, so any middleware that derived a request between
// the write and the read handed handlers a request the value was not filed
// under. Get returned nothing and the caller saw a wrong answer rather than an
// error. A context survives r.WithContext by construction, needs no lock, and
// is collected together with its request instead of being pinned in a global
// map until something remembers to call Delete.

// Set attaches a value to the request, visible to every handler downstream.
//
// It takes a **http.Request so it can rebind the caller's request to one
// carrying the value — an http.Request's context is immutable, so a new request
// is the only way to add to it. Pass the rebound request on:
//
//	func auth(next http.Handler) http.Handler {
//	    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//	        surf.SetUserID(&r, id)
//	        next.ServeHTTP(w, r) // r is the rebound request
//	    })
//	}
func Set(r **http.Request, key string, value interface{}) {
	if r == nil || *r == nil {
		return
	}
	*r = (*r).WithContext(context.WithValue((*r).Context(), contextKey(key), value))
}

// SetMultiple attaches several values at once, rebinding the caller's request
// exactly like Set.
func SetMultiple(r **http.Request, values map[string]interface{}) {
	if r == nil || *r == nil || len(values) == 0 {
		return
	}
	ctx := (*r).Context()
	for key, value := range values {
		ctx = context.WithValue(ctx, contextKey(key), value)
	}
	*r = (*r).WithContext(ctx)
}

// Store attaches a value to a request the caller cannot rebind — a
// HandlerFunc's r, or an app-level Before handler's r, where there is no
// pointer to reassign. It swaps the context inside the *http.Request the caller
// already holds, so the value is visible to that request and to every
// r.WithContext copy derived from it afterwards.
//
// Prefer Set (or per-route Middleware and r.WithContext) where you can rebind:
// Store mutates the request in place, so it must not be called on a request
// another goroutine is reading concurrently. Within a single request-handling
// goroutine — the only place net/http hands you a request — that is safe.
func Store(r *http.Request, key string, value interface{}) {
	if r == nil {
		return
	}
	*r = *r.WithContext(context.WithValue(r.Context(), contextKey(key), value))
}

// Get retrieves a value previously attached to the request by Set, SetMultiple
// or Store. The second return reports whether the key was present.
func Get(r *http.Request, key string) (interface{}, bool) {
	if r == nil {
		return nil, false
	}
	val := r.Context().Value(contextKey(key))
	return val, val != nil
}

// GetString retrieves a string value with a default
func GetString(r *http.Request, key string, defaultVal string) string {
	if val, ok := Get(r, key); ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return defaultVal
}

// Delete is retained for source compatibility and does nothing.
//
// It used to evict the request's entry from the package-level storage map, and
// was the only thing that stopped that map growing without bound. There is no
// such map any more: values live in the request's context and are collected
// with the request, so there is nothing to release. Existing callers — the
// `defer surf.Delete(r)` this package's own logging middleware used to carry,
// and any a consumer wrote — keep compiling and behaving correctly.
//
// A context cannot have a value removed from it, so this cannot be made to
// delete anything; that is deliberate, not an oversight.
//
// Deprecated: request storage is freed automatically with the request.
func Delete(r *http.Request) {}

// GetInt retrieves an int value with a default
func GetInt(r *http.Request, key string, defaultVal int) int {
	if val, ok := Get(r, key); ok {
		switch v := val.(type) {
		case int:
			return v
		case int64:
			return int(v)
		case float64:
			return int(v)
		}
	}
	return defaultVal
}

// MustGet panics if key doesn't exist (for required values)
func MustGet(r *http.Request, key string) interface{} {
	if val, ok := Get(r, key); ok {
		return val
	}
	panic(fmt.Sprintf("required context key %s not found", key))
}

// SetRequestID adds a request ID to the context
func SetRequestID(r **http.Request, id string) {
	Set(r, "request_id", id)
}

// GetRequestID retrieves the request ID from context
func GetRequestID(r *http.Request) string {
	return GetString(r, "request_id", "")
}

// SetUserID adds a user ID to the context
func SetUserID(r **http.Request, userID string) {
	Set(r, "user_id", userID)
}

// GetUserID retrieves the user ID from context
func GetUserID(r *http.Request) string {
	return GetString(r, "user_id", "")
}

// GetService retrieves a service from the application's service container
// It requires access to the application instance through the request context
// Returns zero value if service not found or type assertion fails
func GetService[T any](r *http.Request, key any) T {
	var zero T
	st := stateFromRequest(r)
	if st == nil || st.app == nil {
		return zero
	}
	service := st.app.GetService(key)
	if service == nil {
		return zero
	}
	typed, ok := service.(T)
	if !ok {
		return zero
	}
	return typed
}

// WithRequest provides a fluent interface for setting multiple context values
type WithRequest struct {
	r **http.Request
}

// With creates a new fluent request wrapper
func With(r **http.Request) *WithRequest {
	return &WithRequest{r: r}
}

// Set adds a value to the context
func (wr *WithRequest) Set(key string, value interface{}) *WithRequest {
	Set(wr.r, key, value)
	return wr
}

// SetRequestID adds a request ID
func (wr *WithRequest) SetRequestID(id string) *WithRequest {
	SetRequestID(wr.r, id)
	return wr
}

// SetUserID adds a user ID
func (wr *WithRequest) SetUserID(userID string) *WithRequest {
	SetUserID(wr.r, userID)
	return wr
}

// LogEntry represents a single log entry with all request/response data
type LogEntry struct {
	req     *http.Request
	status  int
	size    int
	latency time.Duration
	rw      *ResponseWriter // Reference to ResponseWriter for custom data
}

// Method returns the HTTP method
func (e *LogEntry) Method() string {
	return e.req.Method
}

// Path returns the request path
func (e *LogEntry) Path() string {
	return e.req.URL.Path
}

// Status returns the response status code
func (e *LogEntry) Status() string {
	return strconv.Itoa(e.status)
}

// StatusCode returns the response status code as int
func (e *LogEntry) StatusCode() int {
	return e.status
}

// Size returns the response size in bytes
func (e *LogEntry) Size() string {
	return strconv.Itoa(e.size)
}

// SizeBytes returns the response size as int
func (e *LogEntry) SizeBytes() int {
	return e.size
}

// Latency returns the request latency
func (e *LogEntry) Latency() string {
	return e.latency.String()
}

// LatencyMs returns the latency in milliseconds, showing fractional ms for sub-millisecond durations
func (e *LogEntry) LatencyMs() string {
	ms := float64(e.latency.Nanoseconds()) / 1000000.0
	if ms < 1.0 {
		// For sub-millisecond, show with 3 decimal places
		return fmt.Sprintf("%.3f", ms)
	}
	// For >= 1ms, show as integer
	return fmt.Sprintf("%.0f", ms)
}

// RemoteAddr returns the client IP address
func (e *LogEntry) RemoteAddr() string {
	return e.req.RemoteAddr
}

// UserAgent returns the User-Agent header
func (e *LogEntry) UserAgent() string {
	return e.req.UserAgent()
}

// Referer returns the Referer header
func (e *LogEntry) Referer() string {
	return e.req.Referer()
}

// Proto returns the HTTP protocol version
func (e *LogEntry) Proto() string {
	return e.req.Proto
}

// Host returns the Host header
func (e *LogEntry) Host() string {
	return e.req.Host
}

// RequestID returns the request ID from context or response header
func (e *LogEntry) RequestID() string {
	// First try to get from context storage
	if id := GetRequestID(e.req); id != "" {
		return id
	}

	// Fallback: get from response header (set by RequestIDMiddleware)
	if rw := GetResponseWriter(e.req); rw != nil {
		if id := rw.Header().Get("X-Request-ID"); id != "" {
			return id
		}
	}

	return ""
}

// UserID returns the user ID from context
func (e *LogEntry) UserID() string {
	return GetUserID(e.req)
}

// CustomVal retrieves a custom value from ResponseWriter or request context
func (e *LogEntry) CustomVal(key string) string {
	// First check ResponseWriter custom data
	if e.rw != nil {
		if val, ok := e.rw.Get(key); ok {
			return formatValue(val)
		}
	}

	// Fallback to old storage method
	if val, ok := Get(e.req, key); ok {
		return formatValue(val)
	}
	return "-"
}

// formatValue converts various types to string
func formatValue(val interface{}) string {
	switch v := val.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}

// formatLog formats the log entry according to the template
func formatLog(template string, entry *LogEntry) string {
	return logTemplateRegex.ReplaceAllStringFunc(template, func(match string) string {
		token := strings.Trim(match, "{}")

		if strings.HasPrefix(token, "$") {
			key := strings.TrimPrefix(token, "$")
			return entry.CustomVal(key)
		}

		// Standard fields
		switch token {
		case "method":
			return entry.Method()
		case "path":
			return entry.Path()
		case "status":
			return entry.Status()
		case "size":
			return entry.Size()
		case "latency":
			return entry.Latency()
		case "latency_ms":
			return entry.LatencyMs()
		case "remote_addr":
			return entry.RemoteAddr()
		case "user_agent":
			return entry.UserAgent()
		case "referer":
			return entry.Referer()
		case "proto":
			return entry.Proto()
		case "host":
			return entry.Host()
		case "request_id":
			return entry.RequestID()
		case "user_id":
			return entry.UserID()
		default:
			// Fallback: try as custom field
			return entry.CustomVal(token)
		}
	})
}

// startTimeKey is the request-storage key under which LoggerMiddleware records
// when a request began, for LoggerAfter to read.
const startTimeKey = "surf.start_time"

// requestStartTime returns the start time LoggerMiddleware recorded on r.
func requestStartTime(r *http.Request) (time.Time, bool) {
	if v, ok := Get(r, startTimeKey); ok {
		if t, ok := v.(time.Time); ok {
			return t, true
		}
	}
	return time.Time{}, false
}

// LoggerMiddleware records the request's start time for LoggerAfter to report.
// Use it as a Before handler, with LoggerAfter as the matching After handler.
//
// The start time is stored on the request itself. It used to live in a
// map[*http.Request]time.Time owned by this closure, which was wrong twice
// over: LoggerAfter constructed its OWN map and so never found the entry (every
// latency it logged was ~0), and nothing ever drained LoggerMiddleware's map, so
// each request stayed reachable for the life of the process.
func LoggerMiddleware(format string) HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		Store(r, startTimeKey, time.Now())
		return nil
	}
}

// LoggerAfter creates the After middleware for logging
func LoggerAfter(format string) HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		start, ok := requestStartTime(r)
		if !ok {
			start = time.Now()
		}

		// Get the ResponseWriter from context
		rw := GetResponseWriter(r)
		if rw == nil {
			return nil
		}

		entry := &LogEntry{
			req:     r,
			status:  rw.Status(),
			size:    rw.Size(),
			latency: time.Since(start),
		}

		slog.Info(formatLog(format, entry))
		return nil
	}
}

// SimpleLogger creates just an After middleware for logging (no Before needed)
func SimpleLogger(format string) HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		// Get the ResponseWriter from context
		rw := GetResponseWriter(r)
		if rw == nil {
			return nil
		}

		entry := &LogEntry{
			req:     r,
			status:  rw.Status(),
			size:    rw.Size(),
			latency: rw.Latency(),
			rw:      rw, // Add reference to ResponseWriter for custom data
		}

		slog.Info(formatLog(format, entry))

		return nil
	}
}

// LoggingMiddleware creates a standard logging middleware
func LoggingMiddleware(format string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Wrap the response writer
			rw := NewResponseWriter(w)
			rw.StartTime = time.Now()

			// Call next handler with wrapped writer
			next.ServeHTTP(rw, r)

			// Log after the request is complete
			entry := &LogEntry{
				req:     r,
				status:  rw.Status(),
				size:    rw.Size(),
				latency: rw.Latency(),
				rw:      rw,
			}

			slog.Info(formatLog(format, entry))
		})
	}
}

// LoggingConfig configures LoggingMiddlewareWithConfig.
type LoggingConfig struct {
	// Format is the log template (e.g. "{method} {path} {status}"). When
	// empty, a sensible default is used.
	Format string

	// SkipPaths lists request paths excluded from logging. A path ending in
	// "*" matches by prefix (e.g. "/health/*"); others match exactly.
	SkipPaths []string
}

// LoggingMiddlewareWithConfig creates a logging middleware that can exclude
// paths (such as health probes) from the log via SkipPaths.
func LoggingMiddlewareWithConfig(config LoggingConfig) Middleware {
	format := config.Format
	if format == "" {
		format = "{method} {path} {status} {latency_ms}ms"
	}
	skip := config.SkipPaths

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if matchAnyGlob(r.URL.Path, skip) {
				next.ServeHTTP(w, r)
				return
			}

			rw := NewResponseWriter(w)
			rw.StartTime = time.Now()
			next.ServeHTTP(rw, r)

			entry := &LogEntry{
				req:     r,
				status:  rw.Status(),
				size:    rw.Size(),
				latency: rw.Latency(),
				rw:      rw,
			}
			slog.Info(formatLog(format, entry))
		})
	}
}

// Logger creates a standard HTTP middleware for logging
func Logger(format string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap response writer to capture status and size
			wrapped := NewResponseWriter(w)

			// Execute the chain
			next.ServeHTTP(wrapped, r)

			// Create log entry
			entry := &LogEntry{
				req:     r,
				status:  wrapped.Status(),
				size:    wrapped.Size(),
				latency: time.Since(start),
			}

			slog.Info(formatLog(format, entry))
		})
	}
}

// RequestIDMiddleware adds a unique request ID to each request (standard middleware)
func RequestIDMiddleware(prefix string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := generateRequestID(prefix)

			// Add to request context
			ctx := context.WithValue(r.Context(), contextKey("request_id"), requestID)
			r = r.WithContext(ctx)

			// Store in ResponseWriter if it's our custom type
			if rw, ok := w.(*ResponseWriter); ok {
				rw.Set("request_id", requestID)
			}

			// Also add to response header for tracing
			setKnownHeader(w.Header(), headerXRequestID, requestID)

			next.ServeHTTP(w, r)
		})
	}
}

// RequestIDFunc creates a middleware function that adds request IDs
func RequestIDFunc(prefix string) MiddlewareFunc {
	return func(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
		requestID := generateRequestID(prefix)

		// Add to request context
		ctx := context.WithValue(r.Context(), contextKey("request_id"), requestID)
		r = r.WithContext(ctx)

		// Store in ResponseWriter if it's our custom type
		if rw, ok := w.(*ResponseWriter); ok {
			rw.Set("request_id", requestID)
		}

		// Also add to response header for tracing
		setKnownHeader(w.Header(), headerXRequestID, requestID)

		next(w, r)
	}
}

// RequestID creates a standard HTTP middleware for request IDs
func RequestID(prefix string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := generateRequestID(prefix)

			// Add to context
			SetRequestID(&r, requestID)

			// Also add to response header
			setKnownHeader(w.Header(), headerXRequestID, requestID)

			next.ServeHTTP(w, r)
		})
	}
}

// generateRequestID creates a unique request ID with sufficient entropy
func generateRequestID(prefix string) string {
	// Generate 16 random bytes (128 bits) for sufficient uniqueness
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID if crypto/rand fails
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	id := hex.EncodeToString(b)

	if prefix != "" {
		hostname, _ := os.Hostname()
		if hostname != "" {
			return fmt.Sprintf("%s-%s-%s", prefix, hostname, id)
		}
		return fmt.Sprintf("%s-%s", prefix, id)
	}
	return id
}

// RequestLoggerOptions configures the RequestLogger middleware behavior
type RequestLoggerOptions struct {
	Logger             *slog.Logger
	Level              slog.Level
	IncludeReqHeaders  bool
	IncludeRespHeaders bool
	HeaderFilter       func(key string) bool // Optional filter to include/exclude specific headers
	GroupHeaders       bool                  // Group headers under "request_headers" and "response_headers"
}

// DefaultRequestLoggerOptions returns default options for RequestLogger
func DefaultRequestLoggerOptions() *RequestLoggerOptions {
	return &RequestLoggerOptions{
		Logger:             slog.Default(),
		Level:              slog.LevelInfo,
		IncludeReqHeaders:  false,
		IncludeRespHeaders: false,
		GroupHeaders:       true,
		HeaderFilter:       nil, // Include all headers by default
	}
}

// RequestLogger creates a structured request logging middleware using slog
func RequestLogger(logger *slog.Logger) Middleware {
	opts := &RequestLoggerOptions{
		Logger: logger,
		Level:  slog.LevelInfo,
	}
	return RequestLoggerWithOptions(opts)
}

// RequestLoggerWithOptions creates a configurable structured request logging middleware
func RequestLoggerWithOptions(opts *RequestLoggerOptions) Middleware {
	if opts == nil {
		opts = DefaultRequestLoggerOptions()
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Wrap the response writer
			rw := NewResponseWriter(w)
			rw.StartTime = time.Now()

			// Call next handler with wrapped writer
			next.ServeHTTP(rw, r)

			// Create structured log entry
			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rw.Status()),
				slog.Int("size", rw.Size()),
				slog.Duration("latency", rw.Latency()),
				slog.String("remote_addr", r.RemoteAddr),
				slog.String("user_agent", r.UserAgent()),
			}

			// Add request ID from ResponseWriter or context
			if requestID, ok := rw.Get("request_id"); ok {
				attrs = append(attrs, slog.Any("request_id", requestID))
			} else if requestID := r.Context().Value(contextKey("request_id")); requestID != nil {
				attrs = append(attrs, slog.Any("request_id", requestID))
			}

			// Add request headers if enabled
			if opts.IncludeReqHeaders {
				headerAttrs := make([]any, 0)
				for key, values := range r.Header {
					if opts.HeaderFilter == nil || opts.HeaderFilter(key) {
						// Join multiple header values with comma
						headerAttrs = append(headerAttrs, slog.String(key, strings.Join(values, ", ")))
					}
				}
				if len(headerAttrs) > 0 {
					if opts.GroupHeaders {
						attrs = append(attrs, slog.Group("request_headers", headerAttrs...))
					} else {
						for i := 0; i < len(headerAttrs); i++ {
							if attr, ok := headerAttrs[i].(slog.Attr); ok {
								attrs = append(attrs, slog.Any("req_header_"+attr.Key, attr.Value))
							}
						}
					}
				}
			}

			// Add response headers if enabled
			if opts.IncludeRespHeaders {
				headerAttrs := make([]any, 0)
				for key, values := range rw.Header() {
					if opts.HeaderFilter == nil || opts.HeaderFilter(key) {
						headerAttrs = append(headerAttrs, slog.String(key, strings.Join(values, ", ")))
					}
				}
				if len(headerAttrs) > 0 {
					if opts.GroupHeaders {
						attrs = append(attrs, slog.Group("response_headers", headerAttrs...))
					} else {
						for i := 0; i < len(headerAttrs); i++ {
							if attr, ok := headerAttrs[i].(slog.Attr); ok {
								attrs = append(attrs, slog.Any("resp_header_"+attr.Key, attr.Value))
							}
						}
					}
				}
			}

			// Add any other custom data from ResponseWriter (thread-safe copy)
			for key, value := range rw.CustomData() {
				if key != "request_id" { // Already handled above
					attrs = append(attrs, slog.Any(key, value))
				}
			}

			// Log the request
			opts.Logger.LogAttrs(context.Background(), opts.Level, "HTTP Request", attrs...)
		})
	}
}

// SlogMiddlewareWithLevel creates a structured logging middleware with custom log level
func SlogMiddlewareWithLevel(logger *slog.Logger, level slog.Level) Middleware {
	if logger == nil {
		logger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Wrap the response writer
			rw := NewResponseWriter(w)
			rw.StartTime = time.Now()

			// Call next handler with wrapped writer
			next.ServeHTTP(rw, r)

			// Create structured log entry
			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rw.Status()),
				slog.Int("size", rw.Size()),
				slog.Duration("latency", rw.Latency()),
				slog.String("remote_addr", r.RemoteAddr),
				slog.String("user_agent", r.UserAgent()),
			}

			// Add custom fields from ResponseWriter (thread-safe copy)
			for key, value := range rw.CustomData() {
				attrs = append(attrs, slog.Any(key, value))
			}

			// Log with custom level
			logger.LogAttrs(context.Background(), level, "HTTP Request", attrs...)
		})
	}
}

// ReefCompatibleMiddleware creates logging middleware compatible with reef package
func ReefCompatibleMiddleware(logger *slog.Logger) Middleware {
	if logger == nil {
		logger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Wrap the response writer
			rw := NewResponseWriter(w)
			rw.StartTime = time.Now()

			// Call next handler with wrapped writer
			next.ServeHTTP(rw, r)

			// Create reef-style structured log
			logEntry := logger.With(
				"http.method", r.Method,
				"http.path", r.URL.Path,
				"http.status", rw.Status(),
				"http.size", rw.Size(),
				"http.latency", rw.Latency(),
				"http.remote_addr", r.RemoteAddr,
				"http.user_agent", r.UserAgent(),
			)

			// Add custom fields with namespacing (thread-safe copy)
			for key, value := range rw.CustomData() {
				logEntry = logEntry.With(fmt.Sprintf("app.%s", key), value)
			}

			// Log with reef-compatible structure
			logEntry.Info("HTTP request processed")
		})
	}
}

// CombinedMiddleware logs to both traditional log and slog
func CombinedMiddleware(format string, slogger *slog.Logger) Middleware {
	if slogger == nil {
		slogger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Wrap the response writer
			rw := NewResponseWriter(w)
			rw.StartTime = time.Now()

			// Call next handler with wrapped writer
			next.ServeHTTP(rw, r)

			// Create LogEntry for template formatting
			entry := &LogEntry{
				req:     r,
				status:  rw.Status(),
				size:    rw.Size(),
				latency: rw.Latency(),
				rw:      rw,
			}

			// Log with traditional logger using template
			slog.Info(formatLog(format, entry))

			// Also log with structured slog
			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rw.Status()),
				slog.Int("size", rw.Size()),
				slog.Duration("latency", rw.Latency()),
			}

			// Add custom fields (thread-safe copy)
			for key, value := range rw.CustomData() {
				attrs = append(attrs, slog.Any(key, value))
			}

			slogger.LogAttrs(context.Background(), slog.LevelInfo, "HTTP Request", attrs...)
		})
	}
}

// SlogOptions is an alias retained for backward compatibility.
//
// Deprecated: Use RequestLoggerOptions instead.
type SlogOptions = RequestLoggerOptions

// Deprecated: Use DefaultRequestLoggerOptions instead
func DefaultSlogOptions() *RequestLoggerOptions {
	return DefaultRequestLoggerOptions()
}

// Deprecated: Use RequestLogger instead
func SlogMiddleware(logger *slog.Logger) Middleware {
	return RequestLogger(logger)
}

// Deprecated: Use RequestLoggerWithOptions instead
func SlogMiddlewareWithOptions(opts *RequestLoggerOptions) Middleware {
	return RequestLoggerWithOptions(opts)
}

// Context keys for logger
type loggerKey struct{}

// WithRequestLogger adds a logger to the request context
func WithRequestLogger(r *http.Request, logger *slog.Logger) *http.Request {
	ctx := context.WithValue(r.Context(), loggerKey{}, logger)
	return r.WithContext(ctx)
}

// GetRequestLogger retrieves a logger from the request context
// If no logger is found, returns the default slog logger
func GetRequestLogger(r *http.Request) *slog.Logger {
	if logger, ok := r.Context().Value(loggerKey{}).(*slog.Logger); ok {
		return logger
	}
	return slog.Default()
}

// LoggerFromRequest creates a logger with request context (including request_id)
// This allows application logic to log with the same request_id as the HTTP logs
func LoggerFromRequest(r *http.Request, baseLogger *slog.Logger) *slog.Logger {
	if baseLogger == nil {
		baseLogger = slog.Default()
	}

	// Start with base logger
	logger := baseLogger

	// Add request ID if available
	if requestID := GetRequestID(r); requestID != "" {
		logger = logger.With("request_id", requestID)
	}

	// Add any other context values you might want
	if userID := GetUserID(r); userID != "" {
		logger = logger.With("user_id", userID)
	}

	return logger
}

// RequestLoggerInjector creates middleware that injects a context-aware logger into each request
// This logger will automatically include request_id and other context values
func RequestLoggerInjector(baseLogger *slog.Logger) Middleware {
	if baseLogger == nil {
		baseLogger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Create a logger with request context
			logger := LoggerFromRequest(r, baseLogger)

			// Add logger to request context
			r = WithRequestLogger(r, logger)

			next.ServeHTTP(w, r)
		})
	}
}
