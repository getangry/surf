package surf

import (
	"errors"
	"net/http"
)

// Abort is a sentinel error a handler (or before/after handler) may return to
// signal "the response has been fully written, stop processing, and do not
// render an error". The router treats it as successful completion: nothing is
// logged and nothing further is written.
//
// Returning Abort is the explicit, framework-aware replacement for the
// http.ErrAbortHandler pattern. errors.Is(err, http.ErrAbortHandler) is also
// honored for compatibility.
var Abort = errors.New("surf: handler aborted")

// HTTPError is an error that carries an HTTP status code and a client-safe
// message. When a handler returns an error that wraps an *HTTPError, the error
// renderer uses its Code and Message instead of a generic 500. The optional
// Err field is logged but never sent to the client.
type HTTPError struct {
	Code    int
	Message string
	Err     error
}

// Error implements the error interface.
func (e *HTTPError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return http.StatusText(e.Code)
}

// Unwrap exposes the underlying error for errors.Is / errors.As.
func (e *HTTPError) Unwrap() error { return e.Err }

// NewHTTPError builds an *HTTPError with the given status code and message.
func NewHTTPError(code int, message string) *HTTPError {
	return &HTTPError{Code: code, Message: message}
}

// Errorf is a convenience for wrapping an underlying error with a status code
// and a client-safe message.
func Errorf(code int, message string, cause error) *HTTPError {
	return &HTTPError{Code: code, Message: message, Err: cause}
}

// ErrorRenderer renders a handler-returned error to the client. It is only
// invoked when the response has not already been written.
type ErrorRenderer func(w http.ResponseWriter, r *http.Request, err error)

// DefaultErrorRenderer writes a JSON error envelope. If err wraps an
// *HTTPError, its Code and Message drive the response; otherwise a generic 500
// is sent so internal details are never leaked.
func DefaultErrorRenderer(w http.ResponseWriter, r *http.Request, err error) {
	code := http.StatusInternalServerError
	message := http.StatusText(http.StatusInternalServerError)

	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		if httpErr.Code != 0 {
			code = httpErr.Code
		}
		if httpErr.Message != "" {
			message = httpErr.Message
		}
	}

	_ = JSONError(w, code, message)
}
