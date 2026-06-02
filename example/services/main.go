//go:build ignore
// +build ignore

package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/getangry/surf"
)

// Example services
type UserService struct {
	DB *sql.DB
}

func (us *UserService) GetUser(id string) map[string]any {
	// In real code, this would query the database
	return map[string]any{
		"id":    id,
		"name":  "User " + id,
		"email": fmt.Sprintf("user%s@example.com", id),
	}
}

type ConfigService struct {
	APIKey   string
	BaseURL  string
	Features []string
}

func main() {
	app := surf.NewApp()

	// Note: In real code, you'd initialize these properly
	mockDB := &sql.DB{} // This would be sql.Open("postgres", "...")

	userService := &UserService{DB: mockDB}
	configService := &ConfigService{
		APIKey:   "secret-api-key",
		BaseURL:  "https://api.example.com",
		Features: []string{"auth", "logging", "metrics"},
	}

	app.Set("db", mockDB)
	app.Set("userService", userService)
	app.Set("config", configService)

	app.Use(surf.RequestIDMiddleware("services"))
	app.Use(surf.LoggingMiddleware("{method} {path} {status} {latency_ms}ms"))

	// Routes using service container
	app.Get("/users/:id", func(w http.ResponseWriter, r *http.Request) error {
		id := surf.Param(r, "id")

		userService := surf.GetService[*UserService](r, "userService")
		config := surf.GetService[*ConfigService](r, "config")

		// Use services
		user := userService.GetUser(id)
		user["api_version"] = "v1"
		user["features"] = config.Features

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(user)
	})

	app.Get("/config", func(w http.ResponseWriter, r *http.Request) error {
		config := surf.GetService[*ConfigService](r, "config")

		response := map[string]any{
			"base_url": config.BaseURL,
			"features": config.Features,
			// Don't expose sensitive data like APIKey
		}

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(response)
	})

	app.Get("/health", func(w http.ResponseWriter, r *http.Request) error {
		// Example of accessing database service
		db := surf.GetService[*sql.DB](r, "db")

		// In real code: err := db.Ping()
		healthy := db != nil // Mock check

		status := "healthy"
		if !healthy {
			status = "unhealthy"
			w.WriteHeader(http.StatusServiceUnavailable)
		}

		response := map[string]any{
			"status":    status,
			"timestamp": time.Now().Unix(),
		}

		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(response)
	})

	// Example of middleware that uses services
	app.UseFunc(func(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
		config := surf.GetService[*ConfigService](r, "config")

		w.Header().Set("X-API-Version", "v1")
		w.Header().Set("X-Features", fmt.Sprintf("%v", config.Features))

		next(w, r)
	})

	fmt.Println("Services demo starting on :8082")
	fmt.Println("Try:")
	fmt.Println("  curl http://localhost:8082/users/123")
	fmt.Println("  curl http://localhost:8082/config")
	fmt.Println("  curl http://localhost:8082/health")

	server := &http.Server{
		Addr:    ":8082",
		Handler: app,
	}

	if err := server.ListenAndServe(); err != nil {
		panic(err)
	}
}