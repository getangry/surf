package surf

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type signupBody struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

func (b signupBody) Validate() error {
	if b.Name == "" {
		return errors.New("name is required")
	}
	return nil
}

func newJSONRequest(body string) *http.Request {
	r := httptest.NewRequest("POST", "/", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestBindValid(t *testing.T) {
	var b signupBody
	if err := Bind(newJSONRequest(`{"name":"Ada","email":"a@b.c"}`), &b); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if b.Name != "Ada" {
		t.Errorf("name = %q", b.Name)
	}
}

func TestBindMalformed(t *testing.T) {
	var b signupBody
	err := Bind(newJSONRequest(`{"name":`), &b)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Code != http.StatusBadRequest {
		t.Fatalf("err = %v, want 400 HTTPError", err)
	}
}

func TestBindTrailingContent(t *testing.T) {
	var b signupBody
	err := Bind(newJSONRequest(`{"name":"Ada"}{"x":1}`), &b)
	if err == nil {
		t.Fatal("expected error for trailing content")
	}
}

func TestBindWrongContentType(t *testing.T) {
	var b signupBody
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"Ada"}`))
	r.Header.Set("Content-Type", "text/plain")
	err := Bind(r, &b)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("err = %v, want 415 HTTPError", err)
	}
}

func TestBindWithLimit(t *testing.T) {
	var b signupBody
	err := BindWithLimit(newJSONRequest(`{"name":"AdaLovelace"}`), &b, 8)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("err = %v, want 413 HTTPError", err)
	}
}

func TestBindAndValidate(t *testing.T) {
	var b signupBody
	err := BindAndValidate(newJSONRequest(`{"email":"a@b.c"}`), &b)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("err = %v, want 422 HTTPError", err)
	}
	if !contains(httpErr.Message, "name is required") {
		t.Errorf("message = %q", httpErr.Message)
	}
}
