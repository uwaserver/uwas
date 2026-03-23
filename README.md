# UWAS

**Unified Web Application Server**

One binary to serve them all.

Apache + Nginx + Varnish + Caddy → UWAS

---

<p align="center">
  <img src="assets/banner.jpeg" alt="UWAS Logo" width="100%">
</p>

[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Tests](https://img.shields.io/badge/tests-1718_passing-brightgreen)]()
[![Coverage](https://img.shields.io/badge/coverage-88.6%25-brightgreen)]()

## What is UWAS?

UWAS replaces your entire web server stack — Apache, Nginx, Varnish, Certbot — with a single Go binary. Auto HTTPS, built-in caching, PHP support, .htaccess compatibility, reverse proxy with load balancing, and an AI-ready MCP interface.

One binary. Zero hassle. Production ready.

## Features

- **Auto HTTPS** — Let's Encrypt certificates with zero configuration
- **HTTP/3 (QUIC)** — Via quic-go with Alt-Svc header advertisement
- **Built-in Cache** — Varnish-level caching with grace mode, tag-based purge
- **PHP Ready** — FastCGI with connection pooling and .htaccess support
- **Per-domain PHP** — Multiple PHP versions per domain with auto-port assignment
- **Load Balancer** — 5 algorithms, health checks, circuit breaker
- **WebSocket Proxy** — TCP hijack with bidirectional tunneling
- **URL Rewrite** — Apache mod_rewrite compatible engine
- **Brotli + Gzip** — Dual compression with Accept-Encoding negotiation
- **Image Optimization** — On-the-fly WebP/AVIF conversion
- **Dashboard** — 15-page React 19 admin dashboard with real-time stats
- **Analytics** — Per-domain traffic analytics, referrer tracking, user agent breakdown
- **Observable** — Prometheus metrics with p50/p95/p99 latency percentiles
- **Distributed Tracing** — W3C Trace Context (traceparent) propagation
- **Uptime Monitoring** — Per-domain health checks with alerting
- **A/B Testing** — Canary routing with cookie stickiness
- **Request Mirroring** — Shadow traffic to secondary backends
- **Backup/Restore** — Local, S3, SFTP storage providers with scheduling
- **Nginx/Apache Migration** — `uwas migrate nginx/apache <file>` CLI converter
- **Audit Logging** — Track all admin actions with timestamps and IPs
- **AI-Native** — MCP server for LLM-driven management
- **Secure** — WAF rules, rate limiting, security headers, blocked paths
- **Per-domain CORS** — Cross-Origin Resource Sharing per domain
- **Basic Authentication** — Per-domain HTTP Basic Auth
- **IP Access Control** — Per-domain IP whitelist/blacklist
- **Custom Error Pages** — Per-domain 404/500 error pages
- **Daemon Mode** — `uwas serve -d` for background operation
- **First-Run UX** — Auto-config creation, interactive setup, startup banner
- **Single Binary** — No dependencies, just download and run

## Quick Start

```bash
# Just run it — creates config automatically on first launch
uwas

# Or build from source
git clone https://github.com/uwaserver/uwas.git && cd uwas
make build
./bin/uwas
```

## Configuration

UWAS uses a single YAML file. See [`uwas.example.yaml`](uwas.example.yaml) for a full reference.

### Static Site

```yaml
global:
  http_listen: ":80"
  https_listen: ":443"
  acme:
    email: you@example.com

domains:
  - host: example.com
    root: /var/www/html
    type: static
    ssl:
      mode: auto
```

### WordPress / PHP

```yaml
domains:
  - host: blog.example.com
    root: /var/www/wordpress
    type: php
    ssl:
      mode: auto
    php:
      fpm_address: "unix:/var/run/php/php8.3-fpm.sock"
    htaccess:
      mode: import
    cache:
      enabled: true
      ttl: 1800
```

### Reverse Proxy

```yaml
domains:
  - host: api.example.com
    type: proxy
    ssl:
      mode: auto
    proxy:
      upstreams:
        - address: "http://127.0.0.1:3000"
          weight: 3
        - address: "http://127.0.0.1:3001"
          weight: 1
      algorithm: least_conn
      health_check:
        path: /health
        interval: 10s
```

## Requirements

| Component | Minimum | Recommended | Notes |
|-----------|---------|-------------|-------|
| Go | 1.26+ | 1.26+ | For building from source |
| PHP | 7.4+ | 8.3+ / 8.4+ | Only needed for PHP sites |
| PHP-FPM | Any | 8.3-fpm | Linux/macOS: `php-fpm`, Windows: `php-cgi -b` |

**PHP compatibility tested with:** PHP 8.1, 8.2, 8.3, 8.4

**Supported PHP connection modes:**
- Unix socket: `fpm_address: "unix:/var/run/php/php8.3-fpm.sock"` (Linux/macOS)
- TCP: `fpm_address: "tcp:127.0.0.1:9000"` (all platforms)

**No PHP needed** for static sites, reverse proxy, or redirect domains.

## CLI

```
uwas                         Start server (auto-setup if no config)
uwas serve    -c uwas.yaml   Start with specific config
uwas serve    -d             Start as background daemon
uwas version                 Print version info
uwas config   validate       Validate config file
uwas domain   list           List domains
uwas cache    stats          Cache statistics
uwas cache    purge          Purge cache
uwas status                  Server status via admin API
uwas reload                  Hot-reload configuration
uwas migrate  nginx <file>   Convert Nginx config to UWAS
uwas migrate  apache <file>  Convert Apache config to UWAS
uwas backup                  Create config backup
uwas restore                 Restore from backup
uwas php      list           List detected PHP versions
uwas php      start <ver>    Start PHP-FPM for version
uwas help                    Show help
```

## Architecture

```
Request Flow:

  TCP → TLS (SNI routing)
    → HTTP Parse
      → Middleware Chain:
          Recovery → Request ID → Security Headers → Access Log
        → Virtual Host Lookup
          → Per-domain: IP ACL → BasicAuth → CORS → Header Transform
            → Security Guard (blocked paths, WAF)
              → Rewrite Engine (mod_rewrite compatible)
              → Cache Lookup (L1 memory + L2 disk)
                → Handler:
                    ├── Static File  (ETag, Range, pre-compressed, SPA)
                    ├── FastCGI/PHP  (connection pool, CGI env)
                    ├── Reverse Proxy (5 LB algorithms, circuit breaker)
                    └── Redirect     (301/302/307/308)
              → Cache Store
    → Response
```

## Project Layout

```
cmd/uwas/                → CLI entry point
internal/
  admin/                 → REST API (health, stats, domains, metrics)
  alerting/              → Webhook alerts, error spike detection
  analytics/             → Per-domain traffic analytics
  backup/                → Backup/restore with Local, S3, SFTP
  build/                 → Version info (ldflags)
  cache/                 → L1 memory (256-shard LRU) + L2 disk cache
  cli/                   → CLI framework and commands
  config/                → YAML parser, validation, defaults
  handler/
    fastcgi/             → PHP handler, CGI environment builder
    proxy/               → Reverse proxy, load balancing, health checks
    static/              → Static files, MIME, ETag, pre-compressed
  logger/                → Structured logger (slog wrapper)
  mcp/                   → MCP server for AI management
  metrics/               → Prometheus-compatible metrics
  middleware/            → Chain, recovery, request ID, rate limit, gzip, CORS, WAF
  migrate/               → Nginx/Apache config converter
  monitor/               → Uptime monitoring per domain
  phpmanager/            → PHP version management
  rewrite/               → URL rewrite engine, conditions, variables
  router/                → Virtual host routing, request context
  server/                → HTTP/HTTPS server, dispatch, error pages
  tls/                   → TLS manager, ACME client, auto-renewal
    acme/                → RFC 8555 ACME protocol, JWS signing
pkg/
  fastcgi/               → FastCGI binary protocol, connection pool
  htaccess/              → .htaccess parser and converter
```

## Comparison

| Feature | UWAS | Nginx | Caddy | Apache | LiteSpeed |
|---------|------|-------|-------|--------|-----------|
| Single binary | Yes | No | Yes | No | No |
| Auto HTTPS | Yes | No | Yes | No | Yes |
| Built-in cache | Yes | No | No | No | Yes |
| PHP FastCGI | Yes | Yes | Yes | Yes | Yes |
| .htaccess support | Yes | No | No | Yes | Yes |
| Load balancer | Yes | Yes | No | No | Yes |
| WAF | Basic | No | No | Mod | Yes |
| MCP / AI-native | Yes | No | No | No | No |
| Open source | Apache 2.0 | BSD | Apache 2.0 | Apache 2.0 | Proprietary |

## Performance

Tested with [hey](https://github.com/rakyll/hey) on AMD Ryzen 9 9950X3D:

| Scenario | Requests/sec | Avg Latency |
|----------|-------------|-------------|
| Small static file (14B) | **7,000** | 7.1ms |
| 4KB static file | **7,100** | 7.0ms |
| 100K requests @ 200 concurrent | **7,254** | 27ms |
| 404 error page | **22,000** | 2.2ms |
| Cache L1 lookup (bench) | **75,000,000** | 31ns |
| VHost routing (bench) | **70,000,000** | 35ns |

## Deployment

### Systemd

```bash
sudo cp init/uwas.service /etc/systemd/system/
sudo systemctl enable uwas
sudo systemctl start uwas

# Live config reload (zero downtime)
sudo systemctl reload uwas
```

### Docker

```bash
docker build -t uwas .
docker run -p 80:80 -p 443:443 -v ./uwas.yaml:/etc/uwas/uwas.yaml uwas
```

## Migration from Nginx/Apache

```bash
# Convert existing Nginx config
uwas migrate nginx /etc/nginx/sites-enabled/example.conf > uwas.yaml

# Convert Apache config
uwas migrate apache /etc/apache2/sites-enabled/example.conf > uwas.yaml
```

## Admin API

When `admin.enabled: true`, the REST API is available at `127.0.0.1:9443`:

```
GET  /api/v1/health          → Health status (public, no auth)
GET  /api/v1/system          → System info (Go, OS, memory, goroutines)
GET  /api/v1/stats           → Stats + latency percentiles
GET  /api/v1/domains         → Domain list
GET  /api/v1/domains/{host}  → Domain detail
POST /api/v1/domains         → Create domain
PUT  /api/v1/domains/{host}  → Update domain
DELETE /api/v1/domains/{host} → Delete domain
GET  /api/v1/config          → Sanitized config
GET  /api/v1/config/raw      → Raw YAML config
PUT  /api/v1/config/raw      → Update config
POST /api/v1/reload          → Reload config
GET  /api/v1/metrics         → Prometheus metrics
GET  /api/v1/logs            → Access logs
GET  /api/v1/audit           → Audit log
GET  /api/v1/certs           → Certificate info
GET  /api/v1/monitor         → Uptime monitoring
GET  /api/v1/alerts          → Alert history
POST /api/v1/cache/purge     → Purge cache
GET  /api/v1/cache/stats     → Cache statistics
GET  /api/v1/analytics       → Traffic analytics
GET  /api/v1/php             → PHP versions
GET  /api/v1/backups         → Backup list
POST /api/v1/backups         → Create backup
POST /api/v1/backups/restore → Restore backup
GET  /api/v1/sse/stats       → Server-Sent Events stream
GET  /api/v1/mcp/tools       → MCP tool listing
POST /api/v1/mcp/call        → Invoke MCP tool
```

Protected with `Authorization: Bearer <api_key>` when `admin.api_key` is set.

## Dashboard

UWAS includes a built-in React 19 dashboard at `/_uwas/dashboard/`:

- **Overview** — Request stats, cache hit rate, latency percentiles, live chart
- **Domains** — CRUD with templates (WordPress, Static, Proxy, Redirect)
- **Topology** — React Flow network diagram
- **Cache** — Hit/miss/stale breakdown, per-domain rules, tag purge
- **Metrics** — Prometheus metrics viewer with auto-refresh
- **Analytics** — Per-domain traffic, referrers, user agent breakdown
- **Logs** — Real-time access log viewer with status filters
- **Config Editor** — YAML editor for main + per-domain configs
- **Certificates** — SSL certificate timeline and expiry tracking
- **PHP** — Per-domain PHP version management
- **Backups** — Create/restore/delete with Local/S3/SFTP providers
- **Audit Log** — Admin action history with filters
- **Settings** — System info, config reload, export

## MCP Server

UWAS includes a built-in MCP (Model Context Protocol) server for AI-driven management:

**Tools:**
- `domain_list` — List all configured domains
- `stats` — Get server statistics
- `config_show` — Show current configuration
- `cache_purge` — Purge cache by tag or all

## Development

```bash
make dev        # Build development binary
make test       # Run all tests
make lint       # Run go vet + staticcheck
make clean      # Clean build artifacts
```

## License

[Apache License 2.0](LICENSE)

## Contributing

1. Open an issue first to discuss
2. One feature/fix per PR
3. Tests required
4. `go vet` must pass
