package surf

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestQuery(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?name=john&age=30", nil)

	if Query(req, "name") != "john" {
		t.Errorf("Query(name) = %q, want %q", Query(req, "name"), "john")
	}
	if Query(req, "age") != "30" {
		t.Errorf("Query(age) = %q, want %q", Query(req, "age"), "30")
	}
	if Query(req, "missing") != "" {
		t.Errorf("Query(missing) = %q, want empty", Query(req, "missing"))
	}
}

func TestQueryDefault(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?name=john", nil)

	if QueryDefault(req, "name", "default") != "john" {
		t.Errorf("QueryDefault(name) = %q, want %q", QueryDefault(req, "name", "default"), "john")
	}
	if QueryDefault(req, "missing", "default") != "default" {
		t.Errorf("QueryDefault(missing) = %q, want %q", QueryDefault(req, "missing", "default"), "default")
	}

	// Empty value should return default
	req = httptest.NewRequest("GET", "/test?empty=", nil)
	if QueryDefault(req, "empty", "default") != "default" {
		t.Errorf("QueryDefault(empty) = %q, want %q", QueryDefault(req, "empty", "default"), "default")
	}
}

func TestQueryInt(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		key      string
		defVal   int
		expected int
	}{
		{"valid int", "/test?count=42", "count", 0, 42},
		{"negative int", "/test?count=-10", "count", 0, -10},
		{"missing", "/test", "count", 99, 99},
		{"invalid", "/test?count=abc", "count", 99, 99},
		{"empty", "/test?count=", "count", 99, 99},
		{"float", "/test?count=3.14", "count", 99, 99},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.url, nil)
			result := QueryInt(req, tt.key, tt.defVal)
			if result != tt.expected {
				t.Errorf("QueryInt(%q) = %d, want %d", tt.key, result, tt.expected)
			}
		})
	}
}

func TestQueryInt64(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?big=9223372036854775807", nil)

	result := QueryInt64(req, "big", 0)
	if result != 9223372036854775807 {
		t.Errorf("QueryInt64(big) = %d, want max int64", result)
	}

	result = QueryInt64(req, "missing", 123)
	if result != 123 {
		t.Errorf("QueryInt64(missing) = %d, want 123", result)
	}
}

func TestQueryFloat(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		key      string
		defVal   float64
		expected float64
	}{
		{"valid float", "/test?value=3.14", "value", 0, 3.14},
		{"integer as float", "/test?value=42", "value", 0, 42.0},
		{"negative", "/test?value=-1.5", "value", 0, -1.5},
		{"missing", "/test", "value", 99.9, 99.9},
		{"invalid", "/test?value=abc", "value", 99.9, 99.9},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.url, nil)
			result := QueryFloat(req, tt.key, tt.defVal)
			if result != tt.expected {
				t.Errorf("QueryFloat(%q) = %f, want %f", tt.key, result, tt.expected)
			}
		})
	}
}

func TestQueryBool(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		key      string
		defVal   bool
		expected bool
	}{
		{"true", "/test?flag=true", "flag", false, true},
		{"TRUE", "/test?flag=TRUE", "flag", false, true},
		{"1", "/test?flag=1", "flag", false, true},
		{"yes", "/test?flag=yes", "flag", false, true},
		{"on", "/test?flag=on", "flag", false, true},
		{"false", "/test?flag=false", "flag", true, false},
		{"0", "/test?flag=0", "flag", true, false},
		{"missing default true", "/test", "flag", true, true},
		{"missing default false", "/test", "flag", false, false},
		{"invalid", "/test?flag=invalid", "flag", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.url, nil)
			result := QueryBool(req, tt.key, tt.defVal)
			if result != tt.expected {
				t.Errorf("QueryBool(%q) = %v, want %v", tt.key, result, tt.expected)
			}
		})
	}
}

func TestQuerySlice(t *testing.T) {
	req := httptest.NewRequest("GET", "/test?tags=a&tags=b&tags=c", nil)

	result := QuerySlice(req, "tags")
	if len(result) != 3 {
		t.Fatalf("QuerySlice(tags) len = %d, want 3", len(result))
	}
	if result[0] != "a" || result[1] != "b" || result[2] != "c" {
		t.Errorf("QuerySlice(tags) = %v, want [a b c]", result)
	}

	// Missing key
	result = QuerySlice(req, "missing")
	if result != nil {
		t.Errorf("QuerySlice(missing) = %v, want nil", result)
	}
}

func TestRedirect(t *testing.T) {
	app := NewApp()

	app.Get("/old", func(w http.ResponseWriter, r *http.Request) error {
		Redirect(w, r, "/new", http.StatusMovedPermanently)
		return nil
	})

	req := httptest.NewRequest("GET", "/old", nil)
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusMovedPermanently {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusMovedPermanently)
	}
	if rec.Header().Get("Location") != "/new" {
		t.Errorf("Location = %q, want %q", rec.Header().Get("Location"), "/new")
	}
}

func TestRedirectHelpers(t *testing.T) {
	tests := []struct {
		name       string
		redirect   func(http.ResponseWriter, *http.Request, string)
		wantStatus int
	}{
		{"RedirectPermanent", RedirectPermanent, http.StatusMovedPermanently},
		{"RedirectTemporary", RedirectTemporary, http.StatusFound},
		{"RedirectSeeOther", RedirectSeeOther, http.StatusSeeOther},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := NewApp()
			app.Get("/test", func(w http.ResponseWriter, r *http.Request) error {
				tt.redirect(w, r, "/target")
				return nil
			})

			req := httptest.NewRequest("GET", "/test", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestCustomNotFoundHandler(t *testing.T) {
	app := NewApp()

	app.NotFound(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("Custom 404: Page not found"))
	})

	app.Get("/exists", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("ok"))
		return nil
	})

	t.Run("custom 404", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/does-not-exist", nil)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
		}
		if rec.Body.String() != "Custom 404: Page not found" {
			t.Errorf("body = %q, want custom message", rec.Body.String())
		}
	})

	t.Run("existing route works", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/exists", nil)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
		}
	})
}

func TestCustomMethodNotAllowedHandler(t *testing.T) {
	app := NewApp()

	app.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte("Custom 405: Method not allowed"))
	})

	app.Get("/resource", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("ok"))
		return nil
	})

	app.Post("/resource", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("created"))
		return nil
	})

	t.Run("custom 405", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/resource", nil)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
		}
		if rec.Body.String() != "Custom 405: Method not allowed" {
			t.Errorf("body = %q, want custom message", rec.Body.String())
		}
		// Should have Allow header
		allow := rec.Header().Get("Allow")
		if allow == "" {
			t.Error("Allow header should be set")
		}
	})
}

func TestMethodNotAllowedDefault(t *testing.T) {
	app := NewApp()

	app.Get("/resource", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("ok"))
		return nil
	})

	req := httptest.NewRequest("POST", "/resource", nil)
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}

	allow := rec.Header().Get("Allow")
	if allow != "GET" {
		t.Errorf("Allow = %q, want %q", allow, "GET")
	}
}

func TestStaticFileServing(t *testing.T) {
	// Create temp directory with test files
	tmpDir, err := os.MkdirTemp("", "surf-static-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test files
	testContent := "Hello, World!"
	if err := os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte(testContent), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Create subdirectory with file
	subDir := filepath.Join(tmpDir, "sub")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "nested.txt"), []byte("nested content"), 0644); err != nil {
		t.Fatalf("failed to create nested file: %v", err)
	}

	app := NewApp()
	app.Static("/static", tmpDir)

	t.Run("serve file", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/static/test.txt", nil)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if rec.Body.String() != testContent {
			t.Errorf("body = %q, want %q", rec.Body.String(), testContent)
		}
	})

	t.Run("serve nested file", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/static/sub/nested.txt", nil)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if rec.Body.String() != "nested content" {
			t.Errorf("body = %q, want %q", rec.Body.String(), "nested content")
		}
	})

	t.Run("file not found", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/static/nonexistent.txt", nil)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
		}
	})

	t.Run("directory traversal blocked", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/static/../../../etc/passwd", nil)
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d for directory traversal", rec.Code, http.StatusNotFound)
		}
	})
}

func TestStaticFile(t *testing.T) {
	// Create temp file
	tmpFile, err := os.CreateTemp("", "surf-favicon-*.ico")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	content := []byte("fake icon content")
	if _, err := tmpFile.Write(content); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	tmpFile.Close()

	app := NewApp()
	app.StaticFile("/favicon.ico", tmpFile.Name())

	req := httptest.NewRequest("GET", "/favicon.ico", nil)
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != string(content) {
		t.Errorf("body = %q, want %q", rec.Body.String(), string(content))
	}
}

func TestGetAllowedMethods(t *testing.T) {
	app := NewApp()

	app.Get("/resource", func(w http.ResponseWriter, r *http.Request) error {
		return nil
	})
	app.Post("/resource", func(w http.ResponseWriter, r *http.Request) error {
		return nil
	})
	app.Put("/resource", func(w http.ResponseWriter, r *http.Request) error {
		return nil
	})

	methods := app.router.getAllowedMethods("/resource")
	if len(methods) != 3 {
		t.Errorf("got %d methods, want 3", len(methods))
	}

	// Check no methods for non-existent route
	methods = app.router.getAllowedMethods("/nonexistent")
	if len(methods) != 0 {
		t.Errorf("got %d methods for nonexistent, want 0", len(methods))
	}
}

func TestStaticSymlinkEscapeBlocked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks behave differently on Windows; OpenRoot semantics vary")
	}

	// Two directories: served/ (the docroot) and secret/ (must stay private).
	tmp, err := os.MkdirTemp("", "surf-symlink-test")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmp)

	served := filepath.Join(tmp, "served")
	secret := filepath.Join(tmp, "secret")
	if err := os.MkdirAll(served, 0o755); err != nil {
		t.Fatalf("mkdir served: %v", err)
	}
	if err := os.MkdirAll(secret, 0o755); err != nil {
		t.Fatalf("mkdir secret: %v", err)
	}

	// Legitimate file inside the docroot.
	if err := os.WriteFile(filepath.Join(served, "ok.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write ok.txt: %v", err)
	}
	// Secret outside the docroot.
	if err := os.WriteFile(filepath.Join(secret, "passwords.txt"), []byte("hunter2"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	// Symlink inside the docroot pointing OUT to the secret file.
	if err := os.Symlink(filepath.Join(secret, "passwords.txt"), filepath.Join(served, "escape")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	app := NewApp()
	app.Static("/files", served)

	// Normal file: served as usual.
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/files/ok.txt", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Errorf("legit file: code=%d body=%q", rec.Code, rec.Body.String())
	}

	// Symlink-escape attempt: kernel (openat2 RESOLVE_BENEATH) refuses; 404.
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/files/escape", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("symlink escape returned status %d, want 404; body=%q",
			rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "hunter2") {
		t.Errorf("symlink escape leaked secret content: %q", rec.Body.String())
	}
}
