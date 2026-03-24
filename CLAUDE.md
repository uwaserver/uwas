# UWAS — Development Guide

## Project

UWAS (Unified Web Application Server) is a single-binary Go web server + hosting control panel. It replaces Apache + Nginx + Varnish + Caddy + cPanel. Features: auto HTTPS, caching, PHP/FastCGI, .htaccess, reverse proxy, WAF, and a 28-page React dashboard with 114 API endpoints.

## Build

```bash
make build          # Production binary (stripped, versioned)
make dev            # Development binary
make test           # Run all tests
make lint           # go vet + staticcheck

# Cross-compile for Linux
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bin/uwas-linux-amd64 ./cmd/uwas

# Dashboard rebuild (auto-embedded via go:embed)
cd web/dashboard && npm run build
```

## Architecture

```
cmd/uwas/            CLI entry point (16 commands)
internal/
  admin/             API server (114 routes) + dashboard embed + TOTP auth
  alerting/          Alert thresholds + notifications
  analytics/         Per-domain traffic analytics
  backup/            Local/S3/SFTP backup + restore
  cache/             L1 memory (256-shard LRU) → L2 disk
  cli/               CLI commands (serve, stop, cert, php, user, domain...)
  config/            Config structs + Duration/ByteSize types + MarshalYAML
  cronjob/           Cron job management (per-domain)
  database/          MySQL/MariaDB management (create DB/user, install)
  dnschecker/        DNS record verification (A/MX/NS/TXT)
  dnsmanager/        Cloudflare DNS record CRUD + sync
  filemanager/       Web file manager (browse/edit/upload/delete)
  firewall/          UFW management via API
  handler/
    fastcgi/         PHP-CGI/FPM handler
    proxy/           Reverse proxy + load balancing + circuit breaker
    static/          Static file serving + try_files + directory listing
  logger/            log/slog wrapper
  mcp/               MCP interface for AI management
  metrics/           Request metrics + latency percentiles
  middleware/        Chain composition, WAF, bot guard, rate limit, CORS, compression
  migrate/           Apache/Nginx config migration
  monitor/           Health monitoring + domain health checks
  notify/            Webhook, Slack, Telegram, Email channels
  phpmanager/        PHP detect, install, start/stop, per-domain assign, config
  rewrite/           Apache mod_rewrite compatible engine
  router/            VHost routing + unknown host tracking
  selfupdate/        Binary self-update from GitHub releases
  server/            Main HTTP/HTTPS/HTTP3 server + request dispatch
  serverip/          Server IP detection (interfaces + public IP)
  services/          systemd service management (start/stop/restart)
  siteuser/          SFTP user management (chroot jail + SSH keys)
  tls/               SNI cert selection + ACME auto-issuance + retry
  wordpress/         One-click WordPress install (DB + config + permissions)
pkg/
  fastcgi/           FastCGI protocol implementation
  htaccess/          .htaccess parser
web/dashboard/       React SPA (28 pages, Vite + TypeScript + Tailwind)
```

## Stats (v1.3.8)

- 43 Go packages, 39 with tests (all passing)
- 28 dashboard pages, 114 API endpoints
- 16 CLI commands
- ~14MB single binary (linux/amd64)

## Conventions

- **Go 1.26+** required
- **stdlib-first** — 4 direct dependencies (`gopkg.in/yaml.v3`, `brotli`, `quic-go`, `x/crypto`)
- No web frameworks, no ORMs, no logging frameworks
- `internal/logger/` wraps `log/slog` — use it everywhere
- Config structs in `internal/config/config.go` — add new fields there
- Tests alongside source: `foo.go` → `foo_test.go`
- Run `go vet ./...` before committing
- Dashboard: TypeScript strict, `npx tsc --noEmit` must pass

## Key Patterns

- **VHost routing**: `internal/router/vhost.go` — exact → alias → wildcard → fallback
- **Unknown domains**: rejected before middleware chain (421 or 403 if blocked)
- **Global middleware**: `internal/middleware/chain.go` — `Chain(A, B, C)(handler)` composition
- **Per-domain middleware**: WAF, IPACL, BasicAuth, CORS, HeaderTransform, BotGuard — applied in `handleRequest()` per domain config
- **Handlers**: static, fastcgi, proxy, redirect — dispatched by `domain.Type`
- **Cache**: L1 memory (256-shard LRU) → L2 disk, checked before handler dispatch
- **TLS**: `internal/tls/manager.go` — SNI-based cert selection, ACME auto-issuance with retry
- **Rewrite**: `internal/rewrite/engine.go` — Apache mod_rewrite compatible
- **Config persist**: Domain CRUD writes to YAML file atomically via `persistConfig()`
- **Settings API**: `GET/PUT /api/v1/settings` — structured key-value, no YAML parsing in frontend
- **2FA**: TOTP via `X-TOTP-Code` header, setup/verify/disable via `/api/v1/auth/2fa/*`
- **PHP lifecycle**: Auto-detect → install → auto-start on boot → auto-assign on domain add

## Testing

```bash
go test ./...                        # All tests (39 packages)
go test ./internal/cache/            # Single package
go test -v -run TestWordPress ./...  # Specific test
```

## Common Tasks

- **Add a config field**: Edit `internal/config/config.go`, add to Settings API in `api.go:handleSettingsGet/Put`
- **Add global middleware**: Create file in `internal/middleware/`, add to chain in `server.go:buildMiddlewareChain()`
- **Add per-domain middleware**: Add config field in `config.go`, wire in `server.go:handleRequest()` after domain lookup
- **Add admin endpoint**: Register in `internal/admin/api.go:registerRoutes()`, add handler method
- **Add MCP tool**: Register in `internal/mcp/server.go:registerTools()`
- **Add CLI command**: Create file in `internal/cli/`, register in `cmd/uwas/main.go`
- **Add dashboard page**: Create in `web/dashboard/src/pages/`, add route in `App.tsx`, add to sidebar group in `Sidebar.tsx`
- **Add API function**: Add to `web/dashboard/src/lib/api.ts` with proper TypeScript interface

## Dashboard Pages (28)

Sites: Domains, Topology, Certificates, DNS, WordPress, File Manager
Server: PHP, PHP Config, Database, SFTP Users, Cron Jobs, Services, IP Management, Email Guide
Performance: Cache, Metrics, Analytics, Logs
Security: Security, Firewall, Unknown Domains, Audit Log
System: Config Editor, Backups, Updates, Settings
Auth: Login (with 2FA/TOTP support)
Overview: Dashboard (stats, health, graphs)
