# Surf - HTTP Web Framework

A lightweight, high-performance HTTP web framework for Go with flexible middleware support and structured logging.

## Features

- **Fast Routing**: Radix tree-based router with O(log n) route matching
- Simple routing with path parameters and wildcards
- **Per-route & per-group middleware**: attach standard middleware to a single route or a whole group, with `Skip` to exclude routes
- **Error-returning handlers**: a returned error is rendered to the client by a configurable renderer
- **Built-in Middleware**: CORS, Recovery, Rate Limiting, Timeout, Gzip compression
- **Typed service container**: register and resolve dependencies by type
- **Backplane**: share KV state (and advisory leases) across instances (pods/tasks) peer-to-peer, encrypted, with no external datastore
- **Request binding & validation**: JSON body binding with size limits and a `Validator` hook
- **JSON response envelopes**: `JSON`, `JSONData`, `JSONList`, `JSONError` helpers
- **SPA serving**: single-page-app handler with `embed.FS` support and asset caching
- **Metrics**: dependency-free Prometheus exposition middleware
- **WebSockets**: RFC 6455 upgrade helper alongside existing SSE support
- Built-in structured logging with slog integration, with path filtering
- **Static File Serving**: Serve directories and individual files
- **Query Parameter Helpers**: Type-safe query parameter parsing
- **Custom Error Handlers**: Customizable 404 and 405 responses
- Custom data storage in ResponseWriter
- Request ID generation
- Graceful server shutdown

See [CHANGELOG.md](CHANGELOG.md) for the full v0.1.0 feature list.

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

## Backplane: shared state across instances

When Surf runs across many pods (Kubernetes) or tasks (ECS), each instance has
its own process memory. Anything kept in a `sync.Mutex` or a local map —
sessions, idempotency keys, rate-limiter state, soft caches — diverges between
instances, so a client whose requests land on different pods sees inconsistent
behavior unless you pin it with session affinity.

The **Backplane** removes that need. It shares two primitives across instances:

- a **key/value store** with per-key TTL (`Get`/`Set`/`Delete`), and
- **advisory leases** with an auto-expiring TTL (best-effort, *not* a lock — see below).

There is no external datastore to operate. Instances coordinate **peer-to-peer**
over an encrypted gossip protocol, discovering each other via Kubernetes
headless-service DNS or a static peer list. Everything is pure Go standard
library — Surf keeps its zero-dependency guarantee.

### Default (single instance)

`app.Backplane()` is always available. With no configuration it is an in-process
`Local` backend — correct for one replica, tests, and local development:

```go
app := surf.NewApp()

type Session struct{ UserID string; Roles []string }
sessions := surf.NewKV[Session](app.Backplane(), "sess:")

sessions.Set(ctx, sid, Session{UserID: "u-42", Roles: []string{"admin"}}, 30*time.Minute)
s, ok, _ := sessions.Get(ctx, sid)
```

### Clustered (many instances)

Pass a clustered backend; the same code now shares state across pods. `NewApp`
starts the node and `Serve`/`Cleanup` stop it.

```go
secret := []byte(os.Getenv("SURF_CLUSTER_SECRET")) // from a Kubernetes Secret

bp := surf.NewClusterBackplane(
    secret,
    surf.K8sHeadless("surf-headless", "default", 7946), // or surf.StaticPeers("10.0.0.1:7946", ...)
    surf.WithClusterBindAddr(":7946"),
)
app := surf.NewApp(surf.WithBackplane(bp))
```

The single shared `secret` is used (via HKDF-derived subkeys) to AES-256-GCM
encrypt and authenticate both stored values and all peer traffic. A leaked
secret compromises the whole cluster; there is no per-peer identity.

### Advisory leases (NOT locks)

```go
lease, err := app.Backplane().Lease(ctx, "refresh-the-cache", 15*time.Second)
if err != nil { /* ... */ }
defer lease.Release(ctx)
// hold the lease while doing work that is merely wasteful to duplicate
```

> ⚠️ **A `Lease` is a coordination hint, not mutual exclusion.** It holds one owner only while cluster membership is stable. Under a network partition it fails *completely*: measured over the test harness, a symmetric two-node split **double-grants the same key 100% of the time**, and the fencing token (`lease.Token()`) **collides 100% of the time**, so it cannot even tiebreak the two holders — globally-monotonic tokens require consensus, which this backend doesn't have.
>
> Use a lease for work that's only *wasteful* to duplicate (deduplicating a background job, electing a soft primary for a cache refresh). **Never** use it to guard money, inventory, or any non-idempotent side effect. For those, point the `Backplane` at a real coordinator (etcd/Consul). The KV store, by contrast, is **eventually consistent** (last-write-wins) and well-suited to sessions, idempotency keys, and caches.
>
> *(Reproduce these numbers: `go test -tags eval -run TestEval -v .`)*

### Approximate distributed rate limiting

The built-in rate limiter can spread a global budget across instances. Set
`Distributed` and pass the backplane; the effective per-instance rate and burst
become the configured values divided by the live instance count:

```go
app.Use(surf.RateLimit(surf.RateLimitConfig{
    RequestsPerSecond: 1000, // cluster-wide budget
    Burst:             2000,
    Distributed:       true,
    Backplane:         app.Backplane(),
}))
```

It is approximate (each instance limits to its own share, so a partition lets
each side use a full local share). To build your own, read the live count
directly — a clustered backplane implements `ClusterSizer`:

```go
n := 1
if cs, ok := app.Backplane().(surf.ClusterSizer); ok {
    n = cs.Size()
}
perInstanceRate := globalRate / float64(n)
```

## v0.1.0 Features

### Per-Route and Per-Group Middleware

Attach standard middleware to a single route, or to a whole group:

```go
// Per-route: middleware wraps this handler only, outermost first.
app.Post("/admin", createAdmin, requireAuth, auditLog)

// Per-group: applies to every route registered on the group.
api := app.Group("/api").Use(requireAuth, surf.RateLimitWithDefaults())
api.Get("/users", listUsers)

// Skip excludes specific routes from the group's Before/After/Use middleware.
api.Skip("/api/health")
api.Get("/health", healthz) // no auth, no rate limit
```

`requireAuth` here is a standard `surf.Middleware` (`func(http.Handler) http.Handler`).
Unlike `Before` handlers, middleware can short-circuit by not calling `next` and
can propagate context with `r.WithContext`.

### Error-Returning Handlers

A returned error is now rendered to the client. Return an `*HTTPError` to control
the status and message; any other error becomes a generic 500 (internal detail is
logged, never leaked):

```go
app.Get("/widgets/:id", func(w http.ResponseWriter, r *http.Request) error {
    widget, err := store.Find(surf.Param(r, "id"))
    if err != nil {
        return surf.NewHTTPError(http.StatusNotFound, "widget not found")
    }
    return surf.JSONData(w, widget)
})
```

If a handler already wrote the response, the renderer is skipped so the response
is never corrupted. Return `surf.Abort` to stop processing silently. Override the
renderer with `surf.NewApp(surf.WithErrorHandler(myRenderer))`.

### Fast-Path Handlers

For the hottest endpoints, `App.Handle` registers a handler that receives a
pooled `*surf.Context` instead of `(w, r)`. The router copies neither the
request nor allocates per-request state — about twice as fast as the standard
path:

```go
app.Handle("GET", "/users/:id", func(c *surf.Context) error {
    return c.JSONData(map[string]string{"id": c.Param("id")})
})
```

`*Context` provides `Param`, `Query`, `Bind`, `JSON`/`JSONData`/`JSONError`,
`String`, and more. Compose fast-path middleware with `CtxMiddleware`, and
resolve typed services with `CtxService[T]`:

```go
app.Handle("GET", "/me", profile, requireAuthCtx)

func profile(c *surf.Context) error {
    db, _ := surf.CtxService[*sql.DB](c)
    _ = db
    return c.String(http.StatusOK, "ok")
}
```

A `*Context` is pooled and recycled when the handler returns — like gin's
`*Context`, do not retain it (or `c.Request`) in a goroutine that outlives the
handler. App-level `Use` middleware still wraps fast-path routes.

### Request Binding & Validation

```go
type SignupBody struct {
    Name  string `json:"name"`
    Email string `json:"email"`
}

func (b SignupBody) Validate() error {
    if b.Name == "" {
        return errors.New("name is required")
    }
    return nil
}

app.Post("/signup", func(w http.ResponseWriter, r *http.Request) error {
    var body SignupBody
    if err := surf.BindAndValidate(r, &body); err != nil {
        return err // 400 for bad JSON, 413 over limit, 422 for validation
    }
    return surf.JSONDataStatus(w, http.StatusCreated, body)
})
```

### JSON Response Envelopes

```go
surf.JSON(w, 200, v)                 // raw value
surf.JSONData(w, v)                  // {"data": v}
surf.JSONList(w, items, total)       // {"data": [...], "total": n}
surf.JSONError(w, 404, "not found") // {"error": "...", "status": 404}
```

### Typed Service Container

`Provide`/`Service` key services by type, eliminating the silent zero-value
bug of string-keyed lookups:

```go
surf.Provide[*sql.DB](app, db)
surf.Provide[Authenticator](app, oktaAuth) // register under an interface

db, ok := surf.Service[*sql.DB](r)
auth := surf.MustService[Authenticator](r) // panics if missing
```

### Single-Page Application Serving

```go
//go:embed all:dist
var distFS embed.FS

sub, _ := fs.Sub(distFS, "dist")
app.SPA("/", sub) // index fallback, immutable caching for /assets/*
```

Use `SPAWithConfig` for a custom index, immutable directories, or
`ExcludePrefixes` to 404 unknown API paths instead of serving HTML.

### Metrics

```go
m := surf.NewMetricsRegistry()
app.Use(m.Middleware())
app.Get("/metrics", m.Handler()) // Prometheus text exposition
```

### Logging with Path Filters

```go
app.Use(surf.LoggingMiddlewareWithConfig(surf.LoggingConfig{
    Format:    "{method} {path} {status} {latency_ms}ms",
    SkipPaths: []string{"/health/*"},
}))
```

### Rate Limiting Behind Proxies

```go
app.Use(surf.RateLimit(surf.RateLimitConfig{
    RequestsPerSecond: 10,
    TrustedProxies:    []string{"10.0.0.0/8"}, // X-Forwarded-For honored only from these
}))
```

### WebSockets

`Upgrade` enforces a **same-origin policy by default** — a handshake whose
`Origin` host differs from the request `Host` is rejected with 403, preventing
cross-site WebSocket hijacking. To accept specific cross-origin clients, use
`UpgradeWithConfig`:

```go
conn, err := surf.UpgradeWithConfig(w, r, surf.UpgradeConfig{
    CheckOrigin: surf.AllowOrigins("https://app.example.com"),
})
```

```go
app.Get("/ws", func(w http.ResponseWriter, r *http.Request) error {
    conn, err := surf.Upgrade(w, r) // same-origin only
    if err != nil {
        return err
    }
    defer conn.Close()
    for {
        mt, data, err := conn.ReadMessage()
        if err != nil {
            return surf.Abort
        }
        if err := conn.WriteMessage(mt, data); err != nil {
            return surf.Abort
        }
    }
})
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

See `example/slog/` for structured logging with:
- slog integration
- Reef package compatibility
- JSON output format
- Graceful shutdown

### Service Container

See `example/services/` for dependency injection with:
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