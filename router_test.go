package surf

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRouterBasicRoutes(t *testing.T) {
	app := NewApp()

	app.Get("/", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("root"))
		return nil
	})

	app.Get("/users", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("users"))
		return nil
	})

	app.Post("/users", func(w http.ResponseWriter, r *http.Request) error {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte("created"))
		return nil
	})

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantBody   string
	}{
		{"GET root", "GET", "/", http.StatusOK, "root"},
		{"GET users", "GET", "/users", http.StatusOK, "users"},
		{"POST users", "POST", "/users", http.StatusCreated, "created"},
		{"GET not found", "GET", "/notfound", http.StatusNotFound, "404 page not found\n"},
		{"wrong method", "DELETE", "/users", http.StatusMethodNotAllowed, "Method Not Allowed\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()

			app.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if rec.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestRouterPathParameters(t *testing.T) {
	app := NewApp()

	app.Get("/users/:id", func(w http.ResponseWriter, r *http.Request) error {
		id := Param(r, "id")
		w.Write([]byte("user:" + id))
		return nil
	})

	app.Get("/users/:id/posts/:postId", func(w http.ResponseWriter, r *http.Request) error {
		id := Param(r, "id")
		postId := Param(r, "postId")
		w.Write([]byte("user:" + id + ",post:" + postId))
		return nil
	})

	tests := []struct {
		name     string
		path     string
		wantBody string
	}{
		{"single param", "/users/123", "user:123"},
		{"param with letters", "/users/abc", "user:abc"},
		{"multiple params", "/users/42/posts/99", "user:42,post:99"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			rec := httptest.NewRecorder()

			app.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if rec.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestRouterWildcard(t *testing.T) {
	app := NewApp()

	app.Get("/static/*", func(w http.ResponseWriter, r *http.Request) error {
		path := Param(r, "*")
		w.Write([]byte("static:" + path))
		return nil
	})

	app.Get("/files/:type/*", func(w http.ResponseWriter, r *http.Request) error {
		fileType := Param(r, "type")
		path := Param(r, "*")
		w.Write([]byte("type:" + fileType + ",path:" + path))
		return nil
	})

	tests := []struct {
		name     string
		path     string
		wantBody string
	}{
		{"wildcard single", "/static/file.js", "static:file.js"},
		{"wildcard nested", "/static/css/style.css", "static:css/style.css"},
		{"wildcard deep", "/static/a/b/c/d.txt", "static:a/b/c/d.txt"},
		{"param with wildcard", "/files/images/photos/cat.jpg", "type:images,path:photos/cat.jpg"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			rec := httptest.NewRecorder()

			app.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if rec.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestRouterGroups(t *testing.T) {
	app := NewApp()

	api := app.Group("/api")
	api.Get("/users", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("api-users"))
		return nil
	})

	v2 := api.Group("/v2")
	v2.Get("/users", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("api-v2-users"))
		return nil
	})

	tests := []struct {
		name     string
		path     string
		wantBody string
	}{
		{"group route", "/api/users", "api-users"},
		{"nested group", "/api/v2/users", "api-v2-users"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			rec := httptest.NewRecorder()

			app.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if rec.Body.String() != tt.wantBody {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestRouterBeforeAfterMiddleware(t *testing.T) {
	app := NewApp()

	var order []string

	app.Before(func(w http.ResponseWriter, r *http.Request) error {
		order = append(order, "app-before")
		return nil
	})

	app.After(func(w http.ResponseWriter, r *http.Request) error {
		order = append(order, "app-after")
		return nil
	})

	api := app.Group("/api")
	api.Before(func(w http.ResponseWriter, r *http.Request) error {
		order = append(order, "group-before")
		return nil
	})
	api.After(func(w http.ResponseWriter, r *http.Request) error {
		order = append(order, "group-after")
		return nil
	})

	api.Get("/test", func(w http.ResponseWriter, r *http.Request) error {
		order = append(order, "handler")
		w.Write([]byte("ok"))
		return nil
	})

	req := httptest.NewRequest("GET", "/api/test", nil)
	rec := httptest.NewRecorder()

	order = nil // reset
	app.ServeHTTP(rec, req)

	expected := []string{"app-before", "group-before", "handler", "group-after", "app-after"}
	if len(order) != len(expected) {
		t.Fatalf("order length = %d, want %d", len(order), len(expected))
	}
	for i, v := range expected {
		if order[i] != v {
			t.Errorf("order[%d] = %q, want %q", i, order[i], v)
		}
	}
}

func TestRouterMiddlewareChain(t *testing.T) {
	app := NewApp()

	var order []string

	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "mw1-before")
			next.ServeHTTP(w, r)
			order = append(order, "mw1-after")
		})
	})

	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "mw2-before")
			next.ServeHTTP(w, r)
			order = append(order, "mw2-after")
		})
	})

	app.Get("/test", func(w http.ResponseWriter, r *http.Request) error {
		order = append(order, "handler")
		w.Write([]byte("ok"))
		return nil
	})

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	order = nil
	app.ServeHTTP(rec, req)

	expected := []string{"mw1-before", "mw2-before", "handler", "mw2-after", "mw1-after"}
	if len(order) != len(expected) {
		t.Fatalf("order length = %d, want %d", len(order), len(expected))
	}
	for i, v := range expected {
		if order[i] != v {
			t.Errorf("order[%d] = %q, want %q", i, order[i], v)
		}
	}
}

func TestRouterHandlerError(t *testing.T) {
	app := NewApp()

	app.Get("/error", func(w http.ResponseWriter, r *http.Request) error {
		return http.ErrBodyNotAllowed // some error
	})

	req := httptest.NewRequest("GET", "/error", nil)
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	// Should NOT contain the actual error message (security): the default
	// renderer emits a generic JSON envelope.
	if got := rec.Body.String(); !strings.Contains(got, `"error":"Internal Server Error"`) {
		t.Errorf("body = %q, want generic JSON error envelope", got)
	}
	if strings.Contains(rec.Body.String(), "ErrBodyNotAllowed") {
		t.Errorf("body leaked internal error detail: %q", rec.Body.String())
	}
}

func TestMatchPath(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		path    string
		match   bool
		params  map[string]string
	}{
		{"exact match", "/users", "/users", true, map[string]string{}},
		{"no match", "/users", "/posts", false, nil},
		{"single param", "/users/:id", "/users/123", true, map[string]string{"id": "123"}},
		{"multiple params", "/users/:id/posts/:postId", "/users/1/posts/2", true, map[string]string{"id": "1", "postId": "2"}},
		{"wildcard", "/static/*", "/static/css/style.css", true, map[string]string{"*": "css/style.css"}},
		{"length mismatch", "/users/:id", "/users", false, nil},
		{"trailing slash mismatch", "/users", "/users/", false, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, ok := matchPath(tt.pattern, tt.path)
			if ok != tt.match {
				t.Errorf("match = %v, want %v", ok, tt.match)
			}
			if tt.match {
				for k, v := range tt.params {
					if params[k] != v {
						t.Errorf("params[%q] = %q, want %q", k, params[k], v)
					}
				}
			}
		})
	}
}

func TestExtractParams(t *testing.T) {
	tests := []struct {
		pattern string
		want    []string
	}{
		{"/users", nil},
		{"/users/:id", []string{"id"}},
		{"/users/:id/posts/:postId", []string{"id", "postId"}},
		{"/:a/:b/:c", []string{"a", "b", "c"}},
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			got := extractParams(tt.pattern)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tt.want))
			}
			for i, v := range tt.want {
				if got[i] != v {
					t.Errorf("params[%d] = %q, want %q", i, got[i], v)
				}
			}
		})
	}
}

func TestParamMissingKey(t *testing.T) {
	app := NewApp()

	app.Get("/test", func(w http.ResponseWriter, r *http.Request) error {
		// Try to get a param that doesn't exist
		val := Param(r, "nonexistent")
		if val != "" {
			t.Errorf("expected empty string for missing param, got %q", val)
		}
		w.Write([]byte("ok"))
		return nil
	})

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestAllHTTPMethods(t *testing.T) {
	app := NewApp()

	methods := []struct {
		register func(string, HandlerFunc, ...Middleware)
		method   string
	}{
		{app.Get, "GET"},
		{app.Post, "POST"},
		{app.Put, "PUT"},
		{app.Delete, "DELETE"},
		{app.Patch, "PATCH"},
		{app.Head, "HEAD"},
		{app.Options, "OPTIONS"},
	}

	for _, m := range methods {
		m.register("/"+m.method, func(w http.ResponseWriter, r *http.Request) error {
			w.Write([]byte(r.Method))
			return nil
		})
	}

	for _, m := range methods {
		t.Run(m.method, func(t *testing.T) {
			req := httptest.NewRequest(m.method, "/"+m.method, nil)
			rec := httptest.NewRecorder()

			app.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			// HEAD doesn't return body
			if m.method != "HEAD" && rec.Body.String() != m.method {
				t.Errorf("body = %q, want %q", rec.Body.String(), m.method)
			}
		})
	}
}
