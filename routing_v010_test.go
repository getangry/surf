package surf

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// tagMiddleware appends val to the X-Trace response header, recording the
// order in which middleware runs.
func tagMiddleware(val string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("X-Trace", val)
			next.ServeHTTP(w, r)
		})
	}
}

// blockMiddleware short-circuits the chain with the given status.
func blockMiddleware(status int) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		})
	}
}

func TestPerRouteMiddlewareRuns(t *testing.T) {
	app := NewApp()
	app.Get("/x", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("ok"))
		return nil
	}, tagMiddleware("a"), tagMiddleware("b"))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))

	trace := rec.Header()["X-Trace"]
	if len(trace) != 2 || trace[0] != "a" || trace[1] != "b" {
		t.Errorf("X-Trace = %v, want [a b]", trace)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestPerRouteMiddlewareShortCircuits(t *testing.T) {
	app := NewApp()
	handlerRan := false
	app.Get("/x", func(w http.ResponseWriter, r *http.Request) error {
		handlerRan = true
		return nil
	}, blockMiddleware(http.StatusForbidden))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))

	if handlerRan {
		t.Error("handler ran despite middleware short-circuit")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("code = %d, want 403", rec.Code)
	}
}

type ctxKey string

func TestPerRouteMiddlewareContextPropagates(t *testing.T) {
	app := NewApp()
	inject := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r = r.WithContext(context.WithValue(r.Context(), ctxKey("user"), "ada"))
			next.ServeHTTP(w, r)
		})
	}
	app.Get("/x", func(w http.ResponseWriter, r *http.Request) error {
		if v, _ := r.Context().Value(ctxKey("user")).(string); v != "ada" {
			t.Errorf("context value = %q, want ada", v)
		}
		return nil
	}, inject)

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
}

func TestGroupUseAndSkip(t *testing.T) {
	app := NewApp()
	api := app.Group("/api").Use(tagMiddleware("grp"))
	api.Skip("/api/health")
	api.Get("/health", func(w http.ResponseWriter, r *http.Request) error { return nil })
	api.Get("/users", func(w http.ResponseWriter, r *http.Request) error { return nil })

	recUsers := httptest.NewRecorder()
	app.ServeHTTP(recUsers, httptest.NewRequest("GET", "/api/users", nil))
	if got := recUsers.Header().Get("X-Trace"); got != "grp" {
		t.Errorf("/api/users X-Trace = %q, want grp", got)
	}

	recHealth := httptest.NewRecorder()
	app.ServeHTTP(recHealth, httptest.NewRequest("GET", "/api/health", nil))
	if got := recHealth.Header().Get("X-Trace"); got != "" {
		t.Errorf("/api/health X-Trace = %q, want empty (skipped)", got)
	}
}

func TestGroupSkipExcludesBefore(t *testing.T) {
	app := NewApp()
	api := app.Group("/api").Before(func(w http.ResponseWriter, r *http.Request) error {
		return NewHTTPError(http.StatusUnauthorized, "unauthorized")
	})
	api.Skip("/api/public")
	api.Get("/public", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("open"))
		return nil
	})
	api.Get("/private", func(w http.ResponseWriter, r *http.Request) error { return nil })

	recPublic := httptest.NewRecorder()
	app.ServeHTTP(recPublic, httptest.NewRequest("GET", "/api/public", nil))
	if recPublic.Code != http.StatusOK || recPublic.Body.String() != "open" {
		t.Errorf("public route: code=%d body=%q", recPublic.Code, recPublic.Body.String())
	}

	recPrivate := httptest.NewRecorder()
	app.ServeHTTP(recPrivate, httptest.NewRequest("GET", "/api/private", nil))
	if recPrivate.Code != http.StatusUnauthorized {
		t.Errorf("private route: code = %d, want 401", recPrivate.Code)
	}
}

func TestHandlerHTTPErrorMapsToResponse(t *testing.T) {
	app := NewApp()
	app.Get("/x", func(w http.ResponseWriter, r *http.Request) error {
		return NewHTTPError(http.StatusNotFound, "no such widget")
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("code = %d, want 404", rec.Code)
	}
	if !contains(rec.Body.String(), `"error":"no such widget"`) {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestHandlerAbortSentinel(t *testing.T) {
	app := NewApp()
	app.Get("/x", func(w http.ResponseWriter, r *http.Request) error {
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte("done"))
		return Abort
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != http.StatusAccepted || rec.Body.String() != "done" {
		t.Errorf("Abort corrupted response: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestErrorAfterResponseWrittenIsNotCorrupted(t *testing.T) {
	app := NewApp()
	app.Get("/x", func(w http.ResponseWriter, r *http.Request) error {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("payload"))
		return errExample // returned after writing; must not append to body
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "payload" {
		t.Errorf("response corrupted: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestWithErrorHandler(t *testing.T) {
	app := NewApp(WithErrorHandler(func(w http.ResponseWriter, r *http.Request, err error) {
		w.WriteHeader(http.StatusTeapot)
		w.Write([]byte("custom"))
	}))
	app.Get("/x", func(w http.ResponseWriter, r *http.Request) error {
		return errExample
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != http.StatusTeapot || rec.Body.String() != "custom" {
		t.Errorf("custom renderer not used: code=%d body=%q", rec.Code, rec.Body.String())
	}
}
