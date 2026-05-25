# Changelog

All notable changes to Surf are documented in this file.

## v0.2.1

A small additive release picking up ideas from two superseded community PRs
(#1 "Security improvements" and #2 "Router improvements") and grafting them
onto the post-v0.2.0 API.

### Added

- **`ResponseWriter.Committed()`** reports whether the response has begun
  (either `WriteHeader` or `Write` was called). Useful for any
  middleware/handler that wants to know whether it's still safe to write
  an error response. The existing error renderer migrated to it.
- **`WithRedirectTrailingSlash()`** app option. When enabled, a request
  whose path doesn't match but whose trailing-slash sibling does receives
  a **308 Permanent Redirect** to the registered variant. Method-scoped:
  a POST to `/foo/` does not redirect to a GET-only `/foo`. Off by default.
  Query strings are preserved.

### Security

- **`Static` now uses `os.OpenRoot`** for kernel-enforced path containment.
  On Linux this resolves every open through `openat2(RESOLVE_BENEATH)`, so
  symlink-escape attempts and `../` components are rejected by the kernel
  rather than by string inspection. Previous implementation used
  `http.Dir` + a `strings.Contains("..")` check; the new test
  `TestStaticSymlinkEscapeBlocked` documents the symlink protection.

  Behavior note: `Static` now panics at registration if the directory does
  not exist or is not a directory. Previously it silently 404'd at request
  time. Catching the misconfiguration loudly at startup is the safer
  default.

### Tests

- Five new middleware-level tests for CORS edge cases (no-Origin header,
  unlisted-origin behavior), Timeout context cancellation observed by the
  handler, and per-peer / spoofed-XFF behavior of the rate limiter when
  using `KeyByIP()`.

### Known issue surfaced

- The legacy `DefaultRateLimitConfig().KeyFunc` still honors
  `X-Forwarded-For` without proxy verification — a pre-v0.1.0 default
  that `KeyByIP()` and `TrustedProxies` were meant to replace but never
  made the default. Slated for a focused follow-up. Workaround today:
  pass `KeyFunc: surf.KeyByIP(trustedProxies...)` explicitly.

## v0.2.0

A performance release that closes most of the gap to gin and echo on the
fast path, plus three additive feature areas (lazy Context accessors, route
introspection, typed handlers). Every performance number in this section is
measured on Apple Silicon with `benchmarks/` (3-run median); re-bench on
your target hardware.

### Added

- **Lazy Context accessors.** `Cookies()`, `Cookie(name)`, `QueryValues()`
  on `*Context`. Each is `sync.Once`-gated and shares a parsed map across
  repeated calls. `c.Query(key)` now reads through the cached map instead of
  re-parsing per call. Routes that never call these accessors pay nothing
  beyond the slightly larger struct (measured cost when unused: 0.1 ns,
  within noise).
- **Route metadata introspection.** `App.Routes() []RouteInfo` returns a
  snapshot of every registered route — Method, Pattern, Params, Style
  (`StyleStandard` vs `StyleContext`), and (for typed handlers) the request
  and response `reflect.Type`. Captured at registration; zero per-request
  cost. Enables a future `surf/openapi` package to emit OpenAPI 3.1 by
  walking the type info.
- **Typed handlers.** Three new generic registrations:
  - `surf.HandleJSON[Req, Resp](app, method, pattern, fn, mw...)` — the
    framework runs `Bind → Validator → call → JSON encode`.
  - `surf.HandleJSONStatus[Req, Resp]` — same, with a custom success status.
  - `surf.HandleQuery[Resp]` — typed response, no request body.

  Each captures the `Req`/`Resp` types into `RouteInfo` for introspection.

### Changed

- **`ResponseWriter.StartTime` is now an exported field** instead of being
  set automatically by `initWriter` / `NewResponseWriter`. Each built-in
  logging middleware sets it itself at the top of its wrapper, so existing
  template formats (`{latency_ms}`) and `RequestLogger` continue to work
  unchanged for users of those middlewares.
- **`Latency()` returns `0` when `StartTime` is the zero value** (the new
  default) instead of `time.Since(some-default)`.
- **Removed: `ResponseWriter.StartTime()` method.** It collided with the
  new field name. Replace `rw.StartTime()` calls with `rw.StartTime`.

The `time.Now()` removal saves ~25 ns per request on Apple Silicon for
routes that don't time their requests. `SimpleLogger` (the After-handler
variant) will see zero latency unless a Before-handler sets `rw.StartTime`.

### Performance

Cross-framework benchmark (`benchmarks/`, Apple Silicon, Go 1.26, 3-run
median, ns/op / allocs/op):

| Router | Static | Param |
|---|---|---|
| `net/http.ServeMux` | 29 / 1 | 77 / 2 |
| gin | 47 / 1 | 54 / 1 |
| echo | 47 / 1 | 57 / 1 |
| **surf-fast (v0.2.0)** | **55 / 2** | **62 / 2** |
| surf-fast (v0.1.0) | 89 / 2 | 100 / 2 |
| chi | 99 / 3 | 195 / 5 |
| surf standard (v0.2.0) | 122 / 3 | 138 / 3 |
| surf standard (v0.1.0) | 145 / 3 | 167 / 3 |

surf-fast static is ~38% faster than v0.1.0, surf-fast param is ~38% faster.
surf-fast beats chi by ~2× on static and ~3× on param. surf-fast beats stdlib
`ServeMux` on the param route by ~24%. gin and echo remain 10–15% faster.

### Performance changes by commit

- Canonical header table — bypass `CanonicalMIMEHeaderKey` for headers surf
  itself writes (Content-Type, Vary, Content-Encoding, Allow, X-Request-Id,
  Retry-After). Measured: −9.8 ns static, −11.6 ns param.
- Drop `time.Now()` from `initWriter`. Logging middleware sets it itself.
  Measured: −23.8 ns static, −24.5 ns param.
- Radix tree: split children into typed slots (`staticChildren`,
  `paramChild`, `wildcardChild`) so search avoids the per-node type filter.
  Measured: −1 to −3 ns per lookup depending on depth.

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
