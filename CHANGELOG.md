# Changelog

All notable changes to UWAS will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

[0.1.0]: https://github.com/uwaserver/uwas/releases/tag/v0.1.0
