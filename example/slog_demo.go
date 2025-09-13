//go:build ignore
// +build ignore

package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/getangry/surf"
	"github.com/getangry/surf/pkg/logger/reef"
)

func main() {
	// Create a new Surf app
	app := surf.NewApp()

	// Create a structured slog logger (JSON format for reef compatibility)
	slogger := slog.New(reef.NewHandler())

	// Option 1: Use only slog middleware
	app.Use(surf.RequestIDMiddleware("demo"))
	app.Use(surf.SlogMiddleware(slogger))

	// Option 2: Use reef-compatible middleware (commented out)
	// app.Use(surf.ReefCompatibleMiddleware(slogger))

	// Option 3: Use both traditional log and slog (commented out)
	// app.Use(surf.CombinedMiddleware("{method} {path} {status} {latency_ms}ms", slogger))

	// Example route
	app.Get("/", func(w http.ResponseWriter, r *http.Request) error {
		// Add custom data to ResponseWriter for structured logging
		if rw, ok := w.(*surf.ResponseWriter); ok {
			rw.Set("operation", "home")
			rw.Set("user_id", "user-123")
		}

		response := map[string]any{
			"message": "Hello, World!",
			"time":    time.Now().Format(time.RFC3339),
		}

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(response)
	})

	app.Get("/api/test/:id", func(w http.ResponseWriter, r *http.Request) error {
		id := surf.Param(r, "id")

		// Add custom data to ResponseWriter for structured logging
		if rw, ok := w.(*surf.ResponseWriter); ok {
			rw.Set("operation", "get_test")
			rw.Set("test_id", id)
			rw.Set("user_id", "user-456")
		}

		response := map[string]any{
			"id":      id,
			"message": "Test successful",
			"data":    []string{"item1", "item2"},
		}

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(response)
	})

	slogger.Info("Server starting", "port", 8081)

	// Create server with custom port
	server := &http.Server{
		Addr:    ":8081",
		Handler: app,
	}

	// Channel to listen for interrupt signals
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Start server in a goroutine
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slogger.Error("Server failed", "error", err)
			os.Exit(1)
		}
	}()

	slogger.Info("Server started, press Ctrl+C to exit")

	// Wait for interrupt signal
	<-quit
	slogger.Info("Shutting down server...")

	// Create context with timeout for graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Gracefully shutdown the server
	if err := server.Shutdown(ctx); err != nil {
		slogger.Error("Server forced to shutdown", "error", err)
	} else {
		slogger.Info("Server shutdown complete")
	}
}
