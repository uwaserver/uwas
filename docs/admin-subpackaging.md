# Admin Sub-Packaging Migration Guide

> **Status:** Phase 3 architecture work — 3 of ~10 domain areas extracted.
> **Pattern:** Established and validated with `database`, `php`, and `cloudflare`.

This document explains the pattern for extracting handler groups from the
`internal/admin` monolith (97 files, 251 routes at the time) into focused sub-packages
under `internal/admin/<area>/`. It is intended for contributors who want to
extract a new sub-package or understand the existing ones.

---

## Why Sub-Package?

The admin package is a single `package admin` with 47 source files and 52 test
files. Every handler is a method on `*Server`, which creates problems:

- **Merge conflicts** — 3+ developers touching different features edit the same package
- **God object** — `api.go` was 1555 LOC holding auth, CSRF, config, SSE, and system info
- **Untyped responses** — 738 occurrences of `map[string]any` with no compile-time contract
- **Test coupling** — 20+ test files directly call `s.handleDB*`, `s.handlePHP*`, etc.

The sub-packaging effort addresses all four by moving handler logic into
separate packages while keeping the `*Server` as the wiring point.

---

## Completed Sub-Packages

| Package | LOC | Routes | Extracted from |
|---------|-----|--------|----------------|
| `internal/admin/authmw` | 326 | — (middleware) | `api.go` authMiddleware |
| `internal/admin/database` | 807 | 34 | `handlers_database.go` |
| `internal/admin/php` | 554 | 21 | `handlers_php.go` |
| `internal/admin/cloudflare` | 848 | 16 | `handlers_cloudflare*.go` (4 files) |

### Remaining candidates (in priority order)

1. `admin/domain` — `handlers_domain.go` (1480 LOC, largest handler file)
2. `admin/apps` — `handlers_apps*.go` (~700 LOC across 5 files)
3. `admin/settings` — `handlers_settings.go` (683 LOC)
4. `admin/backup` — `handlers_backup.go`
5. `admin/files` — `handlers_files.go`
6. `admin/wordpress` — `handlers_wordpress.go`

---

## The Pattern

Every sub-package follows the same 5-part structure. Below is the template
using `admin/widgets` as an example.

### 1. Define a `Deps` interface

The sub-package never imports `admin`. Instead, it declares an interface with
every `Server` method/field it needs. The admin package implements this
interface via an adapter struct.

```go
// internal/admin/widgets/handler.go
package widgets

type Deps interface {
    // Auth
    RequireAdmin(w http.ResponseWriter, r *http.Request) bool
    RequirePin(w http.ResponseWriter, r *http.Request) bool
    // Logging
    LogInfo(msg string, args ...any)
    LogError(msg string, args ...any)
    // Audit
    RecordAudit(r *http.Request, action, detail string, success bool)
    // Pagination
    ParsePagination(r *http.Request) (limit, offset int)
    // Any subsystem-specific deps
    WidgetStore() *WidgetStore
}
```

**Key rules for the Deps interface:**

- **Read state at call time**, not at construction time. If the sub-package
  needs the current `phpMgr`, expose `PHPManager() *phpmanager.Manager` on Deps
  and call it in each handler — don't cache it in the Handler struct. This
  makes test overrides (`s.phpMgr = ...`) work without re-initialization.
- **Pass external resources via Deps methods**, not as constructor arguments.
  This keeps `New(deps)` simple and ensures the sub-package always sees the
  current state.
- **CF API / HTTP operations** go through Deps so the admin's test-mockable
  `cfHTTPClient` is used. Export `*WithClient` variants of API helpers.

### 2. Define a `Handler` struct

```go
type Handler struct {
    deps Deps
}

func New(deps Deps) *Handler {
    return &Handler{deps: deps}
}
```

The Handler holds only the Deps interface — no subsystem pointers, no config,
no mutexes. All state flows through Deps.

### 3. Move handler methods onto `*Handler`

Every `func (s *Server) handleWidgetList(...)` becomes
`func (h *Handler) List(...)`. Replace `s.requireAdmin` → `h.deps.RequireAdmin`,
`s.logger.Info` → `h.deps.LogInfo`, etc.

Include local helpers in the sub-package:
```go
func jsonResponse(w http.ResponseWriter, data any) { ... }
func jsonError(w http.ResponseWriter, msg string, code int) { ... }
func paginateSlice[T any](items []T, limit, offset int) ([]T, int) { ... }
```

### 4. Create the adapter in the admin package

```go
// internal/admin/handlers_widgets.go
package admin

type widgetDeps struct {
    s *Server
}

func (d *widgetDeps) RequireAdmin(w http.ResponseWriter, r *http.Request) bool {
    return d.s.requireAdmin(w, r)
}
func (d *widgetDeps) LogInfo(msg string, args ...any) { d.s.logger.Info(msg, args...) }
// ... implement every Deps method

// Compile-time check
var _ widgets.Deps = (*widgetDeps)(nil)
```

### 5. Add per-Server field + thin wrappers

**Critical:** The handler instance must be a `Server` struct field, **not** a
package-level global. Globals break test isolation because `testServer()`
creates multiple Server instances that would share one handler.

```go
// In api.go Server struct:
type Server struct {
    // ...
    widgetHandler *widgets.Handler
}

// In handlers_widgets.go:
func (s *Server) initWidgetHandler() {
    s.widgetHandler = widgets.New(&widgetDeps{s: s})
}

// Thin wrappers for test compat (tests call s.handleWidgetList directly):
func (s *Server) handleWidgetList(w http.ResponseWriter, r *http.Request) {
    s.widgetHandler.List(w, r)
}
```

Wire the init call in `New()`:
```go
// api.go New()
s.initDBHandler()
s.initCloudflareHandler()
s.initPHPHandler()
s.initWidgetHandler() // ← add here
```

**For handlers that depend on a subsystem being set later** (like PHP's
`SetPHPManager`), initialize the handler in `New()` with nil — the sub-package
reads the manager via Deps at call time, so it will see the updated manager
once `SetPHPManager` is called.

### 6. Test seams

Test seams (package-level vars that tests override) stay in the admin package.
The adapter reads them dynamically:

```go
// admin package
var widgetRunInstall = widgets.RunInstall

func (d *widgetDeps) RunInstall() (string, error) {
    return widgetRunInstall() // test overrides this var
}
```

### 7. Routes stay unchanged

`routes.go` still calls `s.handleWidgetList`. The thin wrappers make this work.
No route registration changes needed.

---

## Checklist for a New Extraction

- [ ] Read the source `handlers_<area>.go` and catalog every `s.*` call
- [ ] Create `internal/admin/<area>/handler.go` with Deps interface + Handler struct
- [ ] Move all handler methods, replacing `s.*` with `h.deps.*`
- [ ] Add local `jsonResponse`/`jsonError`/`paginateSlice` helpers
- [ ] Create `handlers_<area>.go` adapter with `*Deps` struct
- [ ] Add `*Handler` field to `Server` struct in `api.go`
- [ ] Add `init<Area>Handler()` method, call it in `New()`
- [ ] Replace all handler method bodies with thin wrappers
- [ ] Keep test seams as package-level vars, read dynamically by adapter
- [ ] Add `var _ <area>.Deps = (*<area>Deps)(nil)` compile-time check
- [ ] Run `go build ./internal/admin/...` — must compile clean
- [ ] Run `go test ./internal/admin/` — **0 failures**

---

## Common Pitfalls

### 1. Package-level handler globals

**Wrong:**
```go
var widgetHandler *widgets.Handler // shared across all test Servers!
```

**Right:**
```go
type Server struct {
    widgetHandler *widgets.Handler // per-Server instance
}
```

### 2. Caching subsystem pointers in Handler

**Wrong:**
```go
type Handler struct {
    deps Deps
    mgr  *phpmanager.Manager // stale if test sets s.phpMgr after New()
}
```

**Right:**
```go
type Handler struct {
    deps Deps // read mgr via deps.PHPManager() at call time
}
```

### 3. Sub-package using admin's HTTP client

Tests mock the admin's `cfHTTPClient`. If the sub-package has its own client,
test overrides don't take effect.

**Fix:** Export `*WithClient` variants and pass the client through Deps:
```go
// sub-package
func FetchZonesWithClient(client *http.Client, token string) ([]Zone, error) { ... }

// adapter
func (d *cfDeps) FetchZones(token string) ([]cfadmin.Zone, error) {
    return cfadmin.FetchZonesWithClient(cfHTTPClient, token) // admin's test-mockable client
}
```

### 4. State round-trip for persisted config

If the sub-package mutates state (like Cloudflare tunnels), it must pass the
full state back to the adapter for persistence:

**Wrong:**
```go
SaveCloudflareState() error // adapter doesn't know what changed
```

**Right:**
```go
SaveCloudflareState(st *State) error // adapter converts + persists
```

---

## File Organization After Extraction

```
internal/admin/
├── api.go                 # Server struct, New(), core helpers (~1300 LOC)
├── routes.go              # Route registration (unchanged)
├── responses.go           # Typed response structs
├── authmw/                # Auth middleware sub-package
├── database/              # Database handler sub-package
├── php/                   # PHP handler sub-package
├── cloudflare/            # Cloudflare handler sub-package
├── domain/                # Domain handler sub-package
├── apps/                  # Apps CRUD + lifecycle sub-package
├── settings/              # Settings sub-package
├── backup/                # Backup sub-package
├── files/                 # File manager + cron sub-package
├── wordpress/             # WordPress sub-package
├── handlers_database.go   # Adapter (dbDeps + thin wrappers)
├── handlers_php.go        # Adapter (phpDeps + thin wrappers)
├── handlers_cloudflare.go # Adapter (cfDeps + thin wrappers + state types)
├── handlers_domain.go     # Adapter (domainDeps + thin wrappers)
├── handlers_apps.go       # Not yet extracted
└── ...                    # Other handlers + tests
```

Each extracted area produces:
- **1 new sub-package file** (`internal/admin/<area>/handler.go`) — the real logic
- **1 modified adapter file** (`handlers_<area>.go`) — shrinks from full handlers to adapter + thin wrappers

The adapter file should be <300 LOC after extraction. The thin wrappers can be
removed entirely once test files are migrated to call the sub-package directly.

---

## Completed Extractions

| Sub-package | Handler LOC | Routes | Notes |
|-------------|-------------|--------|-------|
| `admin/authmw` | 326 | (middleware) | Function-closure Deps to avoid circular import |
| `admin/database` | 807 | 34 | Service hooks read at call time for test isolation |
| `admin/php` | 554 | 21 | PHPManager read via Deps (not cached at construction) |
| `admin/cloudflare` | 871 | 16 | CF API helpers with injectable HTTP client |
| `admin/domain` | 780 | 15 | Requires `internal/domainutil/` prerequisite (25 pure helpers) |
| `admin/apps` | 700 | 10 | CRUD + lifecycle extracted; deploy/git/webhook deferred to `internal/deploy/` |
| `admin/settings` | 630 | 12 | Config get/put, branding, notifications, recovery codes, raw YAML editor |
| `admin/backup` | 338 | 7 | List, create, domain backup, restore, delete, schedule get/put |
| `admin/files` | 610 | 11 | File manager + cron: workspaces, list, read, write, delete, mkdir, upload, disk usage |
| `admin/wordpress` | 400 | 16 | Install, detect, update, security, harden, optimize DB; install state on Handler struct |
| `admin/deploy` | 1452 | 4 | Git clone/pull, build, rollback, webhook + deploy-key handling |

### Special case: admin/domain

`handlers_domain.go` (1481 LOC) was the largest and most coupled handler file.
Its 25 pure hostname helper functions were used by 7+ other admin files,
making a direct extraction impossible (circular import). The solution was a
two-step migration:

1. **`internal/domainutil/`** (403 LOC) — pure helpers with zero admin dependency
2. **`internal/admin/domain/`** (780 LOC) — handler methods importing `domainutil`

The domain sub-package also required porting the full alias canonicalization
flow (`parseAliasOptions`, `applyDomainCanonicalPreference`, same-site www
detection, redirect alias conflict resolution) — ~200 lines of complex logic
from `domain_alias.go`.

## Remaining Candidates

The deploy pipeline has since been extracted to `internal/admin/deploy`. What
stays in the parent package is the delegating layer:

| File | LOC | Status |
|------|-----|--------|
| `handlers_apps_deploy.go` + `handlers_apps_git.go` + `handlers_apps_webhook.go` + `handlers_apps_keys.go` | 442 | Thin wrappers over `admin/deploy`; they hold the `deployDeps` adapter and the route entry points |

Every admin handler file now either lives in a sub-package or delegates to one.
The wrappers are deliberate: they keep the `Deps` adapter and route
registration next to the rest of the parent's server state, which is what the
sub-packages are built to avoid importing.
