# AGENTS.md

A guide for AI coding agents (and humans new to the repo) working in `surf`.
This file documents how the codebase is structured, the design contracts you
must respect, and the conventions that keep changes additive and reviewable.

---

## What surf is

A minimal HTTP framework for Go. Radix-tree routing, two handler models
(stdlib-compatible and a pooled fast path), small standard middleware suite,
zero third-party dependencies. The project ethos is **additive evolution** —
the v0.1.0 feature release closed a long list of gaps without breaking a
single existing caller. Treat that constraint as binding for future changes
unless the user explicitly authorizes a break.

---

## Repo layout

```
.
├── *.go                  # the surf package (one module)
├── pkg/logger/reef/      # standalone slog handler with colored output
├── benchmarks/           # SEPARATE Go module — keeps surf dependency-free
├── example/              # standalone runnable examples (see Pitfalls)
├── _docs/                # Next.js (fumadocs) documentation site
├── CHANGELOG.md          # release notes
├── PERFORMANCE.md        # CPU-profile findings + ranked v0.1.1 roadmap
└── README.md             # user-facing docs
```

Source files in the surf package — feature → file map:

| File | What's there |
|---|---|
| `app.go` | `App` struct, `NewApp`, `Serve`, options plumbing |
| `options.go` | `WithLogger`, `WithAddr`, `WithErrorHandler`, … |
| `router.go` | Route registration (`Get`/`Post`/…/`Handle`), `Group`, `ServeHTTP`, dispatch, per-route middleware |
| `radix.go` | Radix tree; `search` (map) for tests/`getAllowedMethods`, `searchKV` (slice) for the hot path |
| `state.go` | `reqState` — pooled-context-value carrier for the standard path |
| `ctx.go` | `Context`, `CtxHandler`, `CtxMiddleware`, `CtxService[T]` — the opt-in fast path |
| `response.go` | `ResponseWriter` wrapper (`Hijack`/`Flush`/`Push`/`WriteString`) |
| `context.go` | `contextKey` type (still used by `RequestIDMiddleware` and `Get` fallback) |
| `errors.go` | `Abort`, `HTTPError`, `ErrorRenderer`, `DefaultErrorRenderer` |
| `render.go` | `JSON`, `JSONData`, `JSONList`, `JSONError` envelope helpers |
| `binding.go` | `Bind`, `BindWithLimit`, `BindAndValidate`, `Validator` |
| `service.go` | `Provide[T]`, `Service[T]`, `MustService[T]` — typed DI |
| `spa.go` | `App.SPA` / `SPAWithConfig` for single-page apps over any `fs.FS` |
| `metrics.go` | `MetricsRegistry` — Prometheus text exposition, no deps |
| `websocket.go` | RFC 6455 `Upgrade`, `WSConn`, same-origin policy by default |
| `middleware.go` | CORS, Recovery, RateLimit, Timeout, Gzip |
| `logger.go` | Request logging (template + structured) and the global `requestStorage` map |
| `ip.go` | `IPFromRequest`, `KeyByIP`, trusted-proxy CIDR handling |
| `match.go` | `matchGlob` / `matchAnyGlob` for route-pattern globs |
| `helpers.go` | Query helpers, redirects, `App.Static` / `App.StaticFile` |

---

## The two handler models

surf has **two handler signatures**. Both are first-class. Know which to use.

### Standard path — `HandlerFunc func(w http.ResponseWriter, r *http.Request) error`

```go
app.Get("/users/:id", func(w http.ResponseWriter, r *http.Request) error {
    id := surf.Param(r, "id")
    return surf.JSONData(w, map[string]string{"id": id})
})
```

- Stdlib-compatible signature; integrates with `http.Handler` middleware.
- Per-request: ~3 allocations (the `reqState`, the `r.WithContext` copy, plus
  whatever the handler does).
- Use for: anything that needs `surf.Param(r, …)`, `surf.GetService[T](r, …)`,
  composability with arbitrary `http.Handler` middleware, or that runs through
  app-level `Before`/`After` handlers.

### Fast path — `CtxHandler func(c *Context) error`

```go
app.Handle("GET", "/users/:id", func(c *surf.Context) error {
    return c.JSONData(map[string]string{"id": c.Param("id")})
})
```

- No request copy; `*Context` is pooled. ~0 framework allocations.
- ~2x faster than chi on the param-route benchmark; in the same tier as stdlib
  `ServeMux`. Still behind gin/echo — see `PERFORMANCE.md` for the closing
  plan.
- Compose middleware with `CtxMiddleware`; resolve typed services with
  `CtxService[T](c)`.
- App-level `Use` middleware still wraps fast routes. App `Before`/`After`
  also run, but they get an unmodified request — `surf.Param` is unavailable
  there. Use the Context.

### Choosing

| If you need… | Use |
|---|---|
| Hot loop endpoints (health, lookup-by-ID, lightweight reads) | fast path |
| Existing `http.Handler` middleware ecosystem | standard path |
| `surf.Param`/`Service`/`GetResponseWriter` from a `*http.Request` | standard path |
| Mixed | both — they coexist in the same `App` |

---

## Per-request architecture (the contract)

### `reqState` (standard path)
- One per request. **Not pooled** — deliberately. The `Timeout` middleware
  detaches the handler into a goroutine that outlives `ServeHTTP`; recycling
  the state would let that goroutine write through a freed `ResponseWriter`.
- Embeds `context.Context` and overrides `Value` so a single
  `r.WithContext(st)` attaches everything (app handle, response writer, path
  parameters). This is the design that took the standard path from 14 allocs
  to 3 — do not unravel it by adding more `context.WithValue` calls.

### `Context` (fast path)
- Pooled via `sync.Pool`. Released by **the handler's own goroutine** in a
  `defer` after the handler returns — that is why pooling is safe even under
  `Timeout`. **Don't break this invariant.** If you add middleware that
  detaches the handler into a goroutine, that goroutine must finish before
  `putContext` runs, or you must allocate a fresh Context for it.
- Standard pooling caveat applies: handlers **must not** retain `*Context` or
  `c.Request` in goroutines that outlive the handler. Documented in
  `ctx.go` — keep the doc accurate.

### Path parameters
- Resolved by `radixTree.searchKV` into a reusable `[]paramKV`. Standard path
  copies into `reqState.params`; fast path receives them directly in
  `Context.params` (backed by an inline `[8]paramKV` buffer).
- Read with `surf.Param(r, key)` on the standard path, `c.Param(key)` on the
  fast path. Both linear-scan — fine, routes have very few parameters.

### Middleware chain
- App-level `Middleware` (`app.Use`) is assembled **once**, on the first
  `ServeHTTP`. Register all middleware before the server starts serving;
  adding middleware after first request is silently ignored. Routes can still
  be added at any time (the chain ends in `dispatch`, which is dynamic).

---

## Conventions

### Routes
- Path parameters: `:name`. Wildcard: `*` (one per route, matched as the
  parameter `*`). Route patterns are compared by pattern; `/users/:id`
  conflicts with `/users/:resourceId` at the same tree level — the **first
  registered name wins**, the second route's `Param("resourceId")` returns
  `""`. Use a single consistent name across the codebase (`:id` everywhere is
  the project convention).

### Middleware
- `Middleware = func(http.Handler) http.Handler` — standard. Variadic on every
  route method (`app.Get(pattern, handler, mw1, mw2)`), and on `Group.Use`.
- `Group.Skip(patterns...)` excludes specific routes from the group's
  `Before`/`After`/`Use` middleware. Call **before** registering the affected
  routes.
- `CtxMiddleware = func(CtxHandler) CtxHandler` — fast-path equivalent,
  variadic on `App.Handle`.

### Errors
- Handlers return `error`. A returned error is rendered via `app.errorHandler`
  (default `DefaultErrorRenderer`, configurable with `WithErrorHandler`).
- `*HTTPError` carries the client-facing status code and message. The
  embedded `Err` field is **logged but never serialized** — keep it that way
  to avoid leaking internal detail.
- `surf.Abort` is the sentinel for "the response has been fully written, stop
  silently". `http.ErrAbortHandler` is honored for compatibility.
- The renderer is **skipped** if `rw.wroteHeader || rw.written` is true — a
  handler that pre-wrote a response and then returns an error has its
  response preserved. Preserve this guard.

### JSON envelopes
- `JSON(w, status, v)` — raw write with `Content-Type: application/json`.
- `JSONData(w, v)` → `{"data": v}`.
- `JSONList(w, items, total)` → `{"data": [...], "total": n}`.
- `JSONError(w, status, msg)` → `{"error": msg, "status": status}`.
- On the fast path, the equivalent methods live on `*Context`.

### Services
- `surf.Provide[T](app, value)` — type-keyed registration (reflected type).
- `surf.Service[T](r)` / `MustService[T](r)` — standard-path lookup.
- `surf.CtxService[T](c)` — fast-path lookup.
- Prefer typed services over the string-keyed `App.Set`/`App.GetService` for
  new code — the typed API can't silently return the zero value on a mismatch.

### Body binding
- `surf.Bind(r, &v)` — JSON only, 1 MiB default limit, enforces
  `Content-Type: application/json`, rejects trailing content.
- `surf.BindAndValidate(r, &v)` — same, plus calls `v.Validate()` if `v`
  implements `Validator`. Validation failures become `422 *HTTPError`.

---

## Security defaults (don't weaken these)

- **WebSocket `Upgrade` is same-origin by default.** `SameOriginCheck` rejects
  cross-origin handshakes with `403 *HTTPError`. Cross-origin acceptance is
  opt-in via `UpgradeWithConfig{CheckOrigin: …}` or
  `AllowOrigins("https://app.example")`.
- **Request body size is capped** at 1 MiB by `Bind`. `BindWithLimit` overrides.
- **Generic 500** for any returned error that is not a `*HTTPError` — internal
  detail is logged, not exposed.
- **SPA traversal is contained**: `path.Clean` + `fs.FS.Open`'s `fs.ValidPath`
  contract make `..` impossible to escape the served sub-tree.
- **Trusted-proxy IP**: `IPFromRequest` ignores `X-Forwarded-For` entirely
  unless the direct peer is in the configured trusted set, then walks
  right-to-left to the first untrusted hop. **Never** trust XFF without
  configuring trusted proxies.

---

## Build / test / bench commands

```sh
go build ./...                          # surf package builds
go vet ./...                            # static analysis
gofmt -l .                              # formatting check
go test -race -count=1 .                # full suite, race detector on
go test -cover -count=1 .               # coverage (currently ~77%)
go test -bench=. -benchmem -run='^$' .  # surf's own benchmarks

cd benchmarks && go test -bench=. -benchmem -run='^$'   # vs gin/echo/chi/stdlib
```

CI/check rules:
- Code MUST be `go vet` clean.
- New `.go` files MUST be `gofmt`-clean.
- New behavior MUST be covered by tests.
- Concurrency-sensitive code MUST pass `-race`.
- Do not introduce third-party dependencies into the surf module. The
  `benchmarks/` module is the dependency sandbox.

---

## Pitfalls (real ones you will hit)

1. **`example/` doesn't build.** Multiple files in `package main` each with
   their own `func main()`. It is pre-existing breakage — don't be surprised
   when `go test ./...` fails on the `example` package. Run tests scoped to
   the surf module: `go test ./...` from root will hit this; `go test .` from
   the surf package directory will not.
2. **`time.Now()` in `ResponseWriter.initWriter`** is currently unconditional.
   It is the single biggest unaccounted hot-path cost (see `PERFORMANCE.md`
   item #1). Don't add more `time.Now()` calls without a reason.
3. **`gh` CLI is not used in this repo.** The workflow is: push the branch
   with `git push -u origin <branch>`, then open the PR in the browser using
   the URL GitHub prints. Don't try to install or use `gh`.
4. **Routes can be added after first serve, middleware cannot.** The
   middleware chain is frozen by `sync.Once` on the first request. Order
   `app.Use(...)` before any handler that might trigger `ServeHTTP`.
5. **Param-name collisions across routes** at the same tree level silently
   bind to the first registered name. This is inherent to radix routers;
   stick to `:id` everywhere.
6. **`r.WithContext` does not work the way you'd expect inside a
   `HandlerFunc`.** The function's `r` is passed by value, so reassignments
   don't propagate to later before/after handlers. Per-route `Middleware`
   (the `http.Handler`-based kind) propagates context normally — use that
   when you need to attach values.
7. **`Set`/`Get` (the global request-storage map in `logger.go`)** keys by
   `*http.Request` pointer and is shared process-wide. It's the workaround
   for #6, kept for backward compatibility. Prefer per-route `Middleware` +
   `r.WithContext` for new code.

---

## Performance

Current state (Apple Silicon, `benchmarks/`):

| Router | param ns/op | param allocs |
|---|---|---|
| stdlib `ServeMux` | 80 | 2 |
| gin / echo | 55 / 57 | 1 / 1 |
| chi | 196 | 5 |
| surf (standard) | ~160 | 3 |
| surf (fast path) | ~100 | 2 |

`PERFORMANCE.md` records the CPU profile and a ranked, no-API-change plan to
close the remaining gin/echo gap (item #1 — drop `time.Now()` from the hot
path — is ~25% of the request on darwin, trivial to implement).

---

## How to extend surf

### Adding a new feature
1. Prefer **additive** changes — new functions/types alongside existing ones.
   Breaking changes need explicit user authorization.
2. Match the package's existing patterns (see `service.go` for typed-generic
   APIs, `binding.go` for `*HTTPError`-returning helpers).
3. Add tests in a `_test.go` file. Use `httptest` for routing; race-clean.
4. If the feature has a security dimension, make it **secure by default**
   (see the WebSocket Origin policy).
5. Document in `README.md` and add a CHANGELOG entry under the next version.

### Adding standard middleware
Implement `Middleware = func(http.Handler) http.Handler`. Provide a
`ThingConfig` struct and a `Thing(config) Middleware` constructor, plus a
`ThingWithDefaults() Middleware` convenience. Mirror `CORS`/`Recovery`/`RateLimit`.

### Adding a fast-path helper
If it's a per-request operation usable from a handler, add it as a method on
`*Context` in `ctx.go`. If it's a free function (like `CtxService[T]`),
follow the same pattern.

### Adding a response helper
Put it in `render.go` and have it return `error` so callers can `return
surf.MyHelper(w, ...)` from a handler.

---

## What's deferred

These were considered for v0.1.0 and explicitly punted; consult before
attempting them:

- **Context propagation across the `HandlerFunc` chain** — needs a signature
  change. Its own future release.
- **CORS default tightening** — downstream config concern.
- **Param naming with distinct names per branch** — inherent to radix
  routers; convention (`:id` everywhere) is the answer.
- **Deleting redundant logging functions** — surf carries ~6 overlapping
  logging variants. Cleanup belongs in its own PR.
