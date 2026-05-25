package surf

import (
	"net/http"
	"reflect"
)

// HandleJSON registers a fast-path route whose handler receives a typed
// request value (bound from the JSON body) and returns a typed response
// value (encoded as JSON with status 200).
//
//	type CreateUser struct {
//	    Name  string `json:"name"`
//	    Email string `json:"email"`
//	}
//	type User struct {
//	    ID    string `json:"id"`
//	    Name  string `json:"name"`
//	    Email string `json:"email"`
//	}
//
//	surf.HandleJSON(app, "POST", "/users",
//	    func(c *surf.Context, req CreateUser) (User, error) {
//	        return store.Create(req)
//	    },
//	)
//
// The framework owns the bind → validate → call → encode pipeline. A bind
// failure (bad JSON, oversized body, Validator error) flows through the
// configured error renderer as an *HTTPError. A handler-returned error does
// the same; an *HTTPError sets the status, anything else becomes a generic
// 500.
//
// Use HandleJSONStatus for a non-200 success status, or HandleQuery for an
// endpoint with no request body.
//
// HandleJSON also records the Req and Resp types in the route's RouteInfo
// so introspection / OpenAPI generation can see them.
func HandleJSON[Req any, Resp any](
	app *App,
	method, pattern string,
	fn func(c *Context, req Req) (Resp, error),
	middleware ...CtxMiddleware,
) {
	app.Handle(method, pattern, func(c *Context) error {
		var req Req
		if err := c.BindAndValidate(&req); err != nil {
			return err
		}
		resp, err := fn(c, req)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusOK, resp)
	}, middleware...)
	captureTypes[Req, Resp](app)
}

// HandleJSONStatus is HandleJSON with a caller-specified success status
// (typically http.StatusCreated for POST).
func HandleJSONStatus[Req any, Resp any](
	app *App,
	method, pattern string,
	successStatus int,
	fn func(c *Context, req Req) (Resp, error),
	middleware ...CtxMiddleware,
) {
	app.Handle(method, pattern, func(c *Context) error {
		var req Req
		if err := c.BindAndValidate(&req); err != nil {
			return err
		}
		resp, err := fn(c, req)
		if err != nil {
			return err
		}
		return c.JSON(successStatus, resp)
	}, middleware...)
	captureTypes[Req, Resp](app)
}

// HandleQuery registers a typed route with no request body — useful for GET
// endpoints whose only input comes from path/query parameters via the
// Context. The response is encoded as JSON with status 200.
func HandleQuery[Resp any](
	app *App,
	method, pattern string,
	fn func(c *Context) (Resp, error),
	middleware ...CtxMiddleware,
) {
	app.Handle(method, pattern, func(c *Context) error {
		resp, err := fn(c)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusOK, resp)
	}, middleware...)
	captureRespType[Resp](app)
}

// captureTypes populates the just-registered route's ReqType and RespType.
// Route registration is expected to be single-threaded (during app setup),
// so the post-append mutation is safe.
func captureTypes[Req, Resp any](app *App) {
	n := len(app.router.routeInfo)
	if n == 0 {
		return
	}
	app.router.routeInfo[n-1].ReqType = reflect.TypeOf((*Req)(nil)).Elem()
	app.router.routeInfo[n-1].RespType = reflect.TypeOf((*Resp)(nil)).Elem()
}

// captureRespType populates only the just-registered route's RespType.
func captureRespType[Resp any](app *App) {
	n := len(app.router.routeInfo)
	if n == 0 {
		return
	}
	app.router.routeInfo[n-1].RespType = reflect.TypeOf((*Resp)(nil)).Elem()
}
