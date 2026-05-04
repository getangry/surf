package surf

import (
	"net/http"
	"net/http/httptest"
	"sync"
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
		{"wrong method", "DELETE", "/users", http.StatusNotFound, "404 page not found\n"},
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

	// Should NOT contain the actual error message (security)
	if rec.Body.String() != "Internal Server Error\n" {
		t.Errorf("body = %q, want generic error", rec.Body.String())
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
		register func(string, HandlerFunc)
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

func TestRouterMethodCaseInsensitiveLookup(t *testing.T) {
	app := NewApp()
	app.Get("/x", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("ok"))
		return nil
	})

	for _, m := range []string{"GET", "get", "Get", "gEt"} {
		req := httptest.NewRequest(m, "/x", nil)
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("method %q: status = %d, want 200", m, rec.Code)
		}
	}
}

func TestHandleErrorAfterCommittedHeadersPreservesResponse(t *testing.T) {
	app := NewApp()
	app.Get("/partial", func(w http.ResponseWriter, r *http.Request) error {
		// Handler writes a successful response, then errors. The framework
		// must NOT append "Internal Server Error" or change the status.
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte("partial body"))
		return http.ErrAbortHandler // any error
	})

	req := httptest.NewRequest("GET", "/partial", nil)
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want %d (handler-committed status preserved)", rec.Code, http.StatusAccepted)
	}
	if rec.Body.String() != "partial body" {
		t.Errorf("body = %q, want %q (no error message appended)", rec.Body.String(), "partial body")
	}
}

func TestRouterPanicsOnRegistrationAfterFirstRequest(t *testing.T) {
	app := NewApp()
	app.Get("/before", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("ok"))
		return nil
	})

	// First request freezes the routing tables.
	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/before", nil))

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on route registration after first request")
		}
	}()
	app.Get("/after", func(w http.ResponseWriter, r *http.Request) error {
		return nil
	})
}

func TestRouterConcurrentReadsAfterFreeze(t *testing.T) {
	// Hot-path lookups must be lock-free and safe under concurrent traffic.
	// The race detector will flag any unsynchronized access.
	app := NewApp()
	for _, p := range []string{"/a", "/b", "/c", "/d", "/e"} {
		p := p
		app.Get(p, func(w http.ResponseWriter, r *http.Request) error {
			w.Write([]byte(p))
			return nil
		})
	}

	const goroutines = 50
	const requests = 200
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			paths := []string{"/a", "/b", "/c", "/d", "/e", "/missing"}
			for j := 0; j < requests; j++ {
				app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", paths[j%len(paths)], nil))
			}
		}()
	}
	wg.Wait()
}
