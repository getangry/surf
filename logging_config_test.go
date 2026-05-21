package surf

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoggingMiddlewareSkipPaths(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prev)

	app := NewApp()
	app.Use(LoggingMiddlewareWithConfig(LoggingConfig{
		Format:    "{method} {path} {status}",
		SkipPaths: []string{"/health/*"},
	}))
	app.Get("/health/live", func(w http.ResponseWriter, r *http.Request) error { return nil })
	app.Get("/users", func(w http.ResponseWriter, r *http.Request) error { return nil })

	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/health/live", nil))
	if contains(buf.String(), "/health/live") {
		t.Errorf("skipped path was logged: %q", buf.String())
	}

	buf.Reset()
	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/users", nil))
	if !contains(buf.String(), "/users") {
		t.Errorf("non-skipped path was not logged: %q", buf.String())
	}
}
