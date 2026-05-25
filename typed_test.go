package surf

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type typedReq struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

func (r typedReq) Validate() error {
	if r.Name == "" {
		return errors.New("name is required")
	}
	return nil
}

type typedResp struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func typedJSONPost(body string) *http.Request {
	r := httptest.NewRequest("POST", "/users", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestHandleJSONHappyPath(t *testing.T) {
	app := NewApp()
	HandleJSON(app, "POST", "/users",
		func(c *Context, req typedReq) (typedResp, error) {
			return typedResp{ID: "42", Name: req.Name, Email: req.Email}, nil
		},
	)

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, typedJSONPost(`{"name":"Ada","email":"a@b.c"}`))

	if rec.Code != http.StatusOK {
		t.Errorf("code = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"id":"42"`) || !strings.Contains(body, `"name":"Ada"`) {
		t.Errorf("body = %q", body)
	}
}

func TestHandleJSONStatusCustomStatus(t *testing.T) {
	app := NewApp()
	HandleJSONStatus(app, "POST", "/users", http.StatusCreated,
		func(c *Context, req typedReq) (typedResp, error) {
			return typedResp{ID: "42", Name: req.Name}, nil
		},
	)

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, typedJSONPost(`{"name":"Ada","email":"a@b.c"}`))

	if rec.Code != http.StatusCreated {
		t.Errorf("code = %d, want 201", rec.Code)
	}
}

func TestHandleJSONBindError(t *testing.T) {
	app := NewApp()
	HandleJSON(app, "POST", "/users",
		func(c *Context, req typedReq) (typedResp, error) {
			t.Fatal("handler should not be called on bind failure")
			return typedResp{}, nil
		},
	)

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, typedJSONPost(`{"name":`))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("code = %d, want 400", rec.Code)
	}
}

func TestHandleJSONValidationError(t *testing.T) {
	app := NewApp()
	HandleJSON(app, "POST", "/users",
		func(c *Context, req typedReq) (typedResp, error) {
			t.Fatal("handler should not be called on validation failure")
			return typedResp{}, nil
		},
	)

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, typedJSONPost(`{"email":"a@b.c"}`)) // missing name

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("code = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "name is required") {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestHandleJSONHandlerHTTPError(t *testing.T) {
	app := NewApp()
	HandleJSON(app, "POST", "/users",
		func(c *Context, req typedReq) (typedResp, error) {
			return typedResp{}, NewHTTPError(http.StatusConflict, "user exists")
		},
	)

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, typedJSONPost(`{"name":"Ada","email":"a@b.c"}`))

	if rec.Code != http.StatusConflict {
		t.Errorf("code = %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"error":"user exists"`) {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestHandleQuery(t *testing.T) {
	app := NewApp()
	HandleQuery(app, "GET", "/users/:id",
		func(c *Context) (typedResp, error) {
			return typedResp{ID: c.Param("id"), Name: c.Query("name")}, nil
		},
	)

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/users/7?name=Ada", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("code = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"id":"7"`) ||
		!strings.Contains(rec.Body.String(), `"name":"Ada"`) {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestHandleJSONCapturesTypesInRouteInfo(t *testing.T) {
	app := NewApp()
	HandleJSON(app, "POST", "/users",
		func(c *Context, req typedReq) (typedResp, error) { return typedResp{}, nil },
	)
	HandleQuery(app, "GET", "/healthz",
		func(c *Context) (typedResp, error) { return typedResp{}, nil },
	)

	routes := app.Routes()
	if len(routes) != 2 {
		t.Fatalf("len(routes) = %d, want 2", len(routes))
	}

	// POST /users — both types captured.
	got := routes[0]
	if got.ReqType != reflect.TypeOf(typedReq{}) {
		t.Errorf("ReqType = %v, want typedReq", got.ReqType)
	}
	if got.RespType != reflect.TypeOf(typedResp{}) {
		t.Errorf("RespType = %v, want typedResp", got.RespType)
	}

	// GET /healthz — only resp type captured.
	got = routes[1]
	if got.ReqType != nil {
		t.Errorf("HandleQuery should have ReqType = nil, got %v", got.ReqType)
	}
	if got.RespType != reflect.TypeOf(typedResp{}) {
		t.Errorf("RespType = %v, want typedResp", got.RespType)
	}
}

func TestHandleJSONWithMiddleware(t *testing.T) {
	app := NewApp()
	mw := func(next CtxHandler) CtxHandler {
		return func(c *Context) error {
			c.Writer().Header().Set("X-Wrapped", "yes")
			return next(c)
		}
	}
	HandleJSON(app, "POST", "/users",
		func(c *Context, req typedReq) (typedResp, error) {
			return typedResp{ID: "1", Name: req.Name}, nil
		},
		mw,
	)

	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, typedJSONPost(`{"name":"Ada","email":"a@b.c"}`))

	if rec.Header().Get("X-Wrapped") != "yes" {
		t.Errorf("middleware did not run: X-Wrapped = %q", rec.Header().Get("X-Wrapped"))
	}
	if rec.Code != http.StatusOK {
		t.Errorf("code = %d", rec.Code)
	}
}
