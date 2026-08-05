# Performance Roadmap

This document records where surf's request hot path spends its time and the
ranked plan for closing the remaining gap to gin and echo. Items 1–4 of the
original plan have landed; what remains is item 5 (deliberately skipped) and
the open questions at the bottom.

## Current state (unreleased)

Isolated param-route benchmark (`benchmarks/`, Apple M4, Go 1.25):

| Router | ns/op | B/op | allocs/op |
|---|---|---|---|
| gin | 52 | 48 | 1 |
| echo | 55 | 8 | 1 |
| **surf — fast path `App.Handle`** | **58** | **16** | **1** |
| `net/http.ServeMux` | 75 | 24 | 2 |
| surf — standard `func(w, r)` | 154 | 744 | 3 |
| chi | 187 | 712 | 5 |

The fast path is now level with echo and within ~10% of gin, ~1.3x faster than
stdlib `ServeMux`, and ~3x faster than chi. The static-route benchmark tells
the same story (surf-fast 46 ns, gin 45 ns, echo 47 ns).

For reference, the fast path started at ~100 ns / 2 allocs when this roadmap
was written, and the standard path started at 416 ns / 14 allocs in v0.1.0.

Reproduce:

```sh
cd benchmarks
go test -bench=. -benchmem -run='^$'
go test -bench='BenchmarkParamRoute/surf-fast' -cpuprofile=cpu.prof -run='^$' -benchtime=4s
go tool pprof -top -cum cpu.prof
```

## Original profile findings

CPU profile of `BenchmarkParamRoute/surf-fast` when the roadmap was written,
cumulative attribution:

| Cost | % of request | Source |
|---|---|---|
| `time.Now()` | **~25%** | `ResponseWriter.initWriter` sets `startTime` on every request |
| `Context.String` | ~23% | `io.WriteString` `[]byte` conversion (~9%) + `Header().Set` key canonicalization (~3%) + write |
| radix `searchNodeKV` | ~9% | recursive walk with a per-node child-type scan |
| `sync.Pool.Get` | ~2.5% | Context checkout — largely irreducible |

> **Platform caveat.** These profiles are from macOS/Apple Silicon, where
> `time.Now()` (`runtime.walltime` + `nanotime1`) and GC `madvise` are more
> expensive than on Linux. Re-profile on the target Linux production
> architecture before committing to further rewrites — the *ranking* should
> hold, but the absolute percentages will shift.

## Ranked plan

### 1. Drop `time.Now()` from the hot path — ~25% — **done**

`ResponseWriter.StartTime` is no longer set by the framework. It is exported
and left as the zero value; the timing consumers (the `Logging*` middlewares)
record their own start time, and `Latency()` returns 0 when `StartTime` was
never set. A request that is not being timed pays nothing.

### 2. Canonical-key-free header writes — ~3% — **done**

`setKnownHeader` (`headers.go`) writes known response headers straight into
the header map under their pre-canonicalized keys, skipping
`net/textproto.CanonicalMIMEHeaderKey` on keys that are already canonical.

### 3. Allocation-free string responses — ~9% — **done**

`ResponseWriter.WriteString` previously went through `io.WriteString`, which
falls back to `w.Write([]byte(s))` — one allocation per response — whenever
the underlying writer does not implement `io.StringWriter`. It now delegates
to the writer's own `WriteString` when there is one (net/http's `response` has
one) and otherwise hands `Write` a borrowed byte view of the string.

The view is safe because `io.Writer` already requires implementations not to
modify or retain the slice they are passed; a writer that violates that
contract would corrupt the caller's string. That constraint is documented on
both `WriteString` and the internal `stringView` helper.

### 4. Iterative radix lookup — ~9% — **done**

`searchNodeKV` walks the tree with an explicit backtracking stack (8 frames
inline, spilling to the heap only for pathologically deep trees) instead of
recursing. Two things keep it cheap:

- `radixNode` already splits children into `staticChildren`, `paramChild`, and
  `wildcardChild`, so no per-node type filtering is needed.
- `insert` splits static children on their longest common prefix, so a node's
  static children never share a leading byte and **at most one** can
  prefix-match a path. Static descent is therefore deterministic, and a
  backtracking frame is pushed only where a param or wildcard alternative
  genuinely remains. A static route, or `/users/:id`, pushes nothing.

`radix_property_test.go` guards the rewrite: it checks the iterative walk
against the previous recursive implementation (kept as an oracle) over
hundreds of randomly generated trees and paths, and separately asserts the
disjoint-static-siblings invariant the walk depends on.

### 5. `sync.Pool` overhead — ~2.5%, not worth pursuing

Largely irreducible and already cheap. Leave it.

## What's left

The fast path is down to a single allocation per request: the one-element
`[]string` that `setKnownHeader` stores in the header map for `Content-Type`.
Removing it would mean caching a pre-built `[]string` per known value, which
is only safe if nothing ever mutates the response header map's slices in
place — worth measuring before attempting, and worth roughly 16 B/op.

A re-profile after items 1–4 shows the remaining request time split between
`Context.String` (~15%), the radix lookup (~11%), and `setKnownHeader` (~11%),
with GC (`madvise`/`kevent`) dominating the rest of the samples on macOS. That
GC share is the platform caveat above: re-profile on Linux before ranking any
further work.
