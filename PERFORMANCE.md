# Performance Roadmap

This document records where surf's request hot path currently spends its time
and the ranked plan for closing the remaining gap to gin and echo. It is a
roadmap for a future release — **v0.1.0 ships with the numbers below as-is.**

## Current state (v0.1.0)

Isolated param-route benchmark (`benchmarks/`, Apple Silicon, Go 1.26):

| Router | ns/op | allocs/op |
|---|---|---|
| `net/http.ServeMux` | 80 | 2 |
| gin | 55 | 1 |
| echo | 57 | 1 |
| chi | 196 | 5 |
| surf — standard `func(w, r)` | 160 | 3 |
| surf — fast path `App.Handle` | ~100 | 2 |

surf's fast path is ~2x faster than chi and in the same tier as stdlib
`ServeMux`. gin and echo remain ~1.8x faster. v0.1.0 reduced the standard path
from 416 ns/14 allocs; this roadmap is about the fast path.

Reproduce:

```sh
cd benchmarks
go test -bench=. -benchmem -run='^$'
go test -bench='BenchmarkParamRoute/surf-fast' -cpuprofile=cpu.prof -run='^$' -benchtime=4s
go tool pprof -top -cum cpu.prof
```

## Profile findings

CPU profile of `BenchmarkParamRoute/surf-fast`, cumulative attribution:

| Cost | % of request | Source |
|---|---|---|
| `time.Now()` | **~25%** | `ResponseWriter.initWriter` sets `startTime` on every request |
| `Context.String` | ~23% | `io.WriteString` `[]byte` conversion (~9%) + `Header().Set` key canonicalization (~3%) + write |
| radix `searchNodeKV` | ~9% | recursive walk with a per-node child-type scan |
| `sync.Pool.Get` | ~2.5% | Context checkout — largely irreducible |

> **Platform caveat.** This profile is from macOS/Apple Silicon, where
> `time.Now()` (`runtime.walltime` + `nanotime1`) and GC `madvise` are more
> expensive than on Linux. Re-profile on the target Linux production
> architecture before committing to the rewrite items — the *ranking* should
> hold, but the absolute percentages will shift.

## Ranked plan

### 1. Drop `time.Now()` from the hot path — ~25%, low effort, low risk

`ResponseWriter.startTime` is set unconditionally in `initWriter`/`NewResponseWriter`
but is only read by `Latency()` / `StartTime()`, which only the template
logging middleware uses. A request that is not being timed pays a `time.Now()`
for nothing.

Plan: stop setting `startTime` in the fast-path `initWriter`. Have the timing
consumers (the `Logging*` middlewares) record their own start time — most
already do (`Logger` calls `time.Now()` itself). Alternatively make `startTime`
lazy: set it on first `Latency()` call, or gate it behind a `WithRequestTiming`
option.

Expected: ~100 ns → ~78 ns. This single change moves the fast path into
stdlib `ServeMux` territory.

### 2. Canonical-key-free header writes — ~3%, low effort, low risk

`c.String` calls `Header().Set("Content-Type", …)`, which runs
`net/textproto.CanonicalMIMEHeaderKey` on a key that is already canonical.

Plan: write known response headers (`Content-Type`, `Content-Length`) directly
into the header map under their pre-canonicalized keys, or cache a canonical
`textproto.MIMEHeader` key constant.

### 3. Allocation-free string responses — ~9%, medium effort

`Context.String` → `ResponseWriter.WriteString` → `io.WriteString`. When the
underlying writer does not implement `io.StringWriter` (the common case for
`http.ResponseWriter`), `io.WriteString` falls back to `w.Write([]byte(s))` —
one allocation per response.

Plan: investigate writing strings without the `[]byte` conversion (e.g. an
internal unsafe string→[]byte view for the write call, used only for the
duration of `Write`). This is the one item with real subtlety; measure
carefully and keep it behind the existing `WriteString` method.

### 4. Iterative radix lookup — ~9% → ~4-5%, medium effort, medium risk

`searchNodeKV` recurses and, at each node, scans children three times (static,
param, wildcard). Routers like httprouter keep child kinds in separate fields
and walk iteratively with an explicit backtrack stack.

Plan: rework `radixNode` so static children, the single param child, and the
wildcard child are distinct fields; convert `searchNodeKV` to an iterative walk
with a small fixed-size backtracking stack. This touches the routing core —
gate it behind the full `radix_test.go` suite plus new property tests.

### 5. `sync.Pool` overhead — ~2.5%, not worth pursuing

Largely irreducible and already cheap. Leave it.

## Projected outcome

Items 1 + 2 + 4 together (~37% on this machine) would bring the fast path from
~100 ns to roughly ~65-75 ns — competitive with gin and echo. Item 3 removes
the last allocation for string responses. None of these require an API change;
they are all internal to the fast path and the router.

The work should land in a dedicated `v0.1.1` performance release, each item as
its own reviewable commit, re-profiled on Linux first.
