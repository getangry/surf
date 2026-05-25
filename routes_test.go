package surf

import (
	"net/http"
	"testing"
)

func TestRoutesIntrospection(t *testing.T) {
	app := NewApp()
	app.Get("/users", func(w http.ResponseWriter, r *http.Request) error { return nil })
	app.Post("/users/:id", func(w http.ResponseWriter, r *http.Request) error { return nil })
	app.Handle("GET", "/healthz", func(c *Context) error { return nil })

	api := app.Group("/api")
	api.Get("/widgets", func(w http.ResponseWriter, r *http.Request) error { return nil })
	api.Handle("GET", "/orders/:id", func(c *Context) error { return nil })

	got := app.Routes()
	if len(got) != 5 {
		t.Fatalf("len(Routes()) = %d, want 5", len(got))
	}

	type expect struct {
		Method, Pattern string
		Style           RouteStyle
		NParams         int
	}
	wants := []expect{
		{"GET", "/users", StyleStandard, 0},
		{"POST", "/users/:id", StyleStandard, 1},
		{"GET", "/healthz", StyleContext, 0},
		{"GET", "/api/widgets", StyleStandard, 0},
		{"GET", "/api/orders/:id", StyleContext, 1},
	}
	for i, w := range wants {
		r := got[i]
		if r.Method != w.Method || r.Pattern != w.Pattern || r.Style != w.Style {
			t.Errorf("Routes()[%d] = {%q %q %s}, want {%q %q %s}",
				i, r.Method, r.Pattern, r.Style, w.Method, w.Pattern, w.Style)
		}
		if len(r.Params) != w.NParams {
			t.Errorf("Routes()[%d].Params = %v, want %d params", i, r.Params, w.NParams)
		}
	}
}

func TestRoutesReturnsCopy(t *testing.T) {
	app := NewApp()
	app.Get("/x", func(w http.ResponseWriter, r *http.Request) error { return nil })

	a := app.Routes()
	a[0].Pattern = "/mutated"

	b := app.Routes()
	if b[0].Pattern != "/x" {
		t.Errorf("Routes() snapshot was mutated externally: got %q", b[0].Pattern)
	}
}

func TestRouteStyleString(t *testing.T) {
	if StyleStandard.String() != "standard" {
		t.Errorf("StyleStandard.String() = %q", StyleStandard.String())
	}
	if StyleContext.String() != "context" {
		t.Errorf("StyleContext.String() = %q", StyleContext.String())
	}
}
