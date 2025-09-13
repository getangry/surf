package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/getangry/surf"
)

func main() {
	// Create a new Surf app
	app := surf.NewApp()

	// Global middleware - runs before all routes
	app.Before(func(w http.ResponseWriter, r *http.Request) error {
		log.Printf("Request: %s %s", r.Method, r.URL.Path)
		return nil
	})

	// Global after middleware - log response metrics
	app.After(func(w http.ResponseWriter, r *http.Request) error {
		// Access response metrics using the custom ResponseWriter
		if rw := surf.GetResponseWriter(r); rw != nil {
			log.Printf("Response: %d - %d bytes", rw.Status(), rw.Size())
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
		users := []map[string]string{
			{"id": "1", "name": "Alice"},
			{"id": "2", "name": "Bob"},
		}
		return json.NewEncoder(w).Encode(users)
	})

	api.Get("/users/:id", func(w http.ResponseWriter, r *http.Request) error {
		id := surf.Param(r, "id")
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
		log.Println("Admin request authenticated")
		return nil
	})

	// Log admin response details
	admin.After(func(w http.ResponseWriter, r *http.Request) error {
		// Demonstrate accessing ResponseWriter directly
		if rw, ok := w.(*surf.ResponseWriter); ok {
			log.Printf("Admin response: status=%d, size=%d bytes, written=%v",
				rw.Status(), rw.Size(), rw.Written())
		}
		return nil
	})

	admin.Get("/dashboard", func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte("Admin Dashboard"))
		return nil
	})

	admin.Delete("/users/:id", func(w http.ResponseWriter, r *http.Request) error {
		id := surf.Param(r, "id")
		w.Write([]byte(fmt.Sprintf("User %s deleted", id)))
		return nil
	})

	// Start the server
	log.Println("Server starting on :8080")
	if err := app.Serve(); err != nil {
		log.Fatal(err)
	}
}
