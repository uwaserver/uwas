# Performance Backlog

Items measured and analyzed during the 2026-05-01 Performance Surgeon session
that were **not implemented**. Each entry has measurement evidence, the reason
for parking, and a concrete entry point for future work.

Hardware reference: AMD Ryzen 9 9950X3D, Windows 11, Go 1.26.1.
All numbers from `go test -bench=. ./test/bench/`.

---

## 1. Target-side `EvalSymlinks` in `pathsafe.Base.Contains`

**Cost:** ~45% of `BenchmarkStaticFileServe` allocations (per pprof
`-alloc_objects`). Translates to ~50 of the 119 allocs/op on the static path.

**Where:** `internal/pathsafe/pathsafe.go:30-39` (`Contains` →
`resolvePath(target)` → `filepath.EvalSymlinks`). Called from
`internal/handler/static/handler.go:257` for every candidate in `ResolveRequest`.

**Why it's allocation-heavy on Windows:** `EvalSymlinks` walks each path
component with an Lstat. Each Lstat goes through `syscall.UTF16FromString` /
`syscall.UTF16ToString` plus `path/filepath.toNorm` for normalization — each a
heap alloc. A 5-component path costs ~15-20 allocs.

**Options considered:**

### 1a. Migrate to `os.Root` (Go 1.24+)
Tried in this session and reverted. `os.OpenRoot(docRoot)` returns a directory
handle that performs containment-checked `Stat` / `Open` in a single syscall,
eliminating the EvalSymlinks walk entirely.

**Blocker:** Windows holds a directory handle as long as `*os.Root` is alive.
- **Test impact:** `t.TempDir()` cleanup fails with
  `"The process cannot access the file because it is being used by another process"`
  for ~16 tests in `internal/handler/static`.
- **Production impact (more important):** When a domain is deleted in UWAS, the
  docroot may be removed. With cached `*os.Root`, the rmdir would fail on
  Windows. Fixing requires wiring `pathsafe.InvalidateBase(domain.Root)` into
  every domain-mutation path: admin API delete, migration rollback, deploy
  abort, clone failure, etc. Missing one site = orphan directory handle.

**Reopen path if accepted:**
1. Add `pathsafe.InvalidateBase` call to every domain-delete site.
   Search starting points:
   - `internal/admin/api.go` — domain CRUD endpoints
   - `internal/admin/handlers_*.go`
   - `internal/migrate/` — migration rollback
   - `internal/deploy/` — deploy failure cleanup
2. Use the `os.Root`-backed `Base` (work-in-progress code lives in this
   session's git history under hash inside the revert; can be replayed).
3. The Windows path normalization issue (`filepath.Clean("/"+x)` producing UNC
   `\\x`) was solved with `path.Clean` (URL-style); see commit message of the
   reverted attempt for the fix.

**Projected gain:** −30 allocs/op on static path, ns/op estimated −20%
(~165µs → ~130µs).

### 1b. Resolved-path LRU cache
Map target path → resolved path with bounded size + TTL eviction.
- **Cost:** new infrastructure, eviction policy, TTL choice.
- **Risk:** stale entries during TTL window if a symlink is added to docroot.
- **Why parked:** disproportionate to the gain; no measurement supports
  it being clearly better than 1a.

### 1c. Trust-no-symlinks fast path
Walk docroot once at `NewBase` time. If no symlinks present, skip target-side
EvalSymlinks (lexical containment is then sufficient).
- **Risk:** symlink added at runtime opens an escape window until next rescan.
- **Why parked:** security weakening without explicit user opt-in; the project
  hosts WordPress sites that may legitimately use symlinks.

---

## 2. `RequestID` middleware — dynamic value `[]string{id}` allocation

**Cost:** 1 alloc/req (12.5% of the 4 remaining hoisted middleware allocs after
SecurityHeaders fix).

**Where:** `internal/middleware/requestid.go:21` (`w.Header().Set(requestIDHeader, id)`).

**Why it allocates:** `MIMEHeader.Set` does `[]string{value}` per call. Unlike
SecurityHeaders, the value is unique per request (UUIDv7), so we can't share
a pre-built slice the way `internal/middleware/headers.go:11-15` does for
constants.

**Reopen path:**
- `sync.Pool` of `[]string{""}` slices, reused per request. After response is
  written, return the slice to the pool.
- Lifecycle gotcha: stdlib `net/http` reads the header map during response
  write. The slice must stay valid until that completes. Returning to pool in
  `ReleaseContext` (which is `defer router.ReleaseContext(ctx)`) is safe — by
  that point the response is fully flushed.
- Implementation: extend `internal/router/context.go:ReleaseContext` to walk
  `Response.Header()` and return any pool-managed value slices. That coupling
  is the blast radius — `internal/router/` would need to know about middleware
  internals.

**Projected gain:** −1 alloc/req on every middleware-chain pass. Marginal.

**Why parked:** the cross-package coupling between `router/` and `middleware/`
is the main cost. Worth considering only if `MiddlewareChain` becomes a
bottleneck under e2e load tests (see §5).

---

## 3. `router.generateID` string allocation

**Cost:** 1 alloc/req in `BenchmarkContextAcquireRelease` (33% of the
post-pool 2-alloc baseline).

**Where:** `internal/router/context.go:64-91` (`generateID` returns
`string(sb[:])`).

**Why it allocates:** UUIDv7-format ID is a 36-byte string. The conversion
from `[36]byte` to `string` always heap-allocates the underlying bytes.

**Reopen path:**
- Store ID as `[36]byte` on `RequestContext`, expose as `string` via a method
  that does the conversion lazily.
- Or expose as `String()` returning the same allocated string but cache it on
  the context.
- All consumers of `ctx.ID` (search: `grep -rn 'ctx\.ID' internal/`) must move
  from field access to method call. ~24 callsites.

**Projected gain:** −1 alloc/req in the request hot path.

**Why parked:** field-to-method migration is mechanical but touches many
files; risk/reward unfavorable for 1 alloc.

---

## 4. `r.URL.RequestURI()` allocation in `AcquireContext`

**Cost:** 1 alloc/req (33% of post-pool ContextAcquireRelease baseline) — the
last alloc in that bench.

**Where:** `internal/router/context.go:59`.

**Why it allocates:** stdlib `(*url.URL).RequestURI` builds the path+query
string from URL fields, always heap-allocated.

**Reopen path:**
- Lazy: compute on first `ctx.OriginalURI` access via a method.
  `internal/server/server.go:1541-1542` already has a fallback pattern for
  this case.
- Audit all consumers (24 callsites per session-time grep) and convert to a
  method.

**Projected gain:** −1 alloc/req. Same trade-off as §3.

**Why parked:** Same as §3 — wide blast radius for a single alloc. Combine
with §3 as a single "lazy field" refactor if pursued.

---

## 5. Server-level end-to-end benchmark infrastructure

**Why we need it:** The current `test/bench/bench_test.go` measures isolated
hot paths. Real production performance depends on:
- TLS termination overhead
- Full middleware chain (not just 3 mw)
- Cache lookup vs. cache miss paths
- Connection reuse / Keep-Alive
- HTTP/2 vs HTTP/1.1 path differences

**Current gap:** No bench exists that exercises a real `*server.Server` with a
realistic config. We can't tell if the micro-bench wins translate to e2e ms/req.

**Reopen path:**
1. New file `test/bench/server_bench_test.go`.
2. Spin up a real `server.Server` listening on localhost (no TLS for bench
   simplicity; HTTPS variant separate).
3. Configure 1-3 domains: static, php (mocked FastCGI), proxy (mocked
   upstream).
4. Drive with `b.RunParallel` issuing real HTTP/1.1 requests via a
   `*http.Client` with KeepAlive enabled.
5. Measure: requests/sec, p50/p95/p99 latency, allocs/req, B/req via
   `runtime.ReadMemStats` deltas.

**Estimated effort:** 1-2 days. Pays off as a regression detector for every
future perf claim.

---

## 6. Static-serve `http.ServeContent` allocations

**Cost:** ~6% of `BenchmarkStaticFileServe` allocations (per pprof). Roughly 7
allocs/req in the stdlib `net/http.serveContent` path: `MIMEHeader.Set` for
Content-Length / Last-Modified / Accept-Ranges, `time.Time.Format` for
Last-Modified, etc.

**Where:** `internal/handler/static/handler.go:73, 113` (`http.ServeContent`).

**Why parked:** stdlib internals. The only way to avoid is to write our own
Range / If-None-Match / If-Modified-Since handling, which duplicates ~200
lines of stdlib carefully. Maintenance cost outweighs ~6% allocation savings.

**Reopen criterion:** if the static handler ever shows up as the dominant
cost in a real e2e bench (§5), revisit.

---

## Reproduction commands

```bash
# Run all benches
go test -bench=. -benchmem -benchtime=3s -count=3 ./test/bench/

# Single bench with allocation profile
go test -bench=BenchmarkStaticFileServe -benchmem -benchtime=3s \
        -run=^$ -memprofile=mem.prof ./test/bench/
go tool pprof -text -alloc_objects mem.prof

# Hoisted micro-benchmarks (httptest harness backed out)
go test -bench=Hoisted -benchmem -benchtime=3s ./test/bench/
```

## What's been shipped

See `git log --oneline | grep -E '^[a-f0-9]+ (perf|test\(bench\))'` from
2026-05-01. Six commits, cumulative gains:
- `BenchmarkStaticFileServe`: 270µs → 165µs (−39%), 186 → 119 allocs (−36%)
- `BenchmarkContextAcquireRelease`: 81ns → 62ns (−23%), 3 → 2 allocs
- `BenchmarkMiddlewareChainHoisted`: 109ns → 86ns (−21%), 8 → 4 allocs (−50%)
- `BenchmarkCacheKeyGenerateHoisted`: 106ns → 92ns (−13%), 2 → 1 alloc
