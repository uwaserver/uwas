# UWAS — Unified Web Application Server

## Project Identity

- **Name**: UWAS (Unified Web Application Server)
- **Pronunciation**: "you-wass"
- **Tagline**: One binary to serve them all
- **Language**: Go (1.23+)
- **License**: Apache 2.0
- **Distribution**: Single static binary, Docker image, apt/yum repo
- **Dependencies**: Minimal — stdlib-first, proven libs where stdlib falls short
- **Dependency Policy**: See Section 1.1
- **Repository**: github.com/uwaserver/uwas
- **CLI**: `uwas`
- **Config**: `/etc/uwas/uwas.yaml`
- **Website**: uwaserver.com
- **GitHub Org**: github.com/uwaserver

---

## 1. Vision & Philosophy

UWAS is a zero-dependency Go web application server that runs in a single binary, eliminating the fragmentation in the current web server ecosystem.

### Problem

Today, running a PHP site (WordPress, Laravel) in production requires the typical stack:

```
Certbot → Nginx → Varnish → PHP-FPM → MySQL
           ↑        ↑         ↑
         config   config    config
       separate  separate  separate
```

5 separate services, 5 separate config formats, 5 separate log formats, 5 separate restart/reload mechanisms. Monitoring, debugging, and maintenance complexity grows exponentially.

### Solution

```
UWAS (single binary)
├── TLS termination + Auto ACME     (← Caddy)
├── HTTP/1.1 + HTTP/2 + HTTP/3      (← Nginx)
├── URL rewrite engine               (← Apache mod_rewrite)
├── .htaccess import/converter       (← Apache/LiteSpeed)
├── Built-in HTTP cache              (← Varnish/LiteSpeed)
├── Middleware chain                  (← Traefik)
├── FastCGI (PHP-FPM)               (← Apache/Nginx)
├── Reverse proxy + LB              (← HAProxy)
├── WAF basics                       (← ModSecurity)
├── Observability                    (← Envoy)
├── Admin REST API                   (← Caddy)
└── MCP Server                       (← LLM-native)
```

### Non-Negotiable Constraints

1. **Minimal dependencies** — stdlib-first; see Dependency Policy below
2. **Single static binary** — `go build` produces one executable
3. **Production-ready defaults** — HTTPS by default, secure headers, sane timeouts
4. **Apache compatibility layer** — WordPress "just works" without config changes
5. **Modular architecture** — each feature can be independently enabled/disabled
6. **LLM-native** — AI-driven server management via MCP server

### 1.1 Dependency Policy

**Philosophy**: Don't reinvent the wheel, but don't fall into dependency hell either.

**Will be written with stdlib** (Go stdlib is sufficient):
- HTTP/1.1 + HTTP/2 server (`net/http`)
- TLS termination + SNI (`crypto/tls`)
- Reverse proxy core (`net/http/httputil`)
- Static file serving (`http.ServeContent`)
- Gzip compression (`compress/gzip`)
- Regex engine (`regexp`)
- JSON handling (`encoding/json`)
- Cryptography (`crypto/*` — ECDSA, RSA, x509, CSR)
- Connection pooling (`sync.Pool`, `sync.Mutex`)
- Signal handling, graceful shutdown (`os/signal`, `context`)

**Allowed external dependencies** (proven, stable, well-maintained):
| Dependency | Reason | Why the alternative is not acceptable |
|-----------|-------|-------------------------------|
| `gopkg.in/yaml.v3` | YAML config parser | Writing a YAML parser from scratch is 3000+ lines and error-prone |
| `github.com/andybalholm/brotli` | Brotli compression | Brotli spec is 10K+ lines, we don't want C bindings |
| `github.com/klauspost/compress/zstd` | Zstandard compression | Same reason |
| `github.com/quic-go/quic-go` | HTTP/3 (Phase 2+) | QUIC is a project on its own |
| `golang.org/x/crypto` | OCSP, ACME helper utils | Go extended stdlib, quasi-official |
| `golang.org/x/net` | HTTP/2 server push, ECH | Go extended stdlib |

**Strictly forbidden**:
- Web frameworks (gin, echo, fiber, etc.)
- ORM or database drivers
- Logging frameworks (zap, logrus, etc.) — custom structured logger
- Dependency injection frameworks
- Any "kitchen sink" library

**Target**: Total **< 15 direct dependencies** in `go.sum`, including indirect **< 40 total**.

---

## 2. Architecture Overview

### 2.1 Process Model

```
┌─────────────────────────────────────────┐
│              Master Process              │
│  - Config parsing & validation           │
│  - Signal handling (SIGHUP, SIGTERM)     │
│  - Worker lifecycle management           │
│  - ACME certificate orchestration        │
│  - Admin API server (:9443)              │
│  - MCP Server                            │
└────────────────┬────────────────────────┘
                 │ fork/exec
    ┌────────────┼────────────┐
    ▼            ▼            ▼
┌────────┐ ┌────────┐ ┌────────┐
│Worker 1│ │Worker 2│ │Worker N│   (N = GOMAXPROCS = CPU cores)
│        │ │        │ │        │
│Listener│ │Listener│ │Listener│   SO_REUSEPORT per worker
│  Pool  │ │  Pool  │ │  Pool  │
└────────┘ └────────┘ └────────┘
```

Thanks to Go's goroutine model, each worker process can handle thousands of concurrent connections. Kernel-level load distribution via SO_REUSEPORT.

### 2.2 Request Pipeline

```
TCP Connection
  │
  ▼
TLS Handshake (SNI → domain → certificate)
  │
  ▼
HTTP Parse (HTTP/1.1, HTTP/2, HTTP/3 demux)
  │
  ▼
Virtual Host Lookup (Host header → domain config)
  │
  ▼
Security Guard (blocked paths, IP deny, rate limit check)
  │
  ▼
Rewrite Engine (URL transform, regex rules, conditions)
  │
  ▼
try_files Logic (static file? directory? index file? fallback?)
  │
  ▼
Cache Lookup (hit → serve from cache, skip handler)
  │         │
  │ miss    │ hit
  ▼         └──→ Response Middleware → Client
Handler Router
  ├── Static File Handler (sendfile, range, etag)
  ├── FastCGI Handler (PHP-FPM, connection pool)
  ├── Reverse Proxy Handler (upstream pool, health check)
  └── Redirect Handler (301/302/307/308)
  │
  ▼
Response Middleware Chain
  ├── Cache Store (if cacheable)
  ├── Compression (brotli/gzip/zstd)
  ├── Security Headers (HSTS, CSP, X-Frame, CORS)
  ├── Access Log (structured JSON)
  └── Metrics Collection
  │
  ▼
Client Response
```

### 2.3 Module System

Each major feature implements a Go interface and can be independently enabled/disabled:

```go
type Module interface {
    Name() string
    Init(config ModuleConfig) error
    Start() error
    Stop() error
    Health() HealthStatus
}

type Middleware interface {
    Module
    Process(ctx *RequestContext, next Handler) error
}

type Handler interface {
    Module
    CanHandle(ctx *RequestContext) bool
    Handle(ctx *RequestContext) error
}
```

---

## 3. Configuration System

### 3.1 Config Format: YAML

Main config file: `/etc/uwas/uwas.yaml`

```yaml
# /etc/uwas/uwas.yaml
global:
  worker_count: auto          # auto = runtime.NumCPU()
  max_connections: 65536
  pid_file: /var/run/uwas.pid
  log_level: info             # debug, info, warn, error
  log_format: json            # json, clf (Combined Log Format)

  timeouts:
    read: 30s
    write: 60s
    idle: 120s
    shutdown_grace: 30s

  admin:
    listen: "127.0.0.1:9443"
    enabled: true
    api_key: "${UWAS_ADMIN_KEY}"    # env var expansion

  mcp:
    enabled: true
    listen: "127.0.0.1:9444"

  acme:
    email: "admin@example.com"
    ca_url: "https://acme-v02.api.letsencrypt.org/directory"
    storage: /var/lib/uwas/certs
    dns_provider: cloudflare       # optional: DNS-01 challenge
    dns_credentials:
      api_token: "${CF_API_TOKEN}"
    on_demand: false               # true = certificate is obtained at runtime
    on_demand_ask: "http://localhost:9443/api/v1/domains/verify"

  cache:
    enabled: true
    memory_limit: 512MB
    disk_path: /var/cache/uwas
    disk_limit: 10GB
    default_ttl: 3600
    grace_ttl: 86400              # Varnish grace mode: serve stale for 24 hours
    stale_while_revalidate: true
    purge_key: "${UWAS_PURGE_KEY}"

# Domain Definitions
domains:
  - host: "example.com"
    aliases:
      - "www.example.com"
    root: /var/www/example.com/public
    type: php                     # static | php | proxy | redirect

    ssl:
      mode: auto                  # auto | manual | off
      # manual mode:
      # cert: /path/to/cert.pem
      # key: /path/to/key.pem
      min_version: "1.2"

    php:
      fpm_address: "unix:/var/run/php/php8.3-fpm.sock"
      # fpm_address: "tcp://127.0.0.1:9000"   # TCP alternative
      index_files:
        - index.php
        - index.html
      max_upload: 64MB
      timeout: 300s
      env:
        APP_ENV: production

    cache:
      enabled: true
      ttl: 1800
      rules:
        - match: "^/wp-content/uploads/"
          ttl: 86400
        - match: "^/wp-admin/"
          bypass: true
        - match: "^/api/"
          bypass: true
      tags:
        - "site:example"
      esi: true                   # Edge Side Includes

    rewrites:
      # WordPress pretty permalinks
      - match: "^/(.+)$"
        to: "/index.php"
        conditions:
          - "!is_file"
          - "!is_dir"
      # Custom redirect
      - match: "^/old-page$"
        to: "/new-page"
        status: 301

    htaccess:
      mode: import               # import | convert | off
      # import: parse at runtime, cache in memory
      # convert: convert to YAML at startup, then ignore
      # off: completely ignore .htaccess files

    security:
      blocked_paths:
        - ".git"
        - ".env"
        - "wp-config.php"
        - ".htpasswd"
      hotlink_protection:
        enabled: true
        allowed_referers:
          - "example.com"
          - "www.example.com"
        extensions:
          - jpg
          - png
          - gif
          - webp
      rate_limit:
        requests: 100
        window: 60s
        by: ip                   # ip | header:X-Forwarded-For
      waf:
        enabled: true
        rules:
          - sql_injection
          - xss
          - path_traversal

    headers:
      add:
        X-Content-Type-Options: nosniff
        X-Frame-Options: SAMEORIGIN
        Referrer-Policy: strict-origin-when-cross-origin
      remove:
        - X-Powered-By
        - Server

    compression:
      enabled: true
      algorithms:
        - brotli
        - gzip
        - zstd
      min_size: 1024
      types:
        - text/html
        - text/css
        - application/javascript
        - application/json
        - image/svg+xml

    access_log:
      path: /var/log/uwas/example.com.access.log
      format: json               # json | clf | custom
      buffer_size: 4096
      rotate:
        max_size: 100MB
        max_age: 30d
        max_backups: 10

    error_pages:
      404: /errors/404.html
      500: /errors/500.html
      503: /errors/maintenance.html

  - host: "api.example.com"
    type: proxy
    ssl:
      mode: auto
    proxy:
      upstreams:
        - address: "http://127.0.0.1:3000"
          weight: 3
        - address: "http://127.0.0.1:3001"
          weight: 1
      algorithm: least_conn      # round_robin | least_conn | ip_hash | uri_hash | random
      health_check:
        path: /health
        interval: 10s
        timeout: 5s
        threshold: 3             # how many failures before unhealthy
        rise: 2                  # how many successes before healthy
      sticky:
        type: cookie             # cookie | header | ip
        cookie_name: "UWAS_UPSTREAM"
        ttl: 3600
      circuit_breaker:
        threshold: 5             # how many errors before open
        timeout: 30s             # wait time from open → half-open
      websocket: true
      timeouts:
        connect: 5s
        read: 60s
        write: 60s

  - host: "old.example.com"
    type: redirect
    ssl:
      mode: auto
    redirect:
      target: "https://example.com"
      status: 301
      preserve_path: true
```

### 3.2 .htaccess Compatibility

Supported .htaccess directives (Apache compatible):

| Directive | Support | Notes |
|-----------|---------|-------|
| `RewriteEngine On/Off` | ✅ Full | |
| `RewriteRule` | ✅ Full | PCRE regex, backreferences, flags [L,R,QSA,NC] |
| `RewriteCond` | ✅ Full | %{REQUEST_URI}, %{HTTP_HOST}, %{QUERY_STRING}, etc. |
| `Redirect` / `RedirectMatch` | ✅ Full | 301, 302, 307, 308 |
| `ErrorDocument` | ✅ Full | Local path or URL |
| `DirectoryIndex` | ✅ Full | Multiple index files |
| `Header set/unset/append` | ✅ Full | Response header manipulation |
| `ExpiresActive` / `ExpiresByType` | ✅ Full | Cache-Control header generation |
| `Options -Indexes` | ✅ Full | Directory listing control |
| `Options -FollowSymLinks` | ✅ Full | Symlink follow control |
| `AuthType Basic` | ✅ Full | Basic auth with .htpasswd |
| `Deny/Allow/Order` | ✅ Full | IP-based access control (legacy syntax) |
| `Require` | ✅ Full | Apache 2.4+ syntax |
| `SetEnvIf` | ⚠️ Partial | Basic use cases |
| `FilesMatch` / `Files` | ✅ Full | File pattern matching |
| `<IfModule>` | ✅ Full | Module existence check (always returns true) |
| `php_value` / `php_flag` | ⚠️ Ignored | Managed via PHP-FPM pool config |

**Unsupported** (and will not be supported):
- `mod_php` directives (php_admin_value, etc.) — managed via FPM pool config
- `SSLRequireSSL` — UWAS already defaults to HTTPS
- `mod_proxy` directives — managed via UWAS native proxy config

### 3.3 .htaccess Import Strategy

```
When an .htaccess file is found:
  │
  ├── mode: import
  │     ├── Parse it
  │     ├── Convert supported directives to in-memory rules
  │     ├── Watch for changes via inotify/fswatch
  │     ├── Automatically re-parse on changes
  │     └── Active at runtime (performance: O(1) lookup after initial parse)
  │
  ├── mode: convert
  │     ├── Parse it
  │     ├── Convert to uwas.yaml snippet
  │     ├── Write to stdout or merge into config
  │     ├── Rename .htaccess to .htaccess.bak
  │     └── From now on, only run from YAML config
  │
  └── mode: off
        └── Completely ignore .htaccess files
```

### 3.4 Config Hot Reload

```
SIGHUP → Master Process
  │
  ├── Parse the new config file
  ├── Validation (syntax + semantic)
  ├── If error → write to log, continue with old config
  │
  ├── If successful →
  │   ├── Create new listeners (a new domain may have been added)
  │   ├── Send graceful reload signal to workers
  │   ├── Old workers drain existing connections
  │   ├── New workers start with the new config
  │   └── Old workers shut down once draining is complete
  │
  └── Zero-downtime ✅
```

---

## 4. TLS & ACME Module

### 4.1 Certificate Lifecycle

```
Domain config added
  │
  ├── ssl.mode: auto
  │   │
  │   ├── Is there a valid cert in the disk cache?
  │   │   ├── Yes → Load it, set up renewal timer
  │   │   └── No → Start ACME flow
  │   │
  │   ├── ACME Flow:
  │   │   ├── Create/load account key
  │   │   ├── Create order
  │   │   ├── Choose challenge (HTTP-01 or DNS-01)
  │   │   │   ├── HTTP-01: Set up /.well-known/acme-challenge/ handler
  │   │   │   └── DNS-01: Create TXT record (via provider API)
  │   │   ├── Fulfill the challenge
  │   │   ├── Create CSR (ECDSA P-256 default)
  │   │   ├── Obtain certificate
  │   │   ├── Save to disk
  │   │   └── Update tls.Config.GetCertificate callback
  │   │
  │   └── Renewal:
  │       ├── Auto-renew 30 days before cert expiry
  │       ├── On failure → retry with exponential backoff
  │       ├── If still failing 3 days before expiry → alert (log + metrics)
  │       └── Current cert continues to be used during renewal
  │
  ├── ssl.mode: manual
  │   └── Load the provided cert/key files, watch with fswatch
  │
  └── ssl.mode: off
      └── Plain HTTP (if no redirect)
```

### 4.2 On-Demand TLS

```
TLS handshake received for an unknown domain
  │
  ├── on_demand: false → TLS alert, connection reject
  │
  └── on_demand: true
      ├── HTTP GET to on_demand_ask URL → 200 OK?
      │   ├── No → reject
      │   └── Yes → Start ACME flow
      │       ├── Handshake is held (watch for client timeout)
      │       ├── Cert is obtained and cached
      │       └── Handshake completes
      └── Rate limiting: max 10 certs/minute, max 50 pending
```

### 4.3 TLS Configuration

```go
// SNI-based certificate selection
tls.Config{
    GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
        // 1. Exact match: hello.ServerName → cert cache
        // 2. Wildcard match: *.example.com
        // 3. On-demand TLS (if enabled)
        // 4. Default/fallback cert
    },
    MinVersion: tls.VersionTLS12,
    CipherSuites: modernCipherSuites,
    NextProtos: []string{"h2", "http/1.1"},    // ALPN
    // HTTP/3: separate QUIC listener, same cert
}
```

### 4.4 OCSP Stapling

- Periodic OCSP response fetch for each cert
- Stapled response attached to tls.Config
- If the OCSP responder errors → use cached response (grace period)
- More aggressive retry for Must-Staple certs

---

## 5. HTTP Engine

### 5.1 Protocol Support

| Protocol | Support | Notes |
|----------|---------|-------|
| HTTP/1.0 | ✅ | Legacy compatibility |
| HTTP/1.1 | ✅ | Keep-alive, pipelining, chunked transfer |
| HTTP/2 | ✅ | Full h2 over TLS, h2c (cleartext) optional |
| HTTP/3 | 🗓️ Phase 2 | QUIC over UDP (Go's quic package is evolving) |

### 5.2 Virtual Host Router

```go
type VirtualHost struct {
    Host         string              // exact: "example.com"
    Aliases      []string            // ["www.example.com"]
    Wildcards    []string            // ["*.example.com"]
    Config       *DomainConfig
}

// Lookup priority:
// 1. Exact host match
// 2. Alias match
// 3. Wildcard match (longest first: "*.sub.example.com" > "*.example.com")
// 4. Default host (first defined domain)
```

### 5.3 try_files Logic

Nginx's most commonly used pattern, as a first-class citizen:

```yaml
# Config
try_files:
  - "$uri"                    # /about → does the file /var/www/about exist?
  - "$uri/"                   # /about/ → does the directory /var/www/about/ exist?
  - "$uri/index.html"         # /about/ → does /var/www/about/index.html exist?
  - "$uri/index.php"          # /about/ → does /var/www/about/index.php exist?
  - "/index.php"              # Fallback → WordPress entry point
```

```go
func tryFiles(ctx *RequestContext, candidates []string) (string, bool) {
    for _, candidate := range candidates {
        resolved := expandVariables(candidate, ctx)
        fullPath := filepath.Join(ctx.DocumentRoot, resolved)

        // Security: path traversal protection
        if !isInsideRoot(fullPath, ctx.DocumentRoot) {
            continue
        }

        stat, err := os.Stat(fullPath)
        if err != nil {
            continue
        }

        if stat.IsDir() {
            // If directory → look for index file
            continue
        }

        // File found
        return resolved, true
    }
    return "", false
}
```

---

## 6. Rewrite Engine

### 6.1 Rule Processing

Apache mod_rewrite compatible regex-based rewrite engine:

```go
type RewriteRule struct {
    Pattern    *regexp.Regexp    // PCRE regex
    Target     string            // Replacement string ($1, $2 backrefs)
    Conditions []RewriteCondition
    Flags      RewriteFlags
}

type RewriteCondition struct {
    TestString string            // %{REQUEST_URI}, %{HTTP_HOST}, etc.
    Pattern    *regexp.Regexp
    Negate     bool              // [!F] — negate the CondPattern
    Type       string            // "regex" | "is_file" | "is_dir" | "is_symlink"
}

type RewriteFlags struct {
    Last       bool    // [L] — stop if rule matches
    Redirect   int     // [R=301] — HTTP redirect
    QSAppend   bool    // [QSA] — preserve query string
    NoCase     bool    // [NC] — case-insensitive
    Chain      bool    // [C] — chain with next rule
    Skip       int     // [S=N] — skip N rules
    PassThrough bool   // [PT] — continue to handler after rewrite
    Forbidden  bool    // [F] — return 403
    Gone       bool    // [G] — return 410
}
```

### 6.2 Server Variables

```go
// Variables available in rewrite conditions
var serverVariables = map[string]func(*RequestContext) string{
    "REQUEST_URI":      func(ctx) string { return ctx.Request.URL.Path },
    "REQUEST_FILENAME": func(ctx) string { return ctx.ResolvedPath },
    "QUERY_STRING":     func(ctx) string { return ctx.Request.URL.RawQuery },
    "HTTP_HOST":        func(ctx) string { return ctx.Request.Host },
    "HTTP_REFERER":     func(ctx) string { return ctx.Request.Referer() },
    "HTTP_USER_AGENT":  func(ctx) string { return ctx.Request.UserAgent() },
    "REMOTE_ADDR":      func(ctx) string { return ctx.RemoteIP },
    "REQUEST_METHOD":   func(ctx) string { return ctx.Request.Method },
    "SERVER_PORT":      func(ctx) string { return ctx.ServerPort },
    "HTTPS":            func(ctx) string { ... },
    "THE_REQUEST":      func(ctx) string { ... },  // "GET /path HTTP/1.1"
    "DOCUMENT_ROOT":    func(ctx) string { return ctx.DocumentRoot },
    "SERVER_NAME":      func(ctx) string { return ctx.VHost.Host },
    "TIME":             func(ctx) string { ... },
    "TIME_YEAR":        func(ctx) string { ... },
}
```

---

## 7. Cache Engine

### 7.1 Architecture

```
Request → Cache Key Generate → Lookup
                                 │
                    ┌────────────┼────────────┐
                    ▼            ▼            ▼
              Memory Cache   Disk Cache    MISS
              (LRU, fast)   (large, slow)    │
                    │            │            ▼
                    ▼            ▼       Backend Handler
                   HIT          HIT          │
                    │            │            ▼
                    ▼            ▼       Response
               Freshness     Freshness       │
                Check         Check          ▼
                    │            │      Cache Store
                    ▼            ▼     (if cacheable)
               Serve/Reval  Serve/Reval
```

### 7.2 Cache Key

```go
type CacheKey struct {
    Host         string
    Path         string
    QueryString  string    // configurable: include/exclude/specific params
    Method       string    // GET only (default), HEAD
    Vary         []string  // fields from the Vary header
    // Custom key fragments (cookie, header, etc.)
}

func (k CacheKey) Hash() string {
    h := xxhash.New()  // not in stdlib — use simple FNV-1a
    h.Write([]byte(k.Host))
    h.Write([]byte(k.Path))
    // ...
    return hex.EncodeToString(h.Sum(nil))
}
```

### 7.3 Memory Cache (L1)

```go
type MemoryCache struct {
    shards    [256]*CacheShard    // Sharded mutex map (reduces lock contention)
    maxMemory int64
    usedMem   atomic.Int64
    lru       *ShardedLRU
}

type CacheShard struct {
    mu    sync.RWMutex
    items map[string]*CacheEntry
}

type CacheEntry struct {
    Key         string
    StatusCode  int
    Headers     http.Header
    Body        []byte          // < 1MB → memory, > 1MB → reference to disk
    Created     time.Time
    TTL         time.Duration
    GraceTTL    time.Duration   // Varnish grace mode
    Tags        []string        // Tag-based invalidation
    ETag        string
    LastMod     time.Time
    HitCount    atomic.Int64
    LastAccess  atomic.Int64    // Unix timestamp, for LRU eviction
}
```

### 7.4 Disk Cache (L2)

```
/var/cache/uwas/
├── ab/                         # First 2 hash characters
│   └── cd/                     # Next 2 characters
│       └── abcdef1234.cache    # Full hash
└── _meta/
    └── tags/
        ├── site:example.idx    # Tag → key mapping
        └── page:home.idx
```

### 7.5 Grace Mode (from Varnish)

```
Request received, cache entry expired
  │
  ├── Within grace_ttl?
  │   ├── Yes → Serve stale content
  │   │          Start async revalidation in the background
  │   │          (a goroutine sends a request to the backend)
  │   │          Client does not wait → low latency
  │   │
  │   └── No → Normal request to backend
  │
  └── Is the backend down?
      ├── Yes and within grace → serve stale
      ├── Yes and outside grace → 503 (or custom error page)
      └── No → normal flow
```

### 7.6 Edge Side Includes (ESI)

Caching individual fragments within HTML separately:

```html
<!-- Main page: TTL=60s -->
<html>
<body>
  <header>
    <!--esi <esi:include src="/fragments/nav" /> -->
  </header>
  <main>
    <!-- Dynamic content -->
  </main>
  <footer>
    <!--esi <esi:include src="/fragments/footer" /> -->
  </footer>
</body>
</html>
```

Each `<esi:include>` has its own cache key and is cached with its own TTL. Even if the main page is expired, nav and footer can still be served from cache.

### 7.7 Cache Invalidation

```yaml
# Tag-based purge (the most powerful method)
# POST /api/v1/cache/purge
# { "tags": ["site:example", "type:post"] }

# Path-based purge
# PURGE /path/to/page
# X-Purge-Key: ${UWAS_PURGE_KEY}

# Wildcard purge
# PURGE /wp-content/*
# X-Purge-Key: ${UWAS_PURGE_KEY}

# Full domain purge
# POST /api/v1/cache/purge
# { "domain": "example.com" }
```

---

## 8. FastCGI / PHP Handler

### 8.1 FastCGI Protocol Implementation

Go stdlib has a FastCGI server (accept) but no client (connect). Client implementation:

```go
type FastCGIClient struct {
    pool       *ConnectionPool
    address    string          // Unix socket or TCP
    maxConns   int
    timeout    time.Duration
}

type ConnectionPool struct {
    mu       sync.Mutex
    idle     []*fcgiConn
    active   int32
    maxIdle  int
    maxOpen  int
}

// FastCGI record types
const (
    fcgiBeginRequest    = 1
    fcgiAbortRequest    = 2
    fcgiEndRequest      = 3
    fcgiParams          = 4
    fcgiStdin           = 5
    fcgiStdout          = 6
    fcgiStderr          = 7
)
```

### 8.2 CGI Environment Variables

```go
// Environment variables to be sent to PHP-FPM
func buildFCGIEnv(ctx *RequestContext) map[string]string {
    return map[string]string{
        "SCRIPT_FILENAME":  ctx.ScriptFilename,   // /var/www/site/index.php
        "SCRIPT_NAME":      ctx.ScriptName,        // /index.php
        "PATH_INFO":        ctx.PathInfo,           // /controller/action
        "PATH_TRANSLATED":  ctx.PathTranslated,
        "DOCUMENT_ROOT":    ctx.DocumentRoot,
        "REQUEST_URI":      ctx.Request.URL.RequestURI(),
        "REQUEST_METHOD":   ctx.Request.Method,
        "QUERY_STRING":     ctx.Request.URL.RawQuery,
        "CONTENT_TYPE":     ctx.Request.Header.Get("Content-Type"),
        "CONTENT_LENGTH":   fmt.Sprint(ctx.Request.ContentLength),
        "SERVER_NAME":      ctx.VHost.Host,
        "SERVER_PORT":      ctx.ServerPort,
        "SERVER_PROTOCOL":  ctx.Request.Proto,
        "REMOTE_ADDR":      ctx.RemoteIP,
        "REMOTE_PORT":      ctx.RemotePort,
        "HTTPS":            ctx.IsHTTPS(),
        "HTTP_HOST":        ctx.Request.Host,
        // All HTTP_* headers forwarded
    }
}
```

### 8.3 SCRIPT_NAME / PATH_INFO Split

Critical for PHP frameworks:

```
Request: /blog/index.php/posts/123?page=2

SCRIPT_FILENAME = /var/www/blog/index.php
SCRIPT_NAME     = /blog/index.php
PATH_INFO       = /posts/123
QUERY_STRING    = page=2
```

### 8.4 Upload Handling

```go
// To avoid memory usage on large uploads:
// 1. Stream the request body to a temp file
// 2. Send the temp file as stdin to PHP-FPM
// 3. Delete the temp file when done

func handleUpload(ctx *RequestContext) error {
    if ctx.Request.ContentLength > ctx.MaxUploadSize {
        return ErrRequestEntityTooLarge
    }

    // Stream to temp file (do not hold in memory)
    tmpFile, _ := os.CreateTemp(ctx.TempDir, "uwas-upload-*")
    defer os.Remove(tmpFile.Name())

    written, _ := io.Copy(tmpFile, io.LimitReader(ctx.Request.Body, ctx.MaxUploadSize))
    tmpFile.Seek(0, 0)

    // Send as FastCGI stdin
    return ctx.FCGIClient.SendRequest(ctx.FCGIEnv, tmpFile)
}
```

---

## 9. Reverse Proxy & Load Balancer

### 9.1 Upstream Pool

```go
type UpstreamPool struct {
    Name       string
    Backends   []*Backend
    Algorithm  LoadBalancerAlgorithm
    HealthChk  *HealthChecker
    Circuit    *CircuitBreaker
}

type Backend struct {
    Address     string
    Weight      int
    MaxConns    int
    State       atomic.Int32     // healthy | unhealthy | draining
    ActiveConns atomic.Int32
    TotalReqs   atomic.Int64
    Failures    atomic.Int64
    Latency     *HistogramAtomic // p50, p95, p99 tracking
}
```

### 9.2 Load Balancing Algorithms

| Algorithm | Description | Use Case |
|-----------|-------------|----------|
| `round_robin` | Distribute in order, based on weight | General purpose |
| `least_conn` | Send to the backend with fewest active connections | Long-lived requests |
| `ip_hash` | Fixed backend via client IP hash | Session affinity |
| `uri_hash` | Fixed backend via URI hash | Cache-friendly |
| `random` | Random selection, power of 2 choices | Simple, low overhead |
| `weighted_round_robin` | Smooth weighted round robin | Backends with different capacities |

### 9.3 Health Check

```go
type HealthChecker struct {
    Path       string         // /health
    Interval   time.Duration  // 10s
    Timeout    time.Duration  // 5s
    Threshold  int            // 3 consecutive failures → unhealthy
    Rise       int            // 2 consecutive successes → healthy

    // Advanced
    Method     string         // GET (default) | HEAD | TCP
    ExpectCode int            // 200 (default)
    ExpectBody string         // optional: response body contains
}
```

### 9.4 Circuit Breaker (from HAProxy)

```
State: CLOSED (normal)
  │
  ├── Error count exceeded threshold
  ▼
State: OPEN (all requests rejected)
  │
  ├── Timeout period elapsed
  ▼
State: HALF-OPEN (let one request through, test it)
  │
  ├── Successful → CLOSED
  └── Failed → OPEN (timer reset)
```

### 9.5 WebSocket Proxy

```go
func proxyWebSocket(ctx *RequestContext, backend *Backend) error {
    // 1. Open TCP connection to backend
    // 2. Forward HTTP Upgrade handshake
    // 3. Bidirectional byte copy (goroutine pair)
    // 4. When one side closes, close the other

    backendConn, _ := net.DialTimeout("tcp", backend.Address, 5*time.Second)

    // Hijack client connection
    hijacker := ctx.ResponseWriter.(http.Hijacker)
    clientConn, _, _ := hijacker.Hijack()

    // Bidirectional copy
    go io.Copy(backendConn, clientConn)
    io.Copy(clientConn, backendConn)
}
```

---

## 10. Middleware Chain

### 10.1 Built-in Middlewares

| Middleware | Order | Description |
|-----------|-------|-------------|
| `RequestID` | 1 | Assign unique request ID (UUID v7) |
| `RealIP` | 2 | Parse X-Forwarded-For / X-Real-IP |
| `RateLimit` | 3 | Token bucket per-IP |
| `SecurityGuard` | 4 | Blocked paths, WAF rules |
| `AccessControl` | 5 | IP whitelist/blacklist, basic auth |
| `RewriteEngine` | 6 | URL transform |
| `CacheLookup` | 7 | Cache hit check |
| `Compression` | 8 | Brotli/Gzip/Zstd response compress |
| `SecurityHeaders` | 9 | HSTS, CSP, X-Frame, CORS |
| `AccessLog` | 10 | Structured JSON log |
| `Metrics` | 11 | Prometheus metrics collection |

### 10.2 WAF Rules (Basic)

Basic protection rules in Phase 1:

```go
type WAFRule struct {
    ID          string
    Name        string
    Phase       int        // 1=request, 2=response
    Targets     []string   // "ARGS", "REQUEST_HEADERS", "REQUEST_BODY"
    Pattern     *regexp.Regexp
    Action      string     // "deny" | "log" | "pass"
    Severity    string     // "critical" | "error" | "warning"
}

// Built-in rule sets:
// - SQL injection patterns (UNION, SELECT, DROP, --, etc.)
// - XSS patterns (<script>, onerror=, javascript:, etc.)
// - Path traversal (../, %2e%2e, etc.)
// - Command injection (;, |, &&, $(), backtick)
// - File inclusion (php://, file://, data://)
```

---

## 11. Static File Server

### 11.1 Features

```go
type StaticHandler struct {
    Root              string
    IndexFiles        []string    // ["index.html", "index.htm"]
    DirectoryListing  bool        // default: false
    SPAMode           bool        // true: 404 → index.html
    PreCompressed     []string    // [".br", ".gz", ".zst"]
    ETagMode          string      // "weak" (mtime+size) | "strong" (content hash)
    MaxAge            int         // Cache-Control max-age
    Immutable         bool        // Cache-Control: immutable (hashed assets)
}
```

### 11.2 Zero-Copy Serving

```go
func serveFile(w http.ResponseWriter, r *http.Request, path string) {
    f, _ := os.Open(path)
    stat, _ := f.Stat()

    // ETag check → 304 Not Modified
    etag := generateETag(stat)
    if r.Header.Get("If-None-Match") == etag {
        w.WriteHeader(304)
        return
    }

    // Range request support (video streaming)
    // Go's http.ServeContent handles this
    w.Header().Set("ETag", etag)
    w.Header().Set("Accept-Ranges", "bytes")

    // If a pre-compressed file exists, serve that instead
    if acceptsBrotli(r) {
        if brFile, err := os.Open(path + ".br"); err == nil {
            w.Header().Set("Content-Encoding", "br")
            io.Copy(w, brFile)
            return
        }
    }

    http.ServeContent(w, r, stat.Name(), stat.ModTime(), f)
    // Go's ServeContent internally uses the sendfile syscall
}
```

### 11.3 MIME Type Registry

Extensible MIME type mapping, including modern formats:

```go
var defaultMIMETypes = map[string]string{
    ".html":  "text/html; charset=utf-8",
    ".css":   "text/css; charset=utf-8",
    ".js":    "application/javascript; charset=utf-8",
    ".json":  "application/json; charset=utf-8",
    ".woff2": "font/woff2",
    ".woff":  "font/woff",
    ".webp":  "image/webp",
    ".avif":  "image/avif",
    ".svg":   "image/svg+xml",
    ".wasm":  "application/wasm",
    ".mjs":   "application/javascript",
    ".webm":  "video/webm",
    ".mp4":   "video/mp4",
    // ... 100+ more
}
```

---

## 12. Observability

### 12.1 Access Log

```json
{
  "timestamp": "2025-03-21T14:30:00.123Z",
  "request_id": "01JQKM...",
  "remote_ip": "1.2.3.4",
  "method": "GET",
  "host": "example.com",
  "path": "/blog/hello-world",
  "query": "page=2",
  "protocol": "HTTP/2.0",
  "status": 200,
  "bytes_sent": 15234,
  "duration_ms": 12.5,
  "ttfb_ms": 8.2,
  "upstream": "127.0.0.1:9000",
  "upstream_duration_ms": 10.1,
  "cache": "HIT",
  "tls_version": "TLSv1.3",
  "tls_cipher": "TLS_AES_128_GCM_SHA256",
  "user_agent": "Mozilla/5.0...",
  "referer": "https://example.com/",
  "compression": "br",
  "country": "TR"
}
```

### 12.2 Metrics (Prometheus Format)

```
# HELP uwas_requests_total Total HTTP requests
# TYPE uwas_requests_total counter
uwas_requests_total{host="example.com",method="GET",status="200"} 12345

# HELP uwas_request_duration_seconds Request duration histogram
# TYPE uwas_request_duration_seconds histogram
uwas_request_duration_seconds_bucket{host="example.com",le="0.01"} 8000
uwas_request_duration_seconds_bucket{host="example.com",le="0.05"} 11000

# HELP uwas_cache_hits_total Cache hits
uwas_cache_hits_total{host="example.com",level="memory"} 9500
uwas_cache_hits_total{host="example.com",level="disk"} 1200
uwas_cache_misses_total{host="example.com"} 1300

# HELP uwas_upstream_health Backend health status
uwas_upstream_health{upstream="app:3000"} 1

# HELP uwas_connections_active Current active connections
uwas_connections_active 342

# HELP uwas_tls_certificates_expiry Certificate expiry (unix timestamp)
uwas_tls_certificates_expiry{host="example.com"} 1740000000
```

### 12.3 Built-in Dashboard

Minimal HTML dashboard at the `/_uwas/dashboard` endpoint:

- Real-time request rate, error rate, latency percentiles
- Cache hit ratio (memory vs disk vs miss)
- Upstream health status
- Active connections count
- TLS certificate expiry countdown
- Top paths by request count
- Top 4xx/5xx errors
- Memory/goroutine usage

---

## 13. Admin REST API

### 13.1 Endpoints

```
Base: https://127.0.0.1:9443/api/v1

# Domain Management
GET    /domains                    # List all domains
POST   /domains                    # Add new domain
GET    /domains/{host}             # Domain details
PUT    /domains/{host}             # Update domain
DELETE /domains/{host}             # Delete domain

# Cache Management
POST   /cache/purge                # Purge by tag/path/domain
GET    /cache/stats                # Cache statistics
DELETE /cache                      # Clear all cache

# Certificate Management
GET    /certs                      # List all certificates
POST   /certs/{host}/renew         # Trigger manual renewal
GET    /certs/{host}               # Cert details (expiry, issuer, etc.)

# Upstream Management
GET    /upstreams                  # List upstream pools
PUT    /upstreams/{name}/backends  # Add/remove backends
POST   /upstreams/{name}/drain/{backend}  # Drain backend

# Server Management
POST   /reload                     # Trigger config reload
GET    /health                     # Server health check
GET    /stats                      # General statistics
GET    /metrics                    # Prometheus format

# Config
GET    /config                     # Current config (sanitized)
PATCH  /config                     # Partial config update
```

### 13.2 Authentication

```yaml
admin:
  listen: "127.0.0.1:9443"     # Localhost only
  api_key: "${UWAS_ADMIN_KEY}" # Bearer token auth
  # or
  mutual_tls:
    ca: /path/to/ca.pem        # Client cert auth
```

---

## 14. MCP Server

### 14.1 Tools

```
uwas_domain_list          # List domains
uwas_domain_add           # Add new domain
uwas_domain_remove        # Remove domain
uwas_domain_update        # Update domain config

uwas_cache_purge          # Clear cache (tag/path/domain)
uwas_cache_stats          # Cache statistics

uwas_cert_list            # List certificates
uwas_cert_renew           # Manual renewal

uwas_upstream_list        # List upstreams
uwas_upstream_health      # Backend health status

uwas_stats                # General server statistics
uwas_logs_search          # Search access logs

uwas_config_show          # Show current config
uwas_config_reload        # Config reload

uwas_waf_toggle           # Enable/disable WAF rules
uwas_rate_limit_adjust    # Adjust rate limit
```

### 14.2 Resources

```
uwas://config              # Current config
uwas://stats               # Real-time stats
uwas://domains/{host}      # Domain details
uwas://certs/{host}        # Cert information
uwas://logs/recent         # Last 100 log entries
```

---

## 15. CLI Interface

```bash
# Server Management
uwas serve                          # Start in foreground
uwas serve -c /path/to/uwas.yaml   # Custom config
uwas serve -d                       # Daemon mode
uwas reload                         # Send SIGHUP
uwas stop                           # Graceful shutdown
uwas status                         # Status information

# Domain Management
uwas domain list
uwas domain add example.com --root /var/www/example --type php
uwas domain remove example.com

# Certificate Management
uwas cert list
uwas cert renew example.com
uwas cert import example.com --cert cert.pem --key key.pem

# Cache Management
uwas cache purge --tag "site:example"
uwas cache purge --path "/blog/*"
uwas cache purge --domain example.com
uwas cache stats

# .htaccess Tools
uwas htaccess convert /var/www/site/.htaccess   # → YAML output
uwas htaccess validate /var/www/site/.htaccess   # Syntax check
uwas htaccess test /var/www/site/.htaccess -u "/test/path"  # Rule test

# Config Tools
uwas config validate                # Config syntax check
uwas config test                    # Config + connectivity test
uwas config diff                    # Diff between current and new config

# Benchmarking / Debug
uwas bench example.com              # Simple benchmark
uwas trace example.com/path         # Request lifecycle trace
uwas version                        # Version information
```

---

## 16. Security Defaults

### 16.1 Default Security Headers

```
Strict-Transport-Security: max-age=31536000; includeSubDomains; preload
X-Content-Type-Options: nosniff
X-Frame-Options: SAMEORIGIN
X-XSS-Protection: 0
Referrer-Policy: strict-origin-when-cross-origin
Permissions-Policy: camera=(), microphone=(), geolocation=()
```

### 16.2 Blocked Paths (Default)

```
.git/
.svn/
.env
.htpasswd
.htaccess          # do not serve, only parse
wp-config.php      # execute via PHP but block direct download
composer.json
composer.lock
package.json
node_modules/
vendor/            # block direct access to PHP vendor dir
```

### 16.3 PHP Source Protection

Direct access to `.php` files → execute via FastCGI, never serve as a download. Block access to `.php.bak`, `.php.old`, `.php~`, `.phps` files.

---

## 17. Performance Targets

| Metric | Target | Comparison |
|--------|--------|------------|
| Static file serving | 100K+ rps | Nginx ~95K rps |
| Cache hit response | < 1ms TTFB | Varnish ~0.5ms |
| PHP (FastCGI) | < 5ms overhead | Nginx ~3ms, Apache ~8ms |
| Reverse proxy | 50K+ rps | HAProxy ~42K rps |
| Memory (idle) | < 20MB | Nginx ~5MB, Caddy ~30MB |
| Memory (10K conn) | < 100MB | |
| TLS handshake | < 10ms | |
| Config reload | < 100ms | Zero downtime |
| Binary size | < 20MB | Caddy ~40MB |
| Startup time | < 500ms | |

---

## 18. Phase Plan

### Phase 1: Core Server (MVP)
- TCP listener + HTTP/1.1 + HTTP/2
- TLS + ACME (Let's Encrypt)
- Virtual host routing
- Static file serving (sendfile, etag, range, pre-compressed)
- FastCGI client (PHP-FPM)
- Basic rewrite engine (.htaccess import)
- try_files logic
- Gzip compression
- Access logging (JSON)
- Basic security headers
- CLI (serve, domain, config)
- YAML config

### Phase 2: Cache & Performance
- In-memory LRU cache (sharded)
- Disk cache (L2)
- Grace mode
- ESI (Edge Side Includes)
- Tag-based purge
- Brotli + Zstd compression
- Conditional requests (304)
- Rate limiting (token bucket)
- Connection pooling (FastCGI)

### Phase 3: Proxy & Load Balancer
- Reverse proxy handler
- Load balancing (6 algorithms)
- Health checking
- Circuit breaker
- WebSocket proxy
- Sticky sessions
- Upstream draining

### Phase 4: Advanced Features
- HTTP/3 (QUIC)
- On-Demand TLS
- WAF rules engine
- Admin REST API
- MCP Server
- Built-in dashboard
- Prometheus metrics
- OCSP stapling
- Hotlink protection
- .htaccess → YAML converter tool

### Phase 5: Ecosystem
- Docker official image
- apt/yum packages
- Helm chart
- Ansible/Terraform modules
- WordPress migration guide
- Laravel deployment guide
- Performance tuning guide

---

## 19. File Structure

```
uwas/
├── cmd/
│   └── uwas/
│       └── main.go              # Entry point
├── internal/
│   ├── config/
│   │   ├── config.go            # YAML parser + validation
│   │   ├── htaccess/
│   │   │   ├── parser.go        # .htaccess lexer/parser
│   │   │   ├── converter.go     # .htaccess → internal rules
│   │   │   └── watcher.go       # inotify/fswatch
│   │   └── reload.go            # Hot reload logic
│   ├── server/
│   │   ├── server.go            # Master process
│   │   ├── worker.go            # Worker process
│   │   ├── listener.go          # TCP listener + SO_REUSEPORT
│   │   └── graceful.go          # Graceful shutdown/reload
│   ├── tls/
│   │   ├── manager.go           # Certificate lifecycle
│   │   ├── acme/
│   │   │   ├── client.go        # ACME protocol client
│   │   │   ├── challenge.go     # HTTP-01, DNS-01 solvers
│   │   │   └── storage.go       # Cert disk storage
│   │   ├── ocsp.go              # OCSP stapling
│   │   └── ondemand.go          # On-Demand TLS
│   ├── http/
│   │   ├── mux.go               # HTTP/1.1 + HTTP/2 multiplexer
│   │   ├── vhost.go             # Virtual host router
│   │   ├── request.go           # Request context
│   │   └── response.go          # Response writer wrapper
│   ├── rewrite/
│   │   ├── engine.go            # Rewrite rule processor
│   │   ├── rule.go              # Rule types + flags
│   │   ├── condition.go         # RewriteCond evaluation
│   │   └── variables.go         # Server variable resolver
│   ├── cache/
│   │   ├── engine.go            # Cache orchestrator
│   │   ├── memory.go            # Sharded LRU memory cache
│   │   ├── disk.go              # Disk cache (L2)
│   │   ├── key.go               # Cache key generation
│   │   ├── grace.go             # Grace mode logic
│   │   ├── esi.go               # ESI parser + assembly
│   │   ├── purge.go             # Tag/path/wildcard purge
│   │   └── store.go             # Cache store interface
│   ├── handler/
│   │   ├── static.go            # Static file handler
│   │   ├── fastcgi/
│   │   │   ├── client.go        # FastCGI protocol client
│   │   │   ├── pool.go          # Connection pool
│   │   │   ├── env.go           # CGI env builder
│   │   │   └── response.go      # FastCGI response parser
│   │   ├── proxy/
│   │   │   ├── handler.go       # Reverse proxy handler
│   │   │   ├── upstream.go      # Upstream pool management
│   │   │   ├── balancer.go      # Load balancing algorithms
│   │   │   ├── health.go        # Health checker
│   │   │   ├── circuit.go       # Circuit breaker
│   │   │   └── websocket.go     # WebSocket proxy
│   │   └── redirect.go          # Redirect handler
│   ├── middleware/
│   │   ├── chain.go             # Middleware chain builder
│   │   ├── requestid.go         # Request ID (UUID v7)
│   │   ├── realip.go            # Real IP extraction
│   │   ├── ratelimit.go         # Token bucket rate limiter
│   │   ├── security.go          # Blocked paths + WAF
│   │   ├── access.go            # IP ACL + basic auth
│   │   ├── compress.go          # Brotli/Gzip/Zstd
│   │   ├── headers.go           # Security headers
│   │   ├── cors.go              # CORS handler
│   │   ├── log.go               # Access log writer
│   │   └── metrics.go           # Prometheus metrics
│   ├── admin/
│   │   ├── api.go               # REST API handler
│   │   ├── dashboard.go         # Built-in HTML dashboard
│   │   └── auth.go              # API key + mTLS auth
│   ├── mcp/
│   │   ├── server.go            # MCP protocol handler
│   │   ├── tools.go             # Tool implementations
│   │   └── resources.go         # Resource providers
│   └── cli/
│       ├── root.go              # CLI entry point
│       ├── serve.go             # serve command
│       ├── domain.go            # domain subcommands
│       ├── cert.go              # cert subcommands
│       ├── cache.go             # cache subcommands
│       └── htaccess.go          # htaccess subcommands
├── go.mod
├── go.sum                        # Empty (zero deps)
├── Makefile
├── Dockerfile
├── uwas.example.yaml
├── SPECIFICATION.md
├── IMPLEMENTATION.md
├── TASKS.md
├── BRANDING.md
├── LICENSE
└── README.md
```

---

## 20. Competitive Analysis

| Feature | Apache | Nginx | Varnish | LiteSpeed | Caddy | UWAS |
|---------|--------|-------|---------|-----------|-------|-------|
| Static serving | ⭐⭐ | ⭐⭐⭐ | ❌ | ⭐⭐⭐ | ⭐⭐ | ⭐⭐⭐ |
| PHP/FastCGI | ⭐⭐⭐ | ⭐⭐ | ❌ | ⭐⭐⭐ | ⭐ | ⭐⭐⭐ |
| .htaccess | ⭐⭐⭐ | ❌ | ❌ | ⭐⭐⭐ | ❌ | ⭐⭐ |
| Auto HTTPS | ❌ | ❌ | ❌ | ❌ | ⭐⭐⭐ | ⭐⭐⭐ |
| Built-in cache | ⭐ | ⭐ | ⭐⭐⭐ | ⭐⭐⭐ | ❌ | ⭐⭐⭐ |
| Reverse proxy | ⭐⭐ | ⭐⭐⭐ | ⭐ | ⭐⭐ | ⭐⭐ | ⭐⭐⭐ |
| Load balancing | ⭐ | ⭐⭐ | ⭐ | ⭐⭐ | ⭐⭐ | ⭐⭐⭐ |
| Config simplicity | ⭐ | ⭐⭐ | ⭐⭐ | ⭐ | ⭐⭐⭐ | ⭐⭐⭐ |
| Single binary | ❌ | ❌ | ❌ | ❌ | ✅ | ✅ |
| Zero deps | ❌ | ❌ | ❌ | ❌ | ❌ | ✅ |
| API management | ❌ | ❌ | ❌ | ❌ | ⭐⭐⭐ | ⭐⭐⭐ |
| MCP/LLM native | ❌ | ❌ | ❌ | ❌ | ❌ | ⭐⭐⭐ |
| Open source | ✅ | ✅ | ✅ | ❌ | ✅ | ✅ |
| WordPress ready | ⭐⭐⭐ | ⭐⭐ | ❌ | ⭐⭐⭐ | ❌ | ⭐⭐⭐ |
