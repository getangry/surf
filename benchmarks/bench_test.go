// Package surfbench compares surf's routing throughput against other popular
// Go HTTP frameworks. It lives in its own module so the surf module itself
// stays dependency-free.
//
//	cd benchmarks && go test -bench=. -benchmem
package surfbench

import (
	"net/http"
	"testing"

	"github.com/getangry/surf"
	chi "github.com/go-chi/chi/v5"
	"github.com/gin-gonic/gin"
	echo "github.com/labstack/echo/v4"
)

// mockRW is a no-op http.ResponseWriter so benchmarks measure router and
// framework overhead rather than response recording.
type mockRW struct{ h http.Header }

func (m *mockRW) Header() http.Header {
	if m.h == nil {
		m.h = http.Header{}
	}
	return m.h
}
func (m *mockRW) Write(b []byte) (int, error) { return len(b), nil }
func (m *mockRW) WriteHeader(int)             {}

func surfRouter() http.Handler {
	app := surf.NewApp()
	app.Get("/", func(w http.ResponseWriter, r *http.Request) error {
		_, _ = w.Write([]byte("ok"))
		return nil
	})
	app.Get("/users/:id", func(w http.ResponseWriter, r *http.Request) error {
		_, _ = w.Write([]byte(surf.Param(r, "id")))
		return nil
	})
	return app
}

// surfFastRouter uses surf's opt-in fast path (App.Handle + *surf.Context).
func surfFastRouter() http.Handler {
	app := surf.NewApp()
	app.Handle("GET", "/", func(c *surf.Context) error {
		return c.String(http.StatusOK, "ok")
	})
	app.Handle("GET", "/users/:id", func(c *surf.Context) error {
		return c.String(http.StatusOK, c.Param("id"))
	})
	return app
}

func ginRouter() http.Handler {
	gin.SetMode(gin.ReleaseMode)
	g := gin.New()
	g.GET("/", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	g.GET("/users/:id", func(c *gin.Context) { c.String(http.StatusOK, c.Param("id")) })
	return g
}

func echoRouter() http.Handler {
	e := echo.New()
	e.GET("/", func(c echo.Context) error { return c.String(http.StatusOK, "ok") })
	e.GET("/users/:id", func(c echo.Context) error { return c.String(http.StatusOK, c.Param("id")) })
	return e
}

func chiRouter() http.Handler {
	r := chi.NewRouter()
	r.Get("/", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	r.Get("/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(chi.URLParam(r, "id")))
	})
	return r
}

func stdlibRouter() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	mux.HandleFunc("GET /users/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.PathValue("id")))
	})
	return mux
}

var frameworks = []struct {
	name  string
	build func() http.Handler
}{
	{"surf", surfRouter},
	{"surf-fast", surfFastRouter},
	{"gin", ginRouter},
	{"echo", echoRouter},
	{"chi", chiRouter},
	{"stdlib", stdlibRouter},
}

func benchPath(b *testing.B, method, path string) {
	for _, fw := range frameworks {
		h := fw.build()
		req, _ := http.NewRequest(method, path, nil)
		b.Run(fw.name, func(b *testing.B) {
			w := &mockRW{}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				h.ServeHTTP(w, req)
			}
		})
	}
}

// BenchmarkStaticRoute measures a bare static-route dispatch.
func BenchmarkStaticRoute(b *testing.B) { benchPath(b, "GET", "/") }

// BenchmarkParamRoute measures a single path-parameter dispatch and lookup.
func BenchmarkParamRoute(b *testing.B) { benchPath(b, "GET", "/users/42") }
