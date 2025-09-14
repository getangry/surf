package surf

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Logger context helpers

// Global storage for request context values (since the framework doesn't preserve context changes)
var requestStorage = make(map[*http.Request]map[string]interface{})

// Set adds a value to the request storage (framework limitation workaround)
func Set(r **http.Request, key string, value interface{}) {
	if requestStorage[*r] == nil {
		requestStorage[*r] = make(map[string]interface{})
	}
	requestStorage[*r][key] = value
}

// SetMultiple adds multiple values at once to request storage
func SetMultiple(r **http.Request, values map[string]interface{}) {
	if requestStorage[*r] == nil {
		requestStorage[*r] = make(map[string]interface{})
	}
	for key, value := range values {
		requestStorage[*r][key] = value
	}
}

// Store directly sets a value for a request (internal use)
func Store(r *http.Request, key string, value interface{}) {
	if requestStorage[r] == nil {
		requestStorage[r] = make(map[string]interface{})
	}
	requestStorage[r][key] = value
}

// Get retrieves a value from the request storage or context
func Get(r *http.Request, key string) (interface{}, bool) {
	// First check our global storage
	if storage, exists := requestStorage[r]; exists {
		if val, ok := storage[key]; ok {
			return val, true
		}
	}

	// Fallback to context
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
func GetService[T any](r *http.Request, key any) T {
	if app, ok := r.Context().Value(appKey{}).(*App); ok {
		if service := app.GetService(key); service != nil {
			return service.(T)
		}
	}
	var zero T
	return zero
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
	// Regex to find all tokens
	re := regexp.MustCompile(`\{([^}]+)\}`)

	return re.ReplaceAllStringFunc(template, func(match string) string {
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

// LoggerMiddleware creates a simple After middleware for logging
// Since the framework doesn't propagate context changes from Before middlewares,
// we'll use a global map to track start times by request
func LoggerMiddleware(format string) HandlerFunc {
	startTimes := make(map[*http.Request]time.Time)

	// This should be used as a Before middleware
	return func(w http.ResponseWriter, r *http.Request) error {
		startTimes[r] = time.Now()
		return nil
	}
}

// LoggerAfter creates the After middleware for logging
func LoggerAfter(format string) HandlerFunc {
	startTimes := make(map[*http.Request]time.Time)

	return func(w http.ResponseWriter, r *http.Request) error {
		start, ok := startTimes[r]
		if !ok {
			start = time.Now()
		}

		// Clean up
		delete(startTimes, r)

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

		log.Println(formatLog(format, entry))
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

		log.Println(formatLog(format, entry))

		// Clean up request storage after logging
		delete(requestStorage, r)

		return nil
	}
}

// LoggingMiddleware creates a standard logging middleware
func LoggingMiddleware(format string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Wrap the response writer
			rw := NewResponseWriter(w)

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

			log.Println(formatLog(format, entry))
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

			log.Println(formatLog(format, entry))
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
			w.Header().Set("X-Request-ID", requestID)

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
		w.Header().Set("X-Request-ID", requestID)

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
			w.Header().Set("X-Request-ID", requestID)

			next.ServeHTTP(w, r)
		})
	}
}

// generateRequestID creates a unique request ID
func generateRequestID(prefix string) string {
	// Generate random bytes
	b := make([]byte, 4)
	rand.Read(b)
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

// SlogMiddleware creates a structured logging middleware using slog
func SlogMiddleware(logger *slog.Logger) Middleware {
	if logger == nil {
		logger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Wrap the response writer
			rw := NewResponseWriter(w)

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

			// Add custom fields from ResponseWriter
			if requestID, ok := rw.Get("request_id"); ok {
				attrs = append(attrs, slog.Any("request_id", requestID))
			}

			// Add any other custom data from ResponseWriter
			for key, value := range rw.customData {
				if key != "request_id" { // Already handled above
					attrs = append(attrs, slog.Any(key, value))
				}
			}

			// Log the request
			logger.LogAttrs(context.Background(), slog.LevelInfo, "HTTP Request", attrs...)
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

			// Add custom fields from ResponseWriter
			for key, value := range rw.customData {
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

			// Add custom fields with namespacing
			for key, value := range rw.customData {
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
			log.Println(formatLog(format, entry))

			// Also log with structured slog
			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rw.Status()),
				slog.Int("size", rw.Size()),
				slog.Duration("latency", rw.Latency()),
			}

			// Add custom fields
			for key, value := range rw.customData {
				attrs = append(attrs, slog.Any(key, value))
			}

			slogger.LogAttrs(context.Background(), slog.LevelInfo, "HTTP Request", attrs...)
		})
	}
}
