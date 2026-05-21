package surf

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestJSONData(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := JSONData(rec, map[string]int{"n": 1}); err != nil {
		t.Fatalf("JSONData: %v", err)
	}
	if ct := rec.Header().Get("Content-Type"); ct != jsonContentType {
		t.Errorf("Content-Type = %q", ct)
	}
	var got struct {
		Data map[string]int `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Data["n"] != 1 {
		t.Errorf("data = %v", got.Data)
	}
}

func TestJSONList(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := JSONList(rec, []string{"a", "b"}, 42); err != nil {
		t.Fatalf("JSONList: %v", err)
	}
	var got struct {
		Data  []string `json:"data"`
		Total int      `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Total != 42 || len(got.Data) != 2 {
		t.Errorf("got %+v", got)
	}
}

func TestJSONError(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := JSONError(rec, 404, "missing"); err != nil {
		t.Fatalf("JSONError: %v", err)
	}
	if rec.Code != 404 {
		t.Errorf("code = %d", rec.Code)
	}
	var got errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Error != "missing" || got.Status != 404 {
		t.Errorf("got %+v", got)
	}
}

func TestDefaultErrorRendererHTTPError(t *testing.T) {
	rec := httptest.NewRecorder()
	DefaultErrorRenderer(rec, httptest.NewRequest("GET", "/", nil),
		Errorf(403, "forbidden", errExample))
	if rec.Code != 403 {
		t.Errorf("code = %d, want 403", rec.Code)
	}
	if !contains(rec.Body.String(), `"error":"forbidden"`) {
		t.Errorf("body = %q", rec.Body.String())
	}
	if contains(rec.Body.String(), "example") {
		t.Errorf("body leaked underlying error: %q", rec.Body.String())
	}
}

func TestDefaultErrorRendererGeneric(t *testing.T) {
	rec := httptest.NewRecorder()
	DefaultErrorRenderer(rec, httptest.NewRequest("GET", "/", nil), errExample)
	if rec.Code != 500 {
		t.Errorf("code = %d, want 500", rec.Code)
	}
	if contains(rec.Body.String(), "example") {
		t.Errorf("generic 500 leaked detail: %q", rec.Body.String())
	}
}
