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

UWAS, mevcut web server ekosistemindeki fragmentasyonu ortadan kaldıran, tek binary'de çalışan, sıfır bağımlılık Go web application server'dır.

### Problem

Bugün production'da bir PHP sitesi (WordPress, Laravel) çalıştırmak için tipik stack:

```
Certbot → Nginx → Varnish → PHP-FPM → MySQL
           ↑        ↑         ↑
         config   config    config
         ayrı     ayrı      ayrı
```

5 ayrı servis, 5 ayrı config formatı, 5 ayrı log formatı, 5 ayrı restart/reload mekanizması. Monitoring, debugging ve maintenance karmaşıklığı katlanarak artıyor.

### Çözüm

```
UWAS (tek binary)
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
5. **Modular architecture** — her feature bağımsız enable/disable edilebilir
6. **LLM-native** — MCP server ile AI-driven server management

### 1.1 Dependency Policy

**Felsefe**: Tekerleği yeniden icat etme, ama dependency hell'e de düşme.

**Stdlib ile yazılacak** (Go stdlib yeterli):
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

**İzin verilen external dependencies** (proven, stable, well-maintained):
| Dependency | Neden | Alternatif neden kabul edilmiyor |
|-----------|-------|-------------------------------|
| `gopkg.in/yaml.v3` | YAML config parser | Sıfırdan YAML parser 3000+ satır, hata kaynağı |
| `github.com/andybalholm/brotli` | Brotli compression | Brotli spec 10K+ satır, C binding istemiyoruz |
| `github.com/klauspost/compress/zstd` | Zstandard compression | Aynı sebep |
| `github.com/quic-go/quic-go` | HTTP/3 (Phase 2+) | QUIC tek başına bir proje |
| `golang.org/x/crypto` | OCSP, ACME helper utils | Go extended stdlib, quasi-official |
| `golang.org/x/net` | HTTP/2 server push, ECH | Go extended stdlib |

**Kesinlikle yasak**:
- Web framework'leri (gin, echo, fiber vs.)
- ORM veya database driver
- Logging framework (zap, logrus vs.) — kendi structured logger
- Dependency injection framework
- Herhangi bir "kitchen sink" kütüphane

**Hedef**: `go.sum` dosyasında toplam **< 15 direct dependency**, indirect dahil **< 40 total**.

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

Go'nun goroutine modeli sayesinde her worker process binlerce concurrent connection'ı handle edebilir. SO_REUSEPORT ile kernel-level load distribution.

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
  ├── Cache Store (eğer cacheable ise)
  ├── Compression (brotli/gzip/zstd)
  ├── Security Headers (HSTS, CSP, X-Frame, CORS)
  ├── Access Log (structured JSON)
  └── Metrics Collection
  │
  ▼
Client Response
```

### 2.3 Module System

Her major feature bir Go interface'i implement eder ve bağımsız enable/disable edilebilir:

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

Ana config dosyası: `/etc/uwas/uwas.yaml`

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
    on_demand: false               # true = sertifika runtime'da alınır
    on_demand_ask: "http://localhost:9443/api/v1/domains/verify"

  cache:
    enabled: true
    memory_limit: 512MB
    disk_path: /var/cache/uwas
    disk_limit: 10GB
    default_ttl: 3600
    grace_ttl: 86400              # Varnish grace mode: 24 saat stale serve
    stale_while_revalidate: true
    purge_key: "${UWAS_PURGE_KEY}"

# Domain Tanımları
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
      # fpm_address: "tcp://127.0.0.1:9000"   # TCP alternatif
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
      # import: runtime'da parse et, memory'de cache'le
      # convert: başlangıçta YAML'a çevir, sonra ignore et
      # off: .htaccess dosyalarını tamamen ignore et
    
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
        threshold: 3             # kaç fail sonra unhealthy
        rise: 2                  # kaç success sonra healthy
      sticky:
        type: cookie             # cookie | header | ip
        cookie_name: "UWAS_UPSTREAM"
        ttl: 3600
      circuit_breaker:
        threshold: 5             # kaç hata sonra open
        timeout: 30s             # open → half-open bekleme
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

Desteklenen .htaccess direktifleri (Apache uyumlu):

| Directive | Destek | Notlar |
|-----------|--------|--------|
| `RewriteEngine On/Off` | ✅ Full | |
| `RewriteRule` | ✅ Full | PCRE regex, backreferences, flags [L,R,QSA,NC] |
| `RewriteCond` | ✅ Full | %{REQUEST_URI}, %{HTTP_HOST}, %{QUERY_STRING}, vs. |
| `Redirect` / `RedirectMatch` | ✅ Full | 301, 302, 307, 308 |
| `ErrorDocument` | ✅ Full | Local path veya URL |
| `DirectoryIndex` | ✅ Full | Birden fazla index dosyası |
| `Header set/unset/append` | ✅ Full | Response header manipulation |
| `ExpiresActive` / `ExpiresByType` | ✅ Full | Cache-Control header generation |
| `Options -Indexes` | ✅ Full | Directory listing kontrolü |
| `Options -FollowSymLinks` | ✅ Full | Symlink takip kontrolü |
| `AuthType Basic` | ✅ Full | .htpasswd ile basic auth |
| `Deny/Allow/Order` | ✅ Full | IP bazlı erişim kontrolü (legacy syntax) |
| `Require` | ✅ Full | Apache 2.4+ syntax |
| `SetEnvIf` | ⚠️ Partial | Temel kullanımlar |
| `FilesMatch` / `Files` | ✅ Full | Dosya pattern matching |
| `<IfModule>` | ✅ Full | Modül varlık kontrolü (her zaman true döner) |
| `php_value` / `php_flag` | ⚠️ Ignored | PHP-FPM pool config'den yönetilir |

**Desteklenmeyen** (ve desteklenmeyecek):
- `mod_php` directives (php_admin_value vs.) — FPM pool config ile yönetilir
- `SSLRequireSSL` — UWAS zaten default HTTPS
- `mod_proxy` directives — UWAS native proxy config ile yönetilir

### 3.3 .htaccess Import Stratejisi

```
.htaccess dosyası bulunduğunda:
  │
  ├── mode: import
  │     ├── Parse et
  │     ├── Desteklenen directive'leri in-memory rule'lara çevir
  │     ├── inotify/fswatch ile değişiklikleri izle
  │     ├── Değişiklikte otomatik re-parse
  │     └── Runtime'da aktif (performans: ilk parse sonrası O(1) lookup)
  │
  ├── mode: convert
  │     ├── Parse et
  │     ├── uwas.yaml snippet'ine çevir
  │     ├── stdout'a yaz veya config'e merge et
  │     ├── .htaccess'i .htaccess.bak olarak rename et
  │     └── Artık sadece YAML config'den çalış
  │
  └── mode: off
        └── .htaccess dosyalarını tamamen ignore et
```

### 3.4 Config Hot Reload

```
SIGHUP → Master Process
  │
  ├── Yeni config dosyasını parse et
  ├── Validation (syntax + semantic)
  ├── Hata varsa → log'a yaz, eski config'le devam et
  │
  ├── Başarılı →
  │   ├── Yeni listener'lar oluştur (yeni domain eklenmiş olabilir)
  │   ├── Worker'lara graceful reload sinyali gönder
  │   ├── Eski worker'lar mevcut connection'ları drain eder
  │   ├── Yeni worker'lar yeni config ile başlar
  │   └── Eski worker'lar drain tamamlanınca kapanır
  │
  └── Zero-downtime ✅
```

---

## 4. TLS & ACME Module

### 4.1 Certificate Lifecycle

```
Domain config eklendi
  │
  ├── ssl.mode: auto
  │   │
  │   ├── Disk cache'te valid cert var mı?
  │   │   ├── Evet → Yükle, renewal timer kur
  │   │   └── Hayır → ACME flow başlat
  │   │
  │   ├── ACME Flow:
  │   │   ├── Account key oluştur/yükle
  │   │   ├── Order oluştur
  │   │   ├── Challenge seç (HTTP-01 veya DNS-01)
  │   │   │   ├── HTTP-01: /.well-known/acme-challenge/ handler'ı kur
  │   │   │   └── DNS-01: TXT record oluştur (provider API ile)
  │   │   ├── Challenge'ı karşıla
  │   │   ├── CSR oluştur (ECDSA P-256 default)
  │   │   ├── Certificate al
  │   │   ├── Disk'e kaydet
  │   │   └── tls.Config.GetCertificate callback'ini güncelle
  │   │
  │   └── Renewal:
  │       ├── Cert expiry - 30 gün kala otomatik yenile
  │       ├── Başarısız → retry with exponential backoff
  │       ├── 3 gün kala hâlâ başarısız → alert (log + metrics)
  │       └── Yenileme sırasında mevcut cert kullanılmaya devam eder
  │
  ├── ssl.mode: manual
  │   └── Verilen cert/key dosyalarını yükle, fswatch ile izle
  │
  └── ssl.mode: off
      └── Plain HTTP (redirect yoksa)
```

### 4.2 On-Demand TLS

```
Bilinmeyen domain için TLS handshake geldi
  │
  ├── on_demand: false → TLS alert, connection reject
  │
  └── on_demand: true
      ├── on_demand_ask URL'ine HTTP GET → 200 OK?
      │   ├── Hayır → reject
      │   └── Evet → ACME flow başlat
      │       ├── Handshake bekletilir (client timeout'a dikkat)
      │       ├── Cert alınır, cache'lenir
      │       └── Handshake tamamlanır
      └── Rate limiting: max 10 cert/dakika, max 50 pending
```

### 4.3 TLS Configuration

```go
// SNI-based certificate selection
tls.Config{
    GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
        // 1. Exact match: hello.ServerName → cert cache
        // 2. Wildcard match: *.example.com
        // 3. On-demand TLS (eğer enabled)
        // 4. Default/fallback cert
    },
    MinVersion: tls.VersionTLS12,
    CipherSuites: modernCipherSuites,
    NextProtos: []string{"h2", "http/1.1"},    // ALPN
    // HTTP/3: ayrı QUIC listener, aynı cert
}
```

### 4.4 OCSP Stapling

- Her cert için periyodik OCSP response fetch
- Stapled response tls.Config'e attach
- OCSP responder hata verirse → cached response kullan (grace period)
- Must-Staple cert'ler için daha agresif retry

---

## 5. HTTP Engine

### 5.1 Protocol Support

| Protocol | Destek | Notlar |
|----------|--------|--------|
| HTTP/1.0 | ✅ | Legacy uyumluluk |
| HTTP/1.1 | ✅ | Keep-alive, pipelining, chunked transfer |
| HTTP/2 | ✅ | Full h2 over TLS, h2c (cleartext) opsiyonel |
| HTTP/3 | 🗓️ Phase 2 | QUIC over UDP (Go'nun quic paketi gelişiyor) |

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
// 4. Default host (ilk tanımlanan domain)
```

### 5.3 try_files Logic

Nginx'in en çok kullanılan pattern'i, first-class citizen:

```yaml
# Config
try_files:
  - "$uri"                    # /about → /var/www/about dosyası var mı?
  - "$uri/"                   # /about/ → /var/www/about/ dizini var mı?
  - "$uri/index.html"         # /about/ → /var/www/about/index.html var mı?
  - "$uri/index.php"          # /about/ → /var/www/about/index.php var mı?
  - "/index.php"              # Fallback → WordPress entry point
```

```go
func tryFiles(ctx *RequestContext, candidates []string) (string, bool) {
    for _, candidate := range candidates {
        resolved := expandVariables(candidate, ctx)
        fullPath := filepath.Join(ctx.DocumentRoot, resolved)
        
        // Security: path traversal koruması
        if !isInsideRoot(fullPath, ctx.DocumentRoot) {
            continue
        }
        
        stat, err := os.Stat(fullPath)
        if err != nil {
            continue
        }
        
        if stat.IsDir() {
            // Dizinse → index dosyası ara
            continue
        }
        
        // Dosya bulundu
        return resolved, true
    }
    return "", false
}
```

---

## 6. Rewrite Engine

### 6.1 Rule Processing

Apache mod_rewrite uyumlu regex-based rewrite engine:

```go
type RewriteRule struct {
    Pattern    *regexp.Regexp    // PCRE regex
    Target     string            // Replacement string ($1, $2 backrefs)
    Conditions []RewriteCondition
    Flags      RewriteFlags
}

type RewriteCondition struct {
    TestString string            // %{REQUEST_URI}, %{HTTP_HOST}, vs.
    Pattern    *regexp.Regexp
    Negate     bool              // [!F] — CondPattern'i negate et
    Type       string            // "regex" | "is_file" | "is_dir" | "is_symlink"
}

type RewriteFlags struct {
    Last       bool    // [L] — rule match ederse dur
    Redirect   int     // [R=301] — HTTP redirect
    QSAppend   bool    // [QSA] — query string'i koru
    NoCase     bool    // [NC] — case-insensitive
    Chain      bool    // [C] — sonraki rule ile chain
    Skip       int     // [S=N] — N rule atla
    PassThrough bool   // [PT] — rewrite sonrası handler'a devam
    Forbidden  bool    // [F] — 403 döndür
    Gone       bool    // [G] — 410 döndür
}
```

### 6.2 Server Variables

```go
// Rewrite condition'larda kullanılabilir değişkenler
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
                    ▼            ▼     (eğer cacheable)
               Serve/Reval  Serve/Reval
```

### 7.2 Cache Key

```go
type CacheKey struct {
    Host         string
    Path         string
    QueryString  string    // configurable: include/exclude/specific params
    Method       string    // GET only (default), HEAD
    Vary         []string  // Vary header'daki field'lar
    // Custom key fragments (cookie, header, etc.)
}

func (k CacheKey) Hash() string {
    h := xxhash.New()  // stdlib'de yok — basit FNV-1a kullan
    h.Write([]byte(k.Host))
    h.Write([]byte(k.Path))
    // ...
    return hex.EncodeToString(h.Sum(nil))
}
```

### 7.3 Memory Cache (L1)

```go
type MemoryCache struct {
    shards    [256]*CacheShard    // Sharded mutex map (lock contention azaltır)
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
    Body        []byte          // < 1MB → memory, > 1MB → disk'e referans
    Created     time.Time
    TTL         time.Duration
    GraceTTL    time.Duration   // Varnish grace mode
    Tags        []string        // Tag-based invalidation
    ETag        string
    LastMod     time.Time
    HitCount    atomic.Int64
    LastAccess  atomic.Int64    // Unix timestamp, LRU eviction için
}
```

### 7.4 Disk Cache (L2)

```
/var/cache/uwas/
├── ab/                         # İlk 2 karakter hash
│   └── cd/                     # Sonraki 2 karakter
│       └── abcdef1234.cache    # Full hash
└── _meta/
    └── tags/
        ├── site:example.idx    # Tag → key mapping
        └── page:home.idx
```

### 7.5 Grace Mode (Varnish'ten)

```
Request geldi, cache entry expired
  │
  ├── grace_ttl içinde mi?
  │   ├── Evet → Stale content serve et
  │   │          Arka planda async revalidation başlat
  │   │          (bir goroutine backend'e istek atar)
  │   │          Client beklemez → düşük latency
  │   │
  │   └── Hayır → Backend'e normal istek
  │
  └── Backend down mı?
      ├── Evet ve grace'te → stale serve et
      ├── Evet ve grace dışı → 503 (veya custom error page)
      └── Hayır → normal flow
```

### 7.6 Edge Side Includes (ESI)

HTML içinde cacheable parçaları ayrı cache'leme:

```html
<!-- Ana sayfa: TTL=60s -->
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

Her `<esi:include>` ayrı cache key'e sahip, ayrı TTL ile cache'lenir. Ana sayfa expired olsa bile nav ve footer cache'ten serve edilebilir.

### 7.7 Cache Invalidation

```yaml
# Tag-based purge (en güçlü yöntem)
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

Go stdlib'de FastCGI server (accept) var ama client (connect) yok. Client implementasyonu:

```go
type FastCGIClient struct {
    pool       *ConnectionPool
    address    string          // Unix socket veya TCP
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
// PHP-FPM'e gönderilecek environment variables
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
        // Tüm HTTP_* headers forwarded
    }
}
```

### 8.3 SCRIPT_NAME / PATH_INFO Split

PHP framework'leri için kritik:

```
Request: /blog/index.php/posts/123?page=2

SCRIPT_FILENAME = /var/www/blog/index.php
SCRIPT_NAME     = /blog/index.php
PATH_INFO       = /posts/123
QUERY_STRING    = page=2
```

### 8.4 Upload Handling

```go
// Büyük upload'larda memory kullanmamak için:
// 1. Request body'yi temp dosyaya stream et
// 2. Temp dosyayı PHP-FPM'e stdin olarak gönder
// 3. İşlem bitince temp dosyayı sil

func handleUpload(ctx *RequestContext) error {
    if ctx.Request.ContentLength > ctx.MaxUploadSize {
        return ErrRequestEntityTooLarge
    }
    
    // Stream to temp file (memory'de tutma)
    tmpFile, _ := os.CreateTemp(ctx.TempDir, "uwas-upload-*")
    defer os.Remove(tmpFile.Name())
    
    written, _ := io.Copy(tmpFile, io.LimitReader(ctx.Request.Body, ctx.MaxUploadSize))
    tmpFile.Seek(0, 0)
    
    // FastCGI stdin olarak gönder
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

| Algorithm | Açıklama | Use Case |
|-----------|----------|----------|
| `round_robin` | Sırayla dağıt, weight'e göre | Genel amaç |
| `least_conn` | En az aktif bağlantıya gönder | Uzun süreli request'ler |
| `ip_hash` | Client IP hash ile sabit backend | Session affinity |
| `uri_hash` | URI hash ile sabit backend | Cache-friendly |
| `random` | Rastgele seç, power of 2 choices | Basit, düşük overhead |
| `weighted_round_robin` | Smooth weighted round robin | Farklı kapasiteli backend'ler |

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

### 9.4 Circuit Breaker (HAProxy'den)

```
State: CLOSED (normal)
  │
  ├── Hata sayısı threshold'u aştı
  ▼
State: OPEN (tüm istekler reject)
  │
  ├── timeout süresi geçti
  ▼
State: HALF-OPEN (bir istek geç, test et)
  │
  ├── Başarılı → CLOSED
  └── Başarısız → OPEN (timer reset)
```

### 9.5 WebSocket Proxy

```go
func proxyWebSocket(ctx *RequestContext, backend *Backend) error {
    // 1. Backend'e TCP connection aç
    // 2. HTTP Upgrade handshake'i forward et
    // 3. Bidirectional byte copy (goroutine pair)
    // 4. Bir taraf kapanınca diğerini de kapat
    
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

| Middleware | Sıra | Açıklama |
|-----------|------|----------|
| `RequestID` | 1 | Unique request ID ata (UUID v7) |
| `RealIP` | 2 | X-Forwarded-For / X-Real-IP parse |
| `RateLimit` | 3 | Token bucket per-IP |
| `SecurityGuard` | 4 | Blocked paths, WAF rules |
| `AccessControl` | 5 | IP whitelist/blacklist, basic auth |
| `RewriteEngine` | 6 | URL transform |
| `CacheLookup` | 7 | Cache hit kontrolü |
| `Compression` | 8 | Brotli/Gzip/Zstd response compress |
| `SecurityHeaders` | 9 | HSTS, CSP, X-Frame, CORS |
| `AccessLog` | 10 | Structured JSON log |
| `Metrics` | 11 | Prometheus metrics collection |

### 10.2 WAF Rules (Temel)

Phase 1'de temel koruma kuralları:

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
    
    // Range request desteği (video streaming)
    // Go'nun http.ServeContent bunu handle ediyor
    w.Header().Set("ETag", etag)
    w.Header().Set("Accept-Ranges", "bytes")
    
    // Pre-compressed dosya varsa onu serve et
    if acceptsBrotli(r) {
        if brFile, err := os.Open(path + ".br"); err == nil {
            w.Header().Set("Content-Encoding", "br")
            io.Copy(w, brFile)
            return
        }
    }
    
    http.ServeContent(w, r, stat.Name(), stat.ModTime(), f)
    // Go'nun ServeContent'i dahili olarak sendfile syscall kullanır
}
```

### 11.3 MIME Type Registry

Extensible MIME type mapping, modern formatlar dahil:

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
    // ... 100+ daha
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

`/_uwas/dashboard` endpoint'inde minimal HTML dashboard:

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
GET    /domains                    # Tüm domain'leri listele
POST   /domains                    # Yeni domain ekle
GET    /domains/{host}             # Domain detay
PUT    /domains/{host}             # Domain güncelle
DELETE /domains/{host}             # Domain sil

# Cache Management
POST   /cache/purge                # Tag/path/domain bazlı purge
GET    /cache/stats                # Cache istatistikleri
DELETE /cache                      # Tüm cache'i temizle

# Certificate Management
GET    /certs                      # Tüm sertifikaları listele
POST   /certs/{host}/renew         # Manuel yenileme tetikle
GET    /certs/{host}               # Cert detay (expiry, issuer vs.)

# Upstream Management
GET    /upstreams                  # Upstream pool'ları listele
PUT    /upstreams/{name}/backends  # Backend ekle/çıkar
POST   /upstreams/{name}/drain/{backend}  # Backend drain

# Server Management
POST   /reload                     # Config reload tetikle
GET    /health                     # Server health check
GET    /stats                      # Genel istatistikler
GET    /metrics                    # Prometheus format

# Config
GET    /config                     # Mevcut config (sanitized)
PATCH  /config                     # Partial config update
```

### 13.2 Authentication

```yaml
admin:
  listen: "127.0.0.1:9443"     # Sadece localhost
  api_key: "${UWAS_ADMIN_KEY}" # Bearer token auth
  # veya
  mutual_tls:
    ca: /path/to/ca.pem        # Client cert auth
```

---

## 14. MCP Server

### 14.1 Tools

```
uwas_domain_list          # Domain'leri listele
uwas_domain_add           # Yeni domain ekle
uwas_domain_remove        # Domain sil
uwas_domain_update        # Domain config güncelle

uwas_cache_purge          # Cache temizle (tag/path/domain)
uwas_cache_stats          # Cache istatistikleri

uwas_cert_list            # Sertifikaları listele
uwas_cert_renew           # Manuel yenileme

uwas_upstream_list        # Upstream'leri listele
uwas_upstream_health      # Backend sağlık durumu

uwas_stats                # Genel sunucu istatistikleri
uwas_logs_search          # Access log'larda arama

uwas_config_show          # Mevcut config göster
uwas_config_reload        # Config reload

uwas_waf_toggle           # WAF kurallarını aç/kapat
uwas_rate_limit_adjust    # Rate limit ayarla
```

### 14.2 Resources

```
uwas://config              # Mevcut config
uwas://stats               # Real-time stats
uwas://domains/{host}      # Domain detayları
uwas://certs/{host}        # Cert bilgileri
uwas://logs/recent         # Son 100 log entry
```

---

## 15. CLI Interface

```bash
# Server Management
uwas serve                          # Foreground'da başlat
uwas serve -c /path/to/uwas.yaml   # Custom config
uwas serve -d                       # Daemon mode
uwas reload                         # SIGHUP gönder
uwas stop                           # Graceful shutdown
uwas status                         # Durum bilgisi

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
uwas config diff                    # Mevcut vs yeni config farkı

# Benchmarking / Debug
uwas bench example.com              # Basit benchmark
uwas trace example.com/path         # Request lifecycle trace
uwas version                        # Versiyon bilgisi
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
.htaccess          # serve etme, sadece parse et
wp-config.php      # PHP execute et ama direct download engelle
composer.json
composer.lock
package.json
node_modules/
vendor/            # PHP vendor dir'e direct access engelle
```

### 16.3 PHP Source Protection

`.php` dosyalarına direct erişim → FastCGI üzerinden execute et, asla download olarak serve etme. `.php.bak`, `.php.old`, `.php~`, `.phps` dosyalarına erişimi engelle.

---

## 17. Performance Targets

| Metric | Hedef | Karşılaştırma |
|--------|-------|---------------|
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
- Load balancing (6 algoritma)
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
├── go.sum                        # Boş (zero deps)
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
