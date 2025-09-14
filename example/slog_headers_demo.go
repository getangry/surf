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
	"strings"
	"syscall"
	"time"

	"github.com/getangry/surf"
	"github.com/getangry/surf/pkg/logger/reef"
)

func main() {
	app := surf.NewApp()

	slogger := slog.New(reef.NewHandler())

	// Configure RequestLogger with options
	loggerOpts := &surf.RequestLoggerOptions{
		Logger:             slogger,
		Level:              slog.LevelInfo,
		IncludeReqHeaders:  true,  // Include request headers
		IncludeRespHeaders: true,  // Include response headers
		GroupHeaders:       true,  // Group headers under request_headers and response_headers
		HeaderFilter: func(key string) bool {
			// Filter out sensitive headers
			lower := strings.ToLower(key)
			return !strings.Contains(lower, "authorization") &&
				   !strings.Contains(lower, "cookie") &&
				   !strings.Contains(lower, "x-api-key")
		},
	}

	// Apply middlewares
	app.Use(surf.RequestIDMiddleware("demo"))
	app.Use(surf.RequestLoggerWithOptions(loggerOpts))

	// Example routes
	app.Get("/", func(w http.ResponseWriter, r *http.Request) error {
		// Add custom response headers
		w.Header().Set("X-Custom-Header", "custom-value")
		w.Header().Set("X-Service-Version", "1.0.0")

		if rw, ok := w.(*surf.ResponseWriter); ok {
			rw.Set("operation", "home")
			rw.Set("user_id", "user-123")
		}

		response := map[string]any{
			"message": "Hello with headers!",
			"time":    time.Now().Format(time.RFC3339),
		}

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(response)
	})

	app.Get("/api/test/:id", func(w http.ResponseWriter, r *http.Request) error {
		id := surf.Param(r, "id")

		// Add custom headers
		w.Header().Set("X-Resource-ID", id)
		w.Header().Set("Cache-Control", "no-cache")

		if rw, ok := w.(*surf.ResponseWriter); ok {
			rw.Set("operation", "get_test")
			rw.Set("test_id", id)
			rw.Set("user_id", "user-456")
		}

		response := map[string]any{
			"id":      id,
			"message": "Test with headers",
			"data":    []string{"item1", "item2"},
		}

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(response)
	})

	// Example with minimal headers logging
	app.Get("/minimal", func(w http.ResponseWriter, r *http.Request) error {
		response := map[string]any{
			"message": "Minimal response",
		}

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(response)
	})

	slogger.Info("Server starting with header logging", "port", 8082)

	server := &http.Server{
		Addr:    ":8082",
		Handler: app,
	}

	// Channel to listen for interrupt signals
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slogger.Error("Server failed", "error", err)
			os.Exit(1)
		}
	}()

	slogger.Info("Server started with header logging enabled, press Ctrl+C to exit")

	<-quit
	slogger.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Gracefully shutdown the server
	if err := server.Shutdown(ctx); err != nil {
		slogger.Error("Server forced to shutdown", "error", err)
	} else {
		slogger.Info("Server shutdown complete")
	}
}