package surf

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestStorage(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)

	// Store values
	Store(req, "key1", "value1")
	Store(req, "key2", 42)

	// Get values
	val, ok := Get(req, "key1")
	if !ok {
		t.Error("key1 should exist")
	}
	if val != "value1" {
		t.Errorf("key1 = %v, want %q", val, "value1")
	}

	val, ok = Get(req, "key2")
	if !ok {
		t.Error("key2 should exist")
	}
	if val != 42 {
		t.Errorf("key2 = %v, want 42", val)
	}

	// Get missing
	_, ok = Get(req, "missing")
	if ok {
		t.Error("missing should not exist")
	}

	// Delete
	Delete(req)
	_, ok = Get(req, "key1")
	if ok {
		t.Error("key1 should be deleted")
	}
}

func TestGetString(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)

	Store(req, "str", "hello")
	Store(req, "int", 42)

	// Existing string
	val := GetString(req, "str", "default")
	if val != "hello" {
		t.Errorf("GetString = %q, want %q", val, "hello")
	}

	// Missing key
	val = GetString(req, "missing", "default")
	if val != "default" {
		t.Errorf("GetString missing = %q, want %q", val, "default")
	}

	// Non-string value
	val = GetString(req, "int", "default")
	if val != "default" {
		t.Errorf("GetString non-string = %q, want %q", val, "default")
	}

	Delete(req)
}

func TestGetInt(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)

	Store(req, "int", 42)
	Store(req, "int64", int64(100))
	Store(req, "float64", float64(3.14))
	Store(req, "str", "hello")

	tests := []struct {
		key      string
		defVal   int
		expected int
	}{
		{"int", 0, 42},
		{"int64", 0, 100},
		{"float64", 0, 3},
		{"str", 99, 99},
		{"missing", 99, 99},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			val := GetInt(req, tt.key, tt.defVal)
			if val != tt.expected {
				t.Errorf("GetInt(%q) = %d, want %d", tt.key, val, tt.expected)
			}
		})
	}

	Delete(req)
}

func TestMustGet(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	Store(req, "exists", "value")

	// Should not panic for existing key
	val := MustGet(req, "exists")
	if val != "value" {
		t.Errorf("MustGet = %v, want %q", val, "value")
	}

	// Should panic for missing key
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustGet should panic for missing key")
		}
	}()

	MustGet(req, "missing")

	Delete(req)
}

func TestSetRequestIDAndUserID(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)

	SetRequestID(&req, "req-123")
	SetUserID(&req, "user-456")

	if GetRequestID(req) != "req-123" {
		t.Errorf("GetRequestID = %q, want %q", GetRequestID(req), "req-123")
	}

	if GetUserID(req) != "user-456" {
		t.Errorf("GetUserID = %q, want %q", GetUserID(req), "user-456")
	}

	Delete(req)
}

func TestWithRequestFluent(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)

	With(&req).
		Set("key1", "value1").
		Set("key2", "value2").
		SetRequestID("req-123").
		SetUserID("user-456")

	val, _ := Get(req, "key1")
	if val != "value1" {
		t.Errorf("key1 = %v, want %q", val, "value1")
	}

	val, _ = Get(req, "key2")
	if val != "value2" {
		t.Errorf("key2 = %v, want %q", val, "value2")
	}

	if GetRequestID(req) != "req-123" {
		t.Errorf("request_id = %q, want %q", GetRequestID(req), "req-123")
	}

	if GetUserID(req) != "user-456" {
		t.Errorf("user_id = %q, want %q", GetUserID(req), "user-456")
	}

	Delete(req)
}

func TestLogEntry(t *testing.T) {
	req := httptest.NewRequest("GET", "/test/path?query=1", nil)
	req.Header.Set("User-Agent", "TestAgent")
	req.Header.Set("Referer", "http://example.com")
	req.RemoteAddr = "192.168.1.1:12345"

	Store(req, "request_id", "test-req-id")
	Store(req, "user_id", "test-user-id")

	entry := &LogEntry{
		req:     req,
		status:  201,
		size:    1024,
		latency: 150000000, // 150ms
	}

	if entry.Method() != "GET" {
		t.Errorf("Method = %q, want %q", entry.Method(), "GET")
	}
	if entry.Path() != "/test/path" {
		t.Errorf("Path = %q, want %q", entry.Path(), "/test/path")
	}
	if entry.Status() != "201" {
		t.Errorf("Status = %q, want %q", entry.Status(), "201")
	}
	if entry.StatusCode() != 201 {
		t.Errorf("StatusCode = %d, want %d", entry.StatusCode(), 201)
	}
	if entry.Size() != "1024" {
		t.Errorf("Size = %q, want %q", entry.Size(), "1024")
	}
	if entry.SizeBytes() != 1024 {
		t.Errorf("SizeBytes = %d, want %d", entry.SizeBytes(), 1024)
	}
	if entry.UserAgent() != "TestAgent" {
		t.Errorf("UserAgent = %q, want %q", entry.UserAgent(), "TestAgent")
	}
	if entry.Referer() != "http://example.com" {
		t.Errorf("Referer = %q, want %q", entry.Referer(), "http://example.com")
	}
	if entry.RemoteAddr() != "192.168.1.1:12345" {
		t.Errorf("RemoteAddr = %q, want %q", entry.RemoteAddr(), "192.168.1.1:12345")
	}
	if entry.Proto() != "HTTP/1.1" {
		t.Errorf("Proto = %q, want %q", entry.Proto(), "HTTP/1.1")
	}
	if entry.RequestID() != "test-req-id" {
		t.Errorf("RequestID = %q, want %q", entry.RequestID(), "test-req-id")
	}
	if entry.UserID() != "test-user-id" {
		t.Errorf("UserID = %q, want %q", entry.UserID(), "test-user-id")
	}

	Delete(req)
}

func TestFormatLog(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/users", nil)
	Store(req, "custom_field", "custom_value")

	entry := &LogEntry{
		req:     req,
		status:  201,
		size:    256,
		latency: 50000000, // 50ms
	}

	template := "{method} {path} {status} {size}b {latency_ms}ms custom:{$custom_field}"
	result := formatLog(template, entry)

	expected := "POST /api/users 201 256b 50ms custom:custom_value"
	if result != expected {
		t.Errorf("formatLog = %q, want %q", result, expected)
	}

	Delete(req)
}

func TestGenerateRequestID(t *testing.T) {
	// Without prefix
	id1 := generateRequestID("")
	if len(id1) != 32 { // 16 bytes = 32 hex chars
		t.Errorf("id length = %d, want 32", len(id1))
	}

	// With prefix
	id2 := generateRequestID("api")
	if !strings.HasPrefix(id2, "api-") {
		t.Errorf("id should start with 'api-', got %q", id2)
	}

	// Should be unique
	id3 := generateRequestID("")
	if id1 == id3 {
		t.Error("generated IDs should be unique")
	}
}

func TestRequestIDMiddleware(t *testing.T) {
	app := NewApp()
	app.Use(RequestIDMiddleware("test"))

	var capturedID string
	app.Get("/test", func(w http.ResponseWriter, r *http.Request) error {
		capturedID = r.Context().Value(contextKey("request_id")).(string)
		w.Write([]byte("ok"))
		return nil
	})

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	// Check header
	headerID := rec.Header().Get("X-Request-ID")
	if headerID == "" {
		t.Error("X-Request-ID header should be set")
	}
	if !strings.HasPrefix(headerID, "test-") {
		t.Errorf("header ID should start with 'test-', got %q", headerID)
	}

	// Check context
	if capturedID != headerID {
		t.Errorf("context ID = %q, header ID = %q, should match", capturedID, headerID)
	}
}

func TestLoggingMiddleware(t *testing.T) {
	app := NewApp()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	slog.SetDefault(logger)

	app.Use(LoggingMiddleware("{method} {path} {status}"))

	app.Get("/test", func(w http.ResponseWriter, r *http.Request) error {
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte("ok"))
		return nil
	})

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	// Verify response
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusAccepted)
	}

	// Log output should contain the formatted message
	logOutput := buf.String()
	if !strings.Contains(logOutput, "GET /test 202") {
		t.Errorf("log should contain 'GET /test 202', got: %s", logOutput)
	}
}

func TestRequestLogger(t *testing.T) {
	app := NewApp()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	app.Use(RequestLogger(logger))

	app.Get("/api/data", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte(`{"data": "test"}`))
		return nil
	})

	req := httptest.NewRequest("GET", "/api/data", nil)
	req.Header.Set("User-Agent", "TestClient")
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	logOutput := buf.String()

	// Should contain structured fields
	if !strings.Contains(logOutput, `"method":"GET"`) {
		t.Errorf("log should contain method, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, `"path":"/api/data"`) {
		t.Errorf("log should contain path, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, `"status":200`) {
		t.Errorf("log should contain status, got: %s", logOutput)
	}
}

func TestRequestLoggerWithOptions(t *testing.T) {
	app := NewApp()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	opts := &RequestLoggerOptions{
		Logger:            logger,
		Level:             slog.LevelInfo,
		IncludeReqHeaders: true,
		GroupHeaders:      true,
		HeaderFilter: func(key string) bool {
			return key == "X-Custom"
		},
	}

	app.Use(RequestLoggerWithOptions(opts))

	app.Get("/test", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("ok"))
		return nil
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Custom", "custom-value")
	req.Header.Set("X-Other", "should-be-filtered")
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	logOutput := buf.String()

	// Should contain filtered header
	if !strings.Contains(logOutput, "X-Custom") {
		t.Errorf("log should contain X-Custom header, got: %s", logOutput)
	}
}

func TestGetService(t *testing.T) {
	app := NewApp()

	type MyService struct {
		Value string
	}

	svc := &MyService{Value: "test"}
	app.Set("myService", svc)

	var captured *MyService

	app.Get("/test", func(w http.ResponseWriter, r *http.Request) error {
		captured = GetService[*MyService](r, "myService")
		w.Write([]byte("ok"))
		return nil
	})

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if captured == nil {
		t.Fatal("service should not be nil")
	}
	if captured.Value != "test" {
		t.Errorf("service value = %q, want %q", captured.Value, "test")
	}
}

func TestGetServiceWrongType(t *testing.T) {
	app := NewApp()

	app.Set("stringService", "hello")

	var captured int

	app.Get("/test", func(w http.ResponseWriter, r *http.Request) error {
		captured = GetService[int](r, "stringService")
		w.Write([]byte("ok"))
		return nil
	})

	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	// Should return zero value for wrong type
	if captured != 0 {
		t.Errorf("wrong type service = %d, want 0", captured)
	}
}

func TestLoggerFromRequest(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)
	Store(req, "request_id", "test-123")
	Store(req, "user_id", "user-456")

	var buf bytes.Buffer
	baseLogger := slog.New(slog.NewJSONHandler(&buf, nil))

	logger := LoggerFromRequest(req, baseLogger)
	logger.Info("test message")

	logOutput := buf.String()

	if !strings.Contains(logOutput, `"request_id":"test-123"`) {
		t.Errorf("log should contain request_id, got: %s", logOutput)
	}
	if !strings.Contains(logOutput, `"user_id":"user-456"`) {
		t.Errorf("log should contain user_id, got: %s", logOutput)
	}

	Delete(req)
}

func TestStorageConcurrency(t *testing.T) {
	req := httptest.NewRequest("GET", "/test", nil)

	done := make(chan bool)

	// Concurrent writes
	for i := 0; i < 100; i++ {
		go func(n int) {
			Store(req, "key", n)
			done <- true
		}(i)
	}

	// Wait for writes
	for i := 0; i < 100; i++ {
		<-done
	}

	// Concurrent reads
	for i := 0; i < 100; i++ {
		go func() {
			Get(req, "key")
			done <- true
		}()
	}

	// Wait for reads
	for i := 0; i < 100; i++ {
		<-done
	}

	Delete(req)
}
