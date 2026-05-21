package surf

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// ResponseWriter wraps http.ResponseWriter to track response metrics and store custom data
type ResponseWriter struct {
	http.ResponseWriter
	status      int
	size        int
	written     bool
	wroteHeader bool
	startTime   time.Time
	customData  map[string]interface{}
	mu          sync.RWMutex
}

// NewResponseWriter creates a new ResponseWriter. The customData map is
// allocated lazily on first Set, so a response that stores no custom data
// costs nothing extra.
func NewResponseWriter(w http.ResponseWriter) *ResponseWriter {
	return &ResponseWriter{
		ResponseWriter: w,
		status:         http.StatusOK,
		startTime:      time.Now(),
	}
}

// initWriter wires up the zero-valued ResponseWriter embedded in a reqState or
// Context to wrap w, avoiding a separate ResponseWriter allocation per request.
func (rw *ResponseWriter) initWriter(w http.ResponseWriter) {
	rw.ResponseWriter = w
	rw.status = http.StatusOK
	rw.startTime = time.Now()
}

// recycle clears the ResponseWriter so the pooled Context that owns it can be
// reused by a later request.
func (rw *ResponseWriter) recycle() {
	rw.ResponseWriter = nil
	rw.status = 0
	rw.size = 0
	rw.written = false
	rw.wroteHeader = false
	if rw.customData != nil {
		clear(rw.customData)
	}
}

// WriteString writes s to the response, tracking size like Write. It lets
// callers avoid a []byte conversion for string responses.
func (rw *ResponseWriter) WriteString(s string) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	n, err := io.WriteString(rw.ResponseWriter, s)
	rw.size += n
	rw.written = true
	return n, err
}

// WriteHeader captures the status code and writes the header
func (rw *ResponseWriter) WriteHeader(status int) {
	if rw.wroteHeader {
		return
	}
	rw.status = status
	rw.wroteHeader = true
	rw.ResponseWriter.WriteHeader(status)
}

// Write writes data to the response and tracks the size
func (rw *ResponseWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.size += n
	rw.written = true
	return n, err
}

// Status returns the response status code
func (rw *ResponseWriter) Status() int {
	return rw.status
}

// Size returns the response size in bytes
func (rw *ResponseWriter) Size() int {
	return rw.size
}

// Written returns whether the response has been written
func (rw *ResponseWriter) Written() bool {
	return rw.written
}

// StartTime returns the request start time
func (rw *ResponseWriter) StartTime() time.Time {
	return rw.startTime
}

// Latency returns the elapsed time since request start
func (rw *ResponseWriter) Latency() time.Duration {
	return time.Since(rw.startTime)
}

// Set adds a custom value to the ResponseWriter (thread-safe)
func (rw *ResponseWriter) Set(key string, value interface{}) {
	rw.mu.Lock()
	if rw.customData == nil {
		rw.customData = make(map[string]interface{})
	}
	rw.customData[key] = value
	rw.mu.Unlock()
}

// Get retrieves a custom value from the ResponseWriter (thread-safe)
func (rw *ResponseWriter) Get(key string) (interface{}, bool) {
	rw.mu.RLock()
	defer rw.mu.RUnlock()
	val, ok := rw.customData[key]
	return val, ok
}

// GetString retrieves a string value with a default (thread-safe)
func (rw *ResponseWriter) GetString(key string, defaultVal string) string {
	if val, ok := rw.Get(key); ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return defaultVal
}

// CustomData returns a copy of the custom data map (thread-safe)
func (rw *ResponseWriter) CustomData() map[string]interface{} {
	rw.mu.RLock()
	defer rw.mu.RUnlock()
	copy := make(map[string]interface{}, len(rw.customData))
	for k, v := range rw.customData {
		copy[k] = v
	}
	return copy
}

// Hijack implements the http.Hijacker interface
func (rw *ResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, errors.New("response writer does not support hijacking")
}

// Flush implements the http.Flusher interface
func (rw *ResponseWriter) Flush() {
	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Push implements the http.Pusher interface
func (rw *ResponseWriter) Push(target string, opts *http.PushOptions) error {
	if pusher, ok := rw.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, opts)
	}
	return errors.New("response writer does not support push")
}

// CloseNotify implements the http.CloseNotifier interface.
// Deprecated: Use http.ResponseController or context.Done() instead.
// This method is kept for backwards compatibility but may return nil
// if the underlying ResponseWriter doesn't support it.
func (rw *ResponseWriter) CloseNotify() <-chan bool {
	//nolint:staticcheck // Keeping for backwards compatibility
	if notifier, ok := rw.ResponseWriter.(http.CloseNotifier); ok {
		return notifier.CloseNotify()
	}
	// Return a closed channel instead of nil to prevent nil channel receive bugs
	ch := make(chan bool)
	close(ch)
	return ch
}

// ResponseController returns the response controller for this response
func (rw *ResponseWriter) ResponseController() *http.ResponseController {
	return http.NewResponseController(rw)
}

// GetResponseWriter retrieves the ResponseWriter from the request context.
// It returns nil before the router has begun handling the request.
func GetResponseWriter(r *http.Request) *ResponseWriter {
	if st := stateFromRequest(r); st != nil && st.rw.ResponseWriter != nil {
		return &st.rw
	}
	return nil
}

// ResponseStatus returns the response status code from the context
func ResponseStatus(r *http.Request) int {
	if rw := GetResponseWriter(r); rw != nil {
		return rw.Status()
	}
	return 0
}

// ResponseSize returns the response size in bytes from the context
func ResponseSize(r *http.Request) int {
	if rw := GetResponseWriter(r); rw != nil {
		return rw.Size()
	}
	return 0
}
