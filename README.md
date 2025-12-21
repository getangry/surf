# Surf - HTTP Web Framework

A lightweight, high-performance HTTP web framework for Go with flexible middleware support and structured logging.

## Features

- **Fast Routing**: Radix tree-based router with O(log n) route matching
- Simple routing with path parameters and wildcards
- Dual middleware system: standard middleware and Before/After handlers
- **Built-in Middleware**: CORS, Recovery, Rate Limiting, Timeout, Gzip compression
- Built-in structured logging with slog integration
- Configurable request logging with template-based formats
- **Static File Serving**: Serve directories and individual files
- **Query Parameter Helpers**: Type-safe query parameter parsing
- **Custom Error Handlers**: Customizable 404 and 405 responses
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

### Static File Serving

```go
// Serve a directory
app.Static("/assets", "./public")

// Serve a single file
app.StaticFile("/favicon.ico", "./favicon.ico")
```

### Custom Error Handlers

```go
// Custom 404 handler
app.NotFound(func(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusNotFound)
    w.Write([]byte("Page not found"))
})

// Custom 405 handler
app.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
    w.WriteHeader(http.StatusMethodNotAllowed)
    w.Write([]byte("Method not allowed"))
})
```

## Query Parameter Helpers

Type-safe query parameter parsing with defaults:

```go
app.Get("/search", func(w http.ResponseWriter, r *http.Request) error {
    // String parameters
    query := surf.Query(r, "q")
    name := surf.QueryDefault(r, "name", "anonymous")

    // Numeric parameters
    page := surf.QueryInt(r, "page", 1)
    limit := surf.QueryInt(r, "limit", 20)
    offset := surf.QueryInt64(r, "offset", 0)
    price := surf.QueryFloat(r, "price", 0.0)

    // Boolean parameters (accepts "true", "1", "yes", "on")
    active := surf.QueryBool(r, "active", true)

    // Multi-value parameters (?tags=a&tags=b&tags=c)
    tags := surf.QuerySlice(r, "tags")

    return nil
})
```

## Redirect Helpers

```go
// Generic redirect with custom status code
surf.Redirect(w, r, "/new-location", http.StatusFound)

// Convenience helpers
surf.RedirectPermanent(w, r, "/new")  // 301 Moved Permanently
surf.RedirectTemporary(w, r, "/temp") // 302 Found
surf.RedirectSeeOther(w, r, "/other") // 303 See Other (use after POST)
```

## Middleware

### Standard Middleware

```go
app.Use(surf.RequestIDMiddleware("api"))
app.Use(surf.LoggingMiddleware("{method} {path} {status} {latency_ms}ms"))
app.Use(surf.SlogMiddleware(slogger))
```

### Built-in Middleware

#### CORS

```go
// With defaults (allows all origins)
app.Use(surf.CORSWithDefaults())

// With custom configuration
app.Use(surf.CORS(surf.CORSConfig{
    AllowOrigins:     []string{"https://example.com", "https://api.example.com"},
    AllowMethods:     []string{"GET", "POST", "PUT", "DELETE"},
    AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
    AllowCredentials: true,
    MaxAge:           86400, // 24 hours
}))
```

#### Panic Recovery

```go
// With defaults
app.Use(surf.RecoveryWithDefaults())

// With custom configuration
app.Use(surf.Recovery(surf.RecoveryConfig{
    Logger:    slog.Default(),
    StackSize: 4 << 10, // 4KB
    RecoveryHandler: func(w http.ResponseWriter, r *http.Request, err interface{}) {
        http.Error(w, "Something went wrong", http.StatusInternalServerError)
    },
}))
```

#### Rate Limiting

```go
// With defaults (10 req/sec, burst of 20)
app.Use(surf.RateLimitWithDefaults())

// With custom configuration
app.Use(surf.RateLimit(surf.RateLimitConfig{
    RequestsPerSecond: 100,
    Burst:             200,
    KeyFunc: func(r *http.Request) string {
        return r.Header.Get("X-API-Key") // Rate limit by API key
    },
    SkipFunc: func(r *http.Request) bool {
        return r.URL.Path == "/health" // Skip health checks
    },
}))
```

#### Request Timeout

```go
// With defaults (30 seconds)
app.Use(surf.TimeoutWithDefaults())

// With custom configuration
app.Use(surf.Timeout(surf.TimeoutConfig{
    Timeout: 10 * time.Second,
    TimeoutHandler: func(w http.ResponseWriter, r *http.Request) {
        http.Error(w, "Request timed out", http.StatusGatewayTimeout)
    },
}))
```

#### Gzip Compression

```go
// With defaults (compresses text/html, application/json, etc.)
app.Use(surf.GzipWithDefaults())

// With custom configuration
app.Use(surf.Gzip(surf.GzipConfig{
    Level:   gzip.BestSpeed,
    MinSize: 1024, // Only compress responses > 1KB
    ContentTypes: []string{
        "text/html",
        "application/json",
        "text/css",
    },
}))
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

## Service Container

Register and inject dependencies using the built-in service container:

```go
// Register services at startup
app.Set("db", dbConnection)
app.Set("redis", redisClient)
app.Set("userService", &UserService{DB: dbConnection})

// Use in handlers with type safety
app.Get("/users/:id", func(w http.ResponseWriter, r *http.Request) error {
    db := surf.GetService[*sql.DB](r, "db")
    userService := surf.GetService[*UserService](r, "userService")

    // Use services...
    return nil
})

// Use in middleware
app.UseFunc(func(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
    config := surf.GetService[*ConfigService](r, "config")
    // Use config...
    next(w, r)
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

### Service Container

See `example/services_demo.go` for dependency injection with:
- Service registration and retrieval
- Type-safe service access with generics
- Database and service layer examples
- Middleware service usage

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

## Server Configuration

Configure server settings with functional options:

```go
app := surf.NewApp(
    surf.WithServerConfig(surf.ServerConfig{
        Addr:           ":8080",
        ReadTimeout:    15 * time.Second,
        WriteTimeout:   15 * time.Second,
        IdleTimeout:    60 * time.Second,
        MaxHeaderBytes: 1 << 20, // 1MB
    }),
    surf.WithLogger(slog.Default()),
)
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

## Performance

The router uses a radix tree data structure for fast route matching:

- O(log n) route lookup instead of O(n) linear search
- Efficient memory usage through prefix compression
- Benchmarks show ~73x faster routing with 100 routes

## License

MIT License