package surf

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWithRedirectTrailingSlash_Off_Default(t *testing.T) {
	// Default behavior: no redirect; missing trailing-slash variant 404s.
	app := NewApp()
	app.Get("/users", func(w http.ResponseWriter, r *http.Request) error {
		_, _ = w.Write([]byte("users"))
		return nil
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/users/", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("without option: code = %d, want 404", rec.Code)
	}
}

func TestWithRedirectTrailingSlash_RedirectToCanonical(t *testing.T) {
	app := NewApp(WithRedirectTrailingSlash())
	app.Get("/users", func(w http.ResponseWriter, r *http.Request) error {
		_, _ = w.Write([]byte("users"))
		return nil
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/users/", nil))

	if rec.Code != http.StatusPermanentRedirect {
		t.Errorf("code = %d, want 308", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/users" {
		t.Errorf("Location = %q, want /users", loc)
	}
}

func TestWithRedirectTrailingSlash_RedirectAddsSlash(t *testing.T) {
	app := NewApp(WithRedirectTrailingSlash())
	app.Get("/users/", func(w http.ResponseWriter, r *http.Request) error {
		_, _ = w.Write([]byte("users"))
		return nil
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/users", nil))

	if rec.Code != http.StatusPermanentRedirect {
		t.Errorf("code = %d, want 308", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/users/" {
		t.Errorf("Location = %q, want /users/", loc)
	}
}

func TestWithRedirectTrailingSlash_PreservesQueryString(t *testing.T) {
	app := NewApp(WithRedirectTrailingSlash())
	app.Get("/users", func(w http.ResponseWriter, r *http.Request) error { return nil })

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/users/?page=2&sort=desc", nil))

	if rec.Code != http.StatusPermanentRedirect {
		t.Fatalf("code = %d, want 308", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/users?page=2&sort=desc" {
		t.Errorf("Location = %q, want /users?page=2&sort=desc", loc)
	}
}

func TestWithRedirectTrailingSlash_BothRegistered_NoRedirect(t *testing.T) {
	app := NewApp(WithRedirectTrailingSlash())
	app.Get("/users", func(w http.ResponseWriter, r *http.Request) error {
		_, _ = w.Write([]byte("no-slash"))
		return nil
	})
	app.Get("/users/", func(w http.ResponseWriter, r *http.Request) error {
		_, _ = w.Write([]byte("with-slash"))
		return nil
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/users/", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "with-slash" {
		t.Errorf("/users/ should be served directly: code=%d body=%q",
			rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/users", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "no-slash" {
		t.Errorf("/users should be served directly: code=%d body=%q",
			rec.Code, rec.Body.String())
	}
}

func TestWithRedirectTrailingSlash_NoMatchEitherWay_404(t *testing.T) {
	app := NewApp(WithRedirectTrailingSlash())
	app.Get("/users", func(w http.ResponseWriter, r *http.Request) error { return nil })

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/widgets", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("code = %d, want 404", rec.Code)
	}
}

func TestWithRedirectTrailingSlash_PreservesMethod_POST(t *testing.T) {
	// 308 preserves method; the test asserts the redirect kind, not that
	// a real client re-POSTs (that's the client's job).
	app := NewApp(WithRedirectTrailingSlash())
	app.Post("/users", func(w http.ResponseWriter, r *http.Request) error { return nil })

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("POST", "/users/", nil))

	if rec.Code != http.StatusPermanentRedirect {
		t.Errorf("POST redirect: code = %d, want 308", rec.Code)
	}
}

func TestWithRedirectTrailingSlash_DifferentMethodNoCrossover(t *testing.T) {
	// /users is GET-only. POST /users/ must not redirect to GET /users —
	// the lookup is scoped to the request's method tree.
	app := NewApp(WithRedirectTrailingSlash())
	app.Get("/users", func(w http.ResponseWriter, r *http.Request) error { return nil })

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("POST", "/users/", nil))

	// No POST tree exists for the request method, so the 404/405 path runs.
	// (Currently 404; 405 only triggers when getAllowedMethods sees a
	// matching path on another method, which it doesn't here.)
	if rec.Code == http.StatusPermanentRedirect {
		t.Errorf("cross-method redirect must not happen: code=%d", rec.Code)
	}
}
