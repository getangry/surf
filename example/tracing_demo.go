//go:build ignore
// +build ignore

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/getangry/surf"
	"github.com/getangry/surf/pkg/logger/reef"
)

// BusinessService simulates application logic that needs logging
type BusinessService struct{}

func (s *BusinessService) ProcessOrder(r *http.Request, orderID string) error {
	// Get the logger from request context - it will include the request_id!
	logger := surf.GetRequestLogger(r)

	logger.Info("Starting order processing", "order_id", orderID)

	// Simulate some work
	time.Sleep(10 * time.Millisecond)
	logger.Debug("Validated order details", "order_id", orderID, "status", "valid")

	// Simulate calling another service
	if err := s.callPaymentService(r, orderID); err != nil {
		logger.Error("Payment processing failed", "order_id", orderID, "error", err)
		return err
	}

	logger.Info("Order processed successfully", "order_id", orderID, "processing_time_ms", 10)
	return nil
}

func (s *BusinessService) callPaymentService(r *http.Request, orderID string) error {
	// Get logger with request context
	logger := surf.GetRequestLogger(r)

	logger.Info("Calling payment service", "order_id", orderID, "service", "payment-api")

	// Simulate payment processing
	time.Sleep(5 * time.Millisecond)

	logger.Info("Payment completed", "order_id", orderID, "amount", 99.99, "currency", "USD")
	return nil
}

func main() {
	app := surf.NewApp()

	// Initialize slog with reef handler for nice formatting
	slogger := slog.New(reef.NewHandler())

	// Create business service
	service := &BusinessService{}

	// Apply middlewares in order:
	// 1. RequestIDMiddleware - generates and adds request_id to context
	app.Use(surf.RequestIDMiddleware("trace"))

	// 2. RequestLoggerInjector - creates a logger with request_id and injects it into context
	app.Use(surf.RequestLoggerInjector(slogger))

	// 3. RequestLogger - logs HTTP requests/responses
	app.Use(surf.RequestLogger(slogger))

	// API endpoint that uses application logging
	app.Post("/api/orders", func(w http.ResponseWriter, r *http.Request) error {
		// Parse request
		var order struct {
			ID    string `json:"id"`
			Items []struct {
				Name     string  `json:"name"`
				Quantity int     `json:"quantity"`
				Price    float64 `json:"price"`
			} `json:"items"`
		}

		if err := json.NewDecoder(r.Body).Decode(&order); err != nil {
			// Get logger from context for application logging
			logger := surf.GetRequestLogger(r)
			logger.Error("Failed to parse order", "error", err)

			w.WriteHeader(http.StatusBadRequest)
			return json.NewEncoder(w).Encode(map[string]string{
				"error": "Invalid order format",
			})
		}

		// Log with request context
		logger := surf.GetRequestLogger(r)
		logger.Info("Received order",
			"order_id", order.ID,
			"item_count", len(order.Items),
		)

		// Process order using business service
		if err := service.ProcessOrder(r, order.ID); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return json.NewEncoder(w).Encode(map[string]string{
				"error": "Order processing failed",
			})
		}

		// Success response
		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(map[string]any{
			"order_id": order.ID,
			"status":   "processed",
			"message":  "Order processed successfully",
		})
	})

	// Health check endpoint with minimal logging
	app.Get("/health", func(w http.ResponseWriter, r *http.Request) error {
		// You can still get the logger if needed
		logger := surf.GetRequestLogger(r)
		logger.Debug("Health check requested")

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(map[string]string{
			"status": "healthy",
		})
	})

	// Endpoint demonstrating error logging
	app.Get("/api/simulate-error", func(w http.ResponseWriter, r *http.Request) error {
		logger := surf.GetRequestLogger(r)

		logger.Warn("Simulating error condition", "test", true)

		// Simulate some processing
		time.Sleep(20 * time.Millisecond)

		logger.Error("Simulated error occurred",
			"error_code", "TEST_ERROR",
			"severity", "high",
		)

		w.WriteHeader(http.StatusInternalServerError)
		return fmt.Errorf("simulated error for testing")
	})

	slogger.Info("Server starting with request tracing", "port", 8083)

	server := &http.Server{
		Addr:    ":8083",
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

	slogger.Info("Server started with request tracing enabled")
	slogger.Info("Try these commands:")
	slogger.Info("  curl -X POST http://localhost:8083/api/orders -H 'Content-Type: application/json' -d '{\"id\":\"ORDER-123\",\"items\":[{\"name\":\"Widget\",\"quantity\":2,\"price\":19.99}]}'")
	slogger.Info("  curl http://localhost:8083/health")
	slogger.Info("  curl http://localhost:8083/api/simulate-error")

	<-quit
	slogger.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		slogger.Error("Server forced to shutdown", "error", err)
	} else {
		slogger.Info("Server shutdown complete")
	}
}