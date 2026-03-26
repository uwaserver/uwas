# Changelog

All notable changes to UWAS will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.8.0] - 2026-03-26

### Highlights

**Dual licensing, massive test coverage push, doctor & database hardening.** 50,000+ lines of new tests across 30+ packages, AGPL-3.0 + commercial dual license, MariaDB auto-repair, and multi-user auth improvements.

### License

- **Dual licensing** — AGPL-3.0 for open-source community use, commercial license available for enterprise/proprietary use
- Updated LICENSE, README, CONTRIBUTING, and docs site footer

### New Features

- **DB repair & force uninstall** — `POST /api/v1/database/repair`, `DELETE /api/v1/database/uninstall?force=true` for broken MariaDB installations
- **Doctor: MariaDB auto-repair** — Detects and fixes corrupt InnoDB tablespace, broken permissions, stale PID files
- **Doctor: system checks** — Memory usage, open file descriptors, NTP clock sync diagnostics
- **Login upgrade** — Multi-user auth flow with role-aware session handling
- **Settings: notification channels** — Configure webhook/Slack/Telegram/email notification destinations from dashboard

### Test Coverage (~50,000 new lines)

New test files and major expansions across 30+ packages:

- `internal/admin` — 3,528 lines: API endpoint coverage (domains, PHP, cache, backup, cron, firewall)
- `internal/cli` — 4,464 lines: all CLI commands (install, stop, conflicts, pidcheck, user)
- `internal/sftpserver` — 3,435 lines: SFTP protocol, chroot, permissions, SSH key auth
- `internal/phpmanager` — 3,038 lines: PHP detect, install, start/stop, config, auto-restart
- `internal/wordpress` — 2,646 lines: install, permissions, mu-plugin, wp-config generation
- `internal/server` — 5,149 lines: HTTP/HTTPS dispatch, middleware chain, graceful shutdown
- `internal/migrate` — 2,339 lines: clone, site migration, SSH transfer
- `internal/siteuser` — 1,118 lines: user CRUD, chroot setup, SSH key management
- `internal/auth` — 1,549 lines: RBAC, sessions, API keys, TOTP 2FA, persistence
- `internal/cronjob` — 1,449 lines: cron CRUD, execution, monitoring, failure alerts
- `internal/database` — 1,807 lines: MySQL/MariaDB management + Docker container tests
- `internal/doctor` — 1,559 lines: diagnostics, auto-fix, PHP/permissions/config/ports
- `internal/backup` — 1,357 lines: local/S3/SFTP backup + restore
- `internal/bandwidth` — 1,605 lines: throttle/block, daily/monthly limits
- `internal/tls` — 2,275 lines: SNI routing, ACME client, JWS signing, cert storage
- `internal/dnsmanager` — 2,261 lines: Cloudflare, DigitalOcean, Hetzner, Route53
- `internal/selfupdate` — 712 lines: GitHub release check, download, binary swap
- `internal/serverip` — 984 lines: interface detection, public IP lookup
- `internal/firewall` — 601 lines: UFW rule management
- `internal/notify` — 490 lines: webhook, Slack, Telegram, email channels
- `internal/handler/*` — 1,714 lines: FastCGI, proxy, static handler edge cases
- `internal/middleware` — 848 lines: chain composition, WAF, image optimization
- `internal/router` — 937 lines: vhost routing, unknown domain tracking
- `internal/config` — 829 lines: YAML parsing, Duration/ByteSize types, validation
- `internal/webhook` — 456 lines: event delivery, HMAC signing, retry
- `pkg/fastcgi` — 436 lines: binary protocol, connection pool
- `pkg/htaccess` — 393 lines: parser directives, IfModule, RewriteCond

### Bug Fixes

- **CLI install** — Fixed error handling in package installation flow
- **CLI stop** — Improved PID file cleanup on graceful shutdown
- **CLI conflicts** — Better port conflict detection and reporting
- **Cronjob monitor** — Fixed race condition in concurrent job execution tracking
- **Database manager** — Hardened connection error handling, added timeout for stale connections
- **DNS checker** — Fixed edge case in CNAME chain resolution
- **DNS providers** — Consistent error handling across Cloudflare, DigitalOcean, Hetzner, Route53
- **Doctor** — Expanded diagnostic checks with actionable fix suggestions
- **File manager** — Path traversal guard strengthened for symlink edge cases
- **Firewall** — Improved UFW rule parsing for complex CIDR ranges
- **Image optimization** — Added nil check for missing Accept header
- **Migrate/clone** — Fixed SSH key auth and database dump error propagation
- **Notify channels** — Fixed timeout handling for slow webhook endpoints
- **PHP manager** — Improved version detection and FPM socket path resolution
- **Self-update** — Fixed GitHub API rate limit handling and checksum verification
- **Server IP** — Improved interface filtering for virtual/docker bridges
- **Services** — Better systemd unit file parsing and status detection
- **Site user** — Fixed SSH key format validation and chroot directory permissions
- **TLS/ACME** — Improved retry logic for DNS-01 challenge propagation
- **WordPress** — Fixed wp-config.php generation for non-standard DB prefixes

### Stats

- **44 test packages**, all passing, 0 failures
- **50,000+** new lines of test code
- **30+** packages with expanded coverage
- **83 files** changed in this release

## [1.3.1] - 2026-03-23

### Highlights

**Dead code audit & feature activation.** 2,500+ lines of dead code removed, 9 config-backed features activated, 8 bugs fixed, daemon mode added.

### New Features

- **Daemon mode** — `uwas serve -d` starts server as background process (cross-platform)
- **Per-domain CORS** — `cors.enabled`, allowed origins/methods/headers per domain
- **Per-domain BasicAuth** — `basic_auth.enabled`, username/password per domain
- **Per-domain IP ACL** — `security.ip_whitelist` / `ip_blacklist` now enforced
- **Per-domain header transforms** — `headers.response_add` / `request_add` applied per request
- **Circuit breaker** — `proxy.circuit_breaker.threshold` trips after N failures, auto-recovery
- **Canary routing** — `proxy.canary.enabled` routes % of traffic to canary upstreams
- **Image optimization** — `image_optimization.enabled` serves pre-converted WebP/AVIF
- **Custom error pages** — `error_pages.404: /404.html` serves per-domain error pages
- **MCP API endpoints** — `GET /api/v1/mcp/tools`, `POST /api/v1/mcp/call` in admin API
- **Domain edit** — Edit button in dashboard domain table, pre-filled form with updateDomain API
- **PHP dropdown** — FPM address field auto-detects installed PHP versions

### Bug Fixes

- **Proxy retry bug** — `netErr.Timeout() || true` always retried; fixed to `return true` for all net.Error
- **Config editor crash** — Raw config API returned YAML but frontend expected JSON; wrapped in `{"content": "..."}`
- **Rate limiter blocked dashboard** — Public endpoints (health, dashboard) now exempt from rate limiting
- **SSE auth** — EventSource token via query param support added (browser can't set headers)
- **Dashboard toFixed crash** — Latency cards null-safe when stats fields undefined
- **Response header timing** — Per-domain headers set before handler dispatch, not deferred
- **E2e test locators** — Strict mode violations fixed with exact text matchers

### Dead Code Removed (~2,500 LOC)

- `internal/server/upgrade.go` — Unused GracefulRestart/DrainAndWait (duplicated shutdown logic)
- `internal/logger/accesslog.go` — Unused AccessLogger subsystem (server uses slog middleware)
- Old nginx migration code in `internal/cli/migrate.go` (superseded by `internal/migrate/`)
- Alerter methods DomainDown/CertExpiry/RecordRateLimit (implemented but never wired)
- Handler Name()/Description()/CanHandle() methods (never called from server dispatch)
- Analytics Record() wrapper, requestsInWindow, ActiveDomains() (test-only)
- Dead constants: StatusBypass, shardCount, ToolList struct
- Redundant CustomHeaders middleware (HeaderTransform already covers it)
- Frontend: unused PHP API functions, phantom react-router-dom dependency

### Improvements

- `go mod tidy` fixed mislabeled indirect deps (brotli, quic-go, x/crypto)
- All API wrapper functions exported in frontend api.ts (monitor, alerts, MCP, cache stats)
- Cache page uses api.ts wrapper instead of direct fetch
- CacheStatsData interface moved to shared api.ts
- CLAUDE.md updated with per-domain middleware docs, coverage stats
- 21+ new backend tests, 29 e2e tests passing

### Stats

- **1,718 tests** across 27 packages, 88.6% coverage
- **29/29 Playwright e2e tests** passing
- **0 JS errors** in dashboard
- **0 TODO/FIXME** remaining in codebase

## [1.3.0] - 2026-03-22

### Highlights

**1,728 tests, 93%+ average coverage, 0 failures.** 27 packages, 17k lines of Go source.

### New Features

- **Backup/Restore** — Local filesystem, S3 (AWS SigV4), SFTP over SSH; scheduled backups with auto-pruning
- **HTTP/3 (QUIC)** — via quic-go with Alt-Svc header advertisement
- **WebSocket Proxy** — TCP hijack + bidirectional tunneling for real-time apps
- **Audit Logging** — 500-entry ring buffer tracking all admin actions with timestamps/IPs
- **Latency Metrics** — p50/p95/p99/max percentiles via Prometheus endpoint
- **Slow Request Logging** — WARN-level log for requests exceeding configurable threshold
- **Per-domain PHP** — Multiple PHP versions per domain, auto-port assignment, php.ini editing
- **Nginx/Apache Migration** — `uwas migrate nginx/apache <file>` converts configs to UWAS YAML
- **W3C Trace Context** — traceparent header propagation through reverse proxy
- **Per-handler Metrics** — uwas_requests_by_handler{handler=static/php/proxy/redirect}
- **Connection Limiter** — Reject with 503 when at max capacity
- **System Info API** — GET /api/v1/system (Go version, OS, arch, CPUs, goroutines, memory)

### Dashboard (15 pages)

- **Backups page** — Create/restore/delete with provider selection + scheduling
- **Audit Log page** — Filterable action history with color-coded badges
- **Analytics enhanced** — Referrer tracking + user agent breakdown charts
- **Dashboard** — Latency cards (p50/p95/p99), dual-axis chart with p95 line
- **Settings** — Real system info (Go version, CPUs, goroutines, memory, GC)
- **Config Editor** — In-memory fallback when domain files don't exist

### Security Hardening

- **Admin API rate limiting** — 10 failed auths in 1 minute triggers 5-minute IP block
- **Config validation expanded** — 300+ lines: CIDRs, ports, URLs, regexes, enums, file existence
- **Slowloris protection** — ReadHeaderTimeout (10s), MaxHeaderBytes (1MB)
- **Graceful shutdown** — Connection draining with configurable grace period

### CLI / UX

- **First-run experience** — Auto-config creation in ~/.uwas/, interactive port setup
- **Startup banner** — ASCII art, version, listeners, features, dashboard URL
- **Zero-arg launch** — `uwas` without arguments auto-starts server

### Bug Fixes

- Domain create: SSL, proxy, redirect, WAF payload structures fixed
- Config editor: domain raw GET falls back to in-memory config
- Domain file path: port in hostnames sanitized for filesystem
- Analytics page crash: match actual API response format
- PHP-FPM HTTP_HOST: set from r.Host, not r.Header
- Cache bypass: wp-admin/wp-login session cookie detection

---

## [1.0.0] - 2026-03-22

### Highlights

UWAS v1.0.0 is a feature-complete, production-ready web server that replaces
Apache + Nginx + Varnish + Caddy with a single 13MB Go binary.

**818 tests, 88% coverage, 0 failures.** WordPress 6.9.4 verified.

### Server

- Auto HTTPS with Let's Encrypt ACME client
- Built-in L1 memory + L2 disk cache with grace mode
- PHP-FPM via FastCGI with .htaccess support
- Reverse proxy with 5 load balancing algorithms
- Circuit breaker + health checks + retry logic
- A/B testing / canary routing with cookie stickiness
- Brotli + gzip on-the-fly compression
- URL rewrite engine (Apache mod_rewrite compatible)
- WAF (SQL injection, XSS, path traversal detection)
- Rate limiting (token bucket, per-IP)
- IP whitelist/blacklist (CIDR)
- Basic authentication per-path
- Security headers (HSTS, CSP, X-Frame, CORS)
- Request/response header transforms with variable substitution
- Automatic image optimization (WebP/AVIF serving)
- SPA mode + try_files + directory listing
- Custom error pages per domain
- ETag + 304 Not Modified + Range requests
- Pre-compressed file serving (.br, .gz)
- HTTP/2 via Go stdlib
- SIGHUP config reload (zero-downtime)
- Configurable listen addresses
- Trusted proxies for X-Forwarded-For
- Log rotation (size-based + SIGHUP reopen)
- Systemd service file
- Alerting (webhook + internal ring buffer)
- Uptime monitoring per domain
- Request mirroring (shadow traffic)

### Dashboard (React 19 + Tailwind 4.1)

- 11 pages: Login, Dashboard, Domains, Topology, Cache, Logs,
  Settings, Metrics, Analytics, Config Editor, Certificates
- Domain templates: WordPress, Static, Proxy, Redirect (one-click setup)
- Real-time stats via Server-Sent Events
- Cache management: charts, per-domain view, tag/domain/all purge
- YAML config editor with syntax validation
- SSL certificate timeline with expiry tracking
- Per-domain analytics with traffic charts
- Topology graph with React Flow

### CLI (15 commands)

- `serve`, `version`, `help`
- `config validate/test`
- `domain list/add/remove`
- `cache stats/purge`
- `status`, `reload`
- `migrate nginx/apache <file>`
- `backup`, `restore`

### API (22+ endpoints)

- Health, stats, config, domains CRUD, domain detail
- Cache stats/purge, logs, metrics, SSE live stats
- Certificates, analytics, monitor
- Config raw read/write, domain raw read/write
- Config export (YAML download)
- Alerts

### Configuration

- Single YAML file or split per-domain files (domains.d/)
- Include patterns (glob)
- Environment variable expansion with fallback
- Hot reload via SIGHUP or API

### Security (28 fixes from code review)

- Shared http.Transport (no connection leak)
- Config race mutex, admin CRUD mutex
- RealIP spoofing prevention
- On-demand TLS rate limiting
- Cache key collision fix (full canonical keys)
- Goroutine leak prevention (context-based)
- Request body limits, secret stripping
- WAF URL-decode bypass fix
- Open redirect fix, path traversal validation

### Docker

- Multi-stage Alpine build: 28.5MB image
- docker-compose: UWAS + PHP-FPM + MariaDB
- One-command VPS setup script

### Performance (AMD Ryzen 9 9950X3D)

- Static file: 7,000 req/sec
- Cache L1 hit: 75,000,000 ops/sec
- VHost routing: 70,000,000 ops/sec
- Middleware chain: 308,000 req/sec

## [0.3.0] - 2026-03-22

### Security

- **RealIP spoofing fix**: Proxy headers only trusted when direct connection is from a configured trusted proxy
- **On-demand TLS hardened**: OnDemandAsk URL validation + rate limit (10 certs/minute)
- **CORS restricted**: No more wildcard `*` origin — validates against dashboard/localhost origins only
- **Open redirect fixed**: HTTPS redirect uses canonical `domain.Host` instead of raw `Host` header
- **Dotfile protection**: Checks all path components, not just filename (blocks `/.git/config`)
- **Path traversal**: Fallback try_files path validated against document root
- **Config export sanitized**: Strips DNS credentials, PHP env vars, cache purge key
- **Admin API body limits**: All mutation endpoints limited to 1MB request body
- **WAF double-decode**: Checks URL-decoded query strings to catch encoded attacks

### Fixed

- **Transport leak**: Shared `http.Transport` across proxy requests (was creating one per request)
- **Config race condition**: RWMutex protects config during hot reload
- **Admin CRUD race**: RWMutex protects domain list during add/update/delete
- **Response capture OOM**: Limited to 10MB max body for caching (prevents memory exhaustion)
- **Cache key collision**: Uses full canonical key string (method|host|path|query|vary) instead of hash
- **Goroutine leaks**: Cache cleanup and rate limiter accept context.Context for proper shutdown
- **Disk cache accounting**: Scans existing files on startup to initialize byte counter
- **ACME challenge**: Polls correct challenge URL (was hardcoded to index 0)
- **ETag 304 from cache**: Conditional requests handled against cached ETag
- **Chunked POST**: FastCGI forwards chunked transfer-encoding bodies
- **io.Copy error**: Proxy logs upstream body copy failures
- **Memory aliasing**: Cache deserialize copies body slice

### Performance

- **htaccess caching**: Parsed once per domain root, not on every request
- **Rewrite precompilation**: Regex rules compiled at server init, not per request
- **Nonce pool capped**: ACME nonce pool limited to 10 entries
- **Request context zeroed**: Full struct zero on pool acquire prevents data leak

## [0.2.0] - 2026-03-22

### Added

- **Configurable listen addresses**: `http_listen` and `https_listen` fields in global config
- **Trusted proxies**: `trusted_proxies` CIDR list for X-Forwarded-For real IP extraction
- **.htaccess runtime import**: Parse and apply WordPress/Laravel .htaccess rewrites with proper -f/-d condition checks
- **Directory listing**: Per-domain `directory_listing: true` toggle with HTML table output
- **WAF URL decode**: WAF patterns now check both raw and URL-decoded query strings
- **Admin /health public**: Health endpoint no longer requires authentication
- **Config hot reload**: Live config reload via `POST /api/v1/reload` with document root change support
- **Install script**: `curl -fsSL https://uwaserver.com/install.sh | sh` for Linux/macOS
- **Benchmark suite**: Static file, vhost lookup, middleware chain, cache get/set benchmarks
- **Comprehensive integration tests**: Cache store/hit, rate limiting, multi-domain routing, backend failover, CORS, config reload

### Fixed

- .gitignore pattern `uwas` was blocking `cmd/uwas/` directory
- Dockerfile and CI workflows updated from Go 1.23 to Go 1.26
- GoReleaser docker build removed (binary-only releases)
- Gzip middleware now skips conditional requests (If-None-Match → 304 works correctly)
- Rate limiter correctly wired from per-domain security config

### Changed

- Server ports no longer hardcoded to :80/:443 — fully configurable
- Full middleware chain wired: recovery → request ID → real IP → security headers → gzip → rate limit → WAF → access log
- All documentation translated to English
- Logo and banner assets added

### Performance (AMD Ryzen 9 9950X3D)

- VHost routing: 70M ops/sec
- Cache L1 get: 75M ops/sec
- Middleware chain: 308K req/sec
- Static file serve: 10K req/sec

## [0.1.0] - 2026-03-21

### Added

- **Core Server**
  - HTTP/HTTPS dual listener with graceful shutdown
  - Signal handling (SIGINT, SIGTERM)
  - PID file management
  - Worker count configuration (auto = CPU cores)

- **Configuration**
  - YAML config parser with environment variable expansion (`${VAR}`, `${VAR:-default}`)
  - Semantic validation (duplicate hosts, missing roots, invalid types)
  - Duration parsing (`30s`, `5m`, `1h`) and byte size parsing (`512MB`, `10GB`)
  - Full annotated example config (`uwas.example.yaml`)

- **Virtual Hosting**
  - Exact host matching (O(1) map lookup)
  - Wildcard matching (`*.example.com`)
  - Alias support
  - Default fallback to first domain

- **Static File Serving**
  - ETag generation and `304 Not Modified` support
  - `Range` requests (`Accept-Ranges: bytes`)
  - Pre-compressed file serving (`.br`, `.gz`)
  - SPA mode (fallback to `index.html`)
  - `try_files` logic (`$uri`, `$uri/`, index resolution)
  - 100+ MIME type mappings
  - Path traversal protection
  - Dotfile blocking

- **TLS / HTTPS**
  - ACME client (RFC 8555) with HTTP-01 challenge
  - Automatic certificate issuance from Let's Encrypt
  - SNI-based certificate selection (exact + wildcard)
  - Manual certificate loading
  - Background auto-renewal (12h check, 30d threshold)
  - HTTP to HTTPS redirect with HSTS
  - TLS 1.2+ with modern cipher suites
  - ALPN: `h2`, `http/1.1`

- **FastCGI / PHP**
  - FastCGI binary protocol implementation
  - Connection pooling (configurable max idle/open/lifetime)
  - Full CGI environment variable builder
  - `SCRIPT_NAME` / `PATH_INFO` splitting
  - Per-domain FPM pool support
  - Response header forwarding

- **URL Rewrite Engine**
  - Apache mod_rewrite compatible rules
  - Regex pattern matching with backreferences (`$1`, `%1`)
  - Rewrite conditions (`-f`, `-d`, `!-f`, `!-d`, regex, OR chaining)
  - Flags: `[L]`, `[R=301]`, `[QSA]`, `[NC]`, `[F]`, `[G]`, `[C]`, `[S=N]`
  - Server variable expansion (`%{REQUEST_URI}`, `%{HTTP_HOST}`, etc.)
  - Loop detection (max 10 internal rewrites)

- **.htaccess Support**
  - Parser for Apache .htaccess files
  - Directive converter: RewriteRule, RewriteCond, Redirect, RedirectMatch,
    ErrorDocument, DirectoryIndex, Header, Options, Auth, ExpiresActive
  - Block handling: `<IfModule>`, `<FilesMatch>`, `<Files>`
  - Line continuation and quoted string support

- **Middleware Stack**
  - Panic recovery with stack trace logging
  - UUID v7 request ID generation (preserves incoming)
  - Real IP extraction (X-Forwarded-For, X-Real-IP, CF-Connecting-IP)
  - Token bucket rate limiter (256-shard, per-IP, auto-cleanup)
  - Gzip compression (min size threshold, content type filter)
  - Security headers (HSTS, X-Content-Type-Options, X-Frame-Options, Referrer-Policy)
  - CORS handler (preflight, credentials, configurable origins)
  - Security guard (blocked paths, basic WAF: SQLi, XSS, path traversal)
  - Structured access logging (JSON)

- **Cache Engine**
  - L1 memory cache (256-shard LRU with memory limit)
  - L2 disk cache (hash-based directory sharding)
  - Grace mode (serve stale while revalidating)
  - Tag-based purge
  - Full purge
  - Cache bypass rules (POST, no-cache, configured paths)
  - `X-Cache` and `Age` response headers
  - Binary serialization for disk persistence

- **Reverse Proxy & Load Balancer**
  - 5 algorithms: Round Robin, Least Connections, IP Hash, URI Hash, Random (P2C)
  - Backend health checking (configurable interval, threshold, rise)
  - Circuit breaker (Closed → Open → Half-Open state machine)
  - Proxy headers (X-Forwarded-For, X-Forwarded-Proto, X-Real-IP)
  - Hop-by-hop header stripping
  - WebSocket upgrade detection
  - Per-backend connection tracking and metrics

- **Admin API**
  - REST API: health, stats, domains, config, metrics, reload, cache purge
  - Bearer token authentication
  - Prometheus text format metrics endpoint

- **MCP Server**
  - Tool-based interface: domain_list, stats, config_show, cache_purge

- **CLI**
  - `uwas serve` — Start server
  - `uwas version` — Print version info
  - `uwas config validate` — Validate config file
  - `uwas config test` — Show parsed config details
  - `uwas help` — Usage information

- **Operations**
  - Styled HTML error pages (400, 403, 404, 500, 502, 503, 504)
  - Dockerfile (multi-stage build, Alpine runtime)
  - Makefile (build, dev, test, lint, clean)

[0.2.0]: https://github.com/uwaserver/uwas/releases/tag/v0.2.0
[0.1.0]: https://github.com/uwaserver/uwas/releases/tag/v0.1.0
