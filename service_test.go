package surf

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type greeter interface{ Greet() string }

type enGreeter struct{}

func (enGreeter) Greet() string { return "hello" }

func TestProvideAndService(t *testing.T) {
	app := NewApp()
	Provide[*enGreeter](app, &enGreeter{})
	Provide[greeter](app, enGreeter{}) // register under an interface type

	var (
		gotConcrete bool
		gotIface    string
	)
	app.Get("/svc", func(w http.ResponseWriter, r *http.Request) error {
		if c, ok := Service[*enGreeter](r); ok && c != nil {
			gotConcrete = true
		}
		if i, ok := Service[greeter](r); ok {
			gotIface = i.Greet()
		}
		return nil
	})

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/svc", nil))

	if !gotConcrete {
		t.Error("Service[*enGreeter] did not resolve")
	}
	if gotIface != "hello" {
		t.Errorf("Service[greeter] = %q, want hello", gotIface)
	}
}

func TestServiceMissing(t *testing.T) {
	app := NewApp()
	var resolved bool
	app.Get("/x", func(w http.ResponseWriter, r *http.Request) error {
		_, ok := Service[*enGreeter](r)
		resolved = ok
		return nil
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if resolved {
		t.Error("Service resolved an unregistered type")
	}
}

func TestMustServicePanics(t *testing.T) {
	app := NewApp()
	app.Use(RecoveryWithDefaults())
	app.Get("/x", func(w http.ResponseWriter, r *http.Request) error {
		MustService[*enGreeter](r) // unregistered -> panic -> recovered as 500
		return nil
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/x", nil))
	if rec.Code != 500 {
		t.Errorf("code = %d, want 500", rec.Code)
	}
}
