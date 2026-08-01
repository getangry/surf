package surf

import (
	"context"
	"log/slog"
	"os"
	"time"
)

// Option defines a function type for configuring the App.
type Option func(*App)

// WithLogger sets a custom logger for the Surf application.
func WithLogger(logger *slog.Logger) Option {
	return func(app *App) {
		app.logger = logger
	}
}

// WithContext allows setting a custom context for the Surf application.
func WithContext(ctx context.Context) Option {
	return func(app *App) {
		app.ctx, app.cancel = context.WithCancel(ctx)
	}
}

// WithShutdownChannel allows setting a custom shutdown channel for the Surf application.
func WithShutdownChannel(shutdown chan os.Signal) Option {
	return func(app *App) {
		app.shutdown = shutdown
	}
}

// WithCancelFunc allows setting a custom cancel function for the Surf application.
func WithCancelFunc(cancel context.CancelFunc) Option {
	return func(app *App) {
		app.cancel = cancel
	}
}

// WithAddr sets the server listen address (e.g., ":8080", "0.0.0.0:3000").
func WithAddr(addr string) Option {
	return func(app *App) {
		app.serverConfig.Addr = addr
	}
}

// WithServerConfig sets the complete server configuration.
func WithServerConfig(config ServerConfig) Option {
	return func(app *App) {
		app.serverConfig = config
	}
}

// WithReadTimeout sets the maximum duration for reading the entire request.
func WithReadTimeout(d time.Duration) Option {
	return func(app *App) {
		app.serverConfig.ReadTimeout = d
	}
}

// WithWriteTimeout sets the maximum duration before timing out writes of the response.
func WithWriteTimeout(d time.Duration) Option {
	return func(app *App) {
		app.serverConfig.WriteTimeout = d
	}
}

// WithIdleTimeout sets the maximum amount of time to wait for the next request.
func WithIdleTimeout(d time.Duration) Option {
	return func(app *App) {
		app.serverConfig.IdleTimeout = d
	}
}

// WithMaxHeaderBytes sets the maximum size of request headers.
func WithMaxHeaderBytes(n int) Option {
	return func(app *App) {
		app.serverConfig.MaxHeaderBytes = n
	}
}

// WithErrorHandler sets a custom renderer for errors returned by handlers and
// before/after handlers. When unset, DefaultErrorRenderer writes a JSON error
// envelope. The renderer is only invoked when the response has not already
// been written.
func WithErrorHandler(renderer ErrorRenderer) Option {
	return func(app *App) {
		app.errorHandler = renderer
	}
}

// WithoutAutomaticOptions disables surf's built-in OPTIONS handler. By default,
// an OPTIONS request to a path that has routes but no explicit OPTIONS handler
// receives a 204 response carrying an Allow header (and, when the path exposes a
// QUERY route, an Accept-Query header for RFC 10008 discovery). Use this option
// to restore the older behavior where such requests fall through to the 405
// path. An explicitly registered OPTIONS route always takes precedence either
// way.
func WithoutAutomaticOptions() Option {
	return func(app *App) {
		app.disableAutomaticOptions = true
	}
}

// WithRedirectTrailingSlash enables automatic redirects between /foo and
// /foo/ when only one variant of a path is registered. The redirect uses
// HTTP 308 (Permanent Redirect), which preserves the request method and
// body — a POST stays a POST, unlike the historic 301 behavior in some
// clients.
//
// Only applies within the same method tree: a request that fails to match
// is not silently routed to a different HTTP method's handler.
func WithRedirectTrailingSlash() Option {
	return func(app *App) {
		app.redirectTrailingSlash = true
	}
}
