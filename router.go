package surf

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
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

// Router handles HTTP routing with path parameters
type Router struct {
	routes map[string]map[string]*route // Legacy map storage
	trees  map[string]*radixTree        // Radix trees per method for fast lookup
}

// Group represents a route group with a common prefix and middleware
type Group struct {
	app         *App
	prefix      string
	before      []HandlerFunc
	after       []HandlerFunc
	middlewares []Middleware
	skip        []string
}

// route represents a single route with its handler and path pattern
type route struct {
	pattern     string
	handler     HandlerFunc
	params      []string
	before      []HandlerFunc
	after       []HandlerFunc
	middlewares []Middleware
}

// NewRouter creates a new router instance
func NewRouter() *Router {
	return &Router{
		routes: make(map[string]map[string]*route),
		trees:  make(map[string]*radixTree),
	}
}

// getAllowedMethods returns a list of HTTP methods that have routes for the given path
func (r *Router) getAllowedMethods(path string) []string {
	var methods []string
	for method, tree := range r.trees {
		if route, _ := tree.search(path); route != nil {
			methods = append(methods, method)
		}
	}
	return methods
}

// addRoute adds a route to the router
// Logs a warning if the route pattern already exists (will be overwritten)
func (r *Router) addRoute(method, pattern string, handler HandlerFunc, middleware []Middleware) {
	if r.routes[method] == nil {
		r.routes[method] = make(map[string]*route)
	}
	if r.trees[method] == nil {
		r.trees[method] = newRadixTree()
	}

	// Warn on route conflict (pattern already registered)
	if _, exists := r.routes[method][pattern]; exists {
		slog.Warn("route conflict: overwriting existing route",
			"method", method,
			"pattern", pattern,
		)
	}

	params := extractParams(pattern)
	rt := &route{
		pattern:     pattern,
		handler:     handler,
		params:      params,
		middlewares: middleware,
	}
	r.routes[method][pattern] = rt
	r.trees[method].insert(pattern, rt)
}

// Get registers a GET route. Any middleware is applied to this route only,
// wrapping the handler in declaration order (outermost first).
func (app *App) Get(pattern string, handler HandlerFunc, middleware ...Middleware) {
	app.router.addRoute("GET", pattern, handler, middleware)
}

// Post registers a POST route with optional per-route middleware.
func (app *App) Post(pattern string, handler HandlerFunc, middleware ...Middleware) {
	app.router.addRoute("POST", pattern, handler, middleware)
}

// Put registers a PUT route with optional per-route middleware.
func (app *App) Put(pattern string, handler HandlerFunc, middleware ...Middleware) {
	app.router.addRoute("PUT", pattern, handler, middleware)
}

// Delete registers a DELETE route with optional per-route middleware.
func (app *App) Delete(pattern string, handler HandlerFunc, middleware ...Middleware) {
	app.router.addRoute("DELETE", pattern, handler, middleware)
}

// Patch registers a PATCH route with optional per-route middleware.
func (app *App) Patch(pattern string, handler HandlerFunc, middleware ...Middleware) {
	app.router.addRoute("PATCH", pattern, handler, middleware)
}

// Head registers a HEAD route with optional per-route middleware.
func (app *App) Head(pattern string, handler HandlerFunc, middleware ...Middleware) {
	app.router.addRoute("HEAD", pattern, handler, middleware)
}

// Options registers an OPTIONS route with optional per-route middleware.
func (app *App) Options(pattern string, handler HandlerFunc, middleware ...Middleware) {
	app.router.addRoute("OPTIONS", pattern, handler, middleware)
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
		app:         app,
		prefix:      prefix,
		before:      []HandlerFunc{},
		after:       []HandlerFunc{},
		middlewares: []Middleware{},
		skip:        []string{},
	}
}

// Group creates a nested route group. It inherits the parent group's before
// and after handlers, middleware, and skip patterns.
func (g *Group) Group(prefix string) *Group {
	return &Group{
		app:         g.app,
		prefix:      g.prefix + prefix,
		before:      append([]HandlerFunc{}, g.before...),
		after:       append([]HandlerFunc{}, g.after...),
		middlewares: append([]Middleware{}, g.middlewares...),
		skip:        append([]string{}, g.skip...),
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

// Use adds standard middleware applied to every route in the group. Unlike
// Before, middleware can short-circuit by not calling next and propagates
// context changes via r.WithContext.
func (g *Group) Use(middleware ...Middleware) *Group {
	g.middlewares = append(g.middlewares, middleware...)
	return g
}

// Skip excludes routes whose full pattern matches any of the given patterns
// from this group's Before, After, and Use middleware. A pattern ending in "*"
// matches by prefix; otherwise it must match the full route pattern exactly.
// Call Skip before registering the affected routes.
//
//	api := app.Group("/api").Before(requireAuth)
//	api.Skip("/api/health")
//	api.Get("/health", healthz)   // no auth
//	api.Get("/users", listUsers)  // auth applied
func (g *Group) Skip(patterns ...string) *Group {
	g.skip = append(g.skip, patterns...)
	return g
}

// isSkipped reports whether fullPattern is excluded from group middleware.
func (g *Group) isSkipped(fullPattern string) bool {
	return matchAnyGlob(fullPattern, g.skip)
}

// Get registers a GET route in the group with optional per-route middleware.
func (g *Group) Get(pattern string, handler HandlerFunc, middleware ...Middleware) {
	g.addRoute("GET", pattern, handler, middleware)
}

// Post registers a POST route in the group with optional per-route middleware.
func (g *Group) Post(pattern string, handler HandlerFunc, middleware ...Middleware) {
	g.addRoute("POST", pattern, handler, middleware)
}

// Put registers a PUT route in the group with optional per-route middleware.
func (g *Group) Put(pattern string, handler HandlerFunc, middleware ...Middleware) {
	g.addRoute("PUT", pattern, handler, middleware)
}

// Delete registers a DELETE route in the group with optional per-route middleware.
func (g *Group) Delete(pattern string, handler HandlerFunc, middleware ...Middleware) {
	g.addRoute("DELETE", pattern, handler, middleware)
}

// Patch registers a PATCH route in the group with optional per-route middleware.
func (g *Group) Patch(pattern string, handler HandlerFunc, middleware ...Middleware) {
	g.addRoute("PATCH", pattern, handler, middleware)
}

// Head registers a HEAD route in the group with optional per-route middleware.
func (g *Group) Head(pattern string, handler HandlerFunc, middleware ...Middleware) {
	g.addRoute("HEAD", pattern, handler, middleware)
}

// Options registers an OPTIONS route in the group with optional per-route middleware.
func (g *Group) Options(pattern string, handler HandlerFunc, middleware ...Middleware) {
	g.addRoute("OPTIONS", pattern, handler, middleware)
}

// addRoute adds a route to the group, attaching group and per-route middleware
// unless the route's full pattern has been excluded with Skip.
func (g *Group) addRoute(method, pattern string, handler HandlerFunc, routeMiddleware []Middleware) {
	fullPattern := g.prefix + pattern
	if g.app.router.routes[method] == nil {
		g.app.router.routes[method] = make(map[string]*route)
	}
	if g.app.router.trees[method] == nil {
		g.app.router.trees[method] = newRadixTree()
	}

	if _, exists := g.app.router.routes[method][fullPattern]; exists {
		slog.Warn("route conflict: overwriting existing route",
			"method", method,
			"pattern", fullPattern,
		)
	}

	rt := &route{
		pattern: fullPattern,
		handler: handler,
		params:  extractParams(fullPattern),
	}

	if g.isSkipped(fullPattern) {
		// Excluded from group middleware; per-route middleware still applies.
		rt.middlewares = append([]Middleware{}, routeMiddleware...)
	} else {
		rt.before = append([]HandlerFunc{}, g.before...)
		rt.after = append([]HandlerFunc{}, g.after...)
		rt.middlewares = append(append([]Middleware{}, g.middlewares...), routeMiddleware...)
	}

	g.app.router.routes[method][fullPattern] = rt
	g.app.router.trees[method].insert(fullPattern, rt)
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

// renderError turns an error returned by a handler (or before/after handler)
// into a response. The Abort sentinel and http.ErrAbortHandler are treated as
// successful, silent completion. Other errors are logged; if the response has
// not been written yet, the configured ErrorRenderer (or DefaultErrorRenderer)
// produces the body.
func (app *App) renderError(rw *ResponseWriter, r *http.Request, err error, context string) {
	if err == nil || errors.Is(err, Abort) || errors.Is(err, http.ErrAbortHandler) {
		return
	}

	logger := app.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Error("request handler error",
		"error", err,
		"context", context,
		"method", r.Method,
		"path", r.URL.Path,
		"remote_addr", r.RemoteAddr,
	)

	// The handler already produced a response; writing again would corrupt it.
	if rw.wroteHeader || rw.written {
		return
	}

	renderer := app.errorHandler
	if renderer == nil {
		renderer = DefaultErrorRenderer
	}
	renderer(rw, r, err)
}

// runRoute executes a route's handler wrapped in its per-route middleware and
// returns the handler's error (nil if a middleware short-circuited the chain).
func (app *App) runRoute(rw *ResponseWriter, r *http.Request, rt *route) error {
	if len(rt.middlewares) == 0 {
		return rt.handler(rw, r)
	}

	var handlerErr error
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerErr = rt.handler(w, r)
	})

	var h http.Handler = final
	for i := len(rt.middlewares) - 1; i >= 0; i-- {
		h = rt.middlewares[i](h)
	}
	h.ServeHTTP(rw, r)
	return handlerErr
}

// serveLegacy handles the original Before/After middleware pattern
func (app *App) serveLegacy(w http.ResponseWriter, r *http.Request) {
	rw := NewResponseWriter(w)

	ctx := context.WithValue(r.Context(), responseKey{}, rw)
	r = r.WithContext(ctx)

	for _, handler := range app.before {
		if err := handler(rw, r); err != nil {
			app.renderError(rw, r, err, "app.before")
			return
		}
	}

	// Use radix tree for O(log n) route matching
	if tree, ok := app.router.trees[r.Method]; ok {
		if route, params := tree.search(r.URL.Path); route != nil {
			ctx := r.Context()
			for key, value := range params {
				ctx = context.WithValue(ctx, contextKey(key), value)
			}
			r = r.WithContext(ctx)

			for _, handler := range route.before {
				if err := handler(rw, r); err != nil {
					app.renderError(rw, r, err, "route.before")
					return
				}
			}

			if err := app.runRoute(rw, r, route); err != nil {
				app.renderError(rw, r, err, "route.handler")
				return
			}

			for _, handler := range route.after {
				if err := handler(rw, r); err != nil {
					app.renderError(rw, r, err, "route.after")
					return
				}
			}

			for _, handler := range app.after {
				if err := handler(rw, r); err != nil {
					app.renderError(rw, r, err, "app.after")
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

// extractParams extracts parameter names from a route pattern
func extractParams(pattern string) []string {
	var params []string
	parts := strings.Split(pattern, "/")
	for _, part := range parts {
		if strings.HasPrefix(part, ":") {
			params = append(params, strings.TrimPrefix(part, ":"))
		}
	}
	return params
}

// matchPath checks if a path matches a pattern and extracts parameters
func matchPath(pattern, path string) (map[string]string, bool) {
	patternParts := strings.Split(pattern, "/")
	pathParts := strings.Split(path, "/")

	if strings.Contains(pattern, "*") {
		wildcardIndex := -1
		for i, part := range patternParts {
			if part == "*" {
				wildcardIndex = i
				break
			}
		}

		if wildcardIndex != -1 {
			if wildcardIndex > len(pathParts) {
				return nil, false
			}
			for i := 0; i < wildcardIndex; i++ {
				if i >= len(pathParts) {
					return nil, false
				}
				if !strings.HasPrefix(patternParts[i], ":") && patternParts[i] != pathParts[i] {
					return nil, false
				}
			}

			// Extract parameters before wildcard
			params := make(map[string]string)
			for i := 0; i < wildcardIndex && i < len(pathParts); i++ {
				if strings.HasPrefix(patternParts[i], ":") {
					paramName := strings.TrimPrefix(patternParts[i], ":")
					params[paramName] = pathParts[i]
				}
			}

			// Wildcard matches the rest
			if wildcardIndex < len(pathParts) {
				params["*"] = strings.Join(pathParts[wildcardIndex:], "/")
			}
			return params, true
		}
	}

	// Regular pattern matching
	if len(patternParts) != len(pathParts) {
		return nil, false
	}

	params := make(map[string]string)
	for i, patternPart := range patternParts {
		if strings.HasPrefix(patternPart, ":") {
			// This is a parameter
			paramName := strings.TrimPrefix(patternPart, ":")
			params[paramName] = pathParts[i]
		} else if patternPart != pathParts[i] {
			// Static part doesn't match
			return nil, false
		}
	}

	return params, true
}
