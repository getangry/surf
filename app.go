package surf

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// App represents the Surf application with context, logger, and shutdown handling.
type App struct {
	ctx      context.Context
	logger   *slog.Logger
	cancel   context.CancelFunc
	shutdown chan os.Signal
	services map[any]any
}

// NewApp initializes a new Surf application instance with context and signal handling.
// It sets up a context that can be cancelled and listens for OS signals to gracefully shut down the application.
// The application can be customized with options like a custom logger.
func NewApp(options ...Option) *App {

	app := &App{
		logger: slog.Default().WithGroup("surf"),
	}

	// Load options
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
		app.logger.Error("ERROR: Context already cancelled before server start")
		return app.ctx.Err()
	default:
		log.Println("Context is active, proceeding with server setup")
	}

	// Creates HTTP server
	server := &http.Server{
		Addr:    ":8080",
		Handler: http.DefaultServeMux,
	}

	// Channel captures server startup/runtime errors
	serverErr := make(chan error, 1)

	// Starts HTTP server in goroutine for concurrent signal listening
	go func() {
		log.Println("Starting HTTP server on :8080")
		err := server.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			log.Printf("Server error occurred: %v", err)
			serverErr <- err
		} else {
			log.Println("Server stopped cleanly")
			serverErr <- nil
		}
	}()

	log.Println("Server started, waiting for shutdown signal or error")

	// Block until shutdown signal, context cancellation, or server error
	select {
	case <-app.ctx.Done():
		log.Println("Context cancelled, initiating shutdown")
	case sig := <-app.shutdown:
		print("\b\b")
		slog.With("_c", "white").Info("Received OS signal, initiating shutdown", "signal", sig)
		app.cancel() // Cancels context to signal other components
	case err := <-serverErr:
		if err != nil {
			log.Printf("Server error triggered shutdown: %v", err)
			return err
		}
		log.Println("Server stopped without error")
	}

	// Perform graceful shutdown with timeout
	log.Println("Beginning graceful shutdown")
	ctxShutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctxShutdown); err != nil {
		log.Printf("Graceful shutdown failed: %v", err)
		return err
	}

	log.Println("Shutdown completed successfully")
	return nil
}

// Cleanup stops signal notification and cancels context
func (app *App) Cleanup() {
	log.Println("Cleaning up application resources")
	signal.Stop(app.shutdown)
	app.cancel()
}
