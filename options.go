package surf

import (
	"context"
	"log/slog"
	"os"
)

// Option defines a function type for configuring the App.
type Option func(*App)

// WithLogger sets a custom logger for the Surf application.
func WithLogger(logger *slog.Logger) Option {
	return func(app *App) {
		// Set default logger with white color for context
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
