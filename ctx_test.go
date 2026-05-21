package surf

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleBasic(t *testing.T) {
	app := NewApp()
	app.Handle("GET", "/ping", func(c *Context) error {
		return c.String(http.StatusOK, "pong")
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/ping", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "pong" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestHandleParam(t *testing.T) {
	app := NewApp()
	app.Handle("GET", "/users/:id", func(c *Context) error {
		return c.String(http.StatusOK, c.Param("id"))
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/users/abc", nil))
	if rec.Body.String() != "abc" {
		t.Errorf("param = %q, want abc", rec.Body.String())
	}
}

func TestHandleJSON(t *testing.T) {
	app := NewApp()
	app.Handle("GET", "/j", func(c *Context) error {
		return c.JSONData(map[string]int{"n": 5})
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/j", nil))
	if !contains(rec.Body.String(), `"data"`) || !contains(rec.Body.String(), `"n":5`) {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestHandleBind(t *testing.T) {
	app := NewApp()
	app.Handle("POST", "/s", func(c *Context) error {
		var b signupBody
		if err := c.BindAndValidate(&b); err != nil {
			return err
		}
		return c.String(http.StatusCreated, b.Name)
	})

	jsonPost := func(body string) *http.Request {
		r := httptest.NewRequest("POST", "/s", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		return r
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, jsonPost(`{"name":"Grace","email":"g@h.i"}`))
	if rec.Code != http.StatusCreated || rec.Body.String() != "Grace" {
		t.Fatalf("code=%d body=%q", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, jsonPost(`{"email":"g@h.i"}`)) // missing name
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("validation: code = %d, want 422", rec.Code)
	}
}

func ctxTag(val string) CtxMiddleware {
	return func(next CtxHandler) CtxHandler {
		return func(c *Context) error {
			c.Writer().Header().Add("X-Trace", val)
			return next(c)
		}
	}
}

func TestHandleCtxMiddlewareOrder(t *testing.T) {
	app := NewApp()
	app.Handle("GET", "/m", func(c *Context) error {
		return c.NoContent(http.StatusOK)
	}, ctxTag("a"), ctxTag("b"))

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/m", nil))
	trace := rec.Header()["X-Trace"]
	if len(trace) != 2 || trace[0] != "a" || trace[1] != "b" {
		t.Errorf("X-Trace = %v, want [a b]", trace)
	}
}

func TestHandleCtxMiddlewareShortCircuit(t *testing.T) {
	app := NewApp()
	ran := false
	block := func(next CtxHandler) CtxHandler {
		return func(c *Context) error {
			return c.NoContent(http.StatusForbidden) // does not call next
		}
	}
	app.Handle("GET", "/m", func(c *Context) error {
		ran = true
		return nil
	}, block)

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/m", nil))
	if ran {
		t.Error("handler ran despite short-circuit")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("code = %d, want 403", rec.Code)
	}
}

func TestHandleAppMiddlewareWraps(t *testing.T) {
	app := NewApp()
	app.Use(tagMiddleware("app"))
	app.Handle("GET", "/m", func(c *Context) error {
		return c.NoContent(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/m", nil))
	if rec.Header().Get("X-Trace") != "app" {
		t.Errorf("app middleware did not wrap Context route: %q", rec.Header().Get("X-Trace"))
	}
}

func TestHandleErrorRenders(t *testing.T) {
	app := NewApp()
	app.Handle("GET", "/e", func(c *Context) error {
		return NewHTTPError(http.StatusNotFound, "no widget")
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/e", nil))
	if rec.Code != http.StatusNotFound || !contains(rec.Body.String(), `"error":"no widget"`) {
		t.Errorf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestHandleAbort(t *testing.T) {
	app := NewApp()
	app.Handle("GET", "/a", func(c *Context) error {
		_ = c.String(http.StatusAccepted, "done")
		return Abort
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/a", nil))
	if rec.Code != http.StatusAccepted || rec.Body.String() != "done" {
		t.Errorf("Abort corrupted response: code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestHandleCtxService(t *testing.T) {
	app := NewApp()
	Provide[*enGreeter](app, &enGreeter{})
	app.Handle("GET", "/svc", func(c *Context) error {
		g, ok := CtxService[*enGreeter](c)
		if !ok {
			return NewHTTPError(http.StatusInternalServerError, "missing service")
		}
		return c.String(http.StatusOK, g.Greet())
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/svc", nil))
	if rec.Body.String() != "hello" {
		t.Errorf("body = %q, want hello", rec.Body.String())
	}
}

func TestGroupHandlePrefix(t *testing.T) {
	app := NewApp()
	api := app.Group("/api")
	api.Handle("GET", "/v", func(c *Context) error {
		return c.String(http.StatusOK, "ok")
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Errorf("code=%d body=%q", rec.Code, rec.Body.String())
	}
}

func TestHandleAndLegacyCoexist(t *testing.T) {
	app := NewApp()
	app.Get("/legacy", func(w http.ResponseWriter, r *http.Request) error {
		_, _ = w.Write([]byte("legacy"))
		return nil
	})
	app.Handle("GET", "/fast", func(c *Context) error {
		return c.String(http.StatusOK, "fast")
	})

	for _, tc := range []struct{ path, want string }{{"/legacy", "legacy"}, {"/fast", "fast"}} {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest("GET", tc.path, nil))
		if rec.Body.String() != tc.want {
			t.Errorf("%s = %q, want %q", tc.path, rec.Body.String(), tc.want)
		}
	}
}
