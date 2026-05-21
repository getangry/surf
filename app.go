package surf

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// ServerConfig holds HTTP server configuration
type ServerConfig struct {
	Addr           string
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	IdleTimeout    time.Duration
	MaxHeaderBytes int
}

// DefaultServerConfig returns sensible defaults for production use
func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		Addr:           ":8080",
		ReadTimeout:    15 * time.Second,
		WriteTimeout:   15 * time.Second,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 1 << 20, // 1MB
	}
}

// App represents the Surf application with context, logger, and shutdown handling.
type App struct {
	ctx              context.Context
	logger           *slog.Logger
	cancel           context.CancelFunc
	shutdown         chan os.Signal
	services         map[any]any
	servicesMu       sync.RWMutex
	router           *Router
	before           []HandlerFunc
	after            []HandlerFunc
	middlewares      []Middleware
	serverConfig     ServerConfig
	notFoundHandler  http.HandlerFunc
	methodNotAllowed http.HandlerFunc
	errorHandler     ErrorRenderer
	chain            http.Handler
	chainOnce        sync.Once
}

// NewApp initializes a new Surf application instance with context and signal handling.
// It sets up a context that can be cancelled and listens for OS signals to gracefully shut down the application.
// The application can be customized with options like a custom logger.
func NewApp(options ...Option) *App {
	app := &App{
		logger:       slog.Default().WithGroup("surf"),
		router:       NewRouter(),
		services:     make(map[any]any),
		serverConfig: DefaultServerConfig(),
	}

	for _, opt := range options {
		opt(app)
	}

	if app.ctx == nil {
		app.ctx, app.cancel = context.WithCancel(context.Background())
	}

	if app.shutdown == nil {
		// Initializes shutdown channel for OS signals
		shutdown := make(chan os.Signal, 1)

		// Registers the channel to receive specific OS signals
		signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

		app.shutdown = shutdown
	}

	return app
}

// Serve starts the HTTP server and handles graceful shutdown.
func (app *App) Serve() error {
	// Validates that context is still active
	select {
	case <-app.ctx.Done():
		app.logger.Error("context already cancelled before server start")
		return app.ctx.Err()
	default:
		app.logger.Debug("context is active, proceeding with server setup")
	}

	// Creates HTTP server with timeouts to prevent slowloris attacks
	server := &http.Server{
		Addr:           app.serverConfig.Addr,
		Handler:        app,
		ReadTimeout:    app.serverConfig.ReadTimeout,
		WriteTimeout:   app.serverConfig.WriteTimeout,
		IdleTimeout:    app.serverConfig.IdleTimeout,
		MaxHeaderBytes: app.serverConfig.MaxHeaderBytes,
	}

	// Channel captures server startup/runtime errors
	serverErr := make(chan error, 1)

	// Starts HTTP server in goroutine for concurrent signal listening
	go func() {
		app.logger.Info("starting HTTP server", "addr", app.serverConfig.Addr)
		err := server.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			app.logger.Error("server error occurred", "error", err)
			serverErr <- err
		} else {
			app.logger.Debug("server stopped cleanly")
			serverErr <- nil
		}
	}()

	app.logger.Debug("server started, waiting for shutdown signal or error")

	// Block until shutdown signal, context cancellation, or server error
	select {
	case <-app.ctx.Done():
		app.logger.Info("context cancelled, initiating shutdown")
	case sig := <-app.shutdown:
		app.logger.Info("received OS signal, initiating shutdown", "signal", sig)
		app.cancel() // Cancels context to signal other components
	case err := <-serverErr:
		if err != nil {
			app.logger.Error("server error triggered shutdown", "error", err)
			return err
		}
		app.logger.Debug("server stopped without error")
	}

	// Perform graceful shutdown with timeout
	app.logger.Info("beginning graceful shutdown")
	ctxShutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctxShutdown); err != nil {
		app.logger.Error("graceful shutdown failed", "error", err)
		return err
	}

	app.logger.Info("shutdown completed successfully")
	return nil
}

// Set registers a service in the application's service container (thread-safe)
func (app *App) Set(key any, service any) {
	app.servicesMu.Lock()
	app.services[key] = service
	app.servicesMu.Unlock()
}

// GetService retrieves a service from the application's service container (thread-safe)
func (app *App) GetService(key any) any {
	app.servicesMu.RLock()
	defer app.servicesMu.RUnlock()
	return app.services[key]
}

// Cleanup stops signal notification and cancels context
func (app *App) Cleanup() {
	app.logger.Info("cleaning up application resources")
	signal.Stop(app.shutdown)
	app.cancel()
}

// NotFound sets a custom handler for 404 Not Found responses.
func (app *App) NotFound(handler http.HandlerFunc) {
	app.notFoundHandler = handler
}

// MethodNotAllowed sets a custom handler for 405 Method Not Allowed responses.
func (app *App) MethodNotAllowed(handler http.HandlerFunc) {
	app.methodNotAllowed = handler
}
