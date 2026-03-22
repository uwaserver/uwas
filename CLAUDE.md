# UWAS — Development Guide

## Project

UWAS (Unified Web Application Server) is a single-binary Go web server that replaces Apache + Nginx + Varnish + Caddy. It provides auto HTTPS, built-in caching, PHP/FastCGI support, .htaccess compatibility, reverse proxy with load balancing, and an MCP interface for AI management.

## Build

```bash
make build          # Production binary (stripped, versioned)
make dev            # Development binary
make test           # Run all tests
make lint           # go vet + staticcheck
```

## Architecture

- `cmd/uwas/` — CLI entry point
- `internal/` — Private packages (not importable externally)
- `pkg/` — Public reusable packages (fastcgi protocol, htaccess parser)
- `docs/` — Design documents (specification, implementation guide, tasks, branding)

## Conventions

- **Go 1.23+** required
- **stdlib-first** — only 1 external dependency (`gopkg.in/yaml.v3`)
- No web frameworks, no ORMs, no logging frameworks
- `internal/logger/` wraps `log/slog` — use it everywhere
- Config structs in `internal/config/config.go` — add new fields there
- Tests alongside source: `foo.go` → `foo_test.go`
- Run `go vet ./...` before committing

## Key Patterns

- **VHost routing**: `internal/router/vhost.go` — exact → alias → wildcard → fallback
- **Middleware**: `internal/middleware/chain.go` — `Chain(A, B, C)(handler)` composition
- **Handlers**: static, fastcgi, proxy, redirect — dispatched by `domain.Type`
- **Cache**: L1 memory (256-shard LRU) → L2 disk, checked before handler dispatch
- **TLS**: `internal/tls/manager.go` — SNI-based cert selection, ACME auto-issuance
- **Rewrite**: `internal/rewrite/engine.go` — Apache mod_rewrite compatible

## Testing

```bash
go test ./...                        # All tests
go test ./internal/cache/            # Single package
go test -v -run TestWordPress ./...  # Specific test
```

1,728 tests across 27 packages. No race detector on Windows (CGO_ENABLED=0).
Use `make test-coverage` for coverage report.

## Common Tasks

- **Add a config field**: Edit `internal/config/config.go`, add defaults in `defaults.go`, validate in `validate.go`
- **Add middleware**: Create file in `internal/middleware/`, add to chain in `server.go:buildMiddlewareChain()`
- **Add admin endpoint**: Register in `internal/admin/api.go:registerRoutes()`
- **Add MCP tool**: Register in `internal/mcp/server.go:registerTools()`
- **Add backup provider**: Implement `StorageProvider` interface in `internal/backup/`
- **Add CLI command**: Create file in `internal/cli/`, register in `cmd/uwas/main.go`
- **Add dashboard page**: Create in `web/dashboard/src/pages/`, add route in `App.tsx`, link in `Sidebar.tsx`
