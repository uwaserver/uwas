# UWAS — Task Breakdown

> Her task atomik, test edilebilir, ve bağımsız commit'lenebilir.
> Format: `[SPRINT-TASK] Açıklama (tahmini süre)`
> Status: ⬜ Todo | 🔄 In Progress | ✅ Done | ⏸️ Blocked

---

## Sprint 1: Skeleton (3 gün)

### S1-01: Project Bootstrap ⬜ (2 saat)
- `go mod init github.com/uwaserver/uwas`
- Dizin yapısını oluştur: `cmd/uwas/`, `internal/`, `pkg/`
- `go.mod` + ilk dependency'ler (`gopkg.in/yaml.v3`)
- `.gitignore`, `LICENSE` (Apache 2.0), boş `README.md`
- `Makefile` (build, dev, test, lint, clean targets)
- **Test**: `go build ./...` hatasız tamamlanır

### S1-02: Version & Build Info ⬜ (1 saat)
- `cmd/uwas/version.go` — ldflags ile inject edilen Version, Commit, BuildDate
- `uwas version` komutu: versiyon, commit, build tarihi, Go version, OS/arch yazdırır
- **Test**: `go build -ldflags="-X main.Version=test" && ./uwas version` doğru çıktı verir

### S1-03: CLI Framework ⬜ (3 saat)
- `internal/cli/root.go` — minimal CLI dispatcher (stdlib `flag` + custom subcommand router)
- Subcommand yapısı: `uwas <command> [flags] [args]`
- `uwas serve`, `uwas version`, `uwas help` komutları
- Flag parsing: `-c` (config path), `-d` (daemon), `--log-level`
- Global `--help` ve per-command `--help`
- **Test**: `uwas help` tüm komutları listeler, bilinmeyen komut hata verir

### S1-04: Structured Logger ⬜ (2 saat)
- `internal/logger/logger.go` — `log/slog` wrapper
- JSON ve text format desteği
- Log level: debug, info, warn, error
- `StdLogger()` methodu — `net/http.Server.ErrorLog` uyumluluğu için
- Helper: `WithFields(key, value...)` — contextual logging
- **Test**: Her level'da JSON ve text output doğrulanır

### S1-05: Config Parser ⬜ (4 saat)
- `internal/config/config.go` — ana Config struct
- `internal/config/loader.go` — YAML dosya okuma, parse, env var expansion (`${VAR}`)
- `internal/config/validate.go` — semantic validation (port ranges, path existence, vs.)
- `internal/config/defaults.go` — default değerler (timeouts, worker count, vs.)
- Nested structs: `GlobalConfig`, `DomainConfig`, `CacheConfig`, `ACMEConfig`, `ProxyConfig`
- Duration parsing: "30s", "5m", "1h"
- Byte size parsing: "512MB", "10GB"
- **Test**: Geçerli YAML parse edilir, geçersiz config reject edilir, env var'lar expand edilir

### S1-06: Basic HTTP Server ⬜ (3 saat)
- `internal/server/server.go` — Server struct, Start(), Stop()
- Port 80'de dinle, tüm request'lere "UWAS is running" döndür
- Signal handling: SIGTERM/SIGINT → graceful shutdown
- PID file yazma/silme
- **Test**: Server başlar, HTTP response döner, SIGTERM ile temiz kapanır

### S1-07: uwas.example.yaml ⬜ (1 saat)
- Tam annotated örnek config dosyası
- Her field açıklamalı comment ile
- İki örnek domain: static site + PHP site
- **Çıktı**: Kopyala-yapıştır kullanılabilir referans config

**Sprint 1 Çıkış Kriteri**: `uwas serve -c uwas.example.yaml` çalışır, port 80'de response döner, SIGTERM ile kapanır, config parse edilir ve loglanır.

---

## Sprint 2: Static File Serving (3 gün)

### S2-01: Request Context ⬜ (2 saat)
- `internal/router/context.go` — RequestContext struct
- `internal/router/response.go` — ResponseWriter wrapper (status tracking, bytes counting, TTFB)
- Hijack() ve Flush() desteği
- Context pool (sync.Pool) ile allocation azaltma
- **Test**: ResponseWriter status/bytes doğru track eder

### S2-02: Virtual Host Router ⬜ (3 saat)
- `internal/router/vhost.go` — VHostRouter struct
- Exact match → alias match → wildcard match → default fallback
- Wildcard matching: `*.example.com` → longest match first
- O(1) exact lookup (map), O(n) wildcard (sorted slice, binary search potansiyel)
- Thread-safe: `sync.RWMutex` ile concurrent read, exclusive write (config reload)
- **Test**: 100 domain ile doğru routing, wildcard priority, default fallback

### S2-03: MIME Type Registry ⬜ (1 saat)
- `internal/handler/static/mime.go` — extension → content-type mapping
- 100+ modern format: .woff2, .webp, .avif, .wasm, .mjs, .webm, vs.
- Custom MIME type override (config'den)
- Charset default: UTF-8 (text/* types)
- **Test**: Tüm yaygın extension'lar doğru MIME type döner

### S2-04: Static File Handler ⬜ (4 saat)
- `internal/handler/static/handler.go` — StaticHandler struct
- `http.ServeContent` ile serving (sendfile, range, conditional GET dahil)
- ETag generation: weak ETag (mtime + size hash)
- `If-None-Match` → 304 Not Modified
- `Accept-Ranges: bytes` header
- Index file resolution (index.html, index.htm)
- Security: path traversal koruması (symlink + `..` check)
- `.` ile başlayan dosyalara erişim engeli (default)
- **Test**: Normal file serve, 304, range request, path traversal attempt reject

### S2-05: Pre-Compressed File Serving ⬜ (2 saat)
- `.gz` ve `.br` dosya varsa, Accept-Encoding'e göre serve et
- Öncelik: brotli > gzip > original
- Content-Encoding header doğru set et
- Vary: Accept-Encoding header
- **Test**: `.br` dosya varken brotli destekleyen client'a serve edilir

### S2-06: Directory Listing ⬜ (2 saat)
- `internal/handler/static/listing.go` — opsiyonel dizin listesi
- Default: kapalı (security)
- Config ile per-domain açılabilir
- Minimal HTML template: dosya adı, boyut, tarih
- Symlink gösterme opsiyonu
- **Test**: Enabled → HTML listesi döner, disabled → 403

### S2-07: SPA Mode ⬜ (1 saat)
- Config flag: `spa_mode: true`
- Dosya bulunamazsa → `index.html` fallback (React/Vue/Angular SPA'lar için)
- 404 yerine index.html serve et, client-side routing devralır
- **Test**: `/nonexistent/path` → index.html content döner

### S2-08: try_files Logic ⬜ (3 saat)
- `internal/server/dispatch.go` — resolvePath() implementasyonu
- Variable expansion: `$uri`, `$uri/`, named paths
- Per-domain configurable try_files
- Default: type=static → `$uri, $uri/, $uri/index.html`
- Default: type=php → `$uri, $uri/, /index.php`
- **Test**: WordPress-style fallback, SPA fallback, static site fallback

**Sprint 2 Çıkış Kriteri**: Multi-domain static file serving çalışır. `uwas serve` ile iki farklı domain'in static dosyaları serve edilir. ETag, Range, pre-compressed dosya desteği aktif.

---

## Sprint 3: TLS & ACME (4 gün)

### S3-01: Certificate Storage ⬜ (2 saat)
- `internal/tls/storage.go` — disk'te cert/key saklama
- Dizin yapısı: `/var/lib/uwas/certs/{domain}/`
- `cert.pem`, `key.pem`, `meta.json` (issuer, expiry, created)
- Dosya izinleri: 0600 (key), 0644 (cert)
- Lock file ile concurrent write koruması
- **Test**: Save/load roundtrip, file permissions doğru

### S3-02: Certificate Manager ⬜ (3 saat)
- `internal/tls/manager.go` — Manager struct
- `GetCertificate(hello)` — SNI-based cert selection
- Exact match → wildcard match → on-demand → default
- Cert loading at startup: disk'ten tüm cert'leri yükle
- `sync.Map` ile thread-safe cert store
- Manual cert desteği: config'den cert/key path
- **Test**: SNI routing doğru, wildcard matching, missing cert handling

### S3-03: ACME Directory & Account ⬜ (3 saat)
- `internal/tls/acme/client.go` — Client struct, directory fetch
- `internal/tls/acme/account.go` — account creation/retrieval
- ACME directory endpoint'inden URL'leri fetch et
- Account key generation (ECDSA P-256)
- Account key disk'te persist et
- Nonce pool management
- **Test**: Let's Encrypt staging directory fetch, account create

### S3-04: JWS Signing ⬜ (3 saat)
- `internal/tls/acme/jws.go` — ACME JWS implementation
- ECDSA-SHA256 signing
- JWK (JSON Web Key) encoding
- Protected header: alg, nonce, url, kid/jwk
- Base64url encoding (no padding)
- POST-as-GET (empty payload)
- **Test**: JWS signature verification, known test vectors

### S3-05: HTTP-01 Challenge Solver ⬜ (3 saat)
- `internal/tls/acme/challenge.go` — HTTP-01 handler
- `/.well-known/acme-challenge/{token}` endpoint
- Key authorization computation: `token + "." + thumbprint`
- Challenge token storage (sync.Map, auto-cleanup)
- Port 80 handler integration
- **Test**: Challenge token serve, key authorization correct format

### S3-06: Certificate Issuance Flow ⬜ (4 saat)
- `internal/tls/acme/order.go` — full ACME order lifecycle
- newOrder → getAuthorization → solveChallenge → waitReady → finalize → download
- CSR generation (ECDSA P-256, SAN extension)
- Certificate chain download (PEM format)
- Error handling: rate limits, invalid challenges, retry logic
- **Test**: Full flow against Let's Encrypt staging (integration test)

### S3-07: Auto-Renewal ⬜ (2 saat)
- `internal/tls/renewal.go` — background renewal goroutine
- 12-saatte bir expiry check
- < 30 gün kala → renew
- Exponential backoff on failure
- Hot-swap: yeni cert hemen aktif (tls.Config.GetCertificate dinamik)
- **Test**: Mock cert with short expiry, renewal trigger doğrulanır

### S3-08: HTTP→HTTPS Redirect ⬜ (1 saat)
- Port 80 handler: ACME challenge OR 301 redirect to HTTPS
- `Strict-Transport-Security` header (configurable max-age)
- Redirect preserves path and query string
- **Test**: HTTP request → 301 to HTTPS with correct URL

### S3-09: TLS Configuration Hardening ⬜ (1 saat)
- Minimum TLS 1.2
- Modern cipher suite selection
- ALPN: h2, http/1.1
- Session ticket rotation
- **Test**: SSL Labs compatible config (A+ hedef)

**Sprint 3 Çıkış Kriteri**: `uwas serve` auto-HTTPS ile çalışır. Yeni domain eklediğinde Let's Encrypt'ten otomatik sertifika alır. HTTP→HTTPS redirect aktif. Cert renewal arka planda çalışır.

---

## Sprint 4: FastCGI / PHP (4 gün)

### S4-01: FastCGI Protocol ⬜ (4 saat)
- `pkg/fastcgi/protocol.go` — record header encode/decode
- Name-value pair encoding (1-byte/4-byte length)
- Record types: BEGIN_REQUEST, PARAMS, STDIN, STDOUT, STDERR, END_REQUEST
- Max record size: 65535 bytes (chunking for large payloads)
- **Test**: Encode/decode roundtrip, known protocol byte sequences

### S4-02: FastCGI Client ⬜ (4 saat)
- `pkg/fastcgi/client.go` — request execution
- Unix socket ve TCP connection desteği
- Full request lifecycle: begin → params → stdin → read stdout/stderr → end
- Stderr → server log'a yönlendir
- Timeout handling: context-based cancellation
- **Test**: Mock FastCGI server ile request/response cycle

### S4-03: Connection Pool ⬜ (3 saat)
- `pkg/fastcgi/pool.go` — connection pooling
- Configurable: maxIdle, maxOpen, maxLifetime
- Idle connection health check (stale connection detection)
- Graceful drain (shutdown sırasında mevcut request'leri bekle)
- Metrics: active, idle, total created, wait count
- **Test**: Concurrent requests, pool limit, stale connection eviction

### S4-04: CGI Environment Builder ⬜ (3 saat)
- `internal/handler/fastcgi/env.go` — buildFCGIEnv()
- Tüm standart CGI variables: SCRIPT_FILENAME, REQUEST_URI, QUERY_STRING, vs.
- PATH_INFO / SCRIPT_NAME split (framework uyumluluğu)
- HTTP_* header forwarding
- Custom env variables (config'den per-domain)
- HTTPS detection
- **Test**: WordPress, Laravel, Drupal için doğru env variables

### S4-05: PHP Handler ⬜ (4 saat)
- `internal/handler/fastcgi/handler.go` — PHPHandler struct
- CanHandle: `.php` extension veya type=php fallback
- Response parsing: Status header, Content-Type, body split
- Response header forwarding (Set-Cookie, Location, vs.)
- Large upload handling: request body streaming (disk temp file)
- Configurable max upload size per-domain
- **Test**: PHP info page, POST form, file upload, redirect response

### S4-06: Per-Domain FPM Pool ⬜ (2 saat)
- Farklı domain'ler farklı PHP-FPM socket'lerine bağlanabilmeli
- Config: `php.fpm_address` per-domain
- Farklı PHP versiyonları: domain A → PHP 8.3, domain B → PHP 8.1
- **Test**: İki domain, iki farklı FPM socket, doğru routing

**Sprint 4 Çıkış Kriteri**: WordPress (PHP-FPM ile) çalışır. Sayfalar render edilir, admin panel erişilebilir, dosya upload çalışır. Pretty permalinks henüz çalışmaz (Sprint 5'e bağımlı).

---

## Sprint 5: Rewrite Engine (4 gün)

### S5-01: Rewrite Rule Types ⬜ (2 saat)
- `internal/rewrite/rule.go` — RewriteRule, RewriteFlags structs
- Flag parsing: [L], [R=301], [QSA], [NC], [F], [G], [C], [S=N], [PT]
- Backreference support: $1, $2, ... (regex capture groups)
- %1, %2 (RewriteCond backreferences)
- **Test**: Flag combinations, backreference extraction

### S5-02: Rewrite Conditions ⬜ (3 saat)
- `internal/rewrite/condition.go` — RewriteCondition evaluation
- Server variable resolver: REQUEST_URI, HTTP_HOST, QUERY_STRING, vs.
- Special conditions: `-f` (is file), `-d` (is dir), `-l` (is symlink)
- Negation: `!-f` (is NOT file)
- Regex conditions with backreference capture
- OR chaining: `[OR]` flag
- **Test**: File existence check, regex match, negation, OR logic

### S5-03: Rewrite Engine Core ⬜ (4 saat)
- `internal/rewrite/engine.go` — rule processing loop
- Rule evaluation order (top-to-bottom)
- [L] flag: stop processing on match
- [C] flag: chain (skip remaining if previous didn't match)
- [S=N] flag: skip N rules
- Redirect rules: [R=301/302/307/308] → HTTP redirect response
- Internal rewrite: URI transform, re-enter pipeline
- Loop detection: max 10 internal rewrites
- **Test**: WordPress permalink rules, Laravel rules, redirect chains

### S5-04: Server Variables ⬜ (2 saat)
- `internal/rewrite/variables.go` — %{VAR} expansion
- Full set: REQUEST_URI, REQUEST_FILENAME, QUERY_STRING, HTTP_HOST, HTTP_REFERER, HTTP_USER_AGENT, REMOTE_ADDR, REQUEST_METHOD, SERVER_PORT, HTTPS, THE_REQUEST, DOCUMENT_ROOT, SERVER_NAME, TIME, TIME_YEAR, TIME_MON, TIME_DAY, TIME_HOUR, TIME_MIN, TIME_SEC
- **Test**: Her variable doğru değer döner

### S5-05: .htaccess Parser ⬜ (4 saat)
- `pkg/htaccess/parser.go` — lexer/parser
- Directive parsing: name + arguments
- Block handling: `<IfModule>`, `<FilesMatch>`, `<Files>`, `<Directory>`
- Line continuation: `\` at end of line
- Comment handling: `#`
- Quoted string handling
- **Test**: Real-world .htaccess files: WordPress, Laravel, Drupal, Joomla

### S5-06: .htaccess Directive Converter ⬜ (4 saat)
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

### S5-07: .htaccess File Watcher ⬜ (2 saat)
- `internal/config/htaccess/watcher.go` — inotify-based file watcher
- Document root'larda .htaccess değişikliklerini izle
- Değişiklik → re-parse → in-memory rule update
- Debounce: 500ms (hızlı ardışık değişikliklerde tek parse)
- Fallback: polling mode (inotify desteklenmezse)
- **Test**: .htaccess değiştirilince rule'lar güncellenir

### S5-08: .htaccess Convert CLI Tool ⬜ (2 saat)
- `uwas htaccess convert /path/to/.htaccess` → YAML output
- `uwas htaccess validate /path/to/.htaccess` → syntax check
- `uwas htaccess test /path/to/.htaccess -u "/test/path"` → rule test
- **Test**: WordPress .htaccess → valid YAML config snippet

**Sprint 5 Çıkış Kriteri**: WordPress pretty permalinks çalışır. `.htaccess` dosyası otomatik parse edilir. `uwas htaccess convert` komutu çalışır. Laravel, Drupal gibi framework'lerin standart .htaccess kuralları desteklenir.

---

## Sprint 6: Middleware Stack (3 gün)

### S6-01: Middleware Chain Builder ⬜ (2 saat)
- `internal/middleware/chain.go` — Chain() function
- Functional composition: `Chain(A, B, C)(handler)` → `A(B(C(handler)))`
- Per-domain middleware chain override (opsiyonel)
- **Test**: 3 middleware chain, execution order doğru

### S6-02: Panic Recovery ⬜ (1 saat)
- `internal/middleware/recovery.go`
- Handler'dan panic çıkarsa → 500 Internal Server Error
- Panic stack trace → error log
- Connection'ı temiz kapat
- **Test**: Panic atan handler → 500 response, log entry

### S6-03: Request ID ⬜ (1 saat)
- `internal/middleware/requestid.go`
- UUID v7 generation (timestamp-sortable)
- `X-Request-ID` response header
- Incoming `X-Request-ID` varsa preserve et (proxy chain)
- **Test**: Her response'ta unique ID, incoming ID forwarded

### S6-04: Real IP Extraction ⬜ (2 saat)
- `internal/middleware/realip.go`
- `X-Forwarded-For`, `X-Real-IP`, `CF-Connecting-IP` header parsing
- Trusted proxy CIDR list (configurable)
- Rightmost untrusted IP seçimi (spoofing koruması)
- **Test**: Çeşitli XFF chain'leri, trusted/untrusted proxy senaryoları

### S6-05: Rate Limiter ⬜ (3 saat)
- `internal/middleware/ratelimit.go`
- Token bucket algorithm per-IP
- Configurable: requests/window per-domain
- Sharded map: `[256]sync.Mutex` ile lock contention azalt
- Automatic cleanup: expired bucket'ları periyodik temizle
- `429 Too Many Requests` response + `Retry-After` header
- **Test**: Limit aşıldığında 429, window sonrası reset, concurrent access

### S6-06: Gzip Compression ⬜ (3 saat)
- `internal/middleware/compress.go`
- `Accept-Encoding` negotiation
- Gzip (stdlib `compress/gzip`)
- Min size threshold (configurable, default 1KB)
- Content-Type filter (text/*, application/json, vs. — image'ları compress etme)
- `Vary: Accept-Encoding` header
- Response buffering: header'lar yazılmadan önce compression kararı
- **Test**: Gzip response, min size skip, binary content skip

### S6-07: Security Headers ⬜ (2 saat)
- `internal/middleware/headers.go`
- Default headers: HSTS, X-Content-Type-Options, X-Frame-Options, Referrer-Policy, Permissions-Policy
- Per-domain override: header add/set/remove
- `Server` header removal (configurable)
- `X-Powered-By` removal (always)
- **Test**: Default headers present, custom override çalışır

### S6-08: CORS Handler ⬜ (2 saat)
- `internal/middleware/cors.go`
- Per-domain CORS config: allowed origins, methods, headers
- Preflight (OPTIONS) request handling
- `Access-Control-Allow-*` headers
- Wildcard vs specific origin
- Credentials mode
- **Test**: Preflight response, allowed/blocked origin, credentials

### S6-09: Security Guard ⬜ (3 saat)
- `internal/middleware/security.go`
- Blocked path patterns: `.git`, `.env`, `wp-config.php` vs.
- Basic WAF rules: SQL injection, XSS, path traversal detection
- Per-domain blocked path override
- Request logging for blocked requests
- **Test**: Blocked paths → 403, WAF rules trigger, legitimate requests pass

### S6-10: Access Log Middleware ⬜ (2 saat)
- `internal/middleware/log.go`
- Request tamamlandığında structured JSON log yaz
- Fields: request_id, remote_ip, method, host, path, status, bytes, duration_ms, ttfb_ms, cache, upstream, user_agent, tls_version
- CLF (Combined Log Format) alternatifi — AWStats/GoAccess uyumlu
- Async write: log buffer → periodic flush (blocking I/O engelle)
- **Test**: JSON format doğru, CLF format doğru, tüm field'lar dolu

**Sprint 6 Çıkış Kriteri**: Full middleware stack çalışır. Rate limiting, compression, security headers, CORS, access logging aktif. Production-ready request pipeline.

---

## Sprint 7: Cache Engine (5 gün)

### S7-01: Cache Key Generator ⬜ (2 saat)
- `internal/cache/key.go`
- Key = hash(host + method + path + sorted_query_params + vary_headers)
- Configurable: query param include/exclude list
- Vary header support (Accept-Encoding, Cookie, vs.)
- FNV-1a hash (stdlib `hash/fnv`)
- **Test**: Same request → same key, different vary → different key

### S7-02: Cached Response Type ⬜ (2 saat)
- `internal/cache/entry.go`
- CachedResponse: status code, headers, body ([]byte), created, ttl, tags
- Size calculation: headers + body
- Serialization: binary format for disk cache
- **Test**: Response capture, size calculation, serialize/deserialize roundtrip

### S7-03: Sharded Memory Cache (L1) ⬜ (5 saat)
- `internal/cache/memory.go` — 256 sharded map + LRU eviction
- Get(): fresh hit, stale hit (grace), expired, miss
- Set(): store with TTL + grace TTL + tags
- LRU eviction when memory limit exceeded
- Concurrent-safe: per-shard RWMutex
- Memory tracking: atomic int64 used bytes
- **Test**: Concurrent read/write (race detector), LRU eviction order, memory limit

### S7-04: Disk Cache (L2) ⬜ (4 saat)
- `internal/cache/disk.go`
- Hash-based directory structure: `/var/cache/uwas/ab/cd/abcdef.cache`
- Binary file format: header (expiry, tags, status) + body
- Max disk usage tracking + LRU eviction
- Async write: memory cache hit → lazy disk write
- Read: memory miss → disk check → promote to memory
- **Test**: File write/read, disk limit enforcement, corrupt file handling

### S7-05: Grace Mode ⬜ (3 saat)
- `internal/cache/grace.go`
- Cache entry expired but within grace_ttl → serve stale
- Async revalidation: background goroutine fetches fresh response
- Request coalescing: aynı key için birden fazla miss → tek backend request
- Backend down → stale content serve (grace period süresince)
- **Test**: Expired entry → stale serve, concurrent miss → single backend request

### S7-06: Cache Middleware ⬜ (3 saat)
- `internal/middleware/cache.go`
- Bypass rules: POST, Cookie-based sessions, Cache-Control: no-cache, configured paths
- Response capture wrapper
- Cacheable response check: status 200/301/404, no Set-Cookie, Content-Length > 0
- `X-Cache: HIT/MISS/BYPASS/STALE` response header
- `Age` header (seconds since cached)
- **Test**: Hit/miss flow, bypass rules, uncacheable response skip

### S7-07: Cache Purge ⬜ (3 saat)
- `internal/cache/purge.go`
- Tag-based purge: remove all entries matching tag(s)
- Path-based purge: exact path or wildcard (`/blog/*`)
- Domain-wide purge
- Full purge (tüm cache temizle)
- PURGE HTTP method handler (with auth key)
- **Test**: Tag purge, wildcard purge, concurrent purge safety

### S7-08: ESI Parser ⬜ (4 saat)
- `internal/cache/esi.go`
- HTML stream scan for `<esi:include src="..." />`
- Sub-request execution (internal redirect)
- Fragment caching: her ESI include ayrı cache key
- Fragment assembly: ana response + sub-responses birleştir
- Nested ESI desteği (max 3 depth)
- **Test**: ESI tag parsing, fragment inclusion, nested ESI

### S7-09: Conditional Requests ⬜ (2 saat)
- `If-None-Match` (ETag) → 304 Not Modified
- `If-Modified-Since` (Last-Modified) → 304 Not Modified
- Cache-level: cached response'un ETag/Last-Modified'ını kontrol et
- Client-level: client'ın gönderdiği conditional header'ları check et
- **Test**: ETag match → 304, modified since → 200

**Sprint 7 Çıkış Kriteri**: Full caching layer çalışır. Memory + disk cache, grace mode (stale-while-revalidate), tag-based purge, ESI, conditional requests. Varnish-level caching functionality.

---

## Sprint 8: Reverse Proxy & Load Balancer (4 gün)

### S8-01: Upstream Pool ⬜ (3 saat)
- `internal/handler/proxy/upstream.go` — UpstreamPool struct
- Backend list management: add, remove, drain
- Backend state: healthy, unhealthy, draining
- Per-backend connection tracking (active connections)
- Per-backend metrics: total requests, failures, latency histogram
- **Test**: Backend state transitions, concurrent access

### S8-02: Load Balancer Algorithms ⬜ (4 saat)
- `internal/handler/proxy/balancer.go`
- Round Robin (weighted smooth)
- Least Connections
- IP Hash (consistent, for session affinity)
- URI Hash (for cache-friendly distribution)
- Random (power of 2 choices)
- **Test**: Her algoritma ile distribution uniformity, weighted distribution

### S8-03: Proxy Handler ⬜ (4 saat)
- `internal/handler/proxy/handler.go` — ProxyHandler
- Backend selection via balancer
- Request forwarding: preserve headers, add proxy headers (X-Real-IP, X-Forwarded-*)
- Response forwarding: copy headers + body streaming
- Error handling: backend timeout, connection refused, 502/503/504
- Configurable timeouts: connect, read, write (per-domain)
- Hop-by-hop header stripping
- **Test**: Successful proxy, backend error, timeout, header forwarding

### S8-04: Health Checker ⬜ (3 saat)
- `internal/handler/proxy/health.go` — HealthChecker
- Periodic health checks: HTTP GET to configured path
- Configurable: interval, timeout, threshold (fail count), rise (success count)
- TCP check mode (connect only)
- Health state machine: healthy ←→ unhealthy
- Metrics integration
- **Test**: Healthy → unhealthy transition, recovery, timeout handling

### S8-05: Circuit Breaker ⬜ (3 saat)
- `internal/handler/proxy/circuit.go` — CircuitBreaker
- State machine: CLOSED → OPEN → HALF-OPEN → CLOSED
- Configurable: failure threshold, timeout, half-open max requests
- Per-backend circuit breaker
- Metrics: state changes, rejected requests
- **Test**: State transitions, concurrent request handling, recovery

### S8-06: Sticky Sessions ⬜ (2 saat)
- Cookie-based: `Set-Cookie: UWAS_UPSTREAM=backend_hash`
- Header-based: custom header ile backend selection
- IP-based: IP hash ile implicit affinity
- Cookie TTL configurable
- **Test**: Same cookie → same backend, cookie expiry

### S8-07: WebSocket Proxy ⬜ (3 saat)
- `internal/handler/proxy/websocket.go`
- HTTP Upgrade detection
- Connection hijack (http.Hijacker)
- Bidirectional byte copy (goroutine pair)
- Clean shutdown: bir taraf kapanınca diğerini de kapat
- Ping/pong forwarding
- **Test**: WebSocket echo test, disconnect handling

**Sprint 8 Çıkış Kriteri**: Full reverse proxy + load balancer çalışır. 6 LB algoritması, health checking, circuit breaker, sticky sessions, WebSocket proxy. HAProxy-level functionality.

---

## Sprint 9: Admin & MCP (3 gün)

### S9-01: Admin REST API ⬜ (4 saat)
- `internal/admin/api.go` — HTTP API server
- Go 1.22 routing: `mux.HandleFunc("GET /api/v1/domains", ...)`
- API key authentication (Bearer token)
- JSON request/response
- Endpoints: domains CRUD, cache purge/stats, certs list/renew, reload, health, stats, metrics
- **Test**: Her endpoint manual test, auth check

### S9-02: Prometheus Metrics ⬜ (3 saat)
- `internal/metrics/collector.go` — metrics collection
- Prometheus text format export (`/api/v1/metrics`)
- Counters: requests_total, cache_hits/misses, errors
- Histograms: request_duration, upstream_duration
- Gauges: connections_active, cache_memory_bytes, cert_expiry
- Per-host labels
- **Test**: Metrics format validation, counter increment

### S9-03: Built-in Dashboard ⬜ (4 saat)
- `internal/admin/dashboard.go` — embedded HTML/JS
- `/_uwas/dashboard` endpoint
- Real-time: requests/sec, error rate, cache hit ratio
- Upstream health status
- Certificate expiry timeline
- Top paths, top errors
- Vanilla JS + fetch API (no framework)
- `embed.FS` ile binary'ye gömülü
- **Test**: Dashboard loads, API calls çalışır

### S9-04: MCP Server ⬜ (4 saat)
- `internal/mcp/server.go` — MCP protocol handler (stdio transport)
- `internal/mcp/tools.go` — tool implementations
- Tools: domain_list/add/remove, cache_purge/stats, cert_list/renew, stats, config_show/reload, logs_search
- Resources: uwas://config, uwas://stats, uwas://domains/{host}
- **Test**: Her tool çalışır, valid MCP response format

### S9-05: Config Reload via API ⬜ (2 saat)
- `POST /api/v1/reload` → config reload trigger
- Aynı mantık: parse → validate → update (SIGHUP ile aynı)
- Response: success/failure with details
- **Test**: API ile reload, invalid config reject

**Sprint 9 Çıkış Kriteri**: Admin API tam çalışır. Dashboard real-time stats gösterir. MCP server tüm tool'ları expose eder. Prometheus metrics endpoint aktif.

---

## Sprint 10: Polish & Release (3 gün)

### S10-01: CLI Subcommands ⬜ (3 saat)
- `uwas domain list/add/remove` — API client wrapper
- `uwas cert list/renew/import` — certificate management
- `uwas cache purge/stats/clear` — cache management
- `uwas config validate/test/diff` — config tools
- `uwas htaccess convert/validate/test` — .htaccess tools
- **Test**: Her subcommand help text, basic functionality

### S10-02: Error Pages ⬜ (2 saat)
- Default HTML error pages: 400, 403, 404, 500, 502, 503, 504
- Minimal, clean design (inline CSS, no external resources)
- Custom error page override per-domain (config'den)
- **Test**: Her error code doğru page döner

### S10-03: Graceful Shutdown Refinement ⬜ (2 saat)
- Active connection draining with timeout
- Background goroutine cleanup (ACME, health check, cache cleanup)
- Log flush before exit
- PID file cleanup
- Exit code: 0 (clean), 1 (error), 2 (config error)
- **Test**: Active request sırasında shutdown → request tamamlanır

### S10-04: Integration Test Suite ⬜ (4 saat)
- `test/integration/`
- WordPress test: install, activate theme, create post, pretty permalink
- Laravel test: basic route, API endpoint
- Static site test: multi-domain, SPA mode
- Proxy test: upstream failover, health check
- Cache test: hit/miss/purge cycle
- TLS test: auto-cert (staging), SNI routing
- Docker Compose test environment
- **Test**: Tüm integration testler pass

### S10-05: Benchmark Suite ⬜ (2 saat)
- `test/bench/`
- Static file benchmark (hey/wrk)
- PHP request benchmark
- Cache hit/miss benchmark
- Proxy throughput benchmark
- Memory usage under load
- Comparison script: UWAS vs Nginx vs Caddy
- **Test**: Performans baseline oluştur

### S10-06: Documentation ⬜ (3 saat)
- `README.md` — project overview, quick start, features, comparison table
- `docs/quick-start.md` — 5 dakikada kurulum
- `docs/configuration.md` — full config reference
- `docs/wordpress.md` — WordPress migration guide
- `docs/laravel.md` — Laravel deployment guide
- `docs/htaccess-migration.md` — Apache'den geçiş rehberi
- **Test**: Doküman linkler çalışır, örnekler çalışır

### S10-07: Docker & Distribution ⬜ (2 saat)
- `Dockerfile` — multi-stage build
- `docker-compose.yml` — UWAS + PHP-FPM + MariaDB (WordPress demo)
- `.github/workflows/release.yml` — GoReleaser config
- Binary: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64
- **Test**: Docker build, compose up, WordPress accessible

### S10-08: Release Checklist ⬜ (2 saat)
- CHANGELOG.md
- Version tag: v0.1.0
- GitHub release with binaries
- Docker Hub push
- README badges (build, go report, license)
- **Test**: `go install github.com/uwaserver/uwas@v0.1.0` çalışır

**Sprint 10 Çıkış Kriteri**: v0.1.0 release-ready. Binaries, Docker image, documentation, integration tests passing. WordPress demo çalışır.

---

## Summary

| Sprint | Süre | Task Sayısı | Çıktı |
|--------|------|-------------|-------|
| 1: Skeleton | 3 gün | 7 | Çalışan HTTP server |
| 2: Static | 3 gün | 8 | Multi-domain static serving |
| 3: TLS | 4 gün | 9 | Auto HTTPS (Let's Encrypt) |
| 4: FastCGI | 4 gün | 6 | WordPress PHP çalışır |
| 5: Rewrite | 4 gün | 8 | Pretty permalinks + .htaccess |
| 6: Middleware | 3 gün | 10 | Production middleware stack |
| 7: Cache | 5 gün | 9 | Varnish-level caching |
| 8: Proxy | 4 gün | 7 | HAProxy-level LB |
| 9: Admin | 3 gün | 5 | REST API + MCP + Dashboard |
| 10: Polish | 3 gün | 8 | v0.1.0 release |
| **Toplam** | **36 gün** | **77 task** | **Production-ready UWAS** |

**Gerçekçi timeline**: 36 iş günü + %30 buffer = **~10 hafta**

---

## Dependency Graph (Kritik Yol)

```
S1 ──→ S2 ──→ S5 ──→ S6 ──────→ S7 ──→ S9 ──→ S10
  │              ↑                  │
  └──→ S3 ──────┘                  │
  │                                 │
  └──→ S4 ─────────────────────→ S8 ┘
```

- S3 (TLS) ve S4 (FastCGI) paralel çalışılabilir
- S7 (Cache) ve S8 (Proxy) paralel çalışılabilir
- S5 (Rewrite) → S4'ün çıktısına bağımlı (PHP pretty permalinks test için)
- S6 (Middleware) → S2'nin çıktısına bağımlı (request pipeline)
- S9 (Admin) → S7 ve S8'e bağımlı (cache stats, upstream health)
