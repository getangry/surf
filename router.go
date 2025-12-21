package surf

import (
	"context"
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
	app    *App
	prefix string
	before []HandlerFunc
	after  []HandlerFunc
}

// route represents a single route with its handler and path pattern
type route struct {
	pattern string
	handler HandlerFunc
	params  []string
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
func (r *Router) addRoute(method, pattern string, handler HandlerFunc) {
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
		pattern: pattern,
		handler: handler,
		params:  params,
	}
	r.routes[method][pattern] = rt
	r.trees[method].insert(pattern, rt)
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

// addRoute adds a route to the group
func (g *Group) addRoute(method, pattern string, handler HandlerFunc) {
	fullPattern := g.prefix + pattern
	if g.app.router.routes[method] == nil {
		g.app.router.routes[method] = make(map[string]*route)
	}
	if g.app.router.trees[method] == nil {
		g.app.router.trees[method] = newRadixTree()
	}

	params := extractParams(fullPattern)
	rt := &route{
		pattern: fullPattern,
		handler: handler,
		params:  params,
		before:  append([]HandlerFunc{}, g.before...),
		after:   append([]HandlerFunc{}, g.after...),
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

// handleError logs the error internally and returns a generic error to the client
func handleError(rw *ResponseWriter, r *http.Request, err error, context string) {
	// Log the actual error internally for debugging
	slog.Error("request handler error",
		"error", err,
		"context", context,
		"method", r.Method,
		"path", r.URL.Path,
		"remote_addr", r.RemoteAddr,
	)
	// Return generic error to client to avoid leaking internal details
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