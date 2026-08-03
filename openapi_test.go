package surf

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type oaCreateReq struct {
	Name string `json:"name" required:"true"`
}

type oaUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func TestOpenAPIDoc(t *testing.T) {
	app := NewApp()
	HandleJSONStatus(app, "POST", "/users", http.StatusCreated,
		func(c *Context, req oaCreateReq) (oaUser, error) { return oaUser{}, nil })
	HandleQuery(app, "GET", "/users/:id",
		func(c *Context) (oaUser, error) { return oaUser{}, nil })
	app.Get("/healthz", func(w http.ResponseWriter, r *http.Request) error { return nil })

	doc := app.OpenAPI(APIInfo{Title: "Test", Version: "1.2.3"})

	if doc.OpenAPI != "3.1.0" || doc.Info.Title != "Test" || doc.Info.Version != "1.2.3" {
		t.Fatalf("bad doc header: %+v", doc.Info)
	}

	// POST /users with a typed request body and 201 response.
	post := doc.Paths["/users"].Post
	if post == nil {
		t.Fatal("missing POST /users operation")
	}
	if post.RequestBody == nil || post.RequestBody.Content["application/json"].Schema.Ref != "#/components/schemas/oaCreateReq" {
		t.Errorf("POST /users request body schema wrong: %+v", post.RequestBody)
	}
	if _, ok := post.Responses["201"]; !ok {
		t.Errorf("POST /users should respond 201, got %v", keys(post.Responses))
	}

	// GET /users/{id} — path param converted, response typed, no body.
	get := doc.Paths["/users/{id}"].Get
	if get == nil {
		t.Fatal("missing GET /users/{id} operation (path not converted?)")
	}
	if get.RequestBody != nil {
		t.Error("GET should have no request body")
	}
	if len(get.Parameters) != 1 || get.Parameters[0].Name != "id" || get.Parameters[0].In != "path" || !get.Parameters[0].Required {
		t.Errorf("GET path param wrong: %+v", get.Parameters)
	}
	if _, ok := get.Responses["200"]; !ok {
		t.Error("GET should respond 200")
	}

	// Untyped route degrades: present, no body, free-form response.
	hz := doc.Paths["/healthz"].Get
	if hz == nil {
		t.Fatal("missing GET /healthz")
	}
	if hz.RequestBody != nil || hz.Responses["200"].Content != nil {
		t.Error("untyped route should have no body and free-form response")
	}

	// Nested schemas landed in components.
	if doc.Components == nil || doc.Components.Schemas["oaUser"] == nil || doc.Components.Schemas["oaCreateReq"] == nil {
		t.Error("component schemas missing")
	}
}

func TestOpenAPIHandlerServesJSON(t *testing.T) {
	app := NewApp()
	HandleQuery(app, "GET", "/ping", func(c *Context) (oaUser, error) { return oaUser{}, nil })
	h := app.OpenAPIHandler(APIInfo{Title: "T", Version: "1"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/openapi.json", nil)
	if err := h(rec, req); err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("content-type = %q", ct)
	}
	var doc OpenAPIDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if doc.Paths["/ping"] == nil {
		t.Error("served doc missing /ping")
	}
}

func TestOpenAPIVersionFallback(t *testing.T) {
	app := NewApp()
	doc := app.OpenAPI(APIInfo{Title: "T"})
	if doc.Info.Version != "0.0.0" {
		t.Errorf("empty version should fall back to 0.0.0, got %q", doc.Info.Version)
	}
}

func keys(m map[string]*Response) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// OpenAPIHandler caches the generated document. Building it lazily on first use
// must be synchronized, or concurrent first requests race on the cache.
func TestOpenAPIHandlerConcurrent(t *testing.T) {
	app := NewApp()
	HandleJSON(app, "POST", "/users",
		func(c *Context, req oaCreateReq) (oaUser, error) { return oaUser{}, nil })
	app.Get("/openapi.json", app.OpenAPIHandler(APIInfo{Title: "T", Version: "1"}))

	const n = 16
	var wg sync.WaitGroup
	bodies := make([]string, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, httptest.NewRequest("GET", "/openapi.json", nil))
			bodies[i] = rec.Body.String()
		}()
	}
	wg.Wait()

	for i, b := range bodies {
		if b == "" || b != bodies[0] {
			t.Fatalf("request %d returned a different document than request 0", i)
		}
	}
}
