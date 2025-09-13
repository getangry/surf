# Surf - HTTP Web Framework

A lightweight HTTP web framework for Go with flexible middleware support and structured logging.

## Features

- Simple routing with path parameters and wildcards
- Dual middleware system: standard middleware and Before/After handlers
- Built-in structured logging with slog integration
- Configurable request logging with template-based formats
- Custom data storage in ResponseWriter
- Request ID generation
- Graceful server shutdown

## Quick Start

```go
package main

import (
    "encoding/json"
    "net/http"
    "github.com/getangry/surf"
)

func main() {
    app := surf.NewApp()

    // Add middleware
    app.Use(surf.RequestIDMiddleware("api"))
    app.Use(surf.LoggingMiddleware("{method} {path} {status} {latency_ms}ms"))

    // Define routes
    app.Get("/hello/:name", func(w http.ResponseWriter, r *http.Request) error {
        name := surf.Param(r, "name")
        response := map[string]string{"message": "Hello, " + name + "!"}
        return json.NewEncoder(w).Encode(response)
    })

    // Start server
    app.Serve()
}
```

## Routing

### Basic Routes

```go
app.Get("/users", handler)
app.Post("/users", handler)
app.Put("/users/:id", handler)
app.Delete("/users/:id", handler)
app.Patch("/users/:id", handler)
```

### Path Parameters

```go
app.Get("/users/:id", func(w http.ResponseWriter, r *http.Request) error {
    id := surf.Param(r, "id")
    // Handle user with ID
    return nil
})
```

### Wildcard Routes

```go
app.Get("/static/*", func(w http.ResponseWriter, r *http.Request) error {
    path := surf.Param(r, "*")
    // Serve static files from path
    return nil
})
```

### Route Groups

```go
api := app.Group("/api")
api.Before(func(w http.ResponseWriter, r *http.Request) error {
    w.Header().Set("Content-Type", "application/json")
    return nil
})

api.Get("/users", usersHandler)
api.Post("/users", createUserHandler)

// Nested groups
v2 := api.Group("/v2")
v2.Get("/status", statusHandler)
```

## Middleware

### Standard Middleware

```go
app.Use(surf.RequestIDMiddleware("api"))
app.Use(surf.LoggingMiddleware("{method} {path} {status} {latency_ms}ms"))
app.Use(surf.SlogMiddleware(slogger))
```

### Before/After Handlers

```go
// Global handlers
app.Before(authHandler)
app.After(cleanupHandler)

// Group-specific handlers
api := app.Group("/api")
api.Before(jsonHeaderHandler)
api.After(auditHandler)
```

## Logging

### Template-Based Logging

Configure log format using template syntax:

```go
app.Use(surf.LoggingMiddleware("{method} {path} {status} {latency_ms}ms user:{$user_id}"))
```

Available template variables:
- `{method}` - HTTP method
- `{path}` - Request path
- `{status}` - Response status code
- `{latency_ms}` - Request latency in milliseconds
- `{size}` - Response size in bytes
- `{remote_addr}` - Client IP address
- `{user_agent}` - User agent string
- `{$custom_key}` - Custom data stored in ResponseWriter

### Structured Logging (slog)

```go
import "log/slog"

slogger := slog.Default()

// Option 1: Pure slog middleware
app.Use(surf.SlogMiddleware(slogger))

// Option 2: Reef-compatible middleware
app.Use(surf.ReefCompatibleMiddleware(slogger))

// Option 3: Combined traditional + slog
app.Use(surf.CombinedMiddleware("{method} {path} {status}", slogger))
```

### Custom Data Storage

Store custom data in handlers for logging:

```go
app.Get("/users/:id", func(w http.ResponseWriter, r *http.Request) error {
    if rw, ok := w.(*surf.ResponseWriter); ok {
        rw.Set("operation", "get_user")
        rw.Set("user_id", surf.Param(r, "id"))
        rw.Set("cache_hit", true)
    }
    // Handle request...
    return nil
})
```

## Request Context Storage

Store and retrieve data in request context:

```go
// Store data
surf.Store(r, "user_id", "123")
surf.Store(r, "operation", "create_user")

// Retrieve data
userID := surf.Get(r, "user_id").(string)
operation := surf.GetString(r, "operation")
```

## Examples

### Basic Server

See `example/main.go` for a complete example with:
- Request ID middleware
- Logging middleware
- Route groups
- Authentication middleware
- Custom data storage

### Structured Logging

See `example/slog_demo.go` for structured logging with:
- slog integration
- Reef package compatibility
- JSON output format
- Graceful shutdown

## Middleware Options

### Request ID Middleware

Generates unique request IDs:

```go
app.Use(surf.RequestIDMiddleware("api"))
// Generates IDs like: api-hostname-08d16a17
```

### Logging Middleware Variants

```go
// Traditional text logging
app.Use(surf.LoggingMiddleware("{method} {path} {status} {latency_ms}ms"))

// Pure structured logging
app.Use(surf.SlogMiddleware(slogger))

// Reef-compatible structured logging
app.Use(surf.ReefCompatibleMiddleware(slogger))

// Both text and structured logging
app.Use(surf.CombinedMiddleware("{method} {path} {status}", slogger))
```

## Graceful Shutdown

The framework includes built-in graceful shutdown:

```go
app := surf.NewApp()
// Configure routes and middleware...

if err := app.Serve(); err != nil {
    log.Fatal(err)
}
```

The server will:
1. Listen for SIGINT/SIGTERM signals
2. Stop accepting new connections
3. Wait for existing requests to complete (up to 5 seconds)
4. Shutdown gracefully

## License

MIT License