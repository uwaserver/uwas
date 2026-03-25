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

## Planned

### High Priority

- [ ] **Bandwidth limits per domain** — configurable monthly/daily bandwidth cap, throttle or block when exceeded, dashboard alerts
- [ ] **Multi-user access control** — admin, reseller, user roles with scoped permissions. Resellers manage their own domains. JWT or session-based auth alongside API key
- [ ] **Webhook events system** — fire webhooks on domain.add, domain.delete, cert.renewed, backup.completed, php.crashed, security.blocked events. Configurable per-event URLs
- [ ] **Cron monitoring** — track execution status, duration, exit code for each cron job. Alert on failure. Dashboard timeline view
- [ ] **WordPress white page root cause** — deploy display_errors=On build, capture PHP error, implement permanent fix. Likely open_basedir or missing PHP extension

### Medium Priority

- [ ] **Docker database support** — run MariaDB/PostgreSQL/MySQL in Docker containers managed by UWAS. Choose DB engine per WordPress install
- [ ] **DNS zone editor in dashboard** — full record management UI for all 4 providers. Bulk import/export, template presets (WordPress, email, etc.)
- [ ] **Migration dashboard page** — visual wizard: enter SSH credentials → scan remote → select sites → migrate with progress bar
- [ ] **Clone/staging dashboard page** — one-click clone with domain name input, auto-creates domain + DNS + SSL
- [ ] **SFTP user management via dashboard** — create/delete SFTP users per domain from built-in SFTP server, show connection details
- [ ] **Log rotation** — size-based + age-based rotation for per-domain access logs, configurable retention
- [ ] **Rate limit dashboard UI** — configure requests/window per domain from domain edit form

### Low Priority

- [ ] **Plugin/extension system** — allow 3rd party Go plugins for custom middleware, handlers, dashboard pages
- [ ] **API documentation** — auto-generated OpenAPI/Swagger spec from route registrations
- [ ] **Internationalization (i18n)** — Turkish + English dashboard with language switcher
- [ ] **Dark/light theme toggle** — currently dark only
- [ ] **Mobile responsive** — sidebar collapse, touch-friendly UI on small screens
- [ ] **HTTP/3 (QUIC) improvements** — connection migration, 0-RTT
- [ ] **WebSocket proxy** — transparent WebSocket forwarding for real-time apps
- [ ] **Prometheus exporter** — /metrics endpoint already exists, add Grafana dashboard templates
- [ ] **Email server integration** — deeper Postfix/Dovecot management (mailboxes, aliases, quotas)
- [ ] **Node.js/Python app hosting** — reverse proxy + process manager for non-PHP apps

### Architecture Improvements

- [ ] **Config hot-reload completeness** — ensure ALL per-domain chains rebuild on SIGHUP (currently covers IP ACL, image opt, proxy pools)
- [ ] **Graceful PHP process reload** — restart PHP without dropping active connections
- [ ] **Connection draining** — drain active connections before shutdown instead of hard kill
- [ ] **Structured logging** — JSON log format option for log aggregation (ELK, Loki)
- [ ] **Test coverage** — increase from 40 packages to full coverage including e2e WordPress flow
- [ ] **CI/CD pipeline** — GitHub Actions for build, test, release automation with binary artifacts
