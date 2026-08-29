# UWAS Architecture

> **Unified Web Application Server** — A single-binary Go web server + hosting control panel replacing Apache, Nginx, Varnish, Caddy, and cPanel.

---

## Codebase Statistics

Measured against the tree at v0.9.2. Every figure below is derived from the
source, not maintained by hand — the commands are given so they can be checked.

| Metric | Count | How to check |
|--------|------:|--------------|
| Go source files (excl. tests) | 244 | `find . -name '*.go' -not -name '*_test.go' \| wc -l` |
| Test files | 284 | `find . -name '*_test.go' \| wc -l` |
| Lines of Go (excl. tests) | ~64,500 | `find . -name '*.go' -not -name '*_test.go' -exec cat {} + \| wc -l` |
| Test functions | ~7,000 | `grep -rhoE '^func Test' --include='*_test.go' . \| wc -l` |
| Packages (total) | 69 | `go list ./... \| wc -l` |
| — under `internal/` | 62 | `go list ./internal/... \| wc -l` |
| — under `pkg/` | 2 | `go list ./pkg/... \| wc -l` |
| Packages with tests | 56 | `go list -f '{{if .TestGoFiles}}x{{end}}' ./...` |
| CLI commands | 19 | `grep -c 'app.Register(' cmd/uwas/main.go` |
| Admin API routes | 253 registrations | `grep -cE 'mux\.(HandleFunc\|Handle)\(' internal/admin/routes.go` |
| Dashboard pages | 42 | `ls web/dashboard/src/pages/*.tsx` (excl. `settingsSections.tsx`, which is not a page) |
| Direct Go dependencies | 5 | `go.mod` require block, non-indirect |
| Binary size (linux/amd64) | ~16.4 MB | v0.9.2 release asset |

> **Note:** `internal/admin` is split at the **package** level, not only the file
> level: eleven handler sub-packages sit under it (`admin/apps`, `admin/settings`,
> `admin/domain`, …), plus `admin/dashboard` for the `go:embed` of the SPA,
> alongside the `handlers_*.go` files that remain in the parent as wrappers. Route registration stays in `routes.go`. See `docs/admin-subpackaging.md`
> and the Package Map below.

---

## High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              UWAS Binary                                    │
│                                                                             │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────────┐   │
│  │ HTTP :80 │  │HTTPS :443│  │HTTP3 :443│  │Admin:9443│  │  SFTP :2222  │   │
│  └────┬─────┘  └────┬─────┘  └─────┬────┘  └────┬─────┘  └─────────┬────┘   │
│       │             │              │            │                  │        │
│       └─────────────┴──────┬───────┘            │                  │        │
│                            │                    │                  │        │
│                    ┌───────▼─────────┐    ┌─────▼─────────┐ ┌──────▼──────┐ │
│                    │  Request Router │    │  Admin API    │ │ SFTP Server │ │
│                    │  (VHost + SNI)  │    │  253 routes   │ │ chroot jail │ │
│                    └────────┬────────┘    │  + Dashboard  │ └─────────────┘ │
│                             │             │ + WebSocket   │                 │
│              ┌──────────────┼────┐        │ + MCP         │                 │
│              │              │    │        └───────────────┘                 │
│     ┌────────▼───┐  ┌───────▼──┐ │                                          │
│     │ Middleware │  │  Cache   │ │   ┌─────────────────────────────────┐    │
│     │   Chain    │  │ L1 → L2  │ │   │        Subsystems               │    │
│     └────────┬───┘  │ + ESI    │ │   │                                 │    │
│              │      └──────────┘ │   │  PHP Manager    App Manager     │    │
│     ┌────────▼───────────────┐   │   │  TLS/ACME       Deploy          │    │
│     │    Handler Dispatch    │   │   │  Backup          Database       │    │
│     │                        │   │   │  Cron            Firewall       │    │
│     │  static │ php │ proxy  │   │   │  Webhooks        Analytics      │    │
│     │  app    │ redirect     │   │   │  Alerting        Metrics        │    │
│     └────────────────────────┘   │   │  Doctor          Terminal       │    │
│                                  │   └─────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Request Lifecycle

Every HTTP(S) request follows this exact path through the system:

```
Client Request
      │
      ▼
┌─────────────────────────────┐
│  1. Connection Limiter      │  Semaphore-based (configurable max)
└─────────────┬───────────────┘
              ▼
┌─────────────────────────────┐
│  2. Global Middleware Chain  │  Recovery → RequestID → RealIP
│                              │  → SecurityHeaders → Compress
│                              │  → Global RateLimit
│                              │  → SecurityGuard (blocked paths)
│                              │  → BotGuard → AccessLog
└─────────────┬───────────────┘
              ▼
┌─────────────────────────────┐
│  3. VHost Lookup             │  exact match → wildcard → fallback
│                              │  Unknown → 421 or 403 (if blocked)
└─────────────┬───────────────┘
              ▼
┌─────────────────────────────┐
│  4. Per-Domain Security      │  IP ACL → GeoIP → Basic Auth → CORS
│     (inline in handleRequest)│  → WAF (URL + body scan)
│                              │  → location rate limit
│                              │  These are guard calls inside
│                              │  handleRequest, not a middleware
│                              │  chain. Only the IP ACL is stored
│                              │  as middleware (s.domainChains).
└─────────────┬───────────────┘
              ▼
┌─────────────────────────────┐
│  5. Rewrite Engine           │  .htaccess import (runtime parse)
│                              │  + YAML rewrite rules
│                              │  Apache mod_rewrite compatible
└─────────────┬───────────────┘
              ▼
┌─────────────────────────────┐
│  6. Header Transforms        │  Request add/remove
│                              │  Response add/remove
└─────────────┬───────────────┘
              ▼
┌─────────────────────────────┐
│  7. Cache Lookup             │  L1 memory (256-shard LRU)
│     (if cache enabled)       │  → L2 disk (gzip compressed)
│                              │  → ESI assembly on hit
│                              │  Bypass: .php, wp-admin, cookies
└──────┬──────────────┬───────┘
       │ MISS         │ HIT
       ▼              ▼
┌──────────────┐  ┌──────────────┐
│ 8. Handler   │  │ Serve cached │
│    Dispatch  │  │ + ETag 304   │
│              │  │ + ESI assem. │
│ static ──────│  └──────────────┘
│ php ─────────│
│ proxy ───────│
│ app ─────────│
│ redirect ────│
└──────┬───────┘
       ▼
┌─────────────────────────────┐
│  9. Response Capture         │  Store in cache if cacheable
│                              │  (status, headers, body, TTL, tags)
└─────────────┬───────────────┘
              ▼
┌─────────────────────────────┐
│ 10. Post-Response            │  Per-domain access_log file
│                              │  Panel log ring buffer
│                              │  Metrics recording
│                              │  Analytics aggregation
│                              │  Alert threshold check
│                              │  Webhook dispatch
└─────────────────────────────┘
```

---

## Package Map

```
cmd/uwas/
└── main.go                     CLI entry point

internal/
├── admin/                      API server (253 route registrations) + dashboard embed + auth
│   │
│   │   Twelve sub-packages carry the handler logic; the parent keeps route
│   │   registration, shared server state and the handlers not yet extracted.
│   │   See docs/admin-subpackaging.md for the split and its rules.
│   │
│   ├── apps/                   Standalone application handlers
│   ├── authmw/                 Admin API authentication middleware
│   ├── backup/                 Backup management handlers
│   ├── cloudflare/             Cloudflare integration handlers
│   ├── database/               MySQL/MariaDB handlers
│   ├── deploy/                 Git/Docker deploy pipeline handlers
│   ├── domain/                 Domain CRUD + unknown-domain handlers
│   ├── files/                  File manager handlers
│   ├── php/                    PHP-FPM lifecycle handlers
│   ├── settings/               Global settings, branding, 2FA, raw YAML editor
│   ├── wordpress/              WordPress site handlers
│   ├── dashboard/dist/         Embedded React SPA (go:embed)
│   │
│   ├── api.go                  Core: Server struct, lifecycle, middleware, helpers
│   ├── routes.go               Route registration (themed sub-registrars)
│   ├── handlers_auth.go        Login, 2FA, multi-user RBAC, sessions
│   ├── handlers_domain.go      Domain CRUD, unknown-host tracking
│   ├── domain_alias.go         www↔apex canonical redirect logic
│   ├── handlers_cloudflare.go  CF connection, IPs, cache purge
│   ├── handlers_cloudflare_tunnel.go  CF tunnel lifecycle (create/start/stop)
│   ├── handlers_cloudflare_zones.go   CF zone + DNS record management
│   ├── handlers_settings.go    Settings + raw config editor
│   ├── handlers_php.go         PHP-FPM lifecycle (detect/install/assign)
│   ├── handlers_database.go    MySQL/MariaDB + Docker DB management
│   ├── handlers_backup.go      Backup create/restore/schedule
│   ├── handlers_mcp.go         MCP (AI management) tool calls
│   ├── handlers_cron.go        Cron job monitoring + execution
│   ├── handlers_bandwidth.go   Per-domain bandwidth limits
│   ├── handlers_certs.go       TLS cert listing/renewal/upload
│   ├── handlers_software_library.go  Software Library CRUD + lifecycle (760 LOC)
│   ├── handlers_software_docker.go   Docker Compose runner + container monitoring (581)
│   ├── handlers_software_backup.go   Software instance backup/restore (273)
│   ├── handlers_software_ports.go    Port allocation + conflict detection (162)
│   ├── handlers_software_store.go    Instance persistence + secret/env helpers (197)
│   ├── handlers_software_templates.go  Docker Compose YAML templates, 11 apps (175)
│   ├── handlers_*.go           Thin wrappers delegating to the sub-packages,
│   │                           plus handlers not yet extracted
│   ├── ringbuf.go              Generic ring buffer (log + audit streams)
│   └── audit.go                Audit trail
│
├── alerting/                   Threshold-based alerts + notifications
├── analytics/                  Per-domain traffic analytics + bandwidth
├── apps/                       Standalone app supervision (Node/Python/Ruby/Go/Docker)
│   ├── manager.go              Register, Start, Stop, Stats
│   ├── scaffold.go             Runtime demo file generation for empty workdirs
│   ├── store.go                /etc/uwas/apps.d YAML persistence
│   ├── stats_linux.go          /proc CPU/memory reading
│   └── stats_other.go          No-op stub (non-Linux)
│
├── auth/                       Multi-user auth (admin/reseller/user roles)
├── backup/                     Backup/restore (local, S3, SFTP)
├── bandwidth/                  Per-domain bandwidth tracking + limits
├── build/                      Version + build info (ldflags)
├── cloudflare/                 Cloudflare Tunnel API + local cloudflared binary
├── cache/                      L1 memory LRU → L2 disk + ESI
│   ├── engine.go               Get/Set/Purge orchestration
│   ├── memory.go               256-shard LRU with TTL + grace
│   ├── disk.go                 Gzip-compressed disk cache
│   └── esi.go                  Edge Side Include processor
│
├── cli/                        19 CLI commands
│   ├── root.go                 Command dispatcher
│   ├── serve.go                Start server
│   ├── domain.go               Domain CRUD
│   ├── backup.go               Backup/restore
│   ├── install.go              One-time setup + systemd unit
│   ├── migrate.go              Apache/Nginx config import
│   ├── status.go               Runtime status
│   └── ...                     (command files for cert, config, php, restart, stop, user, version, etc.)
│
├── config/                     YAML config structs + validation + defaults
│   ├── config.go               All config types (Domain, SSL, PHP, Proxy...)
│   ├── defaults.go             Safe defaults for all fields
│   └── validate.go             Full config validation
│
├── cronjob/                    Per-domain cron job management
├── database/                   MySQL/MariaDB: detect, install, CRUD, Docker
├── deploy/                     Git clone → build → restart / Docker pipeline
├── dnschecker/                 DNS record verification (A/MX/NS/TXT)
├── domainroot/                 Resolves the filesystem root shown for a domain
│                               (apps:// proxy domains point at the app WorkDir)
├── domainutil/                 Pure hostname helpers shared by admin and
│                               admin/domain — exists to break an import cycle
├── dnsmanager/                 Cloudflare, Route53, Hetzner, DigitalOcean DNS CRUD
├── doctor/                     System diagnostics + auto-fix
├── filemanager/                Web file manager (browse/edit/upload/delete)
├── firewall/                   UFW wrapper (allow/deny/list)
│
├── handler/
│   ├── fastcgi/                PHP-FPM handler + X-Sendfile + WSOD detection
│   │   ├── handler.go          Execute + retry + timeout
│   │   └── env.go              CGI env vars + open_basedir + PHP_ADMIN_VALUE
│   ├── proxy/                  Reverse proxy + LB + circuit breaker + canary
│   │   ├── handler.go          Forward + WebSocket tunnel
│   │   ├── health.go           Upstream health checker
│   │   ├── balancer.go         round_robin (default, weighted built in),
│   │   │                       least_conn, ip_hash, uri_hash, random, sticky
│   │   │                       + affinity layered over any of them
│   │   ├── circuit.go          Circuit breaker (threshold + auto-heal)
│   │   ├── canary.go           Percentage-based canary routing
│   │   └── mirror.go           Async request mirroring
│   ├── static/                 Static file serving + try_files + ETag
│   (redirect responses are handled by server dispatch/config, not a separate package)
│
├── install/                    Background task manager (install queue)
├── logger/                     log/slog wrapper (structured logging)
├── mcp/                        MCP server (AI management interface)
├── metrics/                    Request metrics + latency percentiles
├── pathsafe/                   Path containment checks with a cached resolved
│                               base — the static-serve hot path
│
├── middleware/                 Composable middlewares
│   ├── chain.go                Chain(A, B, C)(handler) composition
│   ├── recovery.go             Panic recovery
│   ├── requestid.go            X-Request-ID (UUIDv7)
│   ├── realip.go               X-Forwarded-For / X-Real-IP extraction
│   ├── security.go             WAF: SQL/XSS/shell/RCE detection
│   ├── compress.go             Gzip + Brotli (skip images/binary)
│   ├── ratelimit.go            Per-IP token bucket
│   ├── cors.go                 CORS preflight + headers
│   ├── basicauth.go            HTTP Basic Auth (per-domain)
│   ├── ipacl.go                IP whitelist/blacklist (per-domain)
│   ├── geoip.go                Country-level blocking (per-domain)
│   ├── botguard.go             25+ malicious scanner patterns
│   ├── headers.go              Security headers (HSTS, X-Frame, etc.)
│   ├── imageopt.go             WebP/AVIF auto-conversion
│   ├── hotlink.go              Referer-based hotlink protection
│   └── accesslog.go            One request line to the MAIN log, globally.
│                               Not per-domain and not a file — the per-domain
│                               access_log file is written by
│                               server/domainlog.go inside handleRequest.
│                               5xx logs at error, everything else at info;
│                               global.access_log.enabled turns it off.
│
├── migrate/                    Apache/Nginx config converter
├── monitor/                    Domain health checks (HTTP probe)
├── notify/                     Slack, Telegram, Email (SMTP) channels
├── phpmanager/                 PHP detect → install → start → assign → monitor
│   ├── manager.go              Manager type + lifecycle entry points
│   ├── detect.go               Installed version discovery
│   ├── domain.go               Per-domain assignment + pool config
│   ├── fpm.go                  FPM process control
│   ├── ini.go                  Per-domain php.ini generation
│   └── install.go              Platform-aware install (apt/dnf/brew)
│
├── rewrite/                    Apache mod_rewrite engine
│   ├── engine.go               RewriteRule + RewriteCond processing
│   └── variables.go            %{REQUEST_FILENAME}, %{HTTP_HOST}, etc.
│
├── respond/                    Centralised JSON responses + hardening headers
├── rlimit/                     Linux cgroups v2 (CPU/memory/PID limits)
├── router/                     VHost routing + unknown host tracking
├── selfupdate/                 Binary self-update from GitHub releases
│
├── server/                     Main server orchestration
│   ├── server.go               Lifecycle, middleware chain, listeners, signals
│   ├── server_dispatch.go      Request dispatch + per-domain guards + handlers
│   ├── server_routing.go       Guard factories (WAF/IP/Geo/CORS/rate) + proxy pools
│   ├── server_htaccess.go      Htaccess/rewrite parsing + cache
│   ├── server_reload.go        Hot config reload
│   ├── domainlog.go            Per-domain access_log files + rotation
│   ├── capture.go              Response capture for caching
│   └── error.go                Domain error pages
│
├── serverip/                   Server IP detection (interfaces + public)
├── services/                   systemd service control wrapper
├── sftpserver/                 SFTP server with chroot jail + SSH keys
├── siteuser/                   Domain user management (chroot + SSH)
├── terminal/                   WebSocket-to-PTY bridge (browser shell)
│
├── tls/                        TLS manager + ACME
│   ├── manager.go              SNI cert selection + renewal ticker
│   ├── storage.go              Cert file storage (/var/lib/uwas/certs/)
│   └── acme/                   ACME client (Let's Encrypt)
│       ├── client.go           Account, order, challenge solving (HTTP-01, DNS-01)
│       └── jws.go              JWS signing for ACME requests
│                               DNS-01 providers live in internal/dnsmanager
│                               (Cloudflare, Route53, Hetzner, DigitalOcean)
│
├── webhook/                    Event dispatch (HMAC signed, retry)
└── wordpress/                  One-click WP install (DB + config + mu-plugin)

pkg/
├── fastcgi/                    FastCGI protocol implementation
│   ├── client.go               Execute (params + stdin → stdout)
│   ├── pool.go                 Connection pool (idle reuse, stale check)
│   └── protocol.go             Record encoding/decoding
│
└── htaccess/                   .htaccess parser
    └── parser.go               RewriteRule, Header, Expires, ErrorDocument
                                IfModule, php_value, php_flag

web/dashboard/                  React SPA (Vite + TypeScript + Tailwind)
├── src/pages/                  42 page components + settingsSections.tsx
├── src/lib/api.ts              API client for admin REST routes
├── src/components/             Sidebar, PinModal, Card
└── dist/ → go:embed            Compiled into binary
```

---

## Handler Dispatch

```
domain.Type determines the handler:

┌──────────┬──────────────────────────────────────────────────────────────┐
│ Type     │ Flow                                                        │
├──────────┼──────────────────────────────────────────────────────────────┤
│ static   │ try_files ($uri, $uri/, /index.html)                        │
│          │ → serve file (ETag, Range, MIME)                            │
│          │ → or directory listing                                      │
├──────────┼──────────────────────────────────────────────────────────────┤
│ php      │ try_files ($uri, $uri/, /index.php)                         │
│          │ → if .php: FastCGI → PHP-FPM (auto-detect address)         │
│          │ → else: serve static file                                   │
│          │ → X-Sendfile / X-Accel-Redirect support                    │
│          │ → WSOD detection (empty body + 200 → 500)                  │
├──────────┼──────────────────────────────────────────────────────────────┤
│ proxy    │ select upstream (6 algorithms, + optional sticky affinity)  │
│          │ → circuit breaker check                                     │
│          │ → canary routing (% split)                                  │
│          │ → forward request + copy response                           │
│          │ → WebSocket upgrade detection + tunnel                      │
│          │ → mirror (async fire-and-forget)                            │
│          │ → retry on connection error (next backend)                  │
│          │ → gRPC: h2c to a cleartext backend (proxy.grpc)              │
├──────────┼──────────────────────────────────────────────────────────────┤
│ app      │ UWAS → 127.0.0.1:{auto-port}                              │
│          │ → managed process (Node/Python/Ruby/Go)                     │
│          │ → auto-restart on crash                                     │
│          │ → cgroup resource limits (Linux)                            │
├──────────┼──────────────────────────────────────────────────────────────┤
│ redirect │ 301/302/307/308 → target URL                               │
│          │ → preserve_path option                                      │
└──────────┴──────────────────────────────────────────────────────────────┘
```

---

## Caching Architecture

```
Request
  │
  ▼
┌─────────────────────────────────────────┐
│           Bypass Check                   │
│                                          │
│  Skip if:                                │
│  • .php extension (PHP domains)          │
│  • wp-admin, wp-login, wp-json paths     │
│  • Session/auth cookies present          │
│  • POST/PUT/DELETE method                │
│  • X-Cache-Bypass: 1 header              │
│  • Per-domain bypass rules               │
└──────────┬──────────────────────────────┘
           │ cacheable
           ▼
┌─────────────────────────────────────────┐
│     L1 Memory Cache (256-shard LRU)      │
│                                          │
│  • 256 independent shards                │
│  • Per-shard RWMutex (minimal contention)│
│  • Per-entry TTL + grace TTL             │
│  • Configurable memory limit (def 256MB) │
│  • LRU eviction when full                │
│                                          │
│  HIT → serve (+ ETag 304 check)         │
│  STALE → serve + async revalidate        │
│  MISS ↓                                  │
└──────────┬──────────────────────────────┘
           ▼
┌─────────────────────────────────────────┐
│     L2 Disk Cache (gzip compressed)      │
│                                          │
│  • File-based (hash of URL as filename)  │
│  • Gzip compressed on disk               │
│  • Configurable disk limit (def 1GB)     │
│  • Promote to L1 on hit                  │
│                                          │
│  HIT → promote to L1 + serve            │
│  MISS → pass to handler                  │
└──────────┬──────────────────────────────┘
           ▼
┌─────────────────────────────────────────┐
│        Handler (origin fetch)            │
│                                          │
│  Response captured → store in L1 + L2    │
│  (if status 200, body < 10MB,            │
│   cacheable Content-Type)                │
│                                          │
│  ESI detection:                          │
│  If body contains <!--esi:include        │
│  → mark as ESI template                  │
│  → fragments cached separately           │
│  → assembled on cache hit                │
└─────────────────────────────────────────┘
```

### ESI (Edge Side Includes)

```
Page with ESI tags:

  <html>
    <header>...</header>
    <!--esi:include src="/fragment/nav" -->
    <main>Content</main>
    <!--esi:include src="/fragment/footer" -->
  </html>

On cache hit:
  1. Parse ESI tags from cached template
  2. Fetch each fragment (internal subrequest)
  3. Each fragment independently cached with own TTL
  4. Assemble final page
  5. Max depth: 3 levels, max 50 includes per page
```

---

## TLS / ACME Flow

```
Server Start
  │
  ├─ Load existing certs from /var/lib/uwas/certs/{domain}/
  ├─ Load manual certs from domain config (ssl.cert + ssl.key)
  │
  ▼
┌─────────────────────────────────────┐
│  For each domain with ssl.mode=auto │
│                                     │
│  1. Check if cert exists + valid    │
│  2. If missing → queue ACME issue   │
│  3. If expiring (< 30 days) → renew │
└──────────────┬──────────────────────┘
               ▼
┌─────────────────────────────────────┐
│         ACME Issuance                │
│                                     │
│  Challenge types:                   │
│  • HTTP-01: /.well-known/acme-...   │
│  • DNS-01: _acme-challenge TXT      │
│    (via Cloudflare, DigitalOcean,    │
│     Hetzner, Route53)                │
│                                     │
│  Rate limit: 10 certs/minute        │
│  Retry: 3 attempts with backoff     │
│  Fallback: self-signed cert         │
└──────────────┬──────────────────────┘
               ▼
┌─────────────────────────────────────┐
│      SNI Certificate Selection       │
│                                     │
│  tls.Config.GetCertificate callback │
│                                     │
│  1. Exact match: domain → cert     │
│  2. Wildcard match: *.domain       │
│  3. On-demand: issue new cert      │
│  4. Fallback: self-signed          │
└──────────────┬──────────────────────┘
               ▼
┌─────────────────────────────────────┐
│   Per-SNI Config (GetConfigForClient)│
│                                     │
│  The base config carries a TLS 1.2  │
│  floor. A domain needing more gets  │
│  a clone, built from its ClientHello│
│  server name:                       │
│                                     │
│  • ssl.min_version — raises the     │
│    floor (below 1.2 is clamped up)  │
│  • ssl.client_ca — mTLS client CA   │
│    pool + client_auth mode          │
│    (require / request / none)       │
│                                     │
│  A domain configuring neither gets  │
│  nil, so no clone is allocated.     │
└─────────────────────────────────────┘

Renewal Ticker (every 12 hours):
  → Check all certs
  → Renew if < 30 days remaining
  → Alert via webhook on failure
```

---

## Proxy Architecture

```
┌──────────┐     ┌─────────────────────────────────────────┐
│  Client   │────▶│              UWAS Proxy                 │
└──────────┘     │                                          │
                 │  ┌──────────────┐  ┌──────────────────┐ │
                 │  │   Balancer    │  │  Circuit Breaker │ │
                 │  │              │  │                  │ │
                 │  │ round_robin  │  │ threshold: 10    │ │
                 │  │ least_conn   │  │ timeout: 30s     │ │
                 │  │ ip_hash      │  │ auto-heal: yes   │ │
                 │  │ uri_hash     │  │                  │ │
                 │  │ random       │  │                  │ │
                 │  │ sticky       │  │                  │ │
                 │  └──────┬───────┘  └────────┬─────────┘ │
                 │         │                    │           │
                 │         ▼                    ▼           │
                 │  ┌──────────────────────────────────┐   │
                 │  │         Upstream Pool             │   │
                 │  │                                   │   │
                 │  │  ┌─────────┐  ┌─────────┐       │   │
                 │  │  │ :3000   │  │ :3001   │  ...  │   │
                 │  │  │ healthy │  │ healthy │       │   │
                 │  │  └─────────┘  └─────────┘       │   │
                 │  │                                   │   │
                 │  │  Health Check: GET /health         │   │
                 │  │  Interval: 30s, Rise: 2, Fall: 3 │   │
                 │  └──────────────────────────────────┘   │
                 │                                          │
                 │  ┌──────────────┐  ┌──────────────────┐ │
                 │  │   Canary      │  │    Mirror        │ │
                 │  │  10% → :4000 │  │  5% → mirror.com│ │
                 │  │  90% → pool  │  │  async, no-wait  │ │
                 │  └──────────────┘  └──────────────────┘ │
                 │                                          │
                 │  WebSocket: auto-detect Upgrade header   │
                 │  → hijack + bidirectional TCP tunnel     │
                 │                                          │
                 │  gRPC (proxy.grpc): h2c to a cleartext   │
                 │  backend via the stdlib HTTP/2 transport │
                 └──────────────────────────────────────────┘
```

`weighted` is not a separate algorithm — weights are built into round-robin, so
`algorithm: weighted` resolves to `RoundRobin`. Session affinity (`proxy.sticky`)
**layers over** the chosen algorithm rather than replacing it: `least_conn` plus
sticky still places new sessions by least-connections and pins them afterwards.

---

## PHP Integration

```
Domain Add (type: php)
  │
  ▼
┌─────────────────────────────────────┐
│  1. Detect PHP Versions              │
│     Scan: php-cgi, php-fpm binaries  │
│     Versions: 7.4, 8.0–8.4          │
│     Parse: php.ini, extensions       │
└──────────────┬──────────────────────┘
               ▼
┌─────────────────────────────────────┐
│  2. Auto-Assign FPM                  │
│     One FPM per PHP version          │
│     Multiple domains share FPM       │
│     Per-domain override possible     │
└──────────────┬──────────────────────┘
               ▼
┌─────────────────────────────────────┐
│  3. Start PHP-FPM (if not running)   │
│     Listen: 127.0.0.1:{auto-port}   │
│     Monitor: auto-restart on crash   │
│     Alert: webhook on failure        │
└──────────────┬──────────────────────┘
               ▼
┌─────────────────────────────────────┐
│  4. Request Handling                 │
│                                     │
│  try_files: $uri, $uri/, /index.php │
│  → resolve to .php file             │
│  → build FastCGI env (CGI standard) │
│  → send to PHP-FPM via pool         │
│  → read response (headers + body)   │
│  → X-Sendfile / X-Accel-Redirect    │
│  → WSOD detection (empty 200 → 500) │
│                                     │
│  Security per-request:              │
│  • open_basedir = docRoot + parent  │
│  • disable_functions enforced       │
│  • PHP_ADMIN_VALUE injection block  │
└─────────────────────────────────────┘

FastCGI Connection Pool:
  • Max idle: 10, Max open: 64
  • Stale check: 30s idle timeout
  • Max lifetime: 5 minutes
  • Retry on stale connection (GET/HEAD)
  • Domain PHP timeout propagated as deadline
```

---

## Authentication & Authorization

```
┌─────────────────────────────────────────────────────────────┐
│                    Auth Flow                                  │
│                                                              │
│  Request arrives at Admin API                                │
│       │                                                      │
│       ▼                                                      │
│  ┌──────────────────┐                                        │
│  │ Public endpoint?  │──yes──▶ /api/v1/health               │
│  │ (no auth needed)  │         /_uwas/dashboard/*            │
│  └────────┬─────────┘         /api/v1/apps/*/webhook (POST) │
│           │ no                                               │
│           ▼                                                  │
│  ┌──────────────────┐                                        │
│  │ API Key check     │  Authorization: Bearer <key>          │
│  │ (timing-safe)     │  crypto/subtle.ConstantTimeCompare    │
│  └────────┬─────────┘                                        │
│           │ valid                                            │
│           ▼                                                  │
│  ┌──────────────────┐                                        │
│  │ TOTP 2FA?         │  X-TOTP-Code header (if 2FA enabled) │
│  │ (30s window)      │  Rate-limited brute-force protection  │
│  └────────┬─────────┘                                        │
│           │ valid                                            │
│           ▼                                                  │
│  ┌──────────────────┐                                        │
│  │ Destructive op?   │  DELETE domain, uninstall, etc.       │
│  │ (pin required)    │  X-Pin-Code header verification       │
│  └────────┬─────────┘                                        │
│           │ valid                                            │
│           ▼                                                  │
│       Proceed                                                │
└──────────────────────────────────────────────────────────────┘

Multi-User Mode (optional):
  Roles: admin > reseller > user
  • admin: full access (all domains, system config)
  • reseller: assigned domains only
  • user: own domain only
  Session: POST /api/v1/auth/login → token (24h TTL)
  Per-user API keys for automation
```

---

## Middleware Chain

Only the left column is a middleware chain. The per-domain controls are guard
calls made inside `handleRequest`, in the order shown — the sole exception is
the IP ACL, which is pre-compiled per domain and stored in `s.domainChains`.

```
Global chain (server.go buildMiddlewareChain)   Per-domain, inside handleRequest:

  Recovery                                        IP ACL (whitelist/blacklist)
      │                                           GeoIP blocking
  RequestID (UUIDv7)                              Basic Auth
      │                                           CORS
  RealIP                                          WAF (SQL/XSS/shell/RCE)
      │                                           Location rate limit
  Security Headers                                Header transforms
      │                                           Hotlink protection
  Compress (br/gzip, per-domain policy)           Image optimization (WebP/AVIF)
      │                                           Per-domain access_log file
  Global Rate Limit (if configured)
      │
  SecurityGuard (blocked paths)   ← global, not per-domain
      │
  BotGuard                        ← global, not per-domain
      │
  AccessLog                       ← one line to the main log
      │
      ▼
  handleRequest()

Composition: Chain(A, B, C)(handler) → A(B(C(handler)))
```

**The chain is built once.** `buildMiddlewareChain` runs in `New` and is never
called again — reload does not rebuild it. A global setting that the chain
would otherwise capture at boot therefore has to be read per request through a
live value, not closed over. `global.access_log.enabled` uses an `atomic.Bool`
on the Server that reload swaps; `global.log_level` uses the logger's
`slog.LevelVar`. Anything new at this layer needs the same treatment or it will
silently require a restart.

---

## Configuration System

```yaml
# /etc/uwas/uwas.yaml

global:
  http_listen: ":80"
  https_listen: ":443"
  http3: true                    # QUIC/HTTP3 on :443 UDP
  max_connections: 10000
  web_root: "/var/www"

  admin:
    enabled: true
    listen: ":9443"
    api_key: "secret"
    pin_code: "1234"             # destructive ops
    totp_secret: "base32..."     # 2FA

  acme:
    email: "admin@example.com"
    storage: "/var/lib/uwas/certs"
    on_demand: true

  cache:
    enabled: true
    memory_limit: 256MB
    disk_path: "/var/lib/uwas/cache"
    disk_limit: 1GB
    default_ttl: 60

  backup:
    provider: "s3"               # local | s3 | sftp
    schedule: "24h"
    keep: 7

domains:
  - host: "example.com"
    aliases: ["www.example.com"]
    type: "php"                  # static | php | proxy | app | redirect
    root: "/var/www/example.com/public"
    ssl:
      mode: "auto"               # auto | manual | none
    php:
      fpm_address: ""            # auto-detect
      timeout: 300s
      max_upload: 100MB
    cache:
      enabled: true
      ttl: 3600
      esi: true
    security:
      waf: { enabled: true }
      rate_limit: { requests: 1000, window: "1m" }
    htaccess:
      mode: "import"             # import | disabled

  - host: "app.example.com"
    type: "proxy"
    proxy:
      upstreams: ["http://127.0.0.1:3000"]
      algorithm: "round-robin"
      health_check: { path: "/health", interval: "30s" }
      circuit_breaker: { threshold: 10, timeout: "30s" }

  - host: "node.example.com"
    type: "app"
    app:
      runtime: "node"
      command: "npm start"
      port: 0                    # auto-assign
      auto_restart: true
      env: { NODE_ENV: "production" }
    resources:
      cpu_percent: 50
      memory_mb: 512

include:
  - "domains.d/*.yaml"           # per-domain config files
```

### Hot Reload

```
PUT /api/v1/config/raw  →  Validate  →  Atomic swap  →  Reload all:
  • VHost router rebuild
  • TLS cert obtain (new domains)
  • PHP manager reassign
  • App manager register
  • Proxy pool rebuild + health checkers
  • Middleware chain rebuild (per-domain)
  • Cache rules rebuild
  • Rewrite cache invalidate
  • .htaccess cache invalidate
  • Logger level (global.log_level)
  • Request-log switch (global.access_log.enabled)
  Zero downtime — no connection drop
```

What reload does **not** rebuild is the global middleware chain — see the
Middleware Chain section. Settings read by that chain have to be swapped
through a live value; settings read inside `handleRequest` pick up the new
config automatically.

---

## Dashboard Pages (42)

Groups and labels below are the sidebar's own (`web/dashboard/src/components/Sidebar.tsx`).
Two pages are not in the sidebar: **Domain Detail** (`/domains/:host`, reached
from Domains) and **Login**.

```
┌─────────────────────────────────────────────────────┐
│  Overview                                            │
│  └─ Dashboard         Stats, health, graphs          │
├─────────────────────────────────────────────────────┤
│  Sites                                               │
│  ├─ Domains           Domain list + CRUD             │
│  ├─ Topology          Domain dependency graph        │
│  ├─ Certificates      SSL/TLS + ACME status          │
│  ├─ DNS               DNS zone editor                │
│  ├─ Cloudflare        CF settings, tunnels, purge    │
│  ├─ WordPress         One-click WP install           │
│  ├─ Clone / Staging   Site cloning                   │
│  ├─ Migration         Apache/Nginx import wizard     │
│  └─ File Manager      Web file browser/editor        │
├─────────────────────────────────────────────────────┤
│  Server                                              │
│  ├─ PHP               PHP versions + start/stop      │
│  ├─ PHP Config        php.ini editor per version     │
│  ├─ Applications      Standalone app deploy + ENV    │
│  ├─ Software Library  Docker Compose app catalogue   │
│  ├─ Database          MySQL/MariaDB + Docker mgmt    │
│  ├─ DB Explorer       Browser-based DB client        │
│  ├─ SFTP Users        Chroot users + SSH keys        │
│  ├─ Cron Jobs         Per-domain cron management     │
│  ├─ Services          systemd service control        │
│  ├─ Packages          OS package install/remove      │
│  ├─ IP Management     IP assignment                  │
│  └─ Email Guide       DNS record setup guide         │
├─────────────────────────────────────────────────────┤
│  Performance                                         │
│  ├─ Cache             Stats + purge + per-domain     │
│  ├─ Metrics           Request metrics + percentiles  │
│  ├─ Analytics         Traffic analytics + bandwidth  │
│  └─ Logs              Real-time log viewer (SSE)     │
├─────────────────────────────────────────────────────┤
│  Security                                            │
│  ├─ Security          WAF + bot guard stats          │
│  ├─ Firewall          UFW rule management            │
│  ├─ Unknown Domains   Block unconfigured hosts       │
│  ├─ Audit Log         Admin action history           │
│  └─ Admin Users       Multi-user management          │
├─────────────────────────────────────────────────────┤
│  System                                              │
│  ├─ Setup Wizard      First-run setup                │
│  ├─ Config Editor     Raw YAML editor + validation   │
│  ├─ Webhooks          Webhook management             │
│  ├─ Backups           Backup/restore + schedule      │
│  ├─ Terminal          WebSocket browser shell        │
│  ├─ Updates           Self-update from GitHub        │
│  ├─ Doctor            Diagnostics + auto-fix         │
│  ├─ Settings          Structured settings editor     │
│  └─ About             Version + system info          │
├─────────────────────────────────────────────────────┤
│  Not in the sidebar                                  │
│  ├─ Domain Detail     Individual domain config       │
│  └─ Login             API key + TOTP 2FA             │
└─────────────────────────────────────────────────────┘
```

---

## Security Model

```
┌───────────────────────────────────────────────────────────────┐
│                        Security Layers                         │
│                                                                │
│  Layer 1: Network                                              │
│  ├─ TLS 1.2+ with SNI (auto ACME certs); a domain can raise   │
│  │   its own floor via ssl.min_version                        │
│  ├─ HTTP/2 + HTTP/3 (QUIC)                                    │
│  ├─ Connection limiter (semaphore)                             │
│  └─ UFW firewall management                                   │
│                                                                │
│  Layer 2: Request Filtering                                    │
│  ├─ WAF: SQL injection, XSS, shell injection, RCE patterns   │
│  ├─ Bot guard: 25+ malicious scanner signatures               │
│  ├─ Rate limiting: per-IP token bucket (global + per-domain)  │
│  ├─ IP ACL: whitelist/blacklist per domain                    │
│  ├─ GeoIP: country-level blocking per domain                  │
│  ├─ Hotlink protection: referer-based                         │
│  └─ Unknown domain rejection: 421/403 before processing       │
│                                                                │
│  Layer 3: Application                                          │
│  ├─ Path traversal: checked in static, filemanager, X-Sendfile│
│  ├─ Symlink escape: EvalSymlinks + root check                 │
│  ├─ Domain validation: hostname regex rejects injection chars  │
│  ├─ PHP sandbox: open_basedir, disable_functions per domain   │
│  ├─ PHP_ADMIN_VALUE injection blocked (HTTP header filter)    │
│  ├─ WAF body scan: first 64KB, full body restored (no trunc.) │
│  └─ Deploy: SSH key path validation, Dockerfile sanitization  │
│                                                                │
│  Layer 4: Authentication                                       │
│  ├─ API key: timing-safe comparison (crypto/subtle)           │
│  ├─ TOTP 2FA: 30-second window, rate-limited                 │
│  ├─ Pin code: required for destructive operations             │
│  ├─ Webhook HMAC: SHA-256 signature verification              │
│  ├─ mTLS: per-domain client CA pool, require/request/none     │
│  ├─ Git token: redacted in error logs                         │
│  └─ Credentials: masked in dashboard (copy-only)             │
│                                                                │
│  Layer 5: Response                                             │
│  ├─ Security headers: X-Content-Type-Options, X-Frame-Options│
│  ├─ HSTS: Strict-Transport-Security                           │
│  ├─ Referrer-Policy: strict-origin-when-cross-origin          │
│  ├─ Permissions-Policy: geolocation=(), camera=()             │
│  └─ Config secrets: masked in GET /api/v1/settings            │
└───────────────────────────────────────────────────────────────┘
```

---

## Rewrite Engine

Apache mod_rewrite compatible (~95% compatibility):

```
Supported directives:
  RewriteEngine On/Off
  RewriteBase /path
  RewriteCond %{var} pattern [flags]
  RewriteRule pattern substitution [flags]

Supported variables:
  %{REQUEST_URI}          %{REQUEST_FILENAME}
  %{QUERY_STRING}         %{HTTP_HOST}
  %{HTTPS}                %{SERVER_PORT}
  %{REQUEST_METHOD}       %{THE_REQUEST}
  %{REMOTE_ADDR}          %{HTTP_USER_AGENT}
  %{HTTP_REFERER}         %{HTTP_COOKIE}

Supported conditions:
  -f   file exists         -d   directory exists
  -l   symlink exists      -s   file has size > 0
  !-f  file not exists     !-d  directory not exists

Supported flags:
  [L]   last rule          [R=301]  redirect
  [NC]  no case            [QSA]    query string append
  [NE]  no escape          [END]    stop processing

Loop detection: max 10 iterations
```

---

## Data Flow Diagrams

### Domain Add Flow

```
Dashboard: Add Domain
      │
      ▼
POST /api/v1/domains
      │
      ▼
┌──────────────┐
│ Validate host│ hostname regex, no duplicates
└──────┬───────┘
       ▼
┌──────────────┐
│ Write config │ domains.d/{host}.yaml (atomic: temp + rename)
└──────┬───────┘
       ▼
┌──────────────┐
│ Hot reload   │ VHost router + TLS + PHP + middleware
└──────┬───────┘
       ▼
┌──────────────┐
│ ACME cert    │ Auto-issue if ssl.mode=auto
└──────┬───────┘
       ▼
┌──────────────┐
│ PHP assign   │ Start FPM if type=php
└──────┬───────┘
       ▼
┌──────────────┐
│ App routing  │ proxy domains resolve apps://<name>
└──────┬───────┘
       ▼
  201 Created + webhook event
```

### Backup Flow

```
POST /api/v1/backups/create
      │
      ▼
┌───────────────────────────────────────┐
│  Collect:                              │
│  • /etc/uwas/*.yaml (config)          │
│  • /var/lib/uwas/certs/ (TLS certs)   │
│  • /var/www/* (all domain roots)       │
│  • mysqldump (all databases)           │
│  • cron jobs, SFTP users               │
└──────────────┬────────────────────────┘
               ▼
┌───────────────────────────────────────┐
│  Package: tar.gz with metadata.json   │
│  (file permissions, ownership, sizes)  │
└──────────────┬────────────────────────┘
               ▼
┌───────────────────────────────────────┐
│  Upload to provider:                   │
│  • local: /var/lib/uwas/backups/      │
│  • s3: bucket/uwas-backups/           │
│  • sftp: remote:/backups/             │
│                                        │
│  Retention: keep last N (configurable) │
└───────────────────────────────────────┘
```

---

## Dependencies

```
Direct (5 only — stdlib-first philosophy):

  gopkg.in/yaml.v3          YAML config parsing
  github.com/andybalholm/brotli   Brotli compression
  github.com/quic-go/quic-go     HTTP/3 (QUIC)
  golang.org/x/crypto             bcrypt, acme
  golang.org/x/sync               errgroup, semaphore

No web frameworks. No ORMs. No logging frameworks.
net/http + log/slog + crypto/subtle + encoding/json.
```

---

## Build & Deploy

```bash
# Production build (stripped, versioned)
make build
# → bin/uwas (~16.4MB, linux/amd64)

# Development build
make dev

# Cross-compile
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -ldflags="-s -w" -o bin/uwas ./cmd/uwas

# Dashboard rebuild (embedded via go:embed)
cd web/dashboard && npm run build

# Test
go test ./...              # 56 packages carry tests, run in parallel
go test ./... -race        # what CI gates on

# Install (one-time)
uwas install                    # creates dirs, systemd unit, config

# Run
uwas serve -c /etc/uwas/uwas.yaml

# Hot reload (zero downtime)
uwas reload
```

The 19 CLI commands are: `backup` `cache` `cert` `config` `doctor` `domain`
`help` `install` `migrate` `php` `reload` `restart` `restore` `serve` `status`
`stop` `uninstall` `user` `version`.

Self-update has no CLI command. `internal/selfupdate` is reached from the
admin API (`/api/v1/system/update`) and the dashboard's Updates page.

---

## Testing

```
56 packages with tests, 284 test files, ~7,000 test functions

Test approach:
  • Unit tests alongside source: foo.go → foo_test.go
  • Testable hooks: exec.Command, os.Stat wrapped for mocking
  • Mock FastCGI server for handler tests
  • Mock HTTP server for proxy tests
  • Parallel execution: go test (Docker tests use unique ports/PIDs)
  • Pre-push hook: go vet + build + tsc + test (automated)

Key test areas:
  • Config validation (invalid hosts, ports, paths, types)
  • VHost routing (exact, wildcard, fallback, unknown)
  • Cache (LRU eviction, TTL, grace, L2 promotion, ESI)
  • Rewrite engine (conditions, backreferences, loops)
  • FastCGI protocol (record encoding, WSOD detection)
  • Proxy (retry, circuit breaker, WebSocket)
  • Middleware (WAF patterns, rate limiting, CORS, bot guard)
  • Auth (API key timing-safe, TOTP, permissions)
  • TLS (cert loading, SNI selection, renewal)
  • PHP lifecycle (detect, install, start, assign, restart)
  • Database (create, drop, export, import, Docker)
  • Deploy (git clone, build, Docker, token redaction)
```
