# UWAS — Security Architecture Map (sc-recon)

**Target:** UWAS (Unified Web Application Server) — single-binary Go web server + hosting control panel
(replaces Apache/Nginx/Varnish/Caddy/cPanel).
**Root:** `/home/ersinkoc/Codebox/uwas`
**Scope:** Internet-facing server. Findings focus on HTTP-reachable surfaces, FastCGI/PHP, file manager,
SFTP, terminal/PTY, proxy, WAF, auth/TOTP/RBAC, config persistence. `*_test.go`, `examples/`, vendored deps excluded.
**Generated:** Phase 1 reconnaissance for the security-check pipeline.

---

## 1. Technology Stack Detection

**Languages (by LOC):**
- **Go ~59,000 LOC (non-test)** — primary. 438 `.go` source files across `cmd/`, 48 `internal/` packages, `pkg/`.
  Test code is large (~146k LOC across `*_test.go`) but excluded from findings.
- **TypeScript/React ~24,900 LOC** (`web/dashboard/src`, `.ts`/`.tsx`) — admin SPA, secondary.
- Bundled/vendored JS (4,965 `.js`) is mostly dashboard build output / node deps — excluded.

**Go module:** `github.com/uwaserver/uwas`, **Go 1.26.4**. Deliberately stdlib-first.
Direct deps (`go.mod`): `gopkg.in/yaml.v3`, `github.com/andybalholm/brotli`, `github.com/quic-go/quic-go`,
`golang.org/x/crypto` (bcrypt, ssh), `golang.org/x/sync`. No web framework, no ORM, no logging framework.

**HTTP layer:** Go stdlib `net/http` with the 1.22+ method-aware `http.ServeMux` pattern syntax
(`"GET /api/v1/..."`, `{host}` wildcards). HTTP/3 via quic-go.

**Frontend:** React 19, react-router-dom 7, Vite 8, Tailwind 4, recharts, @xyflow/react. Embedded into the
binary via `go:embed` (`internal/admin/dashboard`), served at `/_uwas/dashboard/`.

**Databases (managed, not app-internal):** MySQL/MariaDB management via the `mysql`/`mysqldump` CLIs and
Docker containers (`internal/database/`). UWAS itself stores state as YAML/JSON files on disk — no app DB,
no ORM. SQL is only constructed for the DB-management / SQL-explorer features.

**Build/infra:** `Dockerfile`, `docker-compose.yml`, `docker/`, `.github/workflows/`, `.goreleaser.yml`,
`install.sh`, `update.sh`, systemd `init/`. No Terraform / K8s / Helm.

---

## 2. Application Type Classification

**Primary type:** Monolithic **REST API + management control plane** (250+ JSON endpoints under `/api/v1/`)
bundled with an embedded **React SPA** dashboard, fronting a **reverse-proxy / static / FastCGI web server**.

It is simultaneously:
- A **public-facing HTTP/HTTPS/HTTP3 web server** (`internal/server/`) doing vhost routing, static files,
  PHP/FastCGI, reverse proxy, caching, WAF — the data-plane that serves untrusted internet traffic.
- A **privileged control panel API** (`internal/admin/`) — the management-plane that runs shell commands,
  manages OS packages, firewall, systemd services, databases, files, and a browser terminal. Effectively
  runs as root and is the crown-jewel attack surface.
- A **CLI tool** (`cmd/uwas`, `internal/cli/`) with 19 commands.
- A **built-in SFTP server** (`internal/sftpserver/`) and **WebSocket PTY terminal** (`internal/terminal/`).
- An **MCP server** for AI-driven management (`internal/mcp/`).

Two distinct trust planes share one process: the data-plane (hostile traffic) and the management-plane
(operator-only, root-equivalent). Any data-plane → management-plane bleed is critical.

---

## 3. Entry Points Mapping

### 3a. Admin API HTTP routes — `internal/admin/routes.go` (427 LOC, 14 sub-registrars)
Registered onto `s.mux` in `registerRoutes()`; all wrapped by the global chain in `api.go:Start()`:
`RequestID → authMiddleware → requireJSONMiddleware → mux`.

High-value route groups (method/path → handler file):
- **Auth** (`registerAuthRoutes`, `handlers_auth.go`): `POST /api/v1/auth/login`, `/auth/bootstrap`,
  `/auth/logout`, `GET /auth/me`, `POST /auth/ticket`, `/auth/2fa/{setup,verify,disable,recovery-codes,recover}`,
  user CRUD `/auth/users[/{username}]`, `/auth/users/{username}/{apikey,password}`.
- **Domains** (`handlers_domain.go`): `GET/POST /domains`, `PUT/DELETE/GET /domains/{host}`,
  `/domains/{host}/debug`, `POST /domains/import`, raw YAML get/put `/config/domains/{host}/raw`,
  unknown-domain block/alias/redirect.
- **Config** (`api.go`): `GET/PUT /config/raw`, `GET /config/export`, `POST /reload`.
- **Files** (`handlers_files.go`): `GET .../list|read|disk-usage`, `PUT .../write`, `DELETE .../delete`,
  `POST .../mkdir|upload` under `/files/{domain}/...` — direct filesystem read/write/delete/upload sink.
- **Terminal** (`handlers_terminal.go`): `GET /api/v1/terminal` → WebSocket→PTY (gated by `requireAdmin` + `requirePin`).
- **Database** (`handlers_database.go`): create/drop DBs+users, export/import (`mysqldump`/`mysql`),
  remote-access toggle, Docker DB containers, and **SQL explorer** `POST /database/explore/{db}/query`.
- **PHP** (`handlers_php.go`): install/enable/start/stop, raw php.ini get/put, per-domain assign.
- **Apps/deploy** (`handlers_apps*.go`): app process CRUD/lifecycle, `POST /apps/{name}/deploy`,
  deploy keys, and **`POST /apps/{name}/webhook`** (public, HMAC-authed git deploy hook).
- **System admin** (`handlers_system.go`, `handlers_firewall.go`): services start/stop/restart, UFW
  firewall allow/deny, doctor/fix, self-update, apt package install, SSH key management, setup wizard.
- **Cron** (`handlers_cron.go`): `POST /cron/execute` — runs arbitrary shell command (admin-only).
- **DNS/Cloudflare** (`handlers_dns.go`, `handlers_cloudflare*.go`): DNS record CRUD across providers,
  Cloudflare connect/tunnels/cloudflared install, zone import.
- **Migration** (`handlers_migrate.go`): `POST /migrate`, `/migrate/cpanel`, `/clone` (SSH-based site pull).
- **WordPress** (`handlers_wordpress.go`): install, plugin actions, password change, harden, debug toggle.
- **MCP** (`handlers_mcp.go`): `POST /mcp/call` — dispatches to MCP tools (management actions over JSON).
- **Backups** (`handlers_backup.go`): create/restore/delete/schedule (local/S3/SFTP).
- **Observability**: logs, SSE (`/sse/stats`, `/sse/logs`), audit, monitor, alerts, bandwidth, webhooks.
- **Software store** (`handlers_software_*.go`): docker-compose template install/start/stop/update/backup.
- **Dashboard UI** (`registerDashboardUI`): `/_uwas/dashboard/` SPA file server + index fallback.

### 3b. Public web server (data-plane) — `internal/server/`
`server.go:handleRequest()` → `server_dispatch.go` pipeline. Entry for **all untrusted internet traffic**:
TLS/SNI → HTTP parse → global middleware (recovery, request ID, security headers, rate limit, access log)
→ vhost lookup → per-domain middleware (IP ACL, rate limit, basic auth, CORS, header transform) → security
guard / WAF → bandwidth → rewrite → cache → handler dispatch: **Static | FastCGI/PHP | Proxy | Redirect**.
Sinks: `handleFileRequest` (static FS), `handleProxy` (reverse proxy), FastCGI handler (PHP).

### 3c. CLI commands — `cmd/uwas/main.go`, `internal/cli/`
19 commands: serve, config, domain, cache, cert, status, reload, migrate, backup, restore, stop, restart,
php, user, install, uninstall, doctor, version, help. Several wrap privileged install/exec paths.

### 3d. SFTP server — `internal/sftpserver/server.go`
Pure-Go SSH+SFTP. Password (bcrypt) callback, per-user chroot jail (`safePath`, symlink-resolving).
Network-listening service authenticating untrusted clients.

### 3e. WebSocket endpoints
- Terminal PTY: `GET /api/v1/terminal` (`internal/terminal/handler.go` custom WS handshake + `CheckOrigin`).
- SSE streams: `/api/v1/sse/stats`, `/api/v1/sse/logs` (ticket/token auth).

### 3f. Inbound webhooks / scheduled tasks
- **Deploy webhook** `POST /api/v1/apps/{name}/webhook` — bypasses admin auth, HMAC-only (`handlers_apps_webhook.go`).
- Cron jobs (`internal/cronjob/`) and backup schedules execute commands on a timer.
- Outbound event webhooks (`internal/webhook/`, 11 events, HMAC, retry).

---

## 4. Data Flow Map (sources → processing → sinks)

**Sources (untrusted input):**
- Admin API: JSON bodies, URL path wildcards (`{host}`, `{domain}`, `{name}`, `{db}`, `{table}`,
  `{version}`, `{number}`, `{plugin}`, `{action}`), query params (`ticket`, `token`, `pin`, pagination),
  headers (`Authorization`, `X-Session-Token`, `X-TOTP-Code`, `X-Pin-Code`, `X-Requested-With`, `Origin`,
  `Referer`, `X-Hub-Signature-256`, `X-Gitlab-Token`), multipart uploads (`/files/.../upload`).
- Data-plane: full HTTP request (method, path, query, headers, cookies, body) from the internet; SNI;
  `.htaccess` files on disk; vhost config.
- SFTP: SSH auth + SFTP path/file operations.
- Terminal: WebSocket frames → PTY stdin.
- Config/state files: `domains.d/*.yaml`, users JSON, settings, audit log.

**Processing / controls:**
- `authMiddleware` (auth + CORS + CSRF + rate-limit + TOTP), `requireAdmin`/`requirePin`/`HasPermission`.
- `pathsafe` containment (`IsWithinBaseResolved`, `Base.Contains`) for file ops.
- WAF regex matching (`internal/middleware/security.go`) on URL + first 64KB of body.
- Domain hostname validation regex; SQL allowlist + `EscapeSQL` for explorer; HMAC verify for webhooks.
- bcrypt for passwords, SHA-256 for API-key hashing, `crypto/subtle.ConstantTimeCompare` for secret compares.

**Sinks (security-sensitive):**
- **Command execution** — `os/exec` in 32 non-test packages. Notably:
  `internal/terminal/pty_linux.go` (interactive shell), `handlers_apps_git.go:buildShellCmd` (**`sh -c <command>`**),
  `handlers_cron.go` (cron execute = shell command), `handlers_system.go`, `handlers_domain.go`
  (`chown -R`/`chmod -R` on domain root), `firewall/manager.go` (ufw), `database/manager.go` & `docker.go`
  (mysql/mysqldump/docker), `phpmanager`, `wordpress/installer.go` (wp-cli), `migrate/sitemigrate.go`
  (SSH), `selfupdate/updater.go`, `services/manager.go` (systemctl), `cloudflare/cloudflared.go`.
- **Filesystem** — read/write/delete/upload/mkdir in `handlers_files.go`; static server in `server_dispatch.go`;
  X-Accel-Redirect / X-Sendfile in FastCGI; SFTP file ops; config atomic writes (`atomic_write.go`).
- **SQL** — `handlers_database.go` (`fmt.Sprintf` into information_schema queries with `EscapeSQL`;
  explorer with read-only allowlist + LIMIT injection + dangerous-variant blocklist).
- **Outbound HTTP** — Cloudflare/DNS-provider APIs, S3/SFTP backups, self-update GitHub fetch, reverse proxy.
- **Response rendering** — JSON via `respond`/`jsonResponse`; SPA HTML; security headers.

---

## 5. Trust Boundaries

**Authentication** (`internal/admin/api.go:authMiddleware`, lines 274–516):
- Three modes, per-request from config:
  1. **No-auth fallback** — if `api_key=="" && !users.Enabled`, injects a virtual `admin` user and lets every
     request through. `Start()` (line 233) **refuses to bind** in this mode unless the listener is loopback;
     otherwise only logs a warning. Loopback default `127.0.0.1:9443`.
  2. **Multi-user** (`users.Enabled`, `authMgr`): Bearer API key (SHA-256 hash lookup + constant-time),
     `X-Session-Token` session, plus `ticket`/`token` query params for SSE/WS.
  3. **Legacy global API key**: `Authorization: Bearer <key>` constant-time compared; also accepts
     `ticket`/`token` query → synthesizes a virtual admin user.
- **Public (unauthenticated) endpoints** (lines 304–332): `/api/v1/health`; `GET /settings/branding`;
  everything under `/_uwas/dashboard`; **`POST /api/v1/apps/*/webhook`** (HMAC-gated downstream);
  `/auth/login` and `/auth/bootstrap` (rate-limited).
- **RBAC**: `requireAdmin` checks `user.Role == RoleAdmin` (`handlers_auth.go:513`); `HasPermission`
  (`auth/manager.go:487`) maps roles (admin/reseller/user) → permissions; `CanManageDomain` for per-domain
  scoping. Enforcement is **per-handler** (manual `if !s.requireAdmin(w,r) { return }`), not centralized —
  coverage varies (e.g. `handlers_database.go` 30 calls, `handlers_domain.go` only 1). **Missing checks in
  any handler = privilege escalation; worth verifying in Phase 2/3.**
- **2FA/TOTP**: legacy-auth path enforces `X-TOTP-Code` when `TOTPSecret` set (lines 466–483); multi-user
  TOTP handled separately. `internal/admin/totp.go`.
- **PIN**: `requirePin` (`handlers_auth.go:485`) — extra factor for terminal; constant-time compare;
  accepts `?pin=` query for WebSocket.

**Rate limiting:** IP-based failed-auth lockout in `authMiddleware` (`checkRateLimit`/`recordAuthFailure`);
login lockout window 15 min in `auth/manager.go`. Data-plane has per-domain + global rate-limit middleware
(`middleware/ratelimit.go`).

**Input validation:** JSON bodies capped via `http.MaxBytesReader` (e.g. 1MB on login/user create).
Domain hostname regex. SQL explorer allowlist. Path containment via `pathsafe`. No schema-validation
framework — validation is ad-hoc per handler.

**CSRF** (`authMiddleware` lines 485–512): state-changing methods (POST/PUT/PATCH/DELETE) and selected
"expensive GETs" (`isExpensiveGET`) require `X-Requested-With: XMLHttpRequest` **or** an Origin/Referer that
passes `isAllowedOrigin`.

**CORS** (`authMiddleware` lines 288–300 + `isAllowedOrigin` `handlers_auth.go:152`): reflects the request
Origin only if allowed. `isAllowedOrigin` accepts **any `http(s)://localhost*` or `http(s)://127.0.0.1*`
prefix** (dev convenience) plus the exact dashboard origin (`scheme://r.Host`). Prefix matching on localhost
and Host-derived origin trust are worth scrutiny (Phase 2: CORS/CSRF bypass via crafted Origin/Host).

**WAF** (`middleware/security.go`): `SecurityGuard` (blocked paths) + `DomainWAFGuard` (URL regex on
raw+decoded URI, body regex on first 64KB). Bot guard (`botguard.go`) blocks 25+ scanners. GeoIP, hotlink,
IP ACL middlewares available per-domain.

---

## 6. External Integrations

- **Cloudflare** (`internal/cloudflare/`, `dnsmanager/`): API token auth; tunnels via `cloudflared` binary
  lifecycle; zone import; cache purge; IP-range sync.
- **DNS providers** (`internal/dnsmanager/`): Cloudflare, Route53, Hetzner, DigitalOcean — API credentials,
  record CRUD.
- **Databases** (`internal/database/`): MySQL/MariaDB via local CLI (socket or TCP using `UWAS_DB_*` env)
  and Docker containers; root/user credential handling.
- **Backups** (`internal/backup/`): local, **S3**, **SFTP** targets; restore shells out to `mysql`.
- **Self-update** (`internal/selfupdate/`): downloads release binaries from GitHub.
- **Migration** (`internal/migrate/`): SSH into remote Nginx/Apache/cPanel hosts to pull sites.
- **Notifications** (`internal/notify/`): Webhook, Slack, Telegram, Email (SMTP).
- **Reverse proxy** (`internal/handler/proxy/`): outbound to upstreams; 5 LB algorithms, circuit breaker,
  canary, mirror, WebSocket passthrough.
- **ACME/Let's Encrypt** (`internal/tls/`): auto cert issuance/renewal.
- **MCP** (`internal/mcp/`): AI agent integration exposing management tools.

Secrets for these live in config/state files (see §8) and `.env` (Docker).

---

## 7. Authentication Architecture

**Model:** Hybrid — **API key (Bearer)** + **session token** + optional **TOTP 2FA** + optional **PIN**,
with a legacy single-global-key mode and a no-auth-on-loopback bootstrap mode.

- **Password hashing:** bcrypt (`bcrypt.DefaultCost`) for user passwords and SFTP user passwords
  (`auth/manager.go:302,564`; `sftpserver/server.go:86`).
- **API keys:** generated via `crypto/rand` (`generateAPIKey`), stored as **SHA-256 hash** + 8-char display
  prefix; full key shown once. Legacy plaintext-key compatibility is off by default
  (`SetAllowLegacyPlaintextKey`). Verification uses `subtle.ConstantTimeCompare`.
- **Sessions:** token-based, stored in `Manager` (in-memory + persisted `internal/auth/persist.go`),
  background cleanup loop; `ValidateSession`/`Logout`. Tickets (`POST /auth/ticket`) are short-lived
  single-use, redeemed (`redeemTicket`) for SSE/WS so the real token never sits in a URL.
- **Lockout:** failed-login tracking per username, 15-min window (`isLockedOut`, `recordFailedAttempt`),
  plus IP-level lockout in the admin middleware.
- **MFA:** TOTP (`totp.go`, `ValidateTOTP`) + recovery codes (`/auth/2fa/recovery-codes`, `/recover`).
- **RBAC roles:** admin / reseller / user with per-domain scoping (`CanManageDomain`).
- **Token storage:** API-key hashes & bcrypt hashes in users JSON under data dir; global key & TOTP secret in
  main config YAML.

---

## 8. Sensitive Files & Paths

**Config / state (secrets at rest):**
- `uwas.example.yaml`, runtime config YAML — `global.admin.api_key`, `pin_code`, `totp_secret`,
  TLS cert/key paths, provider API tokens. `internal/admin/atomic_write.go` writes config atomically; check
  perms (0600 expected for secret-bearing files).
- `domains.d/*.yaml` — per-domain vhost config (atomic temp+rename).
- Auth users store (JSON, `internal/auth/persist.go`) — bcrypt hashes, API-key hashes, sessions.
- Audit log persistence (`internal/admin/audit_persist.go`).
- `.env` / `.env.example` — `UWAS_ADMIN_KEY`, `DB_ROOT_PASSWORD`, `DB_PASSWORD`, `UWAS_DB_*` (Docker).

**Sensitive HTTP paths:**
- `/api/v1/*` — entire privileged control plane.
- `/api/v1/terminal` — root shell over WebSocket.
- `/api/v1/config/raw`, `/config/export`, `/config/domains/{host}/raw` — full config read/write.
- `/api/v1/cron/execute`, `/api/v1/mcp/call`, `/api/v1/apps/{name}/deploy` — command-execution surfaces.
- `/api/v1/files/{domain}/...` — arbitrary filesystem in domain roots.
- `/api/v1/metrics` — Prometheus metrics (info exposure).
- `/_uwas/dashboard/` — embedded SPA.

**Deployment:** `Dockerfile`, `docker-compose.yml`, `docker/`, `.github/workflows/`, `.goreleaser.yml`,
`install.sh`, `update.sh`, `init/` (systemd). No Terraform/K8s.

---

## 9. Detected Security Controls

| Control | Location / Notes |
|---|---|
| **Authn (API key/session/TOTP/PIN)** | `internal/admin/api.go:authMiddleware`, `internal/auth/manager.go`, `totp.go` |
| **RBAC** | `auth.HasPermission`, `requireAdmin`, `CanManageDomain` — enforced per-handler (uneven coverage) |
| **Constant-time secret compare** | `crypto/subtle.ConstantTimeCompare` for API key / PIN / key-hash |
| **Password hashing** | bcrypt (users + SFTP) |
| **Rate limiting / lockout** | admin IP lockout + per-user login lockout (15-min); per-domain & global data-plane rate limits |
| **CSRF protection** | `X-Requested-With` + Origin/Referer allowlist on mutating + expensive-GET requests |
| **CORS** | origin-reflect with allowlist (`isAllowedOrigin`) — localhost prefix + Host-derived origin |
| **WAF** | `SecurityGuard` + `DomainWAFGuard` (URL + 64KB body regex: SQLi/XSS/shell/RCE) |
| **Bot guard** | `middleware/botguard.go` (25+ scanners) |
| **Path traversal guard** | `internal/pathsafe` (symlink-resolving containment), SFTP `safePath` |
| **Security headers** | `middleware/security.go` / `headers.go` (global) |
| **Input size limits** | `http.MaxBytesReader` on JSON bodies; WAF body 64KB scan with MultiReader restore |
| **SQL injection mitigation** | `EscapeSQL` + read-only allowlist + dangerous-variant blocklist in explorer |
| **Webhook HMAC** | GitHub `X-Hub-Signature-256` / GitLab token verify (`handlers_apps_webhook.go`) |
| **Audit logging** | `internal/admin/audit.go` + persistence; security event records |
| **WS origin check** | `terminal/handler.go:CheckOrigin`; terminal also needs admin + PIN |
| **Loopback-bind safeguard** | refuses public bind when no auth configured (`api.go:233`) |
| **TLS / ACME** | `internal/tls` auto-HTTPS + renewal; admin listener optional TLS |
| **Secret masking** | settings GET masks secrets; API key shown once; `FullAPIKey` not persisted |
| **PHP sandbox** | per-domain `disable_functions`, `open_basedir`, `allow_url_include=Off` |

**Notable controls to probe in later phases (not yet findings):**
- Per-handler RBAC enforcement is manual — any missed `requireAdmin` is an authz gap.
- `isAllowedOrigin` localhost prefix-matching and Host-derived dashboard origin (CORS/CSRF bypass risk).
- `buildShellCmd` uses `sh -c <command>` and `handlers_cron.go` runs arbitrary shell commands — verify
  authz + input provenance (command injection).
- `handlers_domain.go` runs `chown -R`/`chmod -R` on domain roots derived from input — verify path control.
- File-manager `{domain}` → root resolution (`domainRootForFiles`, `authorizedDomainRoot`) vs traversal.
- Deploy webhook is unauthenticated except HMAC — verify timing-safe compare + secret strength + replay.
- No-auth loopback bootstrap mode: confirm it cannot be reached via proxy/Host trickery.

---

## 10. Language Detection Summary (drives Phase 2)

```
- Go (~70% of source LOC, primary, all server/control-plane logic) → activates sc-lang-go
- TypeScript/React (~30%, admin dashboard SPA)                     → activates sc-lang-typescript
```

Also relevant for Phase 2 cross-cutting skills:
- **Infrastructure-as-code / containers** present (`Dockerfile`, `docker-compose.yml`, `.github/workflows/`,
  `docker/`) → IaC / CI scanning.
- **Supply chain**: Go modules (5 direct deps, stdlib-first) + npm (`web/dashboard/package.json`) → dependency audit.
- **PHP**: not part of UWAS source, but UWAS executes/manages PHP-CGI/FPM and writes php.ini — relevant to
  the FastCGI handler and PHP sandbox config, not a `sc-lang-php` target.

**Test/generated context (exclude from findings):** `*_test.go` (~146k LOC, heavy coverage suites in
`internal/admin/coverpush_*`, `admin_coverage*`), `examples/`, `test/`, `bench.test` binary, dashboard
build output, vendored Go deps.
