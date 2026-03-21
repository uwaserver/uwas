# UWAS — Task Breakdown

> Each task is atomic, testable, and independently committable.
> Format: `[SPRINT-TASK] Description (estimated duration)`
> Status: ⬜ Todo | 🔄 In Progress | ✅ Done | ⏸️ Blocked

---

## Sprint 1: Skeleton (3 days)

### S1-01: Project Bootstrap ⬜ (2 hours)
- `go mod init github.com/uwaserver/uwas`
- Create directory structure: `cmd/uwas/`, `internal/`, `pkg/`
- `go.mod` + initial dependencies (`gopkg.in/yaml.v3`)
- `.gitignore`, `LICENSE` (Apache 2.0), empty `README.md`
- `Makefile` (build, dev, test, lint, clean targets)
- **Test**: `go build ./...` completes without errors

### S1-02: Version & Build Info ⬜ (1 hour)
- `cmd/uwas/version.go` — Version, Commit, BuildDate injected via ldflags
- `uwas version` command: prints version, commit, build date, Go version, OS/arch
- **Test**: `go build -ldflags="-X main.Version=test" && ./uwas version` produces correct output

### S1-03: CLI Framework ⬜ (3 hours)
- `internal/cli/root.go` — minimal CLI dispatcher (stdlib `flag` + custom subcommand router)
- Subcommand structure: `uwas <command> [flags] [args]`
- `uwas serve`, `uwas version`, `uwas help` commands
- Flag parsing: `-c` (config path), `-d` (daemon), `--log-level`
- Global `--help` and per-command `--help`
- **Test**: `uwas help` lists all commands, unknown command returns error

### S1-04: Structured Logger ⬜ (2 hours)
- `internal/logger/logger.go` — `log/slog` wrapper
- JSON and text format support
- Log level: debug, info, warn, error
- `StdLogger()` method — for `net/http.Server.ErrorLog` compatibility
- Helper: `WithFields(key, value...)` — contextual logging
- **Test**: JSON and text output verified at each level

### S1-05: Config Parser ⬜ (4 hours)
- `internal/config/config.go` — main Config struct
- `internal/config/loader.go` — YAML file reading, parsing, env var expansion (`${VAR}`)
- `internal/config/validate.go` — semantic validation (port ranges, path existence, etc.)
- `internal/config/defaults.go` — default values (timeouts, worker count, etc.)
- Nested structs: `GlobalConfig`, `DomainConfig`, `CacheConfig`, `ACMEConfig`, `ProxyConfig`
- Duration parsing: "30s", "5m", "1h"
- Byte size parsing: "512MB", "10GB"
- **Test**: Valid YAML is parsed, invalid config is rejected, env vars are expanded

### S1-06: Basic HTTP Server ⬜ (3 hours)
- `internal/server/server.go` — Server struct, Start(), Stop()
- Listen on port 80, return "UWAS is running" for all requests
- Signal handling: SIGTERM/SIGINT → graceful shutdown
- PID file write/delete
- **Test**: Server starts, returns HTTP response, shuts down cleanly on SIGTERM

### S1-07: uwas.example.yaml ⬜ (1 hour)
- Fully annotated example config file
- Each field with explanatory comment
- Two example domains: static site + PHP site
- **Output**: Copy-paste ready reference config

**Sprint 1 Exit Criteria**: `uwas serve -c uwas.example.yaml` runs, returns response on port 80, shuts down on SIGTERM, config is parsed and logged.

---

## Sprint 2: Static File Serving (3 days)

### S2-01: Request Context ⬜ (2 hours)
- `internal/router/context.go` — RequestContext struct
- `internal/router/response.go` — ResponseWriter wrapper (status tracking, bytes counting, TTFB)
- Hijack() and Flush() support
- Context pool (sync.Pool) to reduce allocation
- **Test**: ResponseWriter correctly tracks status/bytes

### S2-02: Virtual Host Router ⬜ (3 hours)
- `internal/router/vhost.go` — VHostRouter struct
- Exact match → alias match → wildcard match → default fallback
- Wildcard matching: `*.example.com` → longest match first
- O(1) exact lookup (map), O(n) wildcard (sorted slice, potential binary search)
- Thread-safe: concurrent read, exclusive write (config reload) via `sync.RWMutex`
- **Test**: Correct routing with 100 domains, wildcard priority, default fallback

### S2-03: MIME Type Registry ⬜ (1 hour)
- `internal/handler/static/mime.go` — extension → content-type mapping
- 100+ modern formats: .woff2, .webp, .avif, .wasm, .mjs, .webm, etc.
- Custom MIME type override (from config)
- Charset default: UTF-8 (text/* types)
- **Test**: All common extensions return correct MIME type

### S2-04: Static File Handler ⬜ (4 hours)
- `internal/handler/static/handler.go` — StaticHandler struct
- Serving via `http.ServeContent` (includes sendfile, range, conditional GET)
- ETag generation: weak ETag (mtime + size hash)
- `If-None-Match` → 304 Not Modified
- `Accept-Ranges: bytes` header
- Index file resolution (index.html, index.htm)
- Security: path traversal protection (symlink + `..` check)
- Block access to files starting with `.` (default)
- **Test**: Normal file serve, 304, range request, path traversal attempt reject

### S2-05: Pre-Compressed File Serving ⬜ (2 hours)
- If `.gz` or `.br` file exists, serve based on Accept-Encoding
- Priority: brotli > gzip > original
- Set Content-Encoding header correctly
- Vary: Accept-Encoding header
- **Test**: When `.br` file exists, it is served to clients supporting brotli

### S2-06: Directory Listing ⬜ (2 hours)
- `internal/handler/static/listing.go` — optional directory listing
- Default: disabled (security)
- Can be enabled per-domain via config
- Minimal HTML template: file name, size, date
- Symlink display option
- **Test**: Enabled → returns HTML listing, disabled → 403

### S2-07: SPA Mode ⬜ (1 hour)
- Config flag: `spa_mode: true`
- If file not found → `index.html` fallback (for React/Vue/Angular SPAs)
- Serve index.html instead of 404, client-side routing takes over
- **Test**: `/nonexistent/path` → returns index.html content

### S2-08: try_files Logic ⬜ (3 hours)
- `internal/server/dispatch.go` — resolvePath() implementation
- Variable expansion: `$uri`, `$uri/`, named paths
- Per-domain configurable try_files
- Default: type=static → `$uri, $uri/, $uri/index.html`
- Default: type=php → `$uri, $uri/, /index.php`
- **Test**: WordPress-style fallback, SPA fallback, static site fallback

**Sprint 2 Exit Criteria**: Multi-domain static file serving works. `uwas serve` serves static files for two different domains. ETag, Range, and pre-compressed file support active.

---

## Sprint 3: TLS & ACME (4 days)

### S3-01: Certificate Storage ⬜ (2 hours)
- `internal/tls/storage.go` — cert/key storage on disk
- Directory structure: `/var/lib/uwas/certs/{domain}/`
- `cert.pem`, `key.pem`, `meta.json` (issuer, expiry, created)
- File permissions: 0600 (key), 0644 (cert)
- Lock file for concurrent write protection
- **Test**: Save/load roundtrip, file permissions correct

### S3-02: Certificate Manager ⬜ (3 hours)
- `internal/tls/manager.go` — Manager struct
- `GetCertificate(hello)` — SNI-based cert selection
- Exact match → wildcard match → on-demand → default
- Cert loading at startup: load all certs from disk
- Thread-safe cert store via `sync.Map`
- Manual cert support: cert/key path from config
- **Test**: SNI routing correct, wildcard matching, missing cert handling

### S3-03: ACME Directory & Account ⬜ (3 hours)
- `internal/tls/acme/client.go` — Client struct, directory fetch
- `internal/tls/acme/account.go` — account creation/retrieval
- Fetch URLs from ACME directory endpoint
- Account key generation (ECDSA P-256)
- Persist account key on disk
- Nonce pool management
- **Test**: Let's Encrypt staging directory fetch, account create

### S3-04: JWS Signing ⬜ (3 hours)
- `internal/tls/acme/jws.go` — ACME JWS implementation
- ECDSA-SHA256 signing
- JWK (JSON Web Key) encoding
- Protected header: alg, nonce, url, kid/jwk
- Base64url encoding (no padding)
- POST-as-GET (empty payload)
- **Test**: JWS signature verification, known test vectors

### S3-05: HTTP-01 Challenge Solver ⬜ (3 hours)
- `internal/tls/acme/challenge.go` — HTTP-01 handler
- `/.well-known/acme-challenge/{token}` endpoint
- Key authorization computation: `token + "." + thumbprint`
- Challenge token storage (sync.Map, auto-cleanup)
- Port 80 handler integration
- **Test**: Challenge token serve, key authorization correct format

### S3-06: Certificate Issuance Flow ⬜ (4 hours)
- `internal/tls/acme/order.go` — full ACME order lifecycle
- newOrder → getAuthorization → solveChallenge → waitReady → finalize → download
- CSR generation (ECDSA P-256, SAN extension)
- Certificate chain download (PEM format)
- Error handling: rate limits, invalid challenges, retry logic
- **Test**: Full flow against Let's Encrypt staging (integration test)

### S3-07: Auto-Renewal ⬜ (2 hours)
- `internal/tls/renewal.go` — background renewal goroutine
- Expiry check every 12 hours
- Renew when less than 30 days remaining
- Exponential backoff on failure
- Hot-swap: new cert immediately active (dynamic tls.Config.GetCertificate)
- **Test**: Mock cert with short expiry, renewal trigger verified

### S3-08: HTTP→HTTPS Redirect ⬜ (1 hour)
- Port 80 handler: ACME challenge OR 301 redirect to HTTPS
- `Strict-Transport-Security` header (configurable max-age)
- Redirect preserves path and query string
- **Test**: HTTP request → 301 to HTTPS with correct URL

### S3-09: TLS Configuration Hardening ⬜ (1 hour)
- Minimum TLS 1.2
- Modern cipher suite selection
- ALPN: h2, http/1.1
- Session ticket rotation
- **Test**: SSL Labs compatible config (A+ target)

**Sprint 3 Exit Criteria**: `uwas serve` works with auto-HTTPS. When a new domain is added, it automatically obtains a certificate from Let's Encrypt. HTTP→HTTPS redirect active. Cert renewal runs in the background.

---

## Sprint 4: FastCGI / PHP (4 days)

### S4-01: FastCGI Protocol ⬜ (4 hours)
- `pkg/fastcgi/protocol.go` — record header encode/decode
- Name-value pair encoding (1-byte/4-byte length)
- Record types: BEGIN_REQUEST, PARAMS, STDIN, STDOUT, STDERR, END_REQUEST
- Max record size: 65535 bytes (chunking for large payloads)
- **Test**: Encode/decode roundtrip, known protocol byte sequences

### S4-02: FastCGI Client ⬜ (4 hours)
- `pkg/fastcgi/client.go` — request execution
- Unix socket and TCP connection support
- Full request lifecycle: begin → params → stdin → read stdout/stderr → end
- Stderr → redirect to server log
- Timeout handling: context-based cancellation
- **Test**: Request/response cycle with mock FastCGI server

### S4-03: Connection Pool ⬜ (3 hours)
- `pkg/fastcgi/pool.go` — connection pooling
- Configurable: maxIdle, maxOpen, maxLifetime
- Idle connection health check (stale connection detection)
- Graceful drain (wait for existing requests during shutdown)
- Metrics: active, idle, total created, wait count
- **Test**: Concurrent requests, pool limit, stale connection eviction

### S4-04: CGI Environment Builder ⬜ (3 hours)
- `internal/handler/fastcgi/env.go` — buildFCGIEnv()
- All standard CGI variables: SCRIPT_FILENAME, REQUEST_URI, QUERY_STRING, etc.
- PATH_INFO / SCRIPT_NAME split (framework compatibility)
- HTTP_* header forwarding
- Custom env variables (from config, per-domain)
- HTTPS detection
- **Test**: Correct env variables for WordPress, Laravel, Drupal

### S4-05: PHP Handler ⬜ (4 hours)
- `internal/handler/fastcgi/handler.go` — PHPHandler struct
- CanHandle: `.php` extension or type=php fallback
- Response parsing: Status header, Content-Type, body split
- Response header forwarding (Set-Cookie, Location, etc.)
- Large upload handling: request body streaming (disk temp file)
- Configurable max upload size per-domain
- **Test**: PHP info page, POST form, file upload, redirect response

### S4-06: Per-Domain FPM Pool ⬜ (2 hours)
- Different domains should be able to connect to different PHP-FPM sockets
- Config: `php.fpm_address` per-domain
- Different PHP versions: domain A → PHP 8.3, domain B → PHP 8.1
- **Test**: Two domains, two different FPM sockets, correct routing

**Sprint 4 Exit Criteria**: WordPress (with PHP-FPM) works. Pages render, admin panel is accessible, file upload works. Pretty permalinks do not work yet (depends on Sprint 5).

---

## Sprint 5: Rewrite Engine (4 days)

### S5-01: Rewrite Rule Types ⬜ (2 hours)
- `internal/rewrite/rule.go` — RewriteRule, RewriteFlags structs
- Flag parsing: [L], [R=301], [QSA], [NC], [F], [G], [C], [S=N], [PT]
- Backreference support: $1, $2, ... (regex capture groups)
- %1, %2 (RewriteCond backreferences)
- **Test**: Flag combinations, backreference extraction

### S5-02: Rewrite Conditions ⬜ (3 hours)
- `internal/rewrite/condition.go` — RewriteCondition evaluation
- Server variable resolver: REQUEST_URI, HTTP_HOST, QUERY_STRING, etc.
- Special conditions: `-f` (is file), `-d` (is dir), `-l` (is symlink)
- Negation: `!-f` (is NOT file)
- Regex conditions with backreference capture
- OR chaining: `[OR]` flag
- **Test**: File existence check, regex match, negation, OR logic

### S5-03: Rewrite Engine Core ⬜ (4 hours)
- `internal/rewrite/engine.go` — rule processing loop
- Rule evaluation order (top-to-bottom)
- [L] flag: stop processing on match
- [C] flag: chain (skip remaining if previous didn't match)
- [S=N] flag: skip N rules
- Redirect rules: [R=301/302/307/308] → HTTP redirect response
- Internal rewrite: URI transform, re-enter pipeline
- Loop detection: max 10 internal rewrites
- **Test**: WordPress permalink rules, Laravel rules, redirect chains

### S5-04: Server Variables ⬜ (2 hours)
- `internal/rewrite/variables.go` — %{VAR} expansion
- Full set: REQUEST_URI, REQUEST_FILENAME, QUERY_STRING, HTTP_HOST, HTTP_REFERER, HTTP_USER_AGENT, REMOTE_ADDR, REQUEST_METHOD, SERVER_PORT, HTTPS, THE_REQUEST, DOCUMENT_ROOT, SERVER_NAME, TIME, TIME_YEAR, TIME_MON, TIME_DAY, TIME_HOUR, TIME_MIN, TIME_SEC
- **Test**: Each variable returns correct value

### S5-05: .htaccess Parser ⬜ (4 hours)
- `pkg/htaccess/parser.go` — lexer/parser
- Directive parsing: name + arguments
- Block handling: `<IfModule>`, `<FilesMatch>`, `<Files>`, `<Directory>`
- Line continuation: `\` at end of line
- Comment handling: `#`
- Quoted string handling
- **Test**: Real-world .htaccess files: WordPress, Laravel, Drupal, Joomla

### S5-06: .htaccess Directive Converter ⬜ (4 hours)
- `pkg/htaccess/converter.go` — directive → internal rules
- RewriteEngine, RewriteRule, RewriteCond
- Redirect, RedirectMatch
- ErrorDocument, DirectoryIndex
- Header set/unset/append
- ExpiresActive, ExpiresByType
- Options (-Indexes, -FollowSymLinks)
- AuthType Basic, AuthUserFile, Require
- Order/Deny/Allow (legacy syntax)
- `<IfModule>` → always true, process inner directives
- **Test**: WordPress default .htaccess → correct rewrite rules

### S5-07: .htaccess File Watcher ⬜ (2 hours)
- `internal/config/htaccess/watcher.go` — inotify-based file watcher
- Watch for .htaccess changes in document roots
- Change → re-parse → in-memory rule update
- Debounce: 500ms (single parse on rapid successive changes)
- Fallback: polling mode (if inotify not supported)
- **Test**: Rules update when .htaccess is modified

### S5-08: .htaccess Convert CLI Tool ⬜ (2 hours)
- `uwas htaccess convert /path/to/.htaccess` → YAML output
- `uwas htaccess validate /path/to/.htaccess` → syntax check
- `uwas htaccess test /path/to/.htaccess -u "/test/path"` → rule test
- **Test**: WordPress .htaccess → valid YAML config snippet

**Sprint 5 Exit Criteria**: WordPress pretty permalinks work. `.htaccess` file is automatically parsed. `uwas htaccess convert` command works. Standard .htaccess rules from frameworks like Laravel and Drupal are supported.

---

## Sprint 6: Middleware Stack (3 days)

### S6-01: Middleware Chain Builder ⬜ (2 hours)
- `internal/middleware/chain.go` — Chain() function
- Functional composition: `Chain(A, B, C)(handler)` → `A(B(C(handler)))`
- Per-domain middleware chain override (optional)
- **Test**: 3 middleware chain, correct execution order

### S6-02: Panic Recovery ⬜ (1 hour)
- `internal/middleware/recovery.go`
- If handler panics → 500 Internal Server Error
- Panic stack trace → error log
- Close connection cleanly
- **Test**: Panicking handler → 500 response, log entry

### S6-03: Request ID ⬜ (1 hour)
- `internal/middleware/requestid.go`
- UUID v7 generation (timestamp-sortable)
- `X-Request-ID` response header
- If incoming `X-Request-ID` exists, preserve it (proxy chain)
- **Test**: Unique ID in every response, incoming ID forwarded

### S6-04: Real IP Extraction ⬜ (2 hours)
- `internal/middleware/realip.go`
- `X-Forwarded-For`, `X-Real-IP`, `CF-Connecting-IP` header parsing
- Trusted proxy CIDR list (configurable)
- Rightmost untrusted IP selection (spoofing protection)
- **Test**: Various XFF chains, trusted/untrusted proxy scenarios

### S6-05: Rate Limiter ⬜ (3 hours)
- `internal/middleware/ratelimit.go`
- Token bucket algorithm per-IP
- Configurable: requests/window per-domain
- Sharded map: `[256]sync.Mutex` to reduce lock contention
- Automatic cleanup: periodically clean expired buckets
- `429 Too Many Requests` response + `Retry-After` header
- **Test**: 429 when limit exceeded, reset after window, concurrent access

### S6-06: Gzip Compression ⬜ (3 hours)
- `internal/middleware/compress.go`
- `Accept-Encoding` negotiation
- Gzip (stdlib `compress/gzip`)
- Min size threshold (configurable, default 1KB)
- Content-Type filter (text/*, application/json, etc. — do not compress images)
- `Vary: Accept-Encoding` header
- Response buffering: compression decision before headers are written
- **Test**: Gzip response, min size skip, binary content skip

### S6-07: Security Headers ⬜ (2 hours)
- `internal/middleware/headers.go`
- Default headers: HSTS, X-Content-Type-Options, X-Frame-Options, Referrer-Policy, Permissions-Policy
- Per-domain override: header add/set/remove
- `Server` header removal (configurable)
- `X-Powered-By` removal (always)
- **Test**: Default headers present, custom override works

### S6-08: CORS Handler ⬜ (2 hours)
- `internal/middleware/cors.go`
- Per-domain CORS config: allowed origins, methods, headers
- Preflight (OPTIONS) request handling
- `Access-Control-Allow-*` headers
- Wildcard vs specific origin
- Credentials mode
- **Test**: Preflight response, allowed/blocked origin, credentials

### S6-09: Security Guard ⬜ (3 hours)
- `internal/middleware/security.go`
- Blocked path patterns: `.git`, `.env`, `wp-config.php`, etc.
- Basic WAF rules: SQL injection, XSS, path traversal detection
- Per-domain blocked path override
- Request logging for blocked requests
- **Test**: Blocked paths → 403, WAF rules trigger, legitimate requests pass

### S6-10: Access Log Middleware ⬜ (2 hours)
- `internal/middleware/log.go`
- Write structured JSON log when request completes
- Fields: request_id, remote_ip, method, host, path, status, bytes, duration_ms, ttfb_ms, cache, upstream, user_agent, tls_version
- CLF (Combined Log Format) alternative — AWStats/GoAccess compatible
- Async write: log buffer → periodic flush (prevent blocking I/O)
- **Test**: JSON format correct, CLF format correct, all fields populated

**Sprint 6 Exit Criteria**: Full middleware stack works. Rate limiting, compression, security headers, CORS, access logging active. Production-ready request pipeline.

---

## Sprint 7: Cache Engine (5 days)

### S7-01: Cache Key Generator ⬜ (2 hours)
- `internal/cache/key.go`
- Key = hash(host + method + path + sorted_query_params + vary_headers)
- Configurable: query param include/exclude list
- Vary header support (Accept-Encoding, Cookie, etc.)
- FNV-1a hash (stdlib `hash/fnv`)
- **Test**: Same request → same key, different vary → different key

### S7-02: Cached Response Type ⬜ (2 hours)
- `internal/cache/entry.go`
- CachedResponse: status code, headers, body ([]byte), created, ttl, tags
- Size calculation: headers + body
- Serialization: binary format for disk cache
- **Test**: Response capture, size calculation, serialize/deserialize roundtrip

### S7-03: Sharded Memory Cache (L1) ⬜ (5 hours)
- `internal/cache/memory.go` — 256 sharded map + LRU eviction
- Get(): fresh hit, stale hit (grace), expired, miss
- Set(): store with TTL + grace TTL + tags
- LRU eviction when memory limit exceeded
- Concurrent-safe: per-shard RWMutex
- Memory tracking: atomic int64 used bytes
- **Test**: Concurrent read/write (race detector), LRU eviction order, memory limit

### S7-04: Disk Cache (L2) ⬜ (4 hours)
- `internal/cache/disk.go`
- Hash-based directory structure: `/var/cache/uwas/ab/cd/abcdef.cache`
- Binary file format: header (expiry, tags, status) + body
- Max disk usage tracking + LRU eviction
- Async write: memory cache hit → lazy disk write
- Read: memory miss → disk check → promote to memory
- **Test**: File write/read, disk limit enforcement, corrupt file handling

### S7-05: Grace Mode ⬜ (3 hours)
- `internal/cache/grace.go`
- Cache entry expired but within grace_ttl → serve stale
- Async revalidation: background goroutine fetches fresh response
- Request coalescing: multiple misses for the same key → single backend request
- Backend down → serve stale content (for the duration of the grace period)
- **Test**: Expired entry → stale serve, concurrent miss → single backend request

### S7-06: Cache Middleware ⬜ (3 hours)
- `internal/middleware/cache.go`
- Bypass rules: POST, Cookie-based sessions, Cache-Control: no-cache, configured paths
- Response capture wrapper
- Cacheable response check: status 200/301/404, no Set-Cookie, Content-Length > 0
- `X-Cache: HIT/MISS/BYPASS/STALE` response header
- `Age` header (seconds since cached)
- **Test**: Hit/miss flow, bypass rules, uncacheable response skip

### S7-07: Cache Purge ⬜ (3 hours)
- `internal/cache/purge.go`
- Tag-based purge: remove all entries matching tag(s)
- Path-based purge: exact path or wildcard (`/blog/*`)
- Domain-wide purge
- Full purge (clear entire cache)
- PURGE HTTP method handler (with auth key)
- **Test**: Tag purge, wildcard purge, concurrent purge safety

### S7-08: ESI Parser ⬜ (4 hours)
- `internal/cache/esi.go`
- HTML stream scan for `<esi:include src="..." />`
- Sub-request execution (internal redirect)
- Fragment caching: each ESI include has a separate cache key
- Fragment assembly: combine main response + sub-responses
- Nested ESI support (max 3 depth)
- **Test**: ESI tag parsing, fragment inclusion, nested ESI

### S7-09: Conditional Requests ⬜ (2 hours)
- `If-None-Match` (ETag) → 304 Not Modified
- `If-Modified-Since` (Last-Modified) → 304 Not Modified
- Cache-level: check cached response's ETag/Last-Modified
- Client-level: check conditional headers sent by client
- **Test**: ETag match → 304, modified since → 200

**Sprint 7 Exit Criteria**: Full caching layer works. Memory + disk cache, grace mode (stale-while-revalidate), tag-based purge, ESI, conditional requests. Varnish-level caching functionality.

---

## Sprint 8: Reverse Proxy & Load Balancer (4 days)

### S8-01: Upstream Pool ⬜ (3 hours)
- `internal/handler/proxy/upstream.go` — UpstreamPool struct
- Backend list management: add, remove, drain
- Backend state: healthy, unhealthy, draining
- Per-backend connection tracking (active connections)
- Per-backend metrics: total requests, failures, latency histogram
- **Test**: Backend state transitions, concurrent access

### S8-02: Load Balancer Algorithms ⬜ (4 hours)
- `internal/handler/proxy/balancer.go`
- Round Robin (weighted smooth)
- Least Connections
- IP Hash (consistent, for session affinity)
- URI Hash (for cache-friendly distribution)
- Random (power of 2 choices)
- **Test**: Distribution uniformity with each algorithm, weighted distribution

### S8-03: Proxy Handler ⬜ (4 hours)
- `internal/handler/proxy/handler.go` — ProxyHandler
- Backend selection via balancer
- Request forwarding: preserve headers, add proxy headers (X-Real-IP, X-Forwarded-*)
- Response forwarding: copy headers + body streaming
- Error handling: backend timeout, connection refused, 502/503/504
- Configurable timeouts: connect, read, write (per-domain)
- Hop-by-hop header stripping
- **Test**: Successful proxy, backend error, timeout, header forwarding

### S8-04: Health Checker ⬜ (3 hours)
- `internal/handler/proxy/health.go` — HealthChecker
- Periodic health checks: HTTP GET to configured path
- Configurable: interval, timeout, threshold (fail count), rise (success count)
- TCP check mode (connect only)
- Health state machine: healthy ←→ unhealthy
- Metrics integration
- **Test**: Healthy → unhealthy transition, recovery, timeout handling

### S8-05: Circuit Breaker ⬜ (3 hours)
- `internal/handler/proxy/circuit.go` — CircuitBreaker
- State machine: CLOSED → OPEN → HALF-OPEN → CLOSED
- Configurable: failure threshold, timeout, half-open max requests
- Per-backend circuit breaker
- Metrics: state changes, rejected requests
- **Test**: State transitions, concurrent request handling, recovery

### S8-06: Sticky Sessions ⬜ (2 hours)
- Cookie-based: `Set-Cookie: UWAS_UPSTREAM=backend_hash`
- Header-based: custom header for backend selection
- IP-based: implicit affinity via IP hash
- Cookie TTL configurable
- **Test**: Same cookie → same backend, cookie expiry

### S8-07: WebSocket Proxy ⬜ (3 hours)
- `internal/handler/proxy/websocket.go`
- HTTP Upgrade detection
- Connection hijack (http.Hijacker)
- Bidirectional byte copy (goroutine pair)
- Clean shutdown: when one side closes, close the other
- Ping/pong forwarding
- **Test**: WebSocket echo test, disconnect handling

**Sprint 8 Exit Criteria**: Full reverse proxy + load balancer works. 6 LB algorithms, health checking, circuit breaker, sticky sessions, WebSocket proxy. HAProxy-level functionality.

---

## Sprint 9: Admin & MCP (3 days)

### S9-01: Admin REST API ⬜ (4 hours)
- `internal/admin/api.go` — HTTP API server
- Go 1.22 routing: `mux.HandleFunc("GET /api/v1/domains", ...)`
- API key authentication (Bearer token)
- JSON request/response
- Endpoints: domains CRUD, cache purge/stats, certs list/renew, reload, health, stats, metrics
- **Test**: Manual test of each endpoint, auth check

### S9-02: Prometheus Metrics ⬜ (3 hours)
- `internal/metrics/collector.go` — metrics collection
- Prometheus text format export (`/api/v1/metrics`)
- Counters: requests_total, cache_hits/misses, errors
- Histograms: request_duration, upstream_duration
- Gauges: connections_active, cache_memory_bytes, cert_expiry
- Per-host labels
- **Test**: Metrics format validation, counter increment

### S9-03: Built-in Dashboard ⬜ (4 hours)
- `internal/admin/dashboard.go` — embedded HTML/JS
- `/_uwas/dashboard` endpoint
- Real-time: requests/sec, error rate, cache hit ratio
- Upstream health status
- Certificate expiry timeline
- Top paths, top errors
- Vanilla JS + fetch API (no framework)
- Embedded in binary via `embed.FS`
- **Test**: Dashboard loads, API calls work

### S9-04: MCP Server ⬜ (4 hours)
- `internal/mcp/server.go` — MCP protocol handler (stdio transport)
- `internal/mcp/tools.go` — tool implementations
- Tools: domain_list/add/remove, cache_purge/stats, cert_list/renew, stats, config_show/reload, logs_search
- Resources: uwas://config, uwas://stats, uwas://domains/{host}
- **Test**: Each tool works, valid MCP response format

### S9-05: Config Reload via API ⬜ (2 hours)
- `POST /api/v1/reload` → config reload trigger
- Same logic: parse → validate → update (same as SIGHUP)
- Response: success/failure with details
- **Test**: Reload via API, invalid config rejected

**Sprint 9 Exit Criteria**: Admin API fully works. Dashboard shows real-time stats. MCP server exposes all tools. Prometheus metrics endpoint active.

---

## Sprint 10: Polish & Release (3 days)

### S10-01: CLI Subcommands ⬜ (3 hours)
- `uwas domain list/add/remove` — API client wrapper
- `uwas cert list/renew/import` — certificate management
- `uwas cache purge/stats/clear` — cache management
- `uwas config validate/test/diff` — config tools
- `uwas htaccess convert/validate/test` — .htaccess tools
- **Test**: Help text for each subcommand, basic functionality

### S10-02: Error Pages ⬜ (2 hours)
- Default HTML error pages: 400, 403, 404, 500, 502, 503, 504
- Minimal, clean design (inline CSS, no external resources)
- Custom error page override per-domain (from config)
- **Test**: Correct page returned for each error code

### S10-03: Graceful Shutdown Refinement ⬜ (2 hours)
- Active connection draining with timeout
- Background goroutine cleanup (ACME, health check, cache cleanup)
- Log flush before exit
- PID file cleanup
- Exit code: 0 (clean), 1 (error), 2 (config error)
- **Test**: Shutdown during active request → request completes

### S10-04: Integration Test Suite ⬜ (4 hours)
- `test/integration/`
- WordPress test: install, activate theme, create post, pretty permalink
- Laravel test: basic route, API endpoint
- Static site test: multi-domain, SPA mode
- Proxy test: upstream failover, health check
- Cache test: hit/miss/purge cycle
- TLS test: auto-cert (staging), SNI routing
- Docker Compose test environment
- **Test**: All integration tests pass

### S10-05: Benchmark Suite ⬜ (2 hours)
- `test/bench/`
- Static file benchmark (hey/wrk)
- PHP request benchmark
- Cache hit/miss benchmark
- Proxy throughput benchmark
- Memory usage under load
- Comparison script: UWAS vs Nginx vs Caddy
- **Test**: Establish performance baseline

### S10-06: Documentation ⬜ (3 hours)
- `README.md` — project overview, quick start, features, comparison table
- `docs/quick-start.md` — setup in 5 minutes
- `docs/configuration.md` — full config reference
- `docs/wordpress.md` — WordPress migration guide
- `docs/laravel.md` — Laravel deployment guide
- `docs/htaccess-migration.md` — migration guide from Apache
- **Test**: Doc links work, examples work

### S10-07: Docker & Distribution ⬜ (2 hours)
- `Dockerfile` — multi-stage build
- `docker-compose.yml` — UWAS + PHP-FPM + MariaDB (WordPress demo)
- `.github/workflows/release.yml` — GoReleaser config
- Binary: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64
- **Test**: Docker build, compose up, WordPress accessible

### S10-08: Release Checklist ⬜ (2 hours)
- CHANGELOG.md
- Version tag: v0.1.0
- GitHub release with binaries
- Docker Hub push
- README badges (build, go report, license)
- **Test**: `go install github.com/uwaserver/uwas@v0.1.0` works

**Sprint 10 Exit Criteria**: v0.1.0 release-ready. Binaries, Docker image, documentation, integration tests passing. WordPress demo works.

---

## Summary

| Sprint | Duration | Task Count | Output |
|--------|----------|------------|--------|
| 1: Skeleton | 3 days | 7 | Working HTTP server |
| 2: Static | 3 days | 8 | Multi-domain static serving |
| 3: TLS | 4 days | 9 | Auto HTTPS (Let's Encrypt) |
| 4: FastCGI | 4 days | 6 | WordPress PHP works |
| 5: Rewrite | 4 days | 8 | Pretty permalinks + .htaccess |
| 6: Middleware | 3 days | 10 | Production middleware stack |
| 7: Cache | 5 days | 9 | Varnish-level caching |
| 8: Proxy | 4 days | 7 | HAProxy-level LB |
| 9: Admin | 3 days | 5 | REST API + MCP + Dashboard |
| 10: Polish | 3 days | 8 | v0.1.0 release |
| **Total** | **36 days** | **77 tasks** | **Production-ready UWAS** |

**Realistic timeline**: 36 business days + 30% buffer = **~10 weeks**

---

## Dependency Graph (Critical Path)

```
S1 ──→ S2 ──→ S5 ──→ S6 ──────→ S7 ──→ S9 ──→ S10
  │              ↑                  │
  └──→ S3 ──────┘                  │
  │                                 │
  └──→ S4 ─────────────────────→ S8 ┘
```

- S3 (TLS) and S4 (FastCGI) can be worked on in parallel
- S7 (Cache) and S8 (Proxy) can be worked on in parallel
- S5 (Rewrite) → depends on S4's output (for PHP pretty permalinks testing)
- S6 (Middleware) → depends on S2's output (request pipeline)
- S9 (Admin) → depends on S7 and S8 (cache stats, upstream health)
