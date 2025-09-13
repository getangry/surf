package surf

import (
	"bufio"
	"errors"
	"net"
	"net/http"
)

// ResponseWriter wraps http.ResponseWriter to track response metrics
type ResponseWriter struct {
	http.ResponseWriter
	status      int
	size        int
	written     bool
	wroteHeader bool
}

// NewResponseWriter creates a new ResponseWriter
func NewResponseWriter(w http.ResponseWriter) *ResponseWriter {
	return &ResponseWriter{
		ResponseWriter: w,
		status:         http.StatusOK,
	}
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

// CloseNotify implements the http.CloseNotifier interface (deprecated)
func (rw *ResponseWriter) CloseNotify() <-chan bool {
	if notifier, ok := rw.ResponseWriter.(http.CloseNotifier); ok {
		return notifier.CloseNotify()
	}
	return nil
}

// ResponseController returns the response controller for this response
func (rw *ResponseWriter) ResponseController() *http.ResponseController {
	return http.NewResponseController(rw)
}

// GetResponseWriter retrieves the ResponseWriter from the request context
func GetResponseWriter(r *http.Request) *ResponseWriter {
	if rw, ok := r.Context().Value(responseKey{}).(*ResponseWriter); ok {
		return rw
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