package surf

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSetAcceptQuery(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		rec := httptest.NewRecorder()
		SetAcceptQuery(rec)
		if got := rec.Header().Get("Accept-Query"); got != "application/json" {
			t.Errorf("Accept-Query = %q, want application/json", got)
		}
	})
	t.Run("custom", func(t *testing.T) {
		rec := httptest.NewRecorder()
		SetAcceptQuery(rec, "application/json", "application/sql")
		if got := rec.Header().Get("Accept-Query"); got != "application/json, application/sql" {
			t.Errorf("Accept-Query = %q", got)
		}
	})
}

// TestAcceptQueryAdvertise covers the automatic Accept-Query header (RFC 10008
// §3) on both the OPTIONS discovery path and the 405 path, plus WithAcceptQuery
// configuration and suppression.
func TestAcceptQueryAdvertise(t *testing.T) {
	t.Run("OPTIONS discovery advertises json", func(t *testing.T) {
		app := NewApp()
		app.Query("/search", func(w http.ResponseWriter, r *http.Request) error { return nil })

		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest("OPTIONS", "/search", nil))

		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
		if got := rec.Header().Get("Accept-Query"); got != "application/json" {
			t.Errorf("Accept-Query = %q, want application/json", got)
		}
	})

	t.Run("405 advertises too", func(t *testing.T) {
		app := NewApp()
		app.Query("/search", func(w http.ResponseWriter, r *http.Request) error { return nil })

		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest("GET", "/search", nil))

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", rec.Code)
		}
		if got := rec.Header().Get("Accept-Query"); got != "application/json" {
			t.Errorf("Accept-Query = %q, want application/json", got)
		}
	})

	t.Run("custom media types", func(t *testing.T) {
		app := NewApp(WithAcceptQuery("application/sql"))
		app.Query("/search", func(w http.ResponseWriter, r *http.Request) error { return nil })

		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest("GET", "/search", nil))

		if got := rec.Header().Get("Accept-Query"); got != "application/sql" {
			t.Errorf("Accept-Query = %q, want application/sql", got)
		}
	})

	t.Run("suppressed when empty", func(t *testing.T) {
		app := NewApp(WithAcceptQuery())
		app.Query("/search", func(w http.ResponseWriter, r *http.Request) error { return nil })

		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest("OPTIONS", "/search", nil))

		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
		if got := rec.Header().Get("Accept-Query"); got != "" {
			t.Errorf("Accept-Query = %q, want empty", got)
		}
	})

	t.Run("not advertised without a QUERY route", func(t *testing.T) {
		app := NewApp()
		app.Get("/search", func(w http.ResponseWriter, r *http.Request) error { return nil })

		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest("OPTIONS", "/search", nil))

		if got := rec.Header().Get("Accept-Query"); got != "" {
			t.Errorf("Accept-Query = %q, want empty (no QUERY route)", got)
		}
	})
}

// TestAcceptQueryNotLeakedOn2xx guards that Accept-Query is only advertised on
// the discovery paths, not on every successful QUERY response.
func TestAcceptQueryNotLeakedOn2xx(t *testing.T) {
	app := NewApp()
	app.Query("/search", func(w http.ResponseWriter, r *http.Request) error {
		w.WriteHeader(http.StatusOK)
		return nil
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("QUERY", "/search", strings.NewReader("{}"))
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Accept-Query"); got != "" {
		t.Errorf("Accept-Query = %q on a 200, want empty", got)
	}
}

// TestAutomaticOptions covers the built-in OPTIONS handler: a 204 with a sorted
// Allow header listing the path's methods plus OPTIONS itself.
func TestAutomaticOptions(t *testing.T) {
	app := NewApp()
	app.Get("/users", func(w http.ResponseWriter, r *http.Request) error { return nil })
	app.Query("/users", func(w http.ResponseWriter, r *http.Request) error { return nil })

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("OPTIONS", "/users", nil))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "GET, OPTIONS, QUERY" {
		t.Errorf("Allow = %q, want sorted \"GET, OPTIONS, QUERY\"", got)
	}
}

// TestAutomaticOptionsExplicitRouteWins verifies a registered OPTIONS route
// takes precedence over the automatic handler.
func TestAutomaticOptionsExplicitRouteWins(t *testing.T) {
	app := NewApp()
	app.Query("/users", func(w http.ResponseWriter, r *http.Request) error { return nil })
	app.Options("/users", func(w http.ResponseWriter, r *http.Request) error {
		w.Header().Set("X-Custom", "explicit")
		w.WriteHeader(http.StatusTeapot)
		return nil
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("OPTIONS", "/users", nil))

	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want 418 (explicit handler)", rec.Code)
	}
	if rec.Header().Get("X-Custom") != "explicit" {
		t.Error("explicit OPTIONS handler did not run")
	}
}

// TestWithoutAutomaticOptions verifies the opt-out restores OPTIONS -> 405.
func TestWithoutAutomaticOptions(t *testing.T) {
	app := NewApp(WithoutAutomaticOptions())
	app.Query("/users", func(w http.ResponseWriter, r *http.Request) error { return nil })

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("OPTIONS", "/users", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405 (automatic OPTIONS disabled)", rec.Code)
	}
	// Accept-Query is still advertised on the 405.
	if got := rec.Header().Get("Accept-Query"); got != "application/json" {
		t.Errorf("Accept-Query = %q, want application/json", got)
	}
}

// TestAcceptQueryDiscoveryWithCORS is the regression guard for the original
// defect: with CORS enabled (which swallows OPTIONS as a 204), a plain OPTIONS
// probe must still reach the automatic handler and carry Accept-Query.
func TestAcceptQueryDiscoveryWithCORS(t *testing.T) {
	app := NewApp()
	app.Use(CORSWithDefaults())
	app.Query("/search", func(w http.ResponseWriter, r *http.Request) error { return nil })

	t.Run("plain OPTIONS reaches discovery", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("OPTIONS", "/search", nil)
		req.Header.Set("Origin", "http://example.com")
		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
		if got := rec.Header().Get("Accept-Query"); got != "application/json" {
			t.Errorf("Accept-Query = %q, want application/json (CORS must not hide discovery)", got)
		}
		if got := rec.Header().Get("Allow"); !strings.Contains(got, "QUERY") {
			t.Errorf("Allow = %q, want it to list QUERY", got)
		}
	})

	t.Run("genuine preflight is still short-circuited by CORS", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("OPTIONS", "/search", nil)
		req.Header.Set("Origin", "http://example.com")
		req.Header.Set("Access-Control-Request-Method", "QUERY")
		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", rec.Code)
		}
		// CORS handled it, so the router's Allow header is absent.
		if got := rec.Header().Get("Allow"); got != "" {
			t.Errorf("Allow = %q, want empty (CORS should short-circuit preflight)", got)
		}
	})
}
