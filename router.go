package surf

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

// ErrRouteConflict is returned when a route pattern is already registered
type ErrRouteConflict struct {
	Method  string
	Pattern string
}

func (e ErrRouteConflict) Error() string {
	return fmt.Sprintf("route conflict: %s %s is already registered", e.Method, e.Pattern)
}

// HandlerFunc is the standard HTTP handler function signature using context.Context
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// Middleware represents a standard middleware function
type Middleware func(next http.Handler) http.Handler

// MiddlewareFunc is a function that can be converted to Middleware
type MiddlewareFunc func(w http.ResponseWriter, r *http.Request, next http.HandlerFunc)

// Router handles HTTP routing with path parameters.
//
// Lifecycle: routes are registered before the first request. The first
// request atomically freezes the routing tables into an immutable snapshot
// served lock-free from then on. Registration after the freeze panics —
// register all routes during setup, not from inside handlers.
type Router struct {
	mu     sync.Mutex                              // guards routes/trees during registration
	routes map[string]map[string]*route            // pattern conflict detection
	trees  map[string]*radixTree                   // build-time tree per method
	frozen atomic.Pointer[map[string]*radixTree]   // read-only snapshot for ServeHTTP
}

// Group represents a route group with a common prefix and middleware
type Group struct {
	app    *App
	prefix string
	before []HandlerFunc
	after  []HandlerFunc
}

// route represents a single route with its handler and path pattern
type route struct {
	pattern string
	handler HandlerFunc
	before  []HandlerFunc
	after   []HandlerFunc
}

// NewRouter creates a new router instance
func NewRouter() *Router {
	return &Router{
		routes: make(map[string]map[string]*route),
		trees:  make(map[string]*radixTree),
	}
}

// canonicalMethod returns method in canonical (uppercase) form. It avoids
// allocation for already-uppercase strings, which is the common case — the
// fast path is a byte loop with no heap traffic. Hot path on every request.
func canonicalMethod(m string) string {
	for i := 0; i < len(m); i++ {
		if m[i] >= 'a' && m[i] <= 'z' {
			return strings.ToUpper(m)
		}
	}
	return m
}

// view returns the routing trees, freezing them on first call. The returned
// map must be treated as read-only — addRoute will panic before mutating it.
func (r *Router) view() map[string]*radixTree {
	if t := r.frozen.Load(); t != nil {
		return *t
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if t := r.frozen.Load(); t != nil {
		return *t
	}
	snap := r.trees
	r.frozen.Store(&snap)
	return snap
}

// getAllowedMethods returns a list of HTTP methods that have routes for the given path
func (r *Router) getAllowedMethods(path string) []string {
	var methods []string
	for method, tree := range r.view() {
		if route, _ := tree.search(path); route != nil {
			methods = append(methods, method)
		}
	}
	return methods
}

// insertRoute is the single mutation point for the routing tables. It takes
// the lock, checks the freeze flag, and panics if registration is happening
// after the first request.
func (r *Router) insertRoute(method, pattern string, rt *route) {
	method = canonicalMethod(method)
	if r.frozen.Load() != nil {
		panic(fmt.Sprintf("surf: cannot register route after first request (%s %s)", method, pattern))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen.Load() != nil {
		panic(fmt.Sprintf("surf: cannot register route after first request (%s %s)", method, pattern))
	}
	if r.routes[method] == nil {
		r.routes[method] = make(map[string]*route)
	}
	if r.trees[method] == nil {
		r.trees[method] = newRadixTree()
	}
	if _, exists := r.routes[method][pattern]; exists {
		slog.Warn("route conflict: overwriting existing route",
			"method", method,
			"pattern", pattern,
		)
	}
	r.routes[method][pattern] = rt
	r.trees[method].insert(pattern, rt)
}

// addRoute adds a route to the router.
// Logs a warning if the route pattern already exists (will be overwritten).
func (r *Router) addRoute(method, pattern string, handler HandlerFunc) {
	r.insertRoute(method, pattern, &route{
		pattern: pattern,
		handler: handler,
	})
}

// Get registers a GET route
func (app *App) Get(pattern string, handler HandlerFunc) {
	app.router.addRoute("GET", pattern, handler)
}

// Post registers a POST route
func (app *App) Post(pattern string, handler HandlerFunc) {
	app.router.addRoute("POST", pattern, handler)
}

// Put registers a PUT route
func (app *App) Put(pattern string, handler HandlerFunc) {
	app.router.addRoute("PUT", pattern, handler)
}

// Delete registers a DELETE route
func (app *App) Delete(pattern string, handler HandlerFunc) {
	app.router.addRoute("DELETE", pattern, handler)
}

// Patch registers a PATCH route
func (app *App) Patch(pattern string, handler HandlerFunc) {
	app.router.addRoute("PATCH", pattern, handler)
}

// Head registers a HEAD route
func (app *App) Head(pattern string, handler HandlerFunc) {
	app.router.addRoute("HEAD", pattern, handler)
}

// Options registers an OPTIONS route
func (app *App) Options(pattern string, handler HandlerFunc) {
	app.router.addRoute("OPTIONS", pattern, handler)
}

// Before adds a middleware handler that runs before the main handler
func (app *App) Before(handler HandlerFunc) {
	app.before = append(app.before, handler)
}

// After adds a middleware handler that runs after the main handler
func (app *App) After(handler HandlerFunc) {
	app.after = append(app.after, handler)
}

// Use adds a standard middleware to the application
func (app *App) Use(middleware Middleware) {
	app.middlewares = append(app.middlewares, middleware)
}

// UseFunc adds a middleware function to the application
func (app *App) UseFunc(fn MiddlewareFunc) {
	middleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fn(w, r, func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r)
			})
		})
	}
	app.Use(middleware)
}

// Group creates a new route group with a common prefix
func (app *App) Group(prefix string) *Group {
	return &Group{
		app:    app,
		prefix: prefix,
		before: []HandlerFunc{},
		after:  []HandlerFunc{},
	}
}

// Group creates a nested route group
func (g *Group) Group(prefix string) *Group {
	return &Group{
		app:    g.app,
		prefix: g.prefix + prefix,
		before: append([]HandlerFunc{}, g.before...),
		after:  append([]HandlerFunc{}, g.after...),
	}
}

// Before adds a middleware handler that runs before the main handler for this group
func (g *Group) Before(handler HandlerFunc) *Group {
	g.before = append(g.before, handler)
	return g
}

// After adds a middleware handler that runs after the main handler for this group
func (g *Group) After(handler HandlerFunc) *Group {
	g.after = append(g.after, handler)
	return g
}

// Get registers a GET route in the group
func (g *Group) Get(pattern string, handler HandlerFunc) {
	g.addRoute("GET", pattern, handler)
}

// Post registers a POST route in the group
func (g *Group) Post(pattern string, handler HandlerFunc) {
	g.addRoute("POST", pattern, handler)
}

// Put registers a PUT route in the group
func (g *Group) Put(pattern string, handler HandlerFunc) {
	g.addRoute("PUT", pattern, handler)
}

// Delete registers a DELETE route in the group
func (g *Group) Delete(pattern string, handler HandlerFunc) {
	g.addRoute("DELETE", pattern, handler)
}

// Patch registers a PATCH route in the group
func (g *Group) Patch(pattern string, handler HandlerFunc) {
	g.addRoute("PATCH", pattern, handler)
}

// Head registers a HEAD route in the group
func (g *Group) Head(pattern string, handler HandlerFunc) {
	g.addRoute("HEAD", pattern, handler)
}

// Options registers an OPTIONS route in the group
func (g *Group) Options(pattern string, handler HandlerFunc) {
	g.addRoute("OPTIONS", pattern, handler)
}

// addRoute adds a route to the group, attaching the group's before/after handlers.
func (g *Group) addRoute(method, pattern string, handler HandlerFunc) {
	g.app.router.insertRoute(method, g.prefix+pattern, &route{
		pattern: g.prefix + pattern,
		handler: handler,
		before:  append([]HandlerFunc{}, g.before...),
		after:   append([]HandlerFunc{}, g.after...),
	})
}

// ServeHTTP implements the http.Handler interface
func (app *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := context.WithValue(r.Context(), appKey{}, app)
	r = r.WithContext(ctx)

	// If we have standard middlewares, chain them
	if len(app.middlewares) > 0 {
		final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			app.serveLegacy(w, r)
		})

		// Chain middlewares in reverse order
		handler := http.Handler(final)
		for i := len(app.middlewares) - 1; i >= 0; i-- {
			handler = app.middlewares[i](handler)
		}

		handler.ServeHTTP(w, r)
		return
	}

	// Fallback to legacy behavior
	app.serveLegacy(w, r)
}

// handleError logs the error internally and returns a generic 500 to the
// client. If the handler already committed headers (e.g. wrote a 200 then
// errored mid-body), we cannot rewrite the response — emit only the log
// entry so the client receives a truncated body rather than a corrupt one
// with "Internal Server Error" appended after partial output.
func handleError(rw *ResponseWriter, r *http.Request, err error, context string) {
	slog.Error("request handler error",
		"error", err,
		"context", context,
		"method", r.Method,
		"path", r.URL.Path,
		"remote_addr", r.RemoteAddr,
		"committed", rw.Committed(),
	)
	if rw.Committed() {
		return
	}
	http.Error(rw, "Internal Server Error", http.StatusInternalServerError)
}

// serveLegacy handles the original Before/After middleware pattern
func (app *App) serveLegacy(w http.ResponseWriter, r *http.Request) {
	rw := NewResponseWriter(w)

	ctx := context.WithValue(r.Context(), responseKey{}, rw)
	r = r.WithContext(ctx)

	for _, handler := range app.before {
		if err := handler(rw, r); err != nil {
			handleError(rw, r, err, "app.before")
			return
		}
	}

	// Use radix tree for O(log n) route matching. view() freezes the
	// routing tables on first call so further reads are lock-free.
	method := canonicalMethod(r.Method)
	if tree, ok := app.router.view()[method]; ok {
		if route, params := tree.search(r.URL.Path); route != nil {
			ctx := r.Context()
			for key, value := range params {
				ctx = context.WithValue(ctx, contextKey(key), value)
			}
			r = r.WithContext(ctx)

			for _, handler := range route.before {
				if err := handler(rw, r); err != nil {
					handleError(rw, r, err, "route.before")
					return
				}
			}

			if err := route.handler(rw, r); err != nil {
				handleError(rw, r, err, "route.handler")
				return
			}

			for _, handler := range route.after {
				if err := handler(rw, r); err != nil {
					handleError(rw, r, err, "route.after")
					return
				}
			}

			for _, handler := range app.after {
				if err := handler(rw, r); err != nil {
					handleError(rw, r, err, "app.after")
					return
				}
			}
			return
		}
	}

	// Check if route exists for other methods (405 Method Not Allowed)
	allowedMethods := app.router.getAllowedMethods(r.URL.Path)
	if len(allowedMethods) > 0 {
		if app.methodNotAllowed != nil {
			rw.Header().Set("Allow", strings.Join(allowedMethods, ", "))
			app.methodNotAllowed(rw, r)
		} else {
			rw.Header().Set("Allow", strings.Join(allowedMethods, ", "))
			http.Error(rw, "Method Not Allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	// No route found - 404
	if app.notFoundHandler != nil {
		app.notFoundHandler(rw, r)
	} else {
		http.NotFound(rw, r)
	}
}

// Param extracts a path parameter from the request context
func Param(r *http.Request, key string) string {
	value := r.Context().Value(contextKey(key))
	if value == nil {
		return ""
	}
	str, ok := value.(string)
	if !ok {
		return ""
	}
	return str
}

