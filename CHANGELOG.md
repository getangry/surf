# Changelog

All notable changes to Surf are documented in this file.

## Unreleased

### Routing

#### Added

- **`App.Query` / `Group.Query`** register routes for the HTTP `QUERY` method
  ([RFC 10008](https://www.rfc-editor.org/rfc/rfc10008.html)) — a safe,
  idempotent, cacheable method that carries a request body, giving you a `GET`
  whose selection criteria travel in the body instead of the URL. Read the
  enclosed content from `r.Body` as you would for `POST` (`surf.Bind` /
  `BindAndValidate` work unchanged); for a fully typed pipeline, the existing
  `HandleJSON`/`HandleJSONStatus` helpers accept `"QUERY"` as the method. The
  default CORS config now advertises `QUERY` in `Access-Control-Allow-Methods`.
- **Automatic `OPTIONS` handling.** An `OPTIONS` request to a path that has
  routes but no explicit `OPTIONS` handler now returns `204 No Content` with a
  sorted `Allow` header (previously it fell through to `405`). An explicitly
  registered `OPTIONS` route still wins; disable the behavior with the new
  **`WithoutAutomaticOptions()`** option. The `Allow` header is now sorted
  deterministically on both the `OPTIONS` and `405` paths.
- **`Accept-Query` advertising** (RFC 10008 §3). Any path with a `QUERY` route
  emits `Accept-Query: application/json` on its automatic `OPTIONS`/`405`
  responses so clients can discover QUERY support and the accepted body formats.
  This works behind the CORS middleware: CORS now short-circuits only a genuine
  preflight (one carrying `Access-Control-Request-Method`), letting a plain
  `OPTIONS` probe reach the automatic handler. Configure the advertised media
  types with **`WithAcceptQuery(...)`** (pass none to suppress the header), or
  set it on a specific response — e.g. a 415 — with **`surf.SetAcceptQuery(w,
  ...)`**.

#### Changed

- **CORS no longer short-circuits every `OPTIONS` request.** Only genuine
  preflights (those with `Access-Control-Request-Method`) get the immediate
  `204`; a plain `OPTIONS` falls through so the router's automatic handler can
  answer with `Allow`/`Accept-Query`. CORS response headers are still set on
  both.

#### Performance

- **Iterative radix lookup.** `searchNodeKV` no longer recurses. It walks the
  tree with an explicit backtracking stack held inline (8 frames) on the
  goroutine stack, and pushes a frame only at a node that actually has an
  untried param or wildcard alternative — a purely static or single-param
  route now resolves with no backtracking state at all. Static children of a
  node never share a leading byte (insert splits on the common prefix), so at
  most one can prefix-match; the walk checks that byte before the `HasPrefix`
  call. Behavior is unchanged, and a new property test
  (`radix_property_test.go`) checks the iterative walk against the old
  recursive one over hundreds of randomly generated trees, plus asserts the
  disjoint-siblings invariant the walk relies on.
- **Allocation-free string responses.** `ResponseWriter.WriteString` now
  prefers the underlying writer's own `WriteString` when it has one, and
  otherwise passes the string to `Write` as a borrowed byte view instead of
  going through `io.WriteString`, whose fallback copies into a fresh
  `[]byte`. `c.String` responses no longer allocate. (`io.Writer` already
  forbids implementations from modifying or retaining the slice they are
  given, which is what makes the view safe.)
- Together, on an Apple M4: the fast path's param route goes **66.9 ns / 2
  allocs → 57.5 ns / 1 alloc**, and its static route **55.5 ns / 2 allocs →
  46.5 ns / 1 alloc** — level with echo (55.1 ns) and within ~10% of gin
  (52.4 ns). See `PERFORMANCE.md`.

### Introspection (OpenAPI & MCP)

#### Added

- **OpenAPI 3.1 generation from typed routes.** `App.OpenAPI(APIInfo)` builds
  a document from the registered routes, and `App.OpenAPIHandler(APIInfo)`
  serves it as JSON (built once, then cached). Typed routes
  (`HandleJSON`/`HandleJSONStatus`/`HandleQuery`) contribute full request and
  response schemas derived by reflection over the captured `Req`/`Resp` types;
  untyped routes degrade to method/path/params with a free-form response. Nested
  named structs are deduplicated into `components.schemas` and referenced with
  `$ref`. Typed routes now also capture their success status
  (`RouteInfo.SuccessStatus`) so the document reports the right response code.
- **MCP endpoint from typed routes.** `MCPHandle` registers a typed route and
  additionally exposes it as a [Model Context Protocol](https://modelcontextprotocol.io)
  tool; `App.MCP(pattern, MCPOptions)` mounts a JSON-RPC 2.0 endpoint speaking
  `initialize`, `tools/list`, and `tools/call`. Exposure is always deliberate —
  a route becomes a tool only through `MCPHandle` — and `MCPOptions.ExposeWhen`
  gates each tool per request for both listing and calling. Tool name,
  description, and argument metadata come from `desc`/`required` struct tags on
  the embedded `surf.MCPRequest` marker. `tools/call` dispatches the real
  handler in process through the full router, so binding, validation,
  middleware, and error rendering behave identically to a live request.
- **`SchemaFor(reflect.Type)`** exposes the reflection-based JSON Schema builder
  (draft 2020-12 / OpenAPI 3.1 compatible) shared by all three introspection
  consumers. Pointers are unwrapped, so a `*T` field is described as optional
  (absent from `required`) rather than as nullable — the schema does not
  represent explicit JSON `null`.

#### Fixed

- **`OpenAPIHandler` builds its cached document under a `sync.Once`.** The
  lazy first build previously raced when concurrent requests arrived before the
  cache was populated.
- **MCP tool calls that omit a path parameter are rejected with `400`.** The
  unfilled `:param` segment stayed in the path literally, still matched the
  route, and ran the handler with `":param"` as the parameter value.
- **The in-process MCP sub-request now carries the caller's `RemoteAddr` and
  `Host`.** IP-keyed middleware (rate limiting, logging, trusted-proxy
  resolution) previously saw an empty address on tool calls, so dispatching
  through `tools/call` sidestepped it.
- **Schema definitions no longer collide across packages.** Two distinct struct
  types sharing a Go type name (`pkga.User` and `pkgb.User`) overwrote each
  other in `$defs`/`components.schemas`, leaving `$ref`s pointing at the wrong
  schema; later arrivals are now qualified with their package name.

### Request storage (`Set` / `Store` / `Get`)

#### Fixed

- **Request-scoped values no longer disappear when a middleware calls
  `r.WithContext`.** Storage was a package-level
  `map[*http.Request]map[string]interface{}` keyed by the request **pointer**.
  `r.WithContext(...)` returns a *new* `*http.Request`, so any middleware that
  derived a request between the write and the read handed handlers a request the
  value was not filed under: `Get` returned nothing and the caller saw a **wrong
  answer, not an error**. In production this made an authenticated user's id
  vanish downstream of the auth middleware, and roughly fifty endpoints told
  signed-in users they were not members of their own organization.

  Values now live in the request's own `context.Context` under the unexported
  `contextKey` type, which `r.WithContext` preserves by construction. The
  signatures are unchanged: `Set` already took `**http.Request`, so it rebinds
  the caller's request to one carrying the value — **pass the rebound request
  on** (`surf.Set(&r, …); next.ServeHTTP(w, r)`).
- **The storage no longer leaks a whole `*http.Request` per request.** The only
  cleanup was a `defer Delete(r)` inside this package's *logging* middleware, so
  an application that used `Set`/`Store` without wiring that middleware pinned
  every request — headers, context and body — for the life of the process. And
  where the pointer had diverged, `Delete` could not free the entry anyway.
  Contexts are collected with their request; there is nothing left to leak.
- **`LoggerMiddleware` / `LoggerAfter` report a real latency, and stop leaking.**
  The start time lived in a second `map[*http.Request]time.Time`, one instance
  per closure — so `LoggerAfter` looked in a map `LoggerMiddleware` had never
  written to (every latency it logged was ~0), and nothing ever drained
  `LoggerMiddleware`'s map. The start time now rides on the request.
- `Get`, `Store` and `Delete` no longer panic on a nil `*http.Request`.

#### Changed

- **`Store` attaches to the request in place** rather than to a global map,
  which is what makes its value visible to later `r.WithContext` copies. It must
  not be called on a request another goroutine is reading concurrently — safe
  in the single goroutine net/http gives each request, and only there. Prefer
  `Set` (or per-route `Middleware` + `r.WithContext`) where you can rebind.
- No lock is taken on any read or write; there is no shared state left to guard.
  `Set`/`Store` now allocate a context node per call (and `Get` boxes its key),
  where the map cost a mutex instead.

#### Deprecated

- **`Delete(r)` is a retained no-op.** It is still exported so existing callers
  compile and behave correctly, but a `context.Context` cannot have a value
  removed from it and there is no longer any process-global state to release.

### Logging (`pkg/logger/reef`)

#### Performance

- The colorized text path now pools its slog handler and output buffers
  instead of reconstructing a handler and reparsing/reformatting on every
  record. It is **allocation-free after warm-up** (down from ~20 allocations
  per record), roughly 2.5× faster on the colorized path. The only remaining
  allocation per record is slog's own — identical to a vanilla slog handler.

#### Added

- **`WithLevelWidth(n)`** (and `ColorConfig.LevelWidth`) sets the minimum width
  the level column is padded to (default 5, which fits the standard levels).
  Raise it for wider custom level names like `CRITICAL`.

#### Changed

- **`WithColors()` / `WithoutColors()` are now order-independent.** They toggle
  the enable flag and fill in unset defaults without discarding custom key
  colors, level colors, or other settings applied by other options. Previously
  `WithColors()` replaced the entire color config, silently wiping options that
  ran before it.
- **JSON output no longer silently ignores colors.** ANSI codes cannot live in
  JSON, so colorization is now explicitly skipped for `JSONHandler`; the
  per-line color control attribute (`reef.Color`) is stripped from the
  structured output rather than rendered.
- **Colorized writes are serialized** with a per-writer mutex, matching the
  concurrency guarantee the standard slog handlers provide. The lock is shared
  across `WithAttrs`/`WithGroup` derivations of the same handler.

#### Fixed

- Custom levels and `slog.LevelVar` are now handled correctly. The handler no
  longer reconstructs the level by probing the four standard levels, which
  mishandled custom or dynamically-changing levels.
- `WithGroup` no longer drops the `addSource` setting.

#### Deprecated

- **`WithForkedOutfile`** leaks the file descriptor it opens and cannot report
  an open error. It no longer panics on a bad path (it logs a warning to stderr
  and leaves the writer unchanged). Use **`WithForkedOutfileCloser`**, which
  returns an `error` and an `io.Closer`.

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

### Security

- **Rate limiter no longer trusts `X-Forwarded-For` by default.**
  `DefaultRateLimitConfig().KeyFunc`, and the fallback used by
  `RateLimit`/`RateLimitWithDefaults` when no `KeyFunc` is set, now key on
  the connecting peer address via `KeyByIP()`. Previously the leftmost
  `X-Forwarded-For` value was trusted unconditionally, so any client could
  bypass the limit (and grow the limiter map without bound) with a spoofed,
  rotating header. `X-Forwarded-For` is still honored when `TrustedProxies`
  is configured.
- **Rate-limiter map is now bounded.** Per-client token buckets that have
  been idle longer than 10 minutes are evicted on insertion of a new key
  (at most once per minute), so the store can no longer grow without limit
  under many distinct or attacker-influenced keys.
- **Forwarded IP parsing hardened.** `IPFromRequest` now skips
  `X-Forwarded-For` entries that are not valid IP addresses instead of
  returning the raw token, preventing attacker-authored strings from
  becoming a client identity in rate-limit keys or logs. `KeyByIP` now
  parses its trusted-proxy list once rather than on every request.
- **Metrics method-label cardinality bounded.** `MetricsRegistry` folds any
  unrecognized HTTP method into a single `other` label, so a client sending
  arbitrary method tokens can no longer grow the counters map or inflate the
  `/metrics` output.
- **CORS: safer credentialed and cache behavior.** With credentials enabled,
  a wildcard origin now reflects the concrete request origin instead of the
  invalid `Access-Control-Allow-Origin: *` pairing, and every reflected
  origin is accompanied by `Vary: Origin` to prevent cross-origin cache
  poisoning.
- **MCP tool calls reject path-injection.** A `tools/call` argument that maps
  to a path parameter is rejected when it contains `/`, and other
  path-significant characters are percent-escaped, so a tool call cannot
  inject extra path segments and reach an endpoint other than the one the
  tool declares.

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
