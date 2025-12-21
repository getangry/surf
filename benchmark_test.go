package surf

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Benchmark basic routing performance
func BenchmarkRouting(b *testing.B) {
	app := NewApp()

	app.Get("/", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("OK"))
		return nil
	})

	app.Get("/users/:id", func(w http.ResponseWriter, r *http.Request) error {
		id := Param(r, "id")
		w.Write([]byte(id))
		return nil
	})

	app.Post("/api/data", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("created"))
		return nil
	})

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"Root", "GET", "/"},
		{"Param", "GET", "/users/123"},
		{"Post", "POST", "/api/data"},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				w := httptest.NewRecorder()
				app.ServeHTTP(w, req)
			}
		})
	}
}

// Benchmark middleware stack
func BenchmarkMiddleware(b *testing.B) {
	tests := []struct {
		name        string
		middlewares int
	}{
		{"NoMiddleware", 0},
		{"1Middleware", 1},
		{"3Middlewares", 3},
		{"5Middlewares", 5},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			app := NewApp()

			// Add dummy middlewares
			for i := 0; i < tt.middlewares; i++ {
				app.Use(func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						next.ServeHTTP(w, r)
					})
				})
			}

			app.Get("/", func(w http.ResponseWriter, r *http.Request) error {
				w.Write([]byte("OK"))
				return nil
			})

			req := httptest.NewRequest("GET", "/", nil)
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				w := httptest.NewRecorder()
				app.ServeHTTP(w, req)
			}
		})
	}
}

// Benchmark ResponseWriter wrapper
func BenchmarkResponseWriter(b *testing.B) {
	b.Run("Direct", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			w := httptest.NewRecorder()
			w.Write([]byte("test response"))
		}
	})

	b.Run("Wrapped", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			w := httptest.NewRecorder()
			rw := NewResponseWriter(w)
			rw.Write([]byte("test response"))
		}
	})

	b.Run("WrappedWithCustomData", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			w := httptest.NewRecorder()
			rw := NewResponseWriter(w)
			rw.Set("key1", "value1")
			rw.Set("key2", "value2")
			rw.Set("key3", "value3")
			rw.Write([]byte("test response"))
		}
	})
}

// Benchmark request storage (the global map concern)
func BenchmarkRequestStorage(b *testing.B) {
	b.Run("Store", func(b *testing.B) {
		req := httptest.NewRequest("GET", "/", nil)
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			Store(req, "key", "value")
		}

		// Cleanup
		Delete(req)
	})

	b.Run("Get", func(b *testing.B) {
		req := httptest.NewRequest("GET", "/", nil)
		Store(req, "key", "value")
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			Get(req, "key")
		}

		// Cleanup
		Delete(req)
	})

	b.Run("ConcurrentAccess", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				req := httptest.NewRequest("GET", "/", nil)
				Store(req, "key", "value")
				Get(req, "key")
				Delete(req)
			}
		})
	})
}

// Benchmark logging middlewares
func BenchmarkLogging(b *testing.B) {
	app := NewApp()
	app.Get("/test", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("test"))
		return nil
	})

	b.Run("NoLogging", func(b *testing.B) {
		req := httptest.NewRequest("GET", "/test", nil)
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			w := httptest.NewRecorder()
			app.ServeHTTP(w, req)
		}
	})

	b.Run("WithRequestLogger", func(b *testing.B) {
		appWithLog := NewApp()

		// Use a no-op logger for benchmarking
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		appWithLog.Use(RequestLogger(logger))

		appWithLog.Get("/test", func(w http.ResponseWriter, r *http.Request) error {
			w.Write([]byte("test"))
			return nil
		})

		req := httptest.NewRequest("GET", "/test", nil)
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			w := httptest.NewRecorder()
			appWithLog.ServeHTTP(w, req)
		}
	})

	b.Run("WithRequestID", func(b *testing.B) {
		appWithID := NewApp()
		appWithID.Use(RequestIDMiddleware("bench"))

		appWithID.Get("/test", func(w http.ResponseWriter, r *http.Request) error {
			w.Write([]byte("test"))
			return nil
		})

		req := httptest.NewRequest("GET", "/test", nil)
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			w := httptest.NewRecorder()
			appWithID.ServeHTTP(w, req)
		}
	})
}

// Benchmark JSON operations
func BenchmarkJSON(b *testing.B) {
	type TestData struct {
		ID     string   `json:"id"`
		Name   string   `json:"name"`
		Count  int      `json:"count"`
		Tags   []string `json:"tags"`
	}

	data := TestData{
		ID:    "test-123",
		Name:  "Test Item",
		Count: 42,
		Tags:  []string{"tag1", "tag2", "tag3"},
	}

	b.Run("Encoding", func(b *testing.B) {
		app := NewApp()
		app.Get("/json", func(w http.ResponseWriter, r *http.Request) error {
			return json.NewEncoder(w).Encode(data)
		})

		req := httptest.NewRequest("GET", "/json", nil)
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			w := httptest.NewRecorder()
			app.ServeHTTP(w, req)
		}
	})

	b.Run("Decoding", func(b *testing.B) {
		app := NewApp()
		app.Post("/json", func(w http.ResponseWriter, r *http.Request) error {
			var d TestData
			if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
				return err
			}
			w.Write([]byte("OK"))
			return nil
		})

		body, _ := json.Marshal(data)
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			req := httptest.NewRequest("POST", "/json", bytes.NewReader(body))
			w := httptest.NewRecorder()
			app.ServeHTTP(w, req)
		}
	})
}

// Benchmark path parameter extraction
func BenchmarkParamExtraction(b *testing.B) {
	app := NewApp()

	app.Get("/users/:id/posts/:postId/comments/:commentId", func(w http.ResponseWriter, r *http.Request) error {
		userID := Param(r, "id")
		postID := Param(r, "postId")
		commentID := Param(r, "commentId")
		w.Write([]byte(userID + postID + commentID))
		return nil
	})

	req := httptest.NewRequest("GET", "/users/123/posts/456/comments/789", nil)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, req)
	}
}

// Benchmark memory allocations
func BenchmarkAllocations(b *testing.B) {
	b.Run("SimpleRequest", func(b *testing.B) {
		app := NewApp()
		app.Get("/", func(w http.ResponseWriter, r *http.Request) error {
			w.Write([]byte("OK"))
			return nil
		})

		req := httptest.NewRequest("GET", "/", nil)
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			w := httptest.NewRecorder()
			app.ServeHTTP(w, req)
		}
	})

	b.Run("WithMiddleware", func(b *testing.B) {
		app := NewApp()
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		app.Use(RequestIDMiddleware("test"))
		app.Use(RequestLogger(logger))

		app.Get("/", func(w http.ResponseWriter, r *http.Request) error {
			w.Write([]byte("OK"))
			return nil
		})

		req := httptest.NewRequest("GET", "/", nil)
		b.ReportAllocs()
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			w := httptest.NewRecorder()
			app.ServeHTTP(w, req)
		}
	})
}

// Compare with standard library mux
func BenchmarkComparison(b *testing.B) {
	b.Run("Surf", func(b *testing.B) {
		app := NewApp()
		app.Get("/api/users/:id", func(w http.ResponseWriter, r *http.Request) error {
			id := Param(r, "id")
			fmt.Fprintf(w, "User: %s", id)
			return nil
		})

		req := httptest.NewRequest("GET", "/api/users/123", nil)
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			w := httptest.NewRecorder()
			app.ServeHTTP(w, req)
		}
	})

	b.Run("StdMux", func(b *testing.B) {
		mux := http.NewServeMux()
		mux.HandleFunc("/api/users/", func(w http.ResponseWriter, r *http.Request) {
			// Note: std mux doesn't have param extraction
			id := r.URL.Path[len("/api/users/"):]
			fmt.Fprintf(w, "User: %s", id)
		})

		req := httptest.NewRequest("GET", "/api/users/123", nil)
		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
		}
	})
}