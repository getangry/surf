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
  replies. `IsWebSocketUpgrade` detects upgrade requests.
- **Logging path filters.** `LoggingMiddlewareWithConfig` accepts `SkipPaths`
  (exact or trailing-`*` prefix) to exclude paths such as health probes.
- **Proxy-aware client IP.** `IPFromRequest` and `KeyByIP` derive the client IP,
  honoring `X-Forwarded-For` only for configured trusted proxy CIDRs.
  `RateLimitConfig` gains a `TrustedProxies` field.

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
