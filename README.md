# UWAS

**Unified Web Application Server**

One binary to serve them all.

Apache + Nginx + Varnish + Caddy + cPanel → UWAS

---

<p align="center">
  <img src="assets/banner.jpeg" alt="UWAS Logo" width="100%">
</p>

> **Note:** This project is currently in development and not yet ready for production use.


[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)](LICENSE)
[![CI](https://github.com/uwaserver/uwas/actions/workflows/ci.yml/badge.svg)](https://github.com/uwaserver/uwas/actions/workflows/ci.yml)

## What is UWAS?

UWAS replaces your entire web server stack and hosting control panel with a single Go binary. Auto HTTPS, built-in caching, PHP support, .htaccess compatibility, reverse proxy, WebSocket forwarding, WAF, multi-user access control, and a 38-page React dashboard with 200+ API endpoints.

One binary. Zero hassle.

## Current Snapshot (v0.0.34)

- **Dashboard pages:** 38 (`web/dashboard/src/pages`)
- **Admin API routes:** 200+ (204 route registrations under `/api/v1` in `internal/admin/api.go`)
- **Go packages:** 50+ (53 from `go list ./...`)
- **Test status:** `go test -p 1 ./...` passing

## Features

### Web Server
- **Auto HTTPS** — Let's Encrypt certificates with zero configuration
- **HTTP/3 (QUIC)** — Via quic-go with Alt-Svc header advertisement
- **Built-in Cache** — Varnish-level caching with grace mode, tag-based purge
- **PHP Ready** — FastCGI with connection pooling and .htaccess support
- **Per-domain PHP** — Multiple PHP versions per domain with auto-port assignment, crash auto-restart
- **Load Balancer** — 5 algorithms, health checks, circuit breaker, canary routing
- **WebSocket Proxy** — Transparent TCP tunnel with hijack + bidirectional pipe
- **URL Rewrite** — Apache mod_rewrite compatible engine
- **Brotli + Gzip** — Dual compression with Accept-Encoding negotiation
- **Image Optimization** — On-the-fly WebP/AVIF conversion

### Hosting Control Panel
- **38-page Dashboard** — React 19 admin panel with dark/light theme
- **Multi-user Auth** — Admin, reseller, user roles with TOTP 2FA
- **WordPress Management** — One-click install, plugin updates, debug mode, error log viewer
- **DNS Zone Editor** — Full CRUD for Cloudflare, Hetzner, DigitalOcean, Route53
- **File Manager** — Browse, edit, upload, delete files via dashboard
- **SFTP Server** — Built-in pure Go SFTP with chroot per domain + SSH key management
- **Database Management** — MySQL/MariaDB + Docker containers (MariaDB/MySQL/PostgreSQL)
- **Site Migration** — SSH wizard: rsync files + dump/import database
- **Clone/Staging** — One-click domain clone with DB duplication
- **Backup/Restore** — Local, S3, SFTP providers with scheduling + webhook notifications
- **Cron Jobs** — Per-domain cron management with execution monitoring and failure alerts

### Security & Monitoring
- **WAF** — SQL injection, XSS, shell, RCE detection
- **Per-domain Rate Limiting** — Sharded token bucket per domain
- **Bandwidth Limits** — Monthly/daily caps with throttle or block action
- **Webhook Events** — 11 event types with HMAC-SHA256 signatures and retry
- **Uptime Monitoring** — Per-domain health checks with alerting
- **Analytics** — Per-domain traffic, referrer tracking, user agent breakdown
- **Prometheus Metrics** — p50/p95/p99 latency percentiles
- **Audit Logging** — Track all admin actions with timestamps and IPs
- **IP Access Control** — Per-domain whitelist/blacklist

### DevOps
- **AI-Native** — MCP server for LLM-driven management
- **Nginx/Apache Migration** — CLI config converter
- **Hot-Reload** — All per-domain chains rebuild on SIGHUP (zero downtime)
- **Self-Update** — Binary auto-update from GitHub releases
- **CI/CD** — GitHub Actions for build, test, release automation
- **Single Binary** — ~14MB, no dependencies, just download and run

## Install

```bash
# One-line install (Linux / macOS)
curl -fsSL https://raw.githubusercontent.com/uwaserver/uwas/main/install.sh | sh
```

### Update

```bash
# One-line update (detects current version, downloads latest, restarts service)
curl -fsSL https://raw.githubusercontent.com/uwaserver/uwas/main/update.sh | sh
```

Or download from [GitHub Releases](https://github.com/uwaserver/uwas/releases).

### Build from Source

```bash
git clone https://github.com/uwaserver/uwas.git && cd uwas
make build        # Production binary → bin/uwas
make linux        # Cross-compile for Linux amd64
```

## Quick Start

```bash
# Start server (auto-creates config on first run)
uwas

# Or with a specific config
uwas serve -c uwas.yaml

# Install as systemd service (auto-start on boot)
sudo uwas install
sudo systemctl start uwas

# Dashboard
# http://your-ip:9443/_uwas/dashboard/

# CLI commands (API key auto-loaded from ~/.uwas/.env)
uwas status
uwas php list
uwas domain list
uwas doctor
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

### Reverse Proxy with WebSocket

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
      websocket: true
      health_check:
        path: /health
        interval: 10s
```

### Bandwidth Limits + Rate Limiting

```yaml
domains:
  - host: example.com
    root: /var/www/html
    type: static
    bandwidth:
      enabled: true
      monthly_limit: 100GB
      daily_limit: 5GB
      action: throttle  # or "block"
    security:
      rate_limit:
        requests: 100
        window: 1m
```

## Requirements

| Component | Minimum | Recommended | Notes |
|-----------|---------|-------------|-------|
| Go | 1.26+ | 1.26+ | For building from source |
| PHP | 7.4+ | 8.3+ / 8.4+ | Only needed for PHP sites |
| Docker | 20.10+ | 24+ | Only for Docker database containers |

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
uwas doctor                  System diagnostics + auto-fix
uwas help                    Show help
```

## Architecture

```
Request Flow:

  TCP → TLS (SNI routing)
    → HTTP Parse
      → Middleware Chain:
          Recovery → Request ID → Security Headers → Rate Limit → Access Log
        → Virtual Host Lookup
          → Per-domain: IP ACL → Rate Limit → BasicAuth → CORS → Header Transform
            → Security Guard (blocked paths, WAF)
              → Bandwidth Check (throttle/block)
              → Rewrite Engine (mod_rewrite compatible)
              → Cache Lookup (L1 memory + L2 disk)
                → Handler:
                    ├── Static File    (ETag, Range, pre-compressed, SPA)
                    ├── FastCGI/PHP    (connection pool, CGI env)
                    ├── Reverse Proxy  (5 LB algorithms, circuit breaker)
                    ├── WebSocket      (TCP tunnel, bidirectional pipe)
                    └── Redirect       (301/302/307/308)
              → Cache Store
              → Bandwidth Record
    → Response
```

## Project Layout

```
cmd/uwas/                → CLI entry point (18 commands)
internal/
  admin/                 → REST API (179 routes) + dashboard embed + TOTP auth
  alerting/              → Alert thresholds + webhook/Slack/Telegram/email notifications
  analytics/             → Per-domain traffic analytics
  auth/                  → Multi-user RBAC (admin/reseller/user) + session + TOTP 2FA
  backup/                → Local/S3/SFTP backup + restore + scheduling
  bandwidth/             → Per-domain bandwidth limits (throttle/block)
  cache/                 → L1 memory (256-shard LRU) + L2 disk cache
  cli/                   → CLI framework and commands
  config/                → YAML parser, validation, defaults, ByteSize/Duration types
  cronjob/               → Cron job management + execution monitoring
  database/              → MySQL/MariaDB management + Docker container support
  dnsmanager/            → Cloudflare, Route53, Hetzner, DigitalOcean DNS CRUD
  dnschecker/            → DNS record verification (A/MX/NS/TXT)
  doctor/                → System diagnostics + auto-fix
  filemanager/           → Web file manager (browse/edit/upload/delete)
  firewall/              → UFW management via API
  handler/
    fastcgi/             → PHP handler, CGI environment builder
    proxy/               → Reverse proxy, load balancing, WebSocket, circuit breaker
    static/              → Static files, MIME, ETag, pre-compressed, SPA
  logger/                → Structured logger (slog wrapper)
  mcp/                   → MCP server for AI management
  metrics/               → Prometheus-compatible metrics
  middleware/            → Chain, recovery, rate limit, gzip, CORS, WAF, bot guard
  migrate/               → Nginx/Apache converter + SSH site migration + clone
  monitor/               → Uptime monitoring per domain
  notify/                → Webhook, Slack, Telegram, Email (SMTP) channels
  phpmanager/            → PHP detect, install, start/stop, per-domain assign
  rewrite/               → URL rewrite engine (Apache mod_rewrite compatible)
  router/                → Virtual host routing, request context
  selfupdate/            → Binary self-update from GitHub releases
  server/                → HTTP/HTTPS/HTTP3 server + request dispatch + log rotation
  sftpserver/            → Built-in SFTP server (pure Go, chroot per domain)
  siteuser/              → SFTP user management (chroot jail + SSH keys)
  tls/                   → TLS manager, ACME client, auto-renewal, cert expiry alerts
    acme/                → RFC 8555 ACME protocol, JWS signing
  webhook/               → Event-driven webhook delivery (11 events, HMAC, retry)
  wordpress/             → WordPress install, manage, debug, permissions
pkg/
  fastcgi/               → FastCGI binary protocol, connection pool
  htaccess/              → .htaccess parser and converter
web/dashboard/           → React 19 SPA (38 pages, Vite + TypeScript + Tailwind)
```

## Dashboard

UWAS includes a 38-page React 19 dashboard at `/_uwas/dashboard/` with dark/light theme:

**Sites:** Dashboard, Domains, Domain Detail, Topology, Certificates, DNS Zone Editor, WordPress, Clone/Staging, Migration, File Manager

**Server:** PHP, PHP Config, Applications, Database, SFTP Users, Cron Jobs, Services, Packages, IP Management, Email Guide

**Performance:** Cache, Metrics, Analytics, Logs

**Security:** Security, Firewall, Unknown Domains, Audit Log, Admin Users

**System:** Config Editor, Webhooks, Backups, Terminal, Updates, Doctor, Settings

**Auth:** Login (with 2FA/TOTP support)

## Comparison

| Feature | UWAS | Nginx | Caddy | Apache | cPanel |
|---------|------|-------|-------|--------|--------|
| Single binary | Yes | No | Yes | No | No |
| Auto HTTPS | Yes | No | Yes | No | Yes |
| Built-in cache | Yes | No | No | No | No |
| PHP FastCGI | Yes | Yes | Yes | Yes | Yes |
| .htaccess support | Yes | No | No | Yes | Yes |
| Load balancer | Yes | Yes | No | No | No |
| WebSocket proxy | Yes | Yes | No | No | No |
| WAF | Yes | No | No | Mod | Yes |
| Control panel | Yes (built-in) | No | No | No | Yes |
| Multi-user auth | Yes | No | No | No | Yes |
| Webhook events | Yes | No | No | No | No |
| DNS management | 4 providers | No | No | No | Yes |
| MCP / AI-native | Yes | No | No | No | No |
| Open source | AGPL-3.0 | BSD | Apache 2.0 | Apache 2.0 | Proprietary |

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

# Or use the dashboard Migration wizard for full site transfer (files + database)
```

## Development

```bash
make dev        # Build development binary
make test       # Run all tests
make lint       # Run go vet + staticcheck
make clean      # Clean build artifacts

# Dashboard
cd web/dashboard && npm run build
```

## License

UWAS is dual-licensed:

- **AGPL-3.0** for open-source and community use — [full text](LICENSE)
- **Commercial license** for enterprise and proprietary use — [uwaserver.com/enterprise](https://uwaserver.com/enterprise)

## Contributing

1. Open an issue first to discuss
2. One feature/fix per PR
3. Tests required
4. `go vet` must pass
