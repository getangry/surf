package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/getangry/surf"
)

func main() {
	app := surf.NewApp()

	app.Use(surf.RequestIDMiddleware("api"))

	app.Use(surf.LoggingMiddleware("{method} {path} {status} {latency_ms}ms id:{$request_id} user:{$user_id}"))

	// Example: Auth middleware that sets user context
	app.Before(func(w http.ResponseWriter, r *http.Request) error {
		// Simulate auth check
		if auth := r.Header.Get("Authorization"); auth != "" {
			// Workaround: set directly in global storage since framework doesn't preserve context
			surf.Store(r, "user_id", "user-123")
			surf.Store(r, "user_role", "admin")
			surf.Store(r, "tenant_id", "tenant-456")
		}
		return nil
	})

	// Simple routes
	app.Get("/", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("Welcome to Surf!"))
		return nil
	})

	app.Get("/hello/:name", func(w http.ResponseWriter, r *http.Request) error {
		name := surf.Param(r, "name")
		w.Write([]byte(fmt.Sprintf("Hello, %s!", name)))
		return nil
	})

	// Wildcard route
	app.Get("/static/*", func(w http.ResponseWriter, r *http.Request) error {
		path := surf.Param(r, "*")
		w.Write([]byte(fmt.Sprintf("Static file path: %s", path)))
		return nil
	})

	// API route group with JSON middleware
	api := app.Group("/api")
	api.Before(func(w http.ResponseWriter, r *http.Request) error {
		w.Header().Set("Content-Type", "application/json")
		return nil
	})

	// API routes
	api.Get("/users", func(w http.ResponseWriter, r *http.Request) error {
		if rw, ok := w.(*surf.ResponseWriter); ok {
			rw.Set("operation", "list_users")
			rw.Set("items_count", 2)
		}

		users := []map[string]string{
			{"id": "1", "name": "Alice"},
			{"id": "2", "name": "Bob"},
		}
		return json.NewEncoder(w).Encode(users)
	})

	api.Get("/users/:id", func(w http.ResponseWriter, r *http.Request) error {
		id := surf.Param(r, "id")

		surf.Store(r, "operation", "get_user")
		surf.Store(r, "target_user", id)
		surf.Store(r, "cache_hit", false)

		user := map[string]string{
			"id":   id,
			"name": "User " + id,
		}
		return json.NewEncoder(w).Encode(user)
	})

	api.Post("/users", func(w http.ResponseWriter, r *http.Request) error {
		var user map[string]string
		if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
			return err
		}

		// Simulate processing time for logging
		time.Sleep(10 * time.Millisecond)

		surf.Store(r, "operation", "create_user")
		surf.Store(r, "new_user_name", user["name"])

		user["id"] = "3"
		w.WriteHeader(http.StatusCreated)
		return json.NewEncoder(w).Encode(user)
	})

	// Nested group - API v2
	v2 := api.Group("/v2")
	v2.Before(func(w http.ResponseWriter, r *http.Request) error {
		log.Println("API v2 request")
		return nil
	})

	v2.Get("/status", func(w http.ResponseWriter, r *http.Request) error {
		status := map[string]interface{}{
			"version": "2.0",
			"status":  "healthy",
			"metrics": map[string]interface{}{
				"response_size":   surf.ResponseSize(r),
				"response_status": surf.ResponseStatus(r),
			},
		}
		return json.NewEncoder(w).Encode(status)
	})

	// Admin routes with authentication middleware
	admin := app.Group("/admin")
	admin.Before(func(w http.ResponseWriter, r *http.Request) error {
		// Simple auth check (in production, use proper authentication)
		auth := r.Header.Get("Authorization")
		if auth != "Bearer secret-token" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return fmt.Errorf("unauthorized access")
		}

		surf.Store(r, "user_type", "admin")
		surf.Store(r, "auth_method", "bearer_token")
		surf.Store(r, "permission_level", "high")

		return nil
	})

	admin.Get("/dashboard", func(w http.ResponseWriter, r *http.Request) error {
		surf.Store(r, "operation", "view_dashboard")
		w.Write([]byte("Admin Dashboard"))
		return nil
	})

	admin.Delete("/users/:id", func(w http.ResponseWriter, r *http.Request) error {
		id := surf.Param(r, "id")
		surf.Store(r, "operation", "delete_user")
		surf.Store(r, "target_user", id)
		surf.Store(r, "danger_level", "high")
		w.Write([]byte(fmt.Sprintf("User %s deleted", id)))
		return nil
	})

	// Example of different log formats you could use:
	/*
		Common format examples:

		// Apache-style combined log
		"{remote_addr} - {$user_id} [{timestamp}] \"{method} {path} {proto}\" {status} {size} \"{referer}\" \"{user_agent}\""

		// Simple format
		"{method} {path} {status} {latency_ms}ms"

		// Structured format with custom fields
		"{method} {path} {status} {latency_ms}ms user:{$user_id} op:{$operation} req:{$request_id}"

		// JSON-like format (could be enhanced to output actual JSON)
		"method={method} path={path} status={status} latency={latency_ms}ms user_id={$user_id} operation={$operation}"

		// Performance-focused format
		"{method} {path} {status} {latency_ms}ms size={size}b"
	*/

	log.Println("Server starting on :8080")
	if err := app.Serve(); err != nil {
		log.Fatal(err)
	}
}
