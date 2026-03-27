# UWAS — Development Guide

## Project

UWAS (Unified Web Application Server) is a single-binary Go web server + hosting control panel. It replaces Apache + Nginx + Varnish + Caddy + cPanel. Features: auto HTTPS, caching, PHP/FastCGI, .htaccess, reverse proxy, WAF, and a 35-page React dashboard with 170+ API endpoints.

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
cmd/uwas/            CLI entry point (18 commands)
internal/
  admin/             API server (185+ routes) + dashboard embed + TOTP auth
  alerting/          Alert thresholds + notifications
  analytics/         Per-domain traffic analytics
  appmanager/        Node.js/Python/Ruby/Go process management (like phpmanager but generic)
  backup/            Local/S3/SFTP backup + restore
  cache/             L1 memory (256-shard LRU) → L2 disk + ESI (Edge Side Includes)
  cli/               CLI commands (serve, stop, cert, php, user, domain, install, doctor...)
  config/            Config structs + Duration/ByteSize types + MarshalYAML
  cronjob/           Cron job management (per-domain)
  database/          MySQL/MariaDB management (create DB/user, install, start/stop)
  dnschecker/        DNS record verification (A/MX/NS/TXT) + server IP match
  dnsmanager/        Cloudflare DNS record CRUD + sync
  doctor/            System diagnostics + auto-fix (PHP, permissions, config, ports)
  filemanager/       Web file manager (browse/edit/upload/delete/disk-usage)
  firewall/          UFW management via API
  handler/
    fastcgi/         PHP-CGI/FPM handler + X-Accel-Redirect + X-Sendfile
    proxy/           Reverse proxy + load balancing + circuit breaker + canary + mirror
    static/          Static file serving + try_files + directory listing + ETag
  logger/            log/slog wrapper
  mcp/               MCP interface for AI management
  metrics/           Request metrics + latency percentiles
  middleware/        Chain composition, WAF, bot guard, rate limit, CORS, compression, GeoIP
  migrate/           Apache/Nginx config migration
  monitor/           Health monitoring + domain health checks
  notify/            Webhook, Slack, Telegram, Email (SMTP) channels + SMTP relay
  phpmanager/        PHP detect, install, start/stop, per-domain assign, config, auto-restart
  rewrite/           Apache mod_rewrite compatible engine (RewriteCond, -f/-d/-l/-s)
  rlimit/            Per-domain resource limits via Linux cgroups v2 (CPU/memory/PID)
  router/            VHost routing + unknown host tracking
  selfupdate/        Binary self-update from GitHub releases
  server/            Main HTTP/HTTPS/HTTP3 server + request dispatch + ESI assembly
  terminal/          WebSocket-to-PTY bridge for browser-based shell (Linux)
  serverip/          Server IP detection (interfaces + public IP)
  services/          systemd service management (start/stop/restart)
  siteuser/          SFTP user management (chroot jail + SSH keys)
  tls/               SNI cert selection + ACME auto-issuance + retry + on-demand
  wordpress/         One-click WordPress install (DB + config + permissions + mu-plugin)
pkg/
  fastcgi/           FastCGI protocol implementation + connection pool
  htaccess/          .htaccess parser (IfModule, RewriteCond, Header, Expires, ErrorDocument)
web/dashboard/       React SPA (33 pages, Vite + TypeScript + Tailwind)
```

## Stats

- 50 Go packages, all with tests (all passing)
- 38 dashboard pages, 190+ API endpoints
- 18 CLI commands
- ~14MB single binary (linux/amd64)

## Conventions

- **Go 1.26+** required
- **stdlib-first** — 4 direct dependencies (`gopkg.in/yaml.v3`, `brotli`, `quic-go`, `x/crypto`)
- No web frameworks, no ORMs, no logging frameworks
- `internal/logger/` wraps `log/slog` — use it everywhere
- Config structs in `internal/config/config.go` — add new fields there
- Tests alongside source: `foo.go` → `foo_test.go`
- Run `go vet ./...` before committing
- Run `go test -p 1 ./...` for reliable results (integration tests need serial)
- Dashboard: TypeScript strict, `npx tsc --noEmit` must pass

## Key Patterns

- **VHost routing**: `internal/router/vhost.go` — exact → alias → wildcard → fallback
- **Unknown domains**: rejected before middleware chain (421 or 403 if blocked) — zero CPU wasted
- **Global middleware**: `internal/middleware/chain.go` — `Chain(A, B, C)(handler)` composition
- **Per-domain middleware**: WAF, IPACL, BasicAuth, CORS, HeaderTransform, BotGuard — applied in `handleRequest()` per domain config
- **Handlers**: static, fastcgi, proxy, redirect — dispatched by `domain.Type`
- **Cache**: L1 memory (256-shard LRU) → L2 disk, checked before handler dispatch
- **TLS**: `internal/tls/manager.go` — SNI-based cert selection, ACME auto-issuance with retry
- **Rewrite**: `internal/rewrite/engine.go` — Apache mod_rewrite compatible (RewriteCond %{REQUEST_FILENAME} !-f)
- **Config persist**: Domain CRUD writes to `domains.d/*.yaml` atomically (temp+rename)
- **Settings API**: `GET/PUT /api/v1/settings` — structured key-value, secrets masked in GET
- **Domain update**: `PUT /api/v1/domains/{host}` — merge mode (default) or `?replace=true` for full replace
- **2FA**: TOTP via `X-TOTP-Code` header, setup/verify/disable via `/api/v1/auth/2fa/*`
- **PHP lifecycle**: Auto-detect → install → auto-start on boot → auto-assign on domain add → auto-restart on crash
- **Auth**: Timing-safe API key comparison via `crypto/subtle.ConstantTimeCompare`
- **.htaccess**: Parsed at runtime, cached with modTime tracking, auto-invalidated on file change
- **Server timeouts**: Sane defaults (Read 30s, Write 120s, Idle 120s) prevent resource exhaustion

## Security

- WAF: URL + request body inspection, SQL/XSS/shell/RCE detection
- Bot guard: blocks 25+ malicious scanners, localhost bypass
- PHP sandbox: `disable_functions`, `open_basedir`, `allow_url_include=Off` per domain
- Path traversal: checked in static handler, file manager, X-Accel-Redirect, X-Sendfile
- Domain validation: hostname regex rejects injection/traversal characters
- WAF body scan: first 64KB scanned, full body restored via MultiReader (no truncation)

## Testing

```bash
go test -p 1 ./...                   # All tests (40 packages, serial for reliability)
go test ./internal/cache/            # Single package
go test -v -run TestWordPress ./...  # Specific test
```

## Common Tasks

- **Add a config field**: Edit `internal/config/config.go`, add to Settings API in `api.go:handleSettingsGet/Put`
- **Add global middleware**: Create file in `internal/middleware/`, add to chain in `server.go:buildMiddlewareChain()`
- **Add per-domain middleware**: Add config field in `config.go`, wire in `server.go:handleRequest()` after domain lookup, rebuild in `reload()`
- **Add admin endpoint**: Register in `internal/admin/api.go:registerRoutes()`, add handler method
- **Add MCP tool**: Register in `internal/mcp/server.go:registerTools()`
- **Add CLI command**: Create file in `internal/cli/`, register in `cmd/uwas/main.go`
- **Add dashboard page**: Create in `web/dashboard/src/pages/`, add route in `App.tsx`, add to sidebar group in `Sidebar.tsx`
- **Add API function**: Add to `web/dashboard/src/lib/api.ts` with proper TypeScript interface

## Dashboard Pages (38)

Sites: Domains, Domain Detail, Topology, Certificates, DNS, WordPress, Clone/Staging, Migration, File Manager
Server: PHP, PHP Config, Applications, Database, SFTP Users, Cron Jobs, Services, Packages, IP Management, Email Guide
Performance: Cache, Metrics, Analytics, Logs
Security: Security, Firewall, Unknown Domains, Audit Log, Admin Users
System: Config Editor, Webhooks, Backups, Terminal, Updates, Settings, Doctor
Auth: Login (with 2FA/TOTP support)
Overview: Dashboard (stats, health, graphs)
