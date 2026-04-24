# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

**⚠️ MANDATORY:** Before any work, read and obey `AGENT_DIRECTIVES.md` in the project root.

---

## Project Overview

UWAS (Unified Web Application Server) is a single-binary Go web server + hosting control panel. Replaces Apache + Nginx + Varnish + Caddy + cPanel. Auto HTTPS, caching, PHP/FastCGI, .htaccess, reverse proxy, WAF, and a 38-page React dashboard.

**Current Stats (v0.0.38):**
- 52 Go packages, all with tests
- 38 dashboard pages, 205+ API endpoints
- 19 CLI commands
- ~15MB single binary

## Build & Test Commands

```bash
# Development
make dev                    # Build dev binary → bin/uwas
make run                    # Build and run with uwas.example.yaml

# Production
make build                  # Production binary (stripped, versioned)
make linux                  # Cross-compile for Linux amd64
make linux-arm              # Cross-compile for Linux arm64

# Quality
make test                   # Run all tests (serial: -p 1 for reliability)
make test-coverage          # Coverage report for internal/ and pkg/
make lint                   # go vet + staticcheck
make check                  # Full check: lint + TypeScript + tests

# Dashboard
cd web/dashboard && npm run build     # Production build (embedded via go:embed)
cd web/dashboard && npm run dev       # Dev server
npx tsc --noEmit                      # Type check (strict mode)
```

## Architecture

```
cmd/uwas/            CLI entry point (19 commands)
internal/
  admin/             API server (205+ routes) + dashboard embed + TOTP auth
  alerting/          Alert thresholds + notifications
  analytics/         Per-domain traffic analytics
  appmanager/        Node.js/Python/Ruby/Go process management
  auth/              Multi-user RBAC + sessions + TOTP 2FA
  backup/            Local/S3/SFTP backup + restore
  bandwidth/         Per-domain bandwidth limits + throttling
  build/             Build metadata (version, commit, date) via ldflags
  cache/             L1 memory (256-shard LRU) → L2 disk + ESI
  cli/               CLI framework and commands
  config/            YAML parser, validation, ByteSize/Duration types
  cronjob/           Cron job management + execution monitoring
  database/          MySQL/MariaDB management + Docker container support
  deploy/            Git clone/pull + Docker-based app deployment
  dnsmanager/        Cloudflare, Route53, Hetzner, DigitalOcean DNS CRUD
  dnschecker/        DNS record verification (A/MX/NS/TXT)
  doctor/            System diagnostics + auto-fix
  filemanager/       Web file manager (browse/edit/upload/delete)
  firewall/          UFW management via API
  handler/
    fastcgi/         PHP-CGI/FPM handler + X-Accel-Redirect + X-Sendfile
    proxy/           Reverse proxy, LB (5 algorithms), circuit breaker, WebSocket
    static/          Static files, MIME, ETag, pre-compressed, SPA
  install/           System package installer task queue
  logger/            Structured logger (slog wrapper)
  mcp/               MCP server for AI management
  metrics/           Prometheus-compatible metrics
  middleware/        Chain, recovery, rate limit, gzip, CORS, WAF, bot guard
  migrate/           Nginx/Apache converter + SSH site migration + clone
  monitor/           Uptime monitoring per domain
  notify/            Webhook, Slack, Telegram, Email (SMTP) channels
  pathsafe/          Path traversal guard (symlink-resolving containment)
  phpmanager/        PHP detect, install, start/stop, per-domain assign
  rewrite/           Apache mod_rewrite compatible engine
  rlimit/            Per-domain resource limits via Linux cgroups v2
  router/            Virtual host routing, request context
  selfupdate/        Binary self-update from GitHub releases
  server/            HTTP/HTTPS/HTTP3 server + request dispatch
  services/          systemd service management
  sftpserver/        Built-in SFTP server (pure Go, chroot per domain)
  siteuser/          SFTP user management (chroot jail + SSH keys)
  terminal/          WebSocket-to-PTY bridge for browser-based shell
  tls/               TLS manager, ACME client, auto-renewal
  webhook/           Event-driven webhook delivery (11 events, HMAC, retry)
  wordpress/         WordPress install, manage, debug, permissions
pkg/
  fastcgi/           FastCGI binary protocol, connection pool
  htaccess/          .htaccess parser (IfModule, RewriteCond, Header, Expires)
web/dashboard/       React 19 SPA (38 pages, Vite + TypeScript + Tailwind)
```

## Request Flow

```
TCP → TLS (SNI routing)
  → HTTP Parse
    → Global Middleware: Recovery → Request ID → Security Headers → Rate Limit → Access Log
      → Virtual Host Lookup (exact → alias → wildcard → fallback)
        → Per-Domain: IP ACL → Rate Limit → BasicAuth → CORS → Header Transform
          → Security Guard (blocked paths, WAF)
            → Bandwidth Check (throttle/block)
            → Rewrite Engine (mod_rewrite compatible)
            → Cache Lookup (L1 memory → L2 disk)
              → Handler: Static | FastCGI/PHP | Proxy | Redirect
            → Cache Store
            → Bandwidth Record
  → Response
```

## Conventions

- **Go 1.26+** required
- **stdlib-first** — 4 direct deps: `gopkg.in/yaml.v3`, `brotli`, `quic-go`, `x/crypto`
- No web frameworks, no ORMs, no logging frameworks
- Use `internal/logger/` (slog wrapper) everywhere, not stdlib log directly
- Config structs in `internal/config/config.go` — add new fields there
- Tests alongside source: `foo.go` → `foo_test.go`
- Run `go test -p 1 ./...` for reliable results (integration tests need serial)
- Dashboard: TypeScript strict mode, `npx tsc --noEmit` must pass

## Key Patterns

- **VHost routing**: `internal/router/vhost.go` — exact → alias → wildcard → fallback
- **Unknown domains**: rejected before middleware chain (421 or 403 if blocked)
- **Global middleware**: `internal/middleware/chain.go` — `Chain(A, B, C)(handler)` composition
- **Per-domain middleware**: Applied in `server.go:handleRequest()` after domain lookup
- **Handlers**: Dispatched by `domain.Type` — static, fastcgi, proxy, redirect
- **Cache**: L1 memory (256-shard LRU) → L2 disk, checked before handler dispatch
- **TLS**: `internal/tls/manager.go` — SNI cert selection, ACME auto-issuance with retry
- **Rewrite**: `internal/rewrite/engine.go` — Apache mod_rewrite compatible (`RewriteCond %{REQUEST_FILENAME} !-f`)
- **Config persist**: Domain CRUD writes to `domains.d/*.yaml` atomically (temp+rename)
- **Settings API**: `GET/PUT /api/v1/settings` — structured key-value, secrets masked in GET
- **Domain update**: `PUT /api/v1/domains/{host}` — merge mode (default) or `?replace=true` for full replace
- **2FA**: TOTP via `X-TOTP-Code` header, setup/verify/disable via `/api/v1/auth/2fa/*`
- **PHP lifecycle**: Auto-detect → install → auto-start on boot → auto-assign on domain add → auto-restart on crash
- **Auth**: Timing-safe API key comparison via `crypto/subtle.ConstantTimeCompare`
- **.htaccess**: Parsed at runtime, cached with modTime tracking, auto-invalidated on file change

## Security

- WAF: URL + request body inspection (first 64KB), SQL/XSS/shell/RCE detection
- Bot guard: blocks 25+ malicious scanners
- PHP sandbox: `disable_functions`, `open_basedir`, `allow_url_include=Off` per domain
- Path traversal: checked in static handler, file manager, X-Accel-Redirect, X-Sendfile
- Domain validation: hostname regex rejects injection/traversal characters
- SSE/WebSocket auth: short-lived single-use tickets (token never in URL query params)
- Config file permissions: 0600 for files containing secrets
- Credential generation: all uses of `crypto/rand.Read` check errors (panic on failure)

## Common Tasks

| Task | Files to Modify |
|------|-----------------|
| Add config field | `internal/config/config.go` → Settings API in `admin/api.go:handleSettingsGet/Put` |
| Add global middleware | Create in `internal/middleware/`, add to chain in `server.go:buildMiddlewareChain()` |
| Add per-domain middleware | Add config field in `config.go`, wire in `server.go:handleRequest()` after domain lookup |
| Add admin endpoint | Register in `internal/admin/api.go:registerRoutes()`, add handler method |
| Add MCP tool | Register in `internal/mcp/server.go:registerTools()` |
| Add CLI command | Create in `internal/cli/`, register in `cmd/uwas/main.go` |
| Add dashboard page | Create in `web/dashboard/src/pages/`, add route in `App.tsx`, add to `Sidebar.tsx` |
| Add API function | Add to `web/dashboard/src/lib/api.ts` with TypeScript interface |

## Testing

```bash
go test -p 1 ./...                   # All tests (serial for reliability)
go test ./internal/cache/            # Single package
go test -v -run TestWordPress ./...  # Specific test
```

## Dashboard Pages (38)

- **Sites:** Domains, Domain Detail, Topology, Certificates, DNS, WordPress, Clone/Staging, Migration, File Manager
- **Server:** PHP, PHP Config, Applications (Apps), Database, SFTP Users, Cron Jobs, Services, Packages, IP Management, Email Guide
- **Performance:** Cache, Metrics, Analytics, Logs
- **Security:** Security, Firewall, Unknown Domains, Audit Log, Admin Users
- **System:** Config Editor, Webhooks, Backups, Terminal, Updates, Settings, Doctor
- **Auth:** Login (2FA/TOTP support)
- **Overview:** Dashboard, About

<!-- rtk-instructions v2 -->
# RTK (Rust Token Killer) - Token-Optimized Commands

## Golden Rule

**Always prefix commands with `rtk`**. If RTK has a dedicated filter, it uses it. If not, it passes through unchanged. This means RTK is always safe to use.

**Important**: Even in command chains with `&&`, use `rtk`:
```bash
# ❌ Wrong
git add . && git commit -m "msg" && git push

# ✅ Correct
rtk git add . && rtk git commit -m "msg" && rtk git push
```

## RTK Commands by Workflow

### Build & Compile (80-90% savings)
```bash
rtk cargo build         # Cargo build output
rtk cargo check         # Cargo check output
rtk cargo clippy        # Clippy warnings grouped by file (80%)
rtk tsc                 # TypeScript errors grouped by file/code (83%)
rtk lint                # ESLint/Biome violations grouped (84%)
rtk prettier --check    # Files needing format only (70%)
rtk next build          # Next.js build with route metrics (87%)
```

### Test (90-99% savings)
```bash
rtk cargo test          # Cargo test failures only (90%)
rtk vitest run          # Vitest failures only (99.5%)
rtk playwright test     # Playwright failures only (94%)
rtk test <cmd>          # Generic test wrapper - failures only
```

### Git (59-80% savings)
```bash
rtk git status          # Compact status
rtk git log             # Compact log (works with all git flags)
rtk git diff            # Compact diff (80%)
rtk git show            # Compact show (80%)
rtk git add             # Ultra-compact confirmations (59%)
rtk git commit          # Ultra-compact confirmations (59%)
rtk git push            # Ultra-compact confirmations
rtk git pull            # Ultra-compact confirmations
rtk git branch          # Compact branch list
rtk git fetch           # Compact fetch
rtk git stash           # Compact stash
rtk git worktree        # Compact worktree
```

Note: Git passthrough works for ALL subcommands, even those not explicitly listed.

### GitHub (26-87% savings)
```bash
rtk gh pr view <num>    # Compact PR view (87%)
rtk gh pr checks        # Compact PR checks (79%)
rtk gh run list         # Compact workflow runs (82%)
rtk gh issue list       # Compact issue list (80%)
rtk gh api              # Compact API responses (26%)
```

### JavaScript/TypeScript Tooling (70-90% savings)
```bash
rtk pnpm list           # Compact dependency tree (70%)
rtk pnpm outdated       # Compact outdated packages (80%)
rtk pnpm install        # Compact install output (90%)
rtk npm run <script>    # Compact npm script output
rtk npx <cmd>           # Compact npx command output
rtk prisma              # Prisma without ASCII art (88%)
```

### Files & Search (60-75% savings)
```bash
rtk ls <path>           # Tree format, compact (65%)
rtk read <file>         # Code reading with filtering (60%)
rtk grep <pattern>      # Search grouped by file (75%)
rtk find <pattern>      # Find grouped by directory (70%)
```

### Analysis & Debug (70-90% savings)
```bash
rtk err <cmd>           # Filter errors only from any command
rtk log <file>          # Deduplicated logs with counts
rtk json <file>         # JSON structure without values
rtk deps                # Dependency overview
rtk env                 # Environment variables compact
rtk summary <cmd>       # Smart summary of command output
rtk diff                # Ultra-compact diffs
```

### Infrastructure (85% savings)
```bash
rtk docker ps           # Compact container list
rtk docker images       # Compact image list
rtk docker logs <c>     # Deduplicated logs
rtk kubectl get         # Compact resource list
rtk kubectl logs        # Deduplicated pod logs
```

### Network (65-70% savings)
```bash
rtk curl <url>          # Compact HTTP responses (70%)
rtk wget <url>          # Compact download output (65%)
```

### Meta Commands
```bash
rtk gain                # View token savings statistics
rtk gain --history      # View command history with savings
rtk discover            # Analyze Claude Code sessions for missed RTK usage
rtk proxy <cmd>         # Run command without filtering (for debugging)
rtk init                # Add RTK instructions to CLAUDE.md
rtk init --global       # Add RTK to ~/.claude/CLAUDE.md
```

## Token Savings Overview

| Category | Commands | Typical Savings |
|----------|----------|-----------------|
| Tests | vitest, playwright, cargo test | 90-99% |
| Build | next, tsc, lint, prettier | 70-87% |
| Git | status, log, diff, add, commit | 59-80% |
| GitHub | gh pr, gh run, gh issue | 26-87% |
| Package Managers | pnpm, npm, npx | 70-90% |
| Files | ls, read, grep, find | 60-75% |
| Infrastructure | docker, kubectl | 85% |
| Network | curl, wget | 65-70% |

Overall average: **60-90% token reduction** on common development operations.
<!-- /rtk-instructions -->