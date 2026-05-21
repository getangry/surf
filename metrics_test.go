package surf

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMetricsRegistry(t *testing.T) {
	m := NewMetricsRegistry()
	app := NewApp()
	app.Use(m.Middleware())
	app.Get("/ok", func(w http.ResponseWriter, r *http.Request) error {
		w.WriteHeader(http.StatusOK)
		return nil
	})
	app.Get("/metrics", m.Handler())

	for i := 0; i < 3; i++ {
		app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/ok", nil))
	}

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()

	if !contains(body, `surf_http_requests_total{method="GET",status="200"} 3`) {
		t.Errorf("missing request counter:\n%s", body)
	}
	if !contains(body, "surf_http_request_duration_seconds_count 3") {
		t.Errorf("missing histogram count:\n%s", body)
	}
	if !contains(body, `surf_http_request_duration_seconds_bucket{le="+Inf"} 3`) {
		t.Errorf("missing +Inf bucket:\n%s", body)
	}
	if !contains(body, "surf_http_requests_in_flight") {
		t.Errorf("missing in-flight gauge:\n%s", body)
	}
	if ct := rec.Header().Get("Content-Type"); !contains(ct, "text/plain") {
		t.Errorf("Content-Type = %q", ct)
	}
}
