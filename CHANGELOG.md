# Changelog

All notable changes to Surf are documented in this file.

## v0.1.0

A feature release that closes long-standing framework gaps. **All changes are
additive** — existing code compiles and behaves the same, with one intentional,
cosmetic behavior change noted below.

### Added

- **Per-route middleware.** `Get`/`Post`/`Put`/`Delete`/`Patch`/`Head`/`Options`
  on both `App` and `Group` accept optional trailing `...Middleware`, applied to
  that route only.
- **Fast-path handlers.** `App.Handle` / `Group.Handle` register a handler that
  receives a pooled `*Context` (`func(c *Context) error`) instead of `(w, r)`.
  The router copies neither the request nor allocates per-request state.
  `CtxMiddleware` composes fast-path middleware; `CtxService[T]` resolves typed
  services. Use it for the hottest endpoints.
- **Per-group middleware.** `Group.Use(...Middleware)` applies standard
  middleware to every route in a group; `Group.Skip(patterns...)` excludes
  specific routes from the group's `Before`, `After`, and `Use` middleware.
- **Error rendering.** Errors returned by handlers and before/after handlers are
  rendered to the client. `*HTTPError` controls the status code and a
  client-safe message; other errors yield a generic 500. `surf.Abort` is a
  sentinel for "response already written, stop silently" (the framework-aware
  replacement for the `http.ErrAbortHandler` pattern, which is still honored).
  The renderer is configurable via `WithErrorHandler`.
- **JSON helpers.** `JSON`, `JSONData`, `JSONDataStatus`, `JSONList`, and
  `JSONError` write standardized response envelopes.
- **Request binding.** `Bind`, `BindWithLimit`, and `BindAndValidate` decode
  JSON request bodies with a size limit and an optional `Validator` hook.
- **Typed service container.** `Provide[T]`, `Service[T]`, and `MustService[T]`
  register and resolve services keyed by type, removing the silent zero-value
  failure mode of string-keyed `GetService`.
- **SPA serving.** `App.SPA` and `App.SPAWithConfig` serve a single-page app
  from any `fs.FS` (including `embed.FS`), with index fallback, immutable asset
  caching, and excludable prefixes.
- **Metrics.** `MetricsRegistry` provides a middleware and a handler that expose
  request counts, in-flight gauge, and a latency histogram in the Prometheus
  text exposition format, with no external dependencies.
- **WebSockets.** `Upgrade` performs the RFC 6455 handshake and returns a
  `WSConn` supporting text/binary messages, fragmentation, and automatic ping
  replies. `IsWebSocketUpgrade` detects upgrade requests. `Upgrade` enforces a
  same-origin policy by default (`SameOriginCheck`) to prevent cross-site
  WebSocket hijacking; `UpgradeWithConfig` accepts an `UpgradeConfig` with a
  `CheckOrigin` hook, and `AllowOrigins` builds one from an allowlist.
- **Logging path filters.** `LoggingMiddlewareWithConfig` accepts `SkipPaths`
  (exact or trailing-`*` prefix) to exclude paths such as health probes.
- **Proxy-aware client IP.** `IPFromRequest` and `KeyByIP` derive the client IP,
  honoring `X-Forwarded-For` only for configured trusted proxy CIDRs.
  `RateLimitConfig` gains a `TrustedProxies` field.

### Performance

- The per-request hot path was reworked. surf previously threaded the `App`,
  the `ResponseWriter`, and every path parameter through separate
  `context.WithValue` calls, and allocated a `customData` map and a `params`
  map on every request. All per-request state now lives in a single `reqState`
  that also serves as the request context, parameters are resolved into an
  inline buffer, and the `customData` map is allocated lazily.
- Result on an isolated param-route benchmark: the standard `func(w, r)` path
  went from **416 ns/op, 14 allocs/op** to **~160 ns/op, 3 allocs/op** (the
  framework allocations are the `reqState` and the `r.WithContext` request
  copy).
- The opt-in fast path (`App.Handle`, `*Context`) avoids the request copy and
  pools all per-request state: **~98 ns/op, 2 allocs/op** — roughly twice as
  fast as chi on the same benchmark. gin and echo (~55 ns/op) remain ahead
  because their handler-receives-context model is mandatory rather than opt-in.
- The app middleware chain is now assembled once instead of being rebuilt (with
  a closure allocation per middleware) on every request.
- A `benchmarks/` module (separate, so surf stays dependency-free) compares
  surf — both paths — against gin, echo, chi, and `net/http.ServeMux`.

### Changed

- The default response for an *unhandled* handler error is now a JSON envelope
  (`{"error": "...", "status": ...}`) instead of plain text `Internal Server
  Error`. The status code (500) is unchanged. Handlers that wrote their own
  response before returning an error are unaffected — the renderer is skipped
  when the response has already started.

### Not included

- **Context propagation across the `HandlerFunc` chain** would require a
  breaking signature change and is deferred to its own release. Per-route and
  per-group `Middleware` already propagate `context` normally.
- **CORS default tightening** is a downstream configuration concern, not a
  framework gap.
