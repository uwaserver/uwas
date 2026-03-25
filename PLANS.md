# UWAS — Future Plans

## Completed (v1.6.x)

- [x] Built-in SFTP server (pure Go, SFTP v3, chroot per domain)
- [x] DNS providers: Cloudflare, Route53, Hetzner, DigitalOcean
- [x] Site migration wizard (SSH + rsync + DB dump/import)
- [x] Domain clone/staging (duplicate site + DB for testing)
- [x] WordPress management (detect, plugins, updates, permissions, reinstall)
- [x] Package installer (install/remove system packages from dashboard)
- [x] Config validation (pre-save checks for domain add)
- [x] On-the-fly image optimization (WebP/AVIF via cwebp/avifenc)
- [x] Per-domain bandwidth tracking (requests, bytes, status codes)
- [x] SSE real-time log streaming with domain filter
- [x] Full backup (config + certs + web files + DB dump)
- [x] Per-domain backup with auto DB detection
- [x] Domain isolation (open_basedir, symlink protection, per-domain /tmp)
- [x] PHP auto-restart on crash, opcache defaults, version grouping
- [x] 18 security fixes (path traversal, WAF body, timing attack, etc.)
- [x] Install/uninstall scripts, self-update with auto-restart

## Completed (v1.7.x)

- [x] **Bandwidth limits per domain** — configurable monthly/daily bandwidth cap, throttle or block when exceeded, alerts wired to alerter at 90%/100% thresholds
- [x] **Cron monitoring** — track execution status, duration, exit code for each cron job. Alert on failure wired to alerter + webhook. Persistent history (JSON)
- [x] **Webhook events system** — fire webhooks on domain.add/delete/update, cert.renewed, backup.completed/failed, php.crashed, cron.failed, login.success/failed. HMAC-SHA256 signatures, retry with exponential backoff, queue-based delivery
- [x] **Multi-user access control** — admin, reseller, user roles with scoped permissions. Session-based auth + API key + TOTP 2FA. Bcrypt passwords, domain scoping, 15 API endpoints
- [x] **Log rotation** — size-based + age-based rotation, configurable retention, timestamped filenames, gzip compression, background cleanup
- [x] **Per-domain rate limiting** — sharded token bucket rate limiter per domain, configurable requests/window, rebuilt on config reload
- [x] **DNS zone editor** — full CRUD for all 4 providers (Cloudflare, Hetzner, DigitalOcean, Route53). Inline edit, 8 record types, sync A record to server IP
- [x] **Migration dashboard page** — 3-step wizard: SSH connection, target domain, remote database. Operation log + result display
- [x] **Clone/staging dashboard page** — source/target domain picker, auto-suggest staging name, operation log, result display
- [x] **SFTP user management dashboard** — create/delete SFTP users, SSH key management, connection details with copy-to-clipboard
- [x] **Dark/light theme toggle** — CSS custom properties, ThemeProvider context, localStorage persistence. All 31 pages use semantic tokens

## Planned

### High Priority

- [x] **WordPress white page debug** — WP_DEBUG toggle API + dashboard button, WP_DEBUG_LOG + WP_DEBUG_DISPLAY auto-config, error log viewer in dashboard (wp-content/debug.log), per-site enable/disable
- [x] **WebSocket proxy** — transparent WebSocket forwarding via TCP tunnel (hijack + bidirectional pipe), per-domain `proxy.websocket: true` config, X-Forwarded-For headers
- [x] **Config hot-reload completeness** — all per-domain chains rebuild on SIGHUP: vhosts, TLS, htaccess, rewrites, IP ACL, rate limiters, image opt, proxy pools, bandwidth, webhooks, health monitor

### Medium Priority

- [x] **Docker database containers** — create/start/stop/remove MariaDB/MySQL/PostgreSQL containers via Docker CLI. 5 API endpoints, auto-prefixed container names, persistent volume support, 127.0.0.1 port binding
- [x] **Mobile responsive** — collapsible sidebar, responsive grids, touch-friendly min-height, adaptive heading sizes, table scroll on mobile
- [x] **CI/CD pipeline** — GitHub Actions: build + vet + test (Go), TypeScript check + build (dashboard). Release workflow already existed for multi-arch binaries

### Nice to Have

- [ ] **Internationalization (i18n)** — Turkish + English dashboard with language switcher
- [ ] **Node.js/Python app hosting** — reverse proxy + process manager for non-PHP apps
- [ ] **Prometheus/Grafana templates** — /metrics endpoint exists, add dashboard templates
