package surf

import (
	"context"
	"net/http"
	"net/url"
	"sync"
)

// CtxHandler is the fast-path handler signature. Unlike HandlerFunc it
// receives a pooled *Context directly, so the router copies neither the
// *http.Request nor allocates per-request state. Register one with App.Handle.
type CtxHandler func(c *Context) error

// CtxMiddleware wraps a CtxHandler. Compose fast-path middleware by passing
// CtxMiddleware values to App.Handle; they run outermost-first.
type CtxMiddleware func(next CtxHandler) CtxHandler

// Context is the argument to a fast-path handler. It bundles the request, a
// status-tracking response writer, and resolved path parameters.
//
// A Context is pooled and recycled the moment its handler returns. Do not
// retain a *Context — or anything derived from it, including c.Request — in a
// goroutine that outlives the handler.
type Context struct {
	// Request is the incoming request. Unlike the standard path, it is the
	// original request: no per-request copy is made.
	Request *http.Request

	resp   ResponseWriter
	app    *App
	params []paramKV
	pbuf   [inlineParams]paramKV

	// Lazy-parsed request data — populated on first access. Handlers that
	// never call Cookies() or QueryValues() pay nothing for these fields
	// beyond the struct size.
	cookieOnce sync.Once
	cookies    []*http.Cookie
	queryOnce  sync.Once
	query      url.Values
}

var ctxPool = sync.Pool{New: func() any {
	c := &Context{}
	c.params = c.pbuf[:0]
	return c
}}

func getContext() *Context { return ctxPool.Get().(*Context) }

func putContext(c *Context) {
	c.Request = nil
	c.app = nil
	c.resp.recycle()
	c.params = c.params[:0]
	c.cookieOnce = sync.Once{}
	c.cookies = nil
	c.queryOnce = sync.Once{}
	c.query = nil
	ctxPool.Put(c)
}

// init wires a checked-out Context to a request. Path parameters are already
// present in c.params, having been resolved during route matching.
func (c *Context) init(app *App, w http.ResponseWriter, r *http.Request) {
	c.app = app
	c.Request = r
	c.resp.initWriter(w)
}

// Writer returns the status-tracking response writer for this request.
func (c *Context) Writer() *ResponseWriter { return &c.resp }

// Context returns the request's context.
func (c *Context) Context() context.Context { return c.Request.Context() }

// Method returns the request's HTTP method.
func (c *Context) Method() string { return c.Request.Method }

// Path returns the request's URL path.
func (c *Context) Path() string { return c.Request.URL.Path }

// Param returns a resolved path parameter, or "" if absent. The wildcard
// match is available as Param("*").
func (c *Context) Param(key string) string {
	for i := range c.params {
		if c.params[i].key == key {
			return c.params[i].val
		}
	}
	return ""
}

// Query returns a single URL query parameter value, or "" if absent. The
// query string is parsed once per request and shared with QueryValues, so
// repeated calls do not re-parse.
func (c *Context) Query(key string) string {
	return c.QueryValues().Get(key)
}

// QueryValues returns the request's parsed query parameters as url.Values.
// The query string is parsed on the first call and cached; subsequent calls
// (and c.Query) return the same map.
func (c *Context) QueryValues() url.Values {
	c.queryOnce.Do(func() { c.query = c.Request.URL.Query() })
	return c.query
}

// Cookies returns the request's parsed cookies. Parsed on first call and
// cached; subsequent calls return the same slice. Returns nil if the request
// has no Cookie header.
func (c *Context) Cookies() []*http.Cookie {
	c.cookieOnce.Do(func() { c.cookies = c.Request.Cookies() })
	return c.cookies
}

// Cookie returns the named cookie's value, or "" if no such cookie is
// present. To distinguish a missing cookie from one with an empty value,
// iterate Cookies() directly.
func (c *Context) Cookie(name string) string {
	for _, ck := range c.Cookies() {
		if ck.Name == name {
			return ck.Value
		}
	}
	return ""
}

// Header returns a request header value.
func (c *Context) Header(key string) string {
	return c.Request.Header.Get(key)
}

// SetHeader sets a response header. Call it before writing the body.
func (c *Context) SetHeader(key, value string) {
	c.resp.Header().Set(key, value)
}

// Status returns the response status code (200 until WriteHeader is called).
func (c *Context) Status() int { return c.resp.Status() }

// Bind decodes the JSON request body into v. See Bind for details.
func (c *Context) Bind(v any) error { return Bind(c.Request, v) }

// BindAndValidate binds the JSON body and runs Validator if v implements it.
func (c *Context) BindAndValidate(v any) error { return BindAndValidate(c.Request, v) }

// JSON writes v as a JSON response with the given status code.
func (c *Context) JSON(status int, v any) error { return JSON(&c.resp, status, v) }

// JSONData writes v wrapped in a {"data": ...} envelope.
func (c *Context) JSONData(v any) error { return JSONData(&c.resp, v) }

// JSONError writes a {"error": ..., "status": ...} envelope.
func (c *Context) JSONError(status int, message string) error {
	return JSONError(&c.resp, status, message)
}

// String writes a plain-text response.
func (c *Context) String(status int, s string) error {
	c.resp.Header().Set("Content-Type", "text/plain; charset=utf-8")
	c.resp.WriteHeader(status)
	_, err := c.resp.WriteString(s)
	return err
}

// NoContent writes a response with only a status code and no body.
func (c *Context) NoContent(status int) error {
	c.resp.WriteHeader(status)
	return nil
}

// CtxService resolves a service registered with Provide[T] for use inside a
// fast-path handler. It is the *Context counterpart to Service[T].
func CtxService[T any](c *Context) (T, bool) {
	var zero T
	if c == nil || c.app == nil {
		return zero, false
	}
	v := c.app.GetService(typeKey[T]())
	if v == nil {
		return zero, false
	}
	typed, ok := v.(T)
	if !ok {
		return zero, false
	}
	return typed, true
}
