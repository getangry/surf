package surf

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestResponseWriterBasic(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewResponseWriter(rec)

	// Default status is 200
	if rw.Status() != http.StatusOK {
		t.Errorf("default status = %d, want %d", rw.Status(), http.StatusOK)
	}

	// Write should set written flag
	n, err := rw.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("write error: %v", err)
	}
	if n != 5 {
		t.Errorf("bytes written = %d, want 5", n)
	}
	if !rw.Written() {
		t.Error("written flag should be true")
	}
	if rw.Size() != 5 {
		t.Errorf("size = %d, want 5", rw.Size())
	}
}

func TestResponseWriterWriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewResponseWriter(rec)

	rw.WriteHeader(http.StatusCreated)

	if rw.Status() != http.StatusCreated {
		t.Errorf("status = %d, want %d", rw.Status(), http.StatusCreated)
	}

	// Second WriteHeader should be ignored
	rw.WriteHeader(http.StatusNotFound)
	if rw.Status() != http.StatusCreated {
		t.Errorf("status after second WriteHeader = %d, want %d", rw.Status(), http.StatusCreated)
	}
}

func TestResponseWriterImplicitWriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewResponseWriter(rec)

	// Write without WriteHeader should set status to 200
	rw.Write([]byte("test"))

	if rw.Status() != http.StatusOK {
		t.Errorf("implicit status = %d, want %d", rw.Status(), http.StatusOK)
	}
}

func TestResponseWriterLatencyZeroWhenUnset(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewResponseWriter(rec)
	// StartTime is not set by NewResponseWriter; callers opt in.
	if got := rw.Latency(); got != 0 {
		t.Errorf("Latency() with unset StartTime = %v, want 0", got)
	}
}

func TestResponseWriterLatencyAfterStartTimeSet(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewResponseWriter(rec)
	rw.StartTime = time.Now()

	time.Sleep(10 * time.Millisecond)
	latency := rw.Latency()
	if latency < 10*time.Millisecond {
		t.Errorf("latency = %v, want >= 10ms", latency)
	}
}

func TestResponseWriterStartTimeField(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewResponseWriter(rec)

	// Field is exported and settable; reads back the same value.
	before := time.Now()
	rw.StartTime = before
	if rw.StartTime != before {
		t.Errorf("StartTime field read = %v, want %v", rw.StartTime, before)
	}
}

func TestResponseWriterCustomData(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewResponseWriter(rec)

	// Set various types
	rw.Set("string", "value")
	rw.Set("int", 42)
	rw.Set("bool", true)

	// Get string
	val, ok := rw.Get("string")
	if !ok {
		t.Error("string key should exist")
	}
	if val != "value" {
		t.Errorf("string value = %v, want %q", val, "value")
	}

	// Get int
	val, ok = rw.Get("int")
	if !ok {
		t.Error("int key should exist")
	}
	if val != 42 {
		t.Errorf("int value = %v, want 42", val)
	}

	// Get missing key
	_, ok = rw.Get("missing")
	if ok {
		t.Error("missing key should not exist")
	}

	// GetString
	str := rw.GetString("string", "default")
	if str != "value" {
		t.Errorf("GetString = %q, want %q", str, "value")
	}

	// GetString with default
	str = rw.GetString("missing", "default")
	if str != "default" {
		t.Errorf("GetString missing = %q, want %q", str, "default")
	}

	// GetString with non-string value
	str = rw.GetString("int", "default")
	if str != "default" {
		t.Errorf("GetString non-string = %q, want %q", str, "default")
	}
}

func TestResponseWriterCustomDataConcurrent(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewResponseWriter(rec)

	done := make(chan bool)

	// Concurrent writes
	for i := 0; i < 100; i++ {
		go func(n int) {
			rw.Set(string(rune('a'+n%26)), n)
			done <- true
		}(i)
	}

	// Wait for writes
	for i := 0; i < 100; i++ {
		<-done
	}

	// Concurrent reads
	for i := 0; i < 100; i++ {
		go func(n int) {
			rw.Get(string(rune('a' + n%26)))
			done <- true
		}(i)
	}

	// Wait for reads
	for i := 0; i < 100; i++ {
		<-done
	}
}

func TestResponseWriterCustomDataCopy(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewResponseWriter(rec)

	rw.Set("key1", "value1")
	rw.Set("key2", "value2")

	copy := rw.CustomData()

	// Verify copy has the data
	if copy["key1"] != "value1" {
		t.Errorf("copy[key1] = %v, want %q", copy["key1"], "value1")
	}

	// Modify copy shouldn't affect original
	copy["key1"] = "modified"
	val, _ := rw.Get("key1")
	if val != "value1" {
		t.Errorf("original modified: %v, want %q", val, "value1")
	}
}

func TestResponseWriterMultipleWrites(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewResponseWriter(rec)

	rw.Write([]byte("hello"))
	rw.Write([]byte(" "))
	rw.Write([]byte("world"))

	if rw.Size() != 11 {
		t.Errorf("total size = %d, want 11", rw.Size())
	}
	if rec.Body.String() != "hello world" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "hello world")
	}
}

func TestResponseWriterFlush(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewResponseWriter(rec)

	rw.Write([]byte("data"))
	rw.Flush()

	if !rec.Flushed {
		t.Error("response should be flushed")
	}
}

func TestResponseWriterHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	rw := NewResponseWriter(rec)

	rw.Header().Set("X-Custom", "value")

	if rec.Header().Get("X-Custom") != "value" {
		t.Errorf("header not set correctly")
	}
}

func TestGetResponseWriter(t *testing.T) {
	app := NewApp()

	var capturedRW *ResponseWriter

	app.Get("/test", func(w http.ResponseWriter, r *http.Request) error {
		capturedRW = GetResponseWriter(r)
		w.Write([]byte("ok"))
		return nil
	})

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if capturedRW == nil {
		t.Error("GetResponseWriter should return ResponseWriter from context")
	}
}

func TestResponseStatus(t *testing.T) {
	app := NewApp()

	var status int

	app.Get("/test", func(w http.ResponseWriter, r *http.Request) error {
		w.WriteHeader(http.StatusAccepted)
		status = ResponseStatus(r)
		return nil
	})

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if status != http.StatusAccepted {
		t.Errorf("ResponseStatus = %d, want %d", status, http.StatusAccepted)
	}
}

func TestResponseSize(t *testing.T) {
	app := NewApp()

	var size int

	app.After(func(w http.ResponseWriter, r *http.Request) error {
		size = ResponseSize(r)
		return nil
	})

	app.Get("/test", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("12345"))
		return nil
	})

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if size != 5 {
		t.Errorf("ResponseSize = %d, want 5", size)
	}
}
