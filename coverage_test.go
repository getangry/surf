package surf

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPErrorMessageFallbacks(t *testing.T) {
	// Message set: returned verbatim.
	if got := NewHTTPError(400, "bad").Error(); got != "bad" {
		t.Errorf("Error() = %q, want bad", got)
	}
	// No message, underlying error set: delegates to the cause.
	if got := (&HTTPError{Code: 500, Err: errExample}).Error(); got != errExample.Error() {
		t.Errorf("Error() = %q, want %q", got, errExample.Error())
	}
	// No message, no cause: falls back to the canonical status text.
	if got := (&HTTPError{Code: http.StatusNotFound}).Error(); got != "Not Found" {
		t.Errorf("Error() = %q, want Not Found", got)
	}
}

func TestJSONDataStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := JSONDataStatus(rec, http.StatusCreated, map[string]int{"id": 7}); err != nil {
		t.Fatalf("JSONDataStatus: %v", err)
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("code = %d, want 201", rec.Code)
	}
	var got struct {
		Data map[string]int `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Data["id"] != 7 {
		t.Errorf("data = %v", got.Data)
	}
}

func TestBindAndValidateSuccess(t *testing.T) {
	var b signupBody
	if err := BindAndValidate(newJSONRequest(`{"name":"Ada","email":"a@b.c"}`), &b); err != nil {
		t.Fatalf("BindAndValidate: %v", err)
	}
	if b.Name != "Ada" {
		t.Errorf("name = %q", b.Name)
	}
}

func TestMustServiceSuccess(t *testing.T) {
	app := NewApp()
	Provide[*enGreeter](app, &enGreeter{})
	var greeting string
	app.Get("/x", func(w http.ResponseWriter, r *http.Request) error {
		greeting = MustService[*enGreeter](r).Greet()
		return nil
	})
	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/x", nil))
	if greeting != "hello" {
		t.Errorf("greeting = %q, want hello", greeting)
	}
}

func TestIPFromRequestBareProxyIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.5:443"
	r.Header.Set("X-Forwarded-For", "198.51.100.9")
	// A bare IP (no CIDR) in the trusted list is treated as a /32.
	if ip := IPFromRequest(r, []string{"10.0.0.5"}); ip != "198.51.100.9" {
		t.Errorf("ip = %q, want 198.51.100.9", ip)
	}
}

func TestIPFromRequestIPv6(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "[::1]:8080"
	r.Header.Set("X-Forwarded-For", "2001:db8::42")
	if ip := IPFromRequest(r, []string{"::1/128"}); ip != "2001:db8::42" {
		t.Errorf("ip = %q, want 2001:db8::42", ip)
	}
}

func TestIPFromRequestInvalidProxyEntry(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.7:9000"
	// Garbage entries are skipped; peer is then untrusted, header ignored.
	if ip := IPFromRequest(r, []string{"not-an-ip", ""}); ip != "203.0.113.7" {
		t.Errorf("ip = %q, want 203.0.113.7", ip)
	}
}
