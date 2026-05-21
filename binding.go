package surf

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// DefaultMaxBodyBytes is the request body size limit applied by Bind. Use
// BindWithLimit to override it per call.
const DefaultMaxBodyBytes int64 = 1 << 20 // 1 MiB

// Validator is implemented by types that can validate themselves after
// binding. When a value passed to BindAndValidate implements Validator, its
// Validate method runs and any returned error becomes a 422 response.
type Validator interface {
	Validate() error
}

// Bind decodes the JSON request body into v, enforcing DefaultMaxBodyBytes.
// On failure it returns an *HTTPError carrying the appropriate status code
// (400 for malformed JSON, 413 when the limit is exceeded), so a handler can
// simply `return surf.Bind(r, &body)` and let the error renderer respond.
func Bind(r *http.Request, v any) error {
	return BindWithLimit(r, v, DefaultMaxBodyBytes)
}

// BindWithLimit behaves like Bind but enforces a caller-supplied byte limit.
func BindWithLimit(r *http.Request, v any, maxBytes int64) error {
	if r.Body == nil || r.Body == http.NoBody {
		return NewHTTPError(http.StatusBadRequest, "request body is empty")
	}

	if ct := r.Header.Get("Content-Type"); ct != "" {
		if mt := strings.TrimSpace(strings.SplitN(ct, ";", 2)[0]); mt != "" && mt != "application/json" {
			return NewHTTPError(http.StatusUnsupportedMediaType, "expected application/json request body")
		}
	}

	r.Body = http.MaxBytesReader(nil, r.Body, maxBytes)
	defer r.Body.Close()

	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		var maxErr *http.MaxBytesError
		switch {
		case errors.As(err, &maxErr):
			return NewHTTPError(http.StatusRequestEntityTooLarge, "request body too large")
		case errors.Is(err, io.EOF):
			return NewHTTPError(http.StatusBadRequest, "request body is empty")
		default:
			return &HTTPError{Code: http.StatusBadRequest, Message: "invalid JSON request body", Err: err}
		}
	}

	// Reject trailing content after the first JSON value.
	if dec.More() {
		return NewHTTPError(http.StatusBadRequest, "request body must contain a single JSON value")
	}
	return nil
}

// BindAndValidate binds the JSON body into v and, when v implements Validator,
// runs validation. A validation error is returned as a 422 *HTTPError.
func BindAndValidate(r *http.Request, v any) error {
	if err := Bind(r, v); err != nil {
		return err
	}
	if val, ok := v.(Validator); ok {
		if err := val.Validate(); err != nil {
			return &HTTPError{Code: http.StatusUnprocessableEntity, Message: err.Error(), Err: err}
		}
	}
	return nil
}
