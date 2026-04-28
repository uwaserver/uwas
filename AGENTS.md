# UWAS — Development Guide

## Project

UWAS (Unified Web Application Server) is a single-binary Go web server + hosting control panel. It replaces Apache + Nginx + Varnish + Caddy + cPanel. Features: auto HTTPS, caching, PHP/FastCGI, .htaccess, reverse proxy, WAF, and a 40-page React dashboard with 205+ API endpoints.

## Build

```bash
make build          # Production binary (stripped, versioned)
make dev            # Development binary
make test           # Run all tests (-count=1, 10min timeout)
make lint           # go vet + staticcheck
make check          # Full check: lint + TypeScript + tests

# Cross-compile for Linux
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bin/uwas-linux-amd64 ./cmd/uwas

# Dashboard rebuild (auto-embedded via go:embed)
cd web/dashboard && npm run build
```

## Architecture

```
cmd/uwas/            CLI entry point (19 commands)
internal/
  admin/             API server (205+ routes) + dashboard embed + TOTP auth
  alerting/          Alert thresholds + notifications
  analytics/         Per-domain traffic analytics
  appmanager/        Node.js/Python/Ruby/Go process management (like phpmanager but generic)
  auth/              Multi-user RBAC (admin/reseller/user) + sessions + TOTP 2FA
  backup/            Local/S3/SFTP backup + restore
  bandwidth/         Per-domain bandwidth limits + throttling
  build/             Build metadata (version, commit, date) via ldflags
  cache/             L1 memory (256-shard LRU) → L2 disk + ESI (Edge Side Includes)
  cli/               CLI commands (serve, stop, cert, php, user, domain, install, doctor...)
  config/            Config structs + Duration/ByteSize types + MarshalYAML
  cronjob/           Cron job management (per-domain)
  database/          MySQL/MariaDB management (create DB/user, install, start/stop)
  deploy/            Git clone/pull + Docker-based app deployment
  dnschecker/        DNS record verification (A/MX/NS/TXT) + server IP match
  dnsmanager/        Cloudflare, Route53, Hetzner, DigitalOcean DNS CRUD
  doctor/            System diagnostics + auto-fix (PHP, permissions, config, ports)
  filemanager/       Web file manager (browse/edit/upload/delete/disk-usage)
  firewall/          UFW management via API
  install/           System package installer task queue
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
  server/            HTTP/HTTPS/HTTP3 server + request dispatch + ESI assembly + log rotation
  terminal/          WebSocket-to-PTY bridge for browser-based shell (Linux)
  serverip/          Server IP detection (interfaces + public IP)
  services/          systemd service management (start/stop/restart)
  sftpserver/        Built-in SFTP server (pure Go, chroot per domain)
  siteuser/          SFTP user management (chroot jail + SSH keys)
  tls/               SNI cert selection + ACME auto-issuance + retry + on-demand
  webhook/           Event-driven webhook delivery (11 events, HMAC, retry)
  wordpress/         One-click WordPress install (DB + config + permissions + mu-plugin)
pkg/
  fastcgi/           FastCGI protocol implementation + connection pool
  htaccess/          .htaccess parser (IfModule, RewriteCond, Header, Expires, ErrorDocument)
web/dashboard/       React 19 SPA (40 pages, Vite + TypeScript + Tailwind)
```

## Stats

- 52 Go packages, all with tests (all passing)
- 40 dashboard pages, 205+ API endpoints
- 19 CLI commands
- ~15MB single binary (linux/amd64)

## Conventions

- **Go 1.26+** required
- **stdlib-first** — 5 direct dependencies (`gopkg.in/yaml.v3`, `brotli`, `quic-go`, `x/crypto`, `x/sync`)
- No web frameworks, no ORMs, no logging frameworks
- `internal/logger/` wraps `log/slog` — use it everywhere
- Config structs in `internal/config/config.go` — add new fields there
- Tests alongside source: `foo.go` → `foo_test.go`
- Run `go vet ./...` before committing
- Run `go test -p 1 ./...` for reliable results (integration tests need serial)
- Dashboard: TypeScript strict, `cd web/dashboard && npx tsc -b` must pass

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
- **Server timeouts**: Sane defaults (ReadHeader 10s, Read 30s, Write 60s, Idle 120s) prevent resource exhaustion

## Security

- WAF: URL + request body inspection, SQL/XSS/shell/RCE detection
- Bot guard: blocks 25+ malicious scanners, localhost bypass
- PHP sandbox: `disable_functions`, `open_basedir`, `allow_url_include=Off` per domain
- Path traversal: checked in static handler, file manager, X-Accel-Redirect, X-Sendfile
- Domain validation: hostname regex rejects injection/traversal characters
- WAF body scan: first 64KB scanned, full body restored via MultiReader (no truncation)

## Testing

```bash
go test -p 1 ./...                   # All tests (52 packages, serial for reliability)
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

## Dashboard Pages (40)

Sites: Domains, Domain Detail, Topology, Certificates, DNS, Cloudflare, WordPress, Clone/Staging, Migration, File Manager
Server: PHP, PHP Config, Applications, Database, DB Explorer, SFTP Users, Cron Jobs, Services, Packages, IP Management, Email Guide
Performance: Cache, Metrics, Analytics, Logs
Security: Security, Firewall, Unknown Domains, Audit Log, Admin Users, Users
System: Config Editor, Webhooks, Backups, Terminal, Updates, Settings, Doctor
Auth: Login (with 2FA/TOTP support)
Overview: Dashboard (stats, health, graphs)

<!-- dfmt:v1 begin -->
# Context Discipline — REQUIRED

This project uses DFMT to keep large tool outputs from exhausting the
context window. **Read this section at the start of every conversation
in this project.**

## Rule 1 — Prefer DFMT tools over native tools

Always use DFMT's MCP tools when an output might exceed 2 KB:

| Native     | DFMT replacement |
|------------|------------------|
| `Bash`     | `dfmt_exec`      |
| `Read`     | `dfmt_read`      |
| `WebFetch` | `dfmt_fetch`     |
| `Glob`     | `dfmt_glob`      |
| `Grep`     | `dfmt_grep`      |
| `Edit`     | `dfmt_edit`      |
| `Write`    | `dfmt_write`     |

Include an `intent` argument on every call, describing what you need
from the output. The `intent` lets DFMT return the relevant portion of
a large output without flooding the context.

## Rule 2 — On DFMT failure, report and fall back

DFMT is a strong preference, not a hard dependency. If a `dfmt_*` tool
errors, times out, or is unavailable, report the failure to the user
(one short line — which call, what error) and continue with the native
equivalent so the session is not blocked. The ban is on *silent*
fallback — every switch must be announced. After a fallback, drop a
brief `dfmt_remember` note tagged `gap` when practical. If the native
tool is also denied (permission rule, sandbox refusal), stop and ask
the user; do not retry blindly.

## Rule 3 — Record user decisions

When the user states a preference or correction ("use X instead of Y",
"do not modify Z"), call `dfmt_remember` with a `decision` tag so the
choice survives context compaction.

## Why these rules matter

Some agents do not provide hooks to enforce these rules automatically.
**Compliance is your responsibility as the agent.** A single raw shell
output above 8 KB can push earlier context out of the window, erasing
the conversation's history. Following the rules above preserves it.
<!-- dfmt:v1 end -->
