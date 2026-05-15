# Changelog

All notable changes to UWAS will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.5.0] - 2026-05-16

A focused refactor + performance + observability sweep on top of
v0.4.2 — 41 atomic commits closing Phase 1-3 of the internal
`refactor.md` backlog. **No new user-facing features**, but enough
internal shape change to warrant a minor bump: `internal/config` is
sharded across 14 files, `installer.go` / `phpmanager.go` /
`handlers_hosting.go` were split into per-concern files, the legacy
plaintext API-key fallback is now opt-in, and an `internal/respond`
package centralizes JSON response writing (with operator-visible
5xx logging baked in).

**One behavioural change to flag:** plaintext API-key authentication
(`api_key: ABC123...` matched against `global.admin.api_key` without
hashing) is now **disabled by default**. Operators upgrading from
v0.4.x who use a plaintext key without having rotated through the
multi-user flow should set `global.users.allow_legacy_plaintext_api_key:
true` and plan a migration to hashed credentials. The fallback emits
a startup warning when enabled.

Phase 4-6 of the backlog (admin/api.go split, handleRequest
decompose, `sysexec` abstraction, test infrastructure refresh,
frontend page splits) is intentionally deferred — those are
multi-day "L" items that would have padded this release without
new operator value.

### Bug fixes (concurrency)

- **PHP env merge no longer mutates a shared `*config.Domain` pointer**
  (`863b92b`) — two concurrent PHP requests to the same host could
  race the FPMAddress / Env restore. `s.php.Serve` gained an explicit
  `ServeWith(ctx, domain, fpmAddr, env)` overload so the merged state
  rides the call stack instead of the shared struct.
- **Per-domain rate-limiter goroutines stop on reload** (`4f9ff64`) —
  each `RateLimiter` now owns a `context.CancelFunc`; the reload path
  cancels the old map before swapping it in. Frequent hot reloads no
  longer leak N goroutines per cycle.
- **GeoIP external lookups are bounded** (`944cc9c`) — replaced the
  unbounded `go lookupExternal(ip)` fan-out with a 4-worker pool +
  256-slot buffered queue + per-IP singleflight + 5-minute negative
  cache. Random-source-IP sprays can no longer fork goroutines as
  fast as packets arrive.

### Performance (hot-path)

- **TLS handshake allowlist is now lock-free** (`012b7be`) — replaced
  the O(N·M) scan over `m.domains` + aliases under a mutex with an
  `atomic.Pointer[domainAllowlist]` built once in `UpdateDomains`.
  500 domains × 10 aliases per handshake now drops to one atomic load
  + one map probe.
- **IPACL / GeoIP / CORS / WAF guards now run as predicates**
  (`d06b23f`) — each guard exposes a closure form
  (`func(w, r) bool`) that the request hot path calls directly,
  removing the per-request `next.ServeHTTP` wrapper allocation when
  a domain has any of these features enabled.
- **API-key lookup is O(1)** (`3e9b42d`) — `auth.Manager` keeps a
  secondary `map[hash]*User` index alongside the username map.
  Authentication no longer takes an RLock to linear-scan every user
  per admin/MCP request.
- **`LastLogin` updates are lock-free** (`b6ca33d`) — moved to
  `atomic.Int64` (unix seconds) so post-bcrypt verification doesn't
  re-acquire the manager write lock for one field.
- **Cache LRU promotion is debounced** (`6038a3e`) — only every Nth
  read takes the shard write lock to `MoveToFront`, so heavy reads
  on hot content no longer serialize all readers behind one shard.
- **Domain logs use per-host mutexes** (`209279d`) — replaced the
  one-mutex-per-manager design with a per-`domainLogFile` lock so
  request paths writing to different logs don't serialize.
- **ACME renewal split from cert-map iteration** (`3da22ad`) — the
  renewal scan now collects candidates in pass 1, then ranges over
  the candidate slice outside the `sync.Map.Range` callback. ACME
  network I/O (potentially minutes per cert) no longer blocks
  unrelated handshakes that touch the same map.
- **Rewrite engine pre-checks pattern before building Variables**
  (`a4d3431`, `d0cfddf`) — `BuildVariables` (HTTP_HOST, REQUEST_URI,
  …) is skipped entirely when `engine.MightMatch(uri)` proves none
  of the rule patterns could match. `htaccessCacheEntry` also
  caches the pre-built `rewrite.Engine` so each request doesn't
  re-compile the rule set.
- **`time.Now()` consolidated and rate-limit allocations lazy**
  (`8eaabe6`) — one `now := time.Now()` per request feeds all
  location rate-limit checks; the per-key entry is only allocated
  on first hit via `LoadOrStore`.
- **`router.Lookup` + `IsConfigured` collapsed** (`8da706e`) — the
  HTTP entry path needed both; `LookupWithStatus` returns
  `(*Domain, configured bool)` in one pass instead of two.
- **PHP-FPM hot-path lookup is a single map probe** (`70cb196`) —
  `RunningAddrForDomain(host)` replaces the per-request linear
  scan over `GetDomainInstances()`.
- **`isPrivateIP` no longer re-parses CIDRs per request**
  (`1deccb6`) — the six private-network CIDRs are parsed once at
  package init into `[]*net.IPNet`.
- **PHP cacheable extension set lifted to package scope** (`89c7e1d`)
  — the 17-entry `map[string]bool` literal is no longer allocated
  per cache-eligible PHP request.

### Refactor

- **`internal/respond` package** (`ab35ece`) — `respond.JSON`,
  `respond.Error`, `respond.ErrorCause` centralize JSON response
  writing with hardening headers, status code, and 5xx error
  logging in one place. Admin's `jsonError` / `jsonErrorCause`
  delegate to it via `SetLogger`.
- **`internal/admin/api.go` shrunk from 6,275 → 5,717 lines**
  through targeted splits: `handlers_hosting.go` (2,893 LOC) →
  9 themed handler files (`123442d`), `registerRoutes` (328 LOC)
  → themed sub-registrars in `routes.go` (`b09d082`), generic
  `ringBuffer[T]` extracted for logs + audit (`13c2d53`), and
  `phpInstallStatus` ring removed in favor of `taskMgr`
  (`6e13157`).
- **`internal/config/config.go` sharded** (`bceb880`) — the
  737-LOC file split into 14 per-feature files (`global.go`,
  `domain.go`, `backup.go`, `tls.go`, `cache.go`, `security.go`,
  …). `Config` root stays in `config.go`.
- **Typed `DomainType` enum** (`ae62e15`) — `string` Domain.Type
  remains for YAML/JSON compatibility, but `DomainType` /
  `IsValid()` / typed constants now drive validation and dispatch.
- **`config.MergeDomain` extracted** (`87193a9`) — `handleUpdateDomain`
  no longer carries 286 lines of manual nil-check merge logic; the
  pure merge/replace function ships with its own unit tests in
  `internal/config/merge_test.go`.
- **`wordpress/installer.go` split into 4 files** (`6e9ec0a`) —
  `installer.go` (931), `updater.go` (346), `harden.go` (230),
  `dbtools.go` (94). One file per concern.
- **`phpmanager/manager.go` split into 4 files** (`9e4423b`) —
  `manager.go` (780), `detect.go` (381), `ini.go` (172),
  `fpm.go` (273). Lifecycle / detection / INI / FPM are now
  separately readable.
- **`install.Manager` → `install.Queue`** (`9a2e5b1`) — the type
  is a task queue, not a daemon-like owner; the rename frees
  "Manager" for the daemon archetype.
- **`backup.go` shares an `archiveAndUpload` helper** (`70cd8fd`)
  — `CreateBackup` and `CreateDomainBackup` no longer re-implement
  the same tar / gzip / temp-file / upload / cleanup skeleton.
- **Domain validation consolidated** (`4710566`) — moved into
  `config.ValidateDomain`; admin keeps only runtime checks
  (PHP availability, web-root containment).
- **Cloudflare v0.1.6 → v0.2.0 migration gated by schema version**
  (`7b0d06a`) — `state_schema_version` field on the cloudflare
  state file; the legacy `Domain` → `Hostname` rename runs once
  per install, then never again. Slated for removal after v0.6.
- **Domain-handler dispatch consolidated** (`c8db418`) — the three
  switch sites in `server.go` now share a single
  `dispatchHandler(ctx, domain)` method. Pairs with the per-
  handler latency histograms below.

### Security / auth

- **Legacy plaintext API-key fallback gated by config** (`5df01f0`)
  — `users.allow_legacy_plaintext_api_key: false` by default.
  Operators relying on the v0.4.x convenience path must set it to
  `true` explicitly; the manager logs a loud startup warning when
  enabled. Plan is `default false` in v0.5, `removed` in v0.6.

### Observability

- **Per-handler latency histogram** (`c8db418`) — new
  `RecordHandlerLatency(handlerType, status, d)` hook feeds a
  fixed-size ring buffer per handler; `HandlerPercentiles` exposes
  p50/p95/p99/max. Prometheus output adds
  `uwas_handler_duration_seconds{handler,quantile}`. Dashboard
  Metrics page (`/api/v1/stats`) returns the new `handler_latency`
  block.
- **X-Request-ID propagated across proxy / FastCGI / WebSocket**
  (`6565227`) — `RequestID` middleware now stamps the generated ID
  on `r.Header` so downstream copy loops forward it; proxy / FastCGI
  upstream calls include it, and WebSocket tunnel-goroutine log
  entries (`websocket connect failed`, `websocket copy errors`,
  `upstream error`, `retrying upstream`) include `request_id`.
- **`"host"` vs `"domain"` log field standardized** (`ecc994e`) —
  swept slog calls to use `"domain"` for our entities; `"host"`
  remains only for remote / network hosts (ESI fragments,
  upstreams). TLS manager + admin + server log sites converted.
- **`"err"` vs `"error"` log field standardized** (`6023429`) —
  internal slog calls now use `"error"` uniformly; the 4-5
  `"err"` stragglers were removed.
- **Audit entries on the highest-risk endpoints** (`eee8b73`) —
  `handlePHPConfigRawPut`, `handlePHPEnable`, `handlePHPDisable`,
  `handleConfigRawPut` (full config-file overwrite) now record
  audit entries on every branch, success and failure, with size
  / version / domain-count detail.
- **5xx admin responses log at error level** (`27a999e`) — the
  free-function `jsonError` / `jsonErrorCause` helpers
  (centralized in `respond` per the A10 commit) emit a structured
  error-level log with status, message, request_id, and (when
  available) the underlying cause for every 5xx response.

### Error context

- **`internal/database` wraps operations with the (db, user, host)
  tuple** (`e80891c`) — `CreateDatabase` / `DropDatabase` /
  `ChangePassword` / `ListDatabases` / `ListUsers` plus their
  Docker-container equivalents previously returned the raw MySQL
  or `docker exec` error. Operator logs now read
  `drop database "wp_foo" (user "wp_foo"@"localhost"): permission
  denied` instead of bare `permission denied`.
- **CLI `addFileToTar` errors carry the source path** (`9b06cca`,
  `0836ad2`) — `os.Open` / `Stat` / `WriteHeader` / `io.Copy`
  errors wrap with the path; the caller-side `os.IsNotExist`
  check upgraded to `errors.Is(err, fs.ErrNotExist)` so wrapped
  errors are still detected. CLI `apiRequest` callers also
  wrapped with operation context.

### Verification

- `go build ./...`, `go vet ./...` clean.
- `go test ./... -count=1 -short` passes 51 of 54 packages. Three
  failures are pre-existing environment-dependent flakes that
  also fail on v0.4.2: `TestHandleDockerDBCreate_*` (admin —
  requires a running Docker daemon), `TestSFTPProviderListEmpty`
  (backup — known-hosts cache mismatch), `TestInstall_HtaccessWriteFails`
  (wordpress — live network test, refactor.md T3).

## [0.4.2] - 2026-05-15

A security & robustness sweep on top of v0.4.1 — 13 atomic fixes
batched together. No new features; no breaking config changes for
correctly-configured deployments. **One behavioural change to flag:**
the admin API now refuses to bind on a non-loopback address when no
credentials are configured (`api_key` empty AND multi-user disabled)
— this was previously silently exposing the full 221-endpoint API as
RoleAdmin. Set `global.admin.api_key` (or `global.users.enabled: true`)
before upgrading if your listen address is anything other than
`127.0.0.1:*` / `::1:*` / `localhost:*`.

### Security

- **Admin role required on settings/notifications and settings/branding**
  (`e1268ef`) — both PUT handlers previously accepted any authenticated
  caller, letting a RoleUser overwrite system-wide webhook URLs or inject
  branding HTML rendered into other admins' sessions.
- **Constant-time comparison on the deploy webhook `?secret=` path**
  (`4117832`) — the GitHub-HMAC and GitLab-token branches already used
  `subtle.ConstantTimeCompare`, but the fallback query-param branch
  compared with plain `!=`. Recovered the secret byte-by-byte over the
  network meant arbitrary deploy → RCE.
- **SSRF check on Telegram notify channel** (`4128c1c`) — webhook and
  Slack ran the URL through `notifyURLSafetyCheck`; Telegram did not.
- **SSRF check + context propagation on uptime monitor** (`769633e`) —
  the per-30-second domain probe used `http.NewRequest` with no context
  and no safety policy. A stale domain entry pointing at
  `169.254.169.254` would turn the monitor into a metadata scanner.
- **`internal_aliases` validation rejects system directories** (`443969c`)
  — X-Sendfile / X-Accel-Redirect targets outside the docroot are opt-in
  via `internal_aliases`. Validate now refuses entries inside `/etc`,
  `/root`, `/proc`, `/var/log`, `C:\Windows`, `C:\Program Files`, etc.,
  closing the misconfiguration door before a compromised PHP app can
  exploit it.
- **Admin API refuses to bind publicly without credentials** (`39684f8`)
  — the auth-middleware "no creds → virtual admin" convenience kicked in
  regardless of listen address. Start now hard-errors when no
  `api_key` / multi-user is set AND the listen address is non-loopback;
  loopback startups still proceed but emit a loud WARN.
- **SFTP open uses `O_NOFOLLOW` on Unix** (`fc34f2e`) — `safePath()`
  validated containment but a sufficiently fast SFTP user could replace
  the final path component with a symlink between the check and the
  open. The flag is build-tagged: real `syscall.O_NOFOLLOW` on Unix, no
  effect on Windows (which is not vulnerable to the same attack shape).
- **CSRF guard extended to expensive GET endpoints** (`4f93f46`) — the
  middleware only fired on POST/PUT/PATCH/DELETE, leaving
  `GET /api/v1/config/export`, `GET /api/v1/database/{name}/export`, and
  `*/download` endpoints CSRF-reachable. An attacker page could force an
  admin's browser into a full `mysqldump` even though the attacker never
  sees the bytes.
- **Session-token callers can mint auth tickets** (`884c6d5`) —
  `handleAuthTicket` only accepted `Authorization: Bearer`, so a
  browser-session user got a 400 and the dashboard's only escape was to
  pass the raw token in the SSE/WebSocket URL — the very leak the
  ticket flow was built to prevent.

### Fixes

- **Reverse-proxy upstreams accept `host:port` without scheme**
  (`921013e`) — `url.Parse("127.0.0.1:3000")` silently produced an empty
  host and the backend fell out of the pool, surfacing as a cryptic 502
  "no healthy upstreams". `NormalizeProxyUpstreamAddress` now adds the
  `http://` prefix when missing, in both validation and pool
  construction. Same commit also normalises balancer algorithm names so
  the dashboard's dashed forms (`least-conn`) actually dispatch instead
  of silently falling back to round-robin.
- **HTTP→HTTPS redirect only fires when a cert is loaded** (`8b567ab`) —
  auto-SSL domains defaulted to `ssl.mode: auto`. While ACME issuance
  was still in flight (or DNS hadn't propagated), the redirect sent the
  browser straight into a TLS handshake failure with no recoverable
  state. Now port 80 falls through to plain HTTP until `tlsMgr.HasCert`
  reports a usable certificate.
- **Cert upload is atomic and crash-safe** (`994fb99`) — replaced the
  two bare `os.WriteFile` calls with a new internal `atomicWriteFile`
  helper (same-dir temp → fsync → rename). Removes three failure modes:
  half-written cert/key pair after power loss, kernel reorder hiding
  the fsync, and TLS-manager reload racing the writes.
- **WebSocket proxy teardown** (`badf7d3`) — `sync.Once` wraps the
  bidirectional close so two goroutines can race it safely, and
  `SetDeadline(now)` is called before `Close` to unstick any Read
  blocked on a half-open peer. `closeBoth` is now `defer`-ed so a
  panicking `io.Copy` still tears the partner down.

### Verification

- `go build ./...`, `go vet ./...`, `staticcheck ./...` all clean.
- Full `go test -count=1 -short ./...` passes on all 53 packages.
- Live binary regression: schemeless upstream proxies correctly, evil-
  Referer hitting `/config/export` returns 403, non-loopback admin bind
  without credentials refuses to start, `C:\Windows` in
  `internal_aliases` is rejected at reload with a clear error.

## [0.4.0] - 2026-05-05

A polish + hardening release: 67 commits batched together. Highlights are auth persistence across restart, audit-log user attribution everywhere, a visibility-aware polling sweep across the dashboard, secret redaction on every config-export surface, and a per-page UI quality pass that fixed dirty-edit data loss, poll-handle leaks, and dead-code action buttons across roughly 30 pages.

### Security

- **Secret redaction in raw config export** (`b14c4f4`, `b4b260d`, `37f7c78`) — `GET /api/v1/admin/config/raw` and the config-export endpoint were leaking DNS provider tokens (Cloudflare/Route53/Hetzner/DigitalOcean), OAuth client secrets, and alerting webhook URLs in plaintext. All secret-bearing fields are now masked; regression test locks the contract.
- **MCP `domain_get` redaction** (`416f374`) — the MCP tool was returning per-domain secrets (basic-auth credentials, proxy tokens, webhook signing keys) in full. Now redacted before serialization, matching the REST API.
- **Webhooks page no longer leaks HMAC secret** (`546e36a`) — the dashboard previously rendered the per-webhook signing secret in plaintext after creation. Now masked with copy-once reveal.
- **Unknown-host rejection returns 421** (`8397da2`) — requests for hostnames not configured as domains are tracked in the unknown-domains store and answered with `421 Misdirected Request` instead of being routed to the fallback (which previously returned 200 from the placeholder).

### Features

- **Auth persistence across restart** (`4ddc2de`, `c98bb86`, `8eca1fa`) — JWT signing key persists to `~/.uwas/auth.json` (mode 0600) instead of being regenerated on every boot. Active sessions persist to `~/.uwas/sessions.json`. Restarting the server no longer kicks every logged-in user. New cleanup goroutines prune stale `loginAttempts` entries and expired sessions on a fixed cadence.
- **Audit log user attribution everywhere** (`0c55ba0`, `a2a2b80`, `4d311f9`) — migrated the remaining 102 `s.RecordAudit(...)` call sites in production handlers to `s.recordAuditR(r, ...)`, which extracts the authenticated user from the request context. The `User` column on the Audit Log page now populates for every state-changing action (domain/PHP/cache/backup/2FA/cron/Cloudflare/WordPress/database/docker_db/migrate/clone/cert/webhook/settings/notifications/branding/bandwidth/PIN), not just auth endpoints.
- **Audit log rotation + replay** (`b4c2646`) — `~/.uwas/audit.log` now keeps 3 rotated generations; the last 500 entries from all generations replay into the in-memory ring buffer at startup so the audit trail is durable across restarts and rotations.
- **Visibility-aware polling hook** (`caf707d`, `0067f0e`, `02a0eee`, `8cf026a`, `eaf8f12`, `e9f89af`, `8d424f6`) — new `usePolling` hook pauses while the browser tab is hidden. Migrated Domains health, Cloudflare status, Logs live tail, AuditLog refresh, Security, UnknownDomains, Services, Dashboard, Certificates pending-cert refresh. Extended to accept `intervalMs=null` so toggle-driven pause is one effect, not two.
- **Topology: click-to-detail** (`8b26686`) — clicking a domain node in the topology graph now navigates to that domain's detail page.
- **Backups & Webhooks pages** show `FeatureBanner` when the underlying manager is not initialized, so an empty list never silently masks a disabled subsystem.

### Fixes — Dashboard quality sweep

- **Dirty-edit data loss guards** — ConfigEditor (`6ccac32`), Files (`1886735`), and PHP-Config (`46cdddd`) now confirm before discarding unsaved edits. PHP-Config also adds a `post_max_size` ↔ `upload_max_filesize` cross-check.
- **Confirm before destructive action** — Doctor Auto-Fix (`ff1ae16`), Updates install (`e1e1b15`), Firewall disable (`dd91021`, plus warn on enable without SSH), PHP unassign (`daa377f`), Cloudflare disconnect (`958be5e`), Users SSH-key delete (`9cddc51`), IPs domain-IP change (`d8c65d4`).
- **Poll handle leaks** — Database install (`7cf92d5`), Apps deploy (`baf6527`), PHP refresh (`daa377f`) — long-running poll loops now cancel on unmount and on action completion.
- **Cross-domain state bleed** — Security (`92474d1`) WAF bypass + IP allow/deny inputs reset on domain change. WordPress (`9d10541`) clears stale per-site state across actions. DNS (`8e8f5a9`) resets editor state on domain change. DB-Explorer (`9d6d6f6`) clears stale query results.
- **Empty-state and error surfacing** — DomainDetail load errors (`54ff5dc`, retry button instead of "not found"), Topology empty/refresh (`e4683c0`), Certificates empty state (`e4683c0`), EmailGuide empty state (`8cf026a`), DNS empty-state messaging (`8e8f5a9`), DB-Explorer empty state (`9d6d6f6`), AuditLog free-text search + exact-match chip filter (`2712d6a`), Apps env-save errors (`baf6527`), Unknown-Domains action errors + `timeAgo` NaN guard (`f42e2ff`).
- **Toast handling** — auto-dismiss success toasts on Backups (`e4336f8`), Settings (`88a2a3f`), AdminUsers (`6ac3a01`), Cron (`15c6892`), Services (`5a063d8`), IPs (`d8c65d4`), Cache (`71fe505`).
- **Router consistency** (`afaf139`, `eaf8f12`, `e4683c0`, `0832a80`, `8cf026a`) — replaced plain `<a href="/_uwas/dashboard/...">` anchors with React Router `<Link>` on Dashboard (first-run wizard), Domains, DNS, WordPress, EmailGuide. Plain anchors were doing full-page reloads and bypassing the `BrowserRouter` basename. First-run wizard now load-gated so it doesn't flash before domains arrive.
- **Page-specific** — Login clears TOTP digits after rejected code (`883fad0`); Backups removes dead Download button (`e4336f8`); Terminal hides auth ticket on error and `preventDefault` for `Ctrl+C/D` to keep the keystroke in the PTY (`d7bffb9`); About surfaces non-ok health (`565256f`) and refreshes dep/size facts; Logs export RFC 4180 CSV-escapes + touch-friendly button (`33054d3`); Metrics filter + raw `+Inf/-Inf/NaN` rendering (`cdd4e4a`); Cache replaces fake-Redis form, fixes per-domain purge (`71fe505`); Email drops broken last-2-labels ccTLD heuristic (`b8ce9be`); Packages real Escape handler + timeout feedback + null-safe find (`192e45e`); Migration clears cpanel file after import (`15c0339`); Clone/Staging warns on existing target (`7564218`); Cron stable react keys (`15c6892`); Database export download (`7cf92d5`); Domains row click + Tailwind-purge-safe gauges (`8d424f6`); Domains App-runtime selector colors (`c4b1d22`); Analytics independent loads + reset feedback (`260d384`); DB-Explorer ctrl+enter (`9d6d6f6`); Cloudflare/DBExplorer dark-theme alerts (`f57dbf5`); About 35→40 page-count fix (`3fb3a6d`).

### Refactor

- **Cloudflare zone-sync retired** (`7f86026`) — `/api/v1/cloudflare/zones/{id}/sync` was a no-op holdover (fetched DNS records and discarded them). The real implementation is `/api/v1/cloudflare/zones/{id}/import`. Handler, route registration, frontend `syncCloudflareDNS` export, and three tests removed.
- **Dead code prune** (`86e48d4`, staticcheck-driven) — removed ~150 lines no caller reaches: `internal/cache/l1_shard.go` (orphan shard-stats type), `requireRole`/`persistCloudflareState` in admin, `BackupManager.startedAt`, `htaccessCacheEntry.errorPagesOnce`, `sensitiveHeaders`+`sanitizeHeader` in accesslog (header redaction was never wired into the log line), `blockedIPBlocks`/`concatIPBlocks`/`isIPBlocked` in config (superseded by `ipBlockedReason`+policy), three test mock helpers. Plus four lint cleanups (loop → `copy()`, error-string punctuation, `t.Fatal` instead of nil-deref, redundant `var`-then-assign).
- **Code structure cleanup** (`2bbcb41`) — internal readability/maintainability pass.

### Verification

- `go build ./...`, `go vet ./...` clean.
- `go test -count=1 -short ./...` passes (the wordpress placeholder-removal test occasionally fails when run in parallel with other tests in the same package due to a global hook variable race; passes deterministically with `-run` or in isolation; pre-existing, not introduced by this release).
- `node web/dashboard/node_modules/typescript/bin/tsc -b web/dashboard` clean.

## [0.3.1] - 2026-05-04

### Features

- **Audit log persistence** — `~/.uwas/audit.log` JSONL append-only, 10 MB rotation; the last 500 entries replay into the in-memory ring buffer at startup. The audit trail no longer disappears on restart.
- **FeatureBanner on Backups + Webhooks** — both pages report the disabled-reason instead of an empty list.

## [0.3.0] - 2026-05-04

### Features

- **Real Redis client** — replaced the in-memory mock with a from-scratch RESP wire-protocol client (no new dependencies; one mutex-serialized TCP conn, auto-reconnect on I/O error, TLS opt-in via `redis.tls`).
- **App stop persistence** — `AppConfig.Disabled` now survives restart; an app the user explicitly stopped no longer auto-restarts on next boot.
- **Sidebar feature awareness** — disabled features (apps, cron monitor, security stats, unknown domains, webhooks, backups) are dimmed with an "off" badge and hover-tooltip explaining why.

### Security

- **Go 1.26.2** — closes 5 stdlib CVEs flagged by govulncheck (crypto/x509, crypto/tls, archive/tar). CI was already pinned to the 1.26 major track, so released binaries were always patched; this only matters for `go build` from a checkout.

## [0.2.2] - 2026-05-04

### Features

- **`GET /api/v1/features`** — reports which optional subsystems are wired up (apps, bandwidth, cron monitor, unknown domains, security stats, deploys, backups, webhooks, tls, alerting, uptime monitor, php). Used by dashboard pages to show a "feature not enabled" banner instead of a misleading empty list.
- **`FeatureBanner` component** wired into Apps, CronJobs, Security, UnknownDomains, Analytics.
- **DB Explorer existence check** — `/api/v1/db/explore/{db}/tables` now returns 404 with a clear message when the database does not exist, instead of a confusing 500.

## [0.2.1] - 2026-05-04

### Features

- **Cloudflare zones**: real pagination (backend iterates all `result_info.total_pages`) + client-side search filter with "X of Y" count display.
- **Cloudflare zone import**: dry-run preview with hostname checkboxes; user picks defaults (PHP/Static/Proxy/Redirect, web root template) and confirms before adding to UWAS domains.

### Fixes

- `Manage DNS` link in the Cloudflare page now uses React Router's `<Link>` so it respects the `/_uwas/dashboard` basename instead of doing a full-page navigate to `/dns`.

## [0.2.0] - 2026-05-04

### Features

- **Real Cloudflare Tunnels** (Phase B) — `internal/cloudflare/` package wraps the Cloudflare API and the `cloudflared` binary. Create / start / stop / delete tunnels; auto-restart on crash; `cloudflared` binary install via UI; tunnel state persisted in `cloudflare.json`. Replaces the v0.1.6 stub that generated a random hex token in RAM and never spawned a real tunnel.

## [0.1.6] - 2026-05-04

### Features (Cloudflare — Phase A)

- **Cloudflare state persistence** — token, account ID, and connection state stored in `~/.uwas/cloudflare.json` (mode 0600). Token masked in `GET /api/v1/cloudflare/status` responses.
- **Zone import** — `POST /api/v1/cloudflare/zones/{id}/import` adds A/AAAA/CNAME hostnames from a Cloudflare zone as UWAS domains, with a user-chosen default type and webroot. Replaces the v0.1.x "Sync DNS" no-op.
- **DNS page accepts `?domain=` query param** — `Manage DNS` from the Cloudflare page deep-links into the DNS editor for that zone.
- **UI honesty** — tunnel section explicitly labelled "coming next minor release" instead of pretending to work.

## [0.1.5] - 2026-05-04

### Fixes

- **Terminal WebSocket** — allow http same-origin (was https-only, broke http deployments).
- **Self-update** — log auto-restart failures instead of swallowing them. The v0.1.4 binary still has the silent-failure bug; restarting the service manually after upgrading from v0.1.4 is required once.
- **WordPress installer** — use the SHA1 checksum endpoint (the SHA256 endpoint we were calling does not exist).

## [0.1.4] - 2026-05-04

### Fixes

- **Dashboard rebuild** — embedded bundle includes all the `api.ts` safety fixes from v0.1.1–v0.1.3.

## [0.1.3] - 2026-05-03

### CI

- Auto-generate release notes from commit messages.
- Publish releases as `latest`, not prerelease.

## [0.1.2] - 2026-05-03

### Fixes

- **Dashboard** — default array endpoints to `[]` when the backend returns `null`, so no page crashes on a missing handler.

## [0.1.1] - 2026-05-03

### Fixes

- **Dashboard** — guard null and paginated API responses to prevent UI crashes when an endpoint returns `{items, total, ...}` instead of a bare array.

## [0.1.0] - 2026-05-03

Same commit as v0.0.56 (semver bump for clarity). See v0.0.56 entry below.

## [0.0.57] - 2026-05-03

### Fixes

- **Dashboard pagination** — extract `.items` from paginated API responses (continuation of v0.1.1/v0.1.2 fixes for endpoints we missed).
- **Vite 8.0.5** — patches 3 high-severity vulnerabilities in the build toolchain.
- **Backup interval `7d` → `168h`** — config parser only understands hour units; `7d` was rejected.
- **Mobile menu z-index** — toggle button rendered behind the sidebar on small screens.

## [0.0.56] - 2026-05-03

### Features

- **Deploy health check** — git-mode deploy now verifies the app is responding after restart via HTTP health check, and propagates AppPort from the deployed app back to domain config.
- **Deploy concurrent protection** — only one active deploy per domain; concurrent deploys are rejected with clear error.
- **Deploy env persistence** — environment variables (APP_PORT, APP_RUNTIME, APP_COMMAND) are persisted to `.uwasenv` after successful git-mode deploy.
- **Deploy cancellation** — `CancelDeploy` aborts an in-progress deploy by killing the build process and cleaning up.

### Verification

- `go build ./...` passes.
- `go test -short -count=1 ./internal/deploy/...` passes.
- `go test -short -count=1 ./internal/appmanager/...` passes.

## [0.0.55] - 2026-05-02

### Security Fixes

- **SFTP backup 100MB size bound** — `io.LimitReader` prevents unbounded memory allocation when reading backup data for SFTP upload. A size check returns a clear error if the limit is exceeded.
- **WebSocket Origin header validation** — reject WebSocket connections without Origin header to prevent cross-site WebSocket hijacking.
- **io.LimitReader bounds** — added `io.LimitReader` bounds to all `io.ReadAll` calls to prevent unbounded memory allocation (4KB-256MB limits depending on context).
- **CSRF PATCH method** — added PATCH method to CSRF protection (was missing from the allowed methods list).
- **Global rate limit config** — fixed MEDIUM-1/2 where global rate limit was not being properly initialized from config.
- **RFC 1035 domain name validation** — fixed MEDIUM-3 by implementing RFC 1035-compliant domain name validation (no leading/trailing hyphens, no consecutive dots).
- **GDPR consent for IP logging** — fixed MEDIUM-7 by adding RecordIP consent check in audit logging.
- **Per-domain webhook HMAC secret** — fixed MEDIUM-5/6 by ensuring per-domain webhook configs use their own secret, not the global secret.
- **Config validation, path traversal, shell injection** — fixed CRITICAL-2/4/6/7 including config validation, path traversal, and shell injection vulnerabilities in SFTP passwords.
- **CSRF token infrastructure removal** — removed partial CSRF token infrastructure (MEDIUM-8) as it was causing confusion and incompatibility.
- **Session invalidation, DB query limits, HSTS, request IDs** — fixed MEDIUM-9, MEDIUM-11, LOW-1, LOW-4 including session invalidation on logout, database query limits, HSTS header, and request ID tracking.
- **Domain deletion confirmation** — require explicit confirmation for domain deletion (MEDIUM-14).
- **Database identifier validation** — disallow dash in database identifiers to prevent SQL injection via database names.

### Verification

- `go vet ./...` passes.
- `go test -count=1 -short ./...` passes (52 packages).
- `tsc -b` passes in `web/dashboard`.

## [0.0.54] - 2026-04-28

### Security Fixes

- **WordPress checksum verification** — installer was downloading `.sha512` checksum file but computing SHA256 hash. Since SHA512 and SHA256 produce different digests, checksum verification always silently failed. Fixed to download `.sha256` file matching the SHA256 computation.

### Fixes

- **WordPress installer tests** — fixed 16 tests that failed due to mock HTTP handlers returning identical content for both tarball and checksum URLs. Introduced `fakeTarHandlerFunc` with two test servers for proper URL-based routing.
- **Selfupdate updater tests** — fixed 5 tests with the same checksum mock issue. Added `binaryHandler` helper.
- **CLI stop command test** — accept "not supported" error on Windows where SIGTERM is unavailable.

### Verification

- `go vet ./...` passes.
- `go test -count=1 -short ./...` passes (52 packages).
- `tsc -b` passes in `web/dashboard`.

## [0.0.51] - 2026-04-05

### Fixes

- **Dashboard system stats refresh** - system stats bar now refreshes every 2 seconds instead of 10 seconds for near real-time CPU, RAM, disk monitoring.
- **Dashboard dist to .gitignore** - build output directory added to .gitignore to reduce repository size.

### Verification

- `go vet ./...` passes.
- `go test -p 1 ./...` passes.
- `npm run build` passes in `web/dashboard`.

## [0.0.50] - 2026-04-05

### Performance

- **Context acquire/release** - optimized with manual hex encoding, 53% faster (239ns → 76ns), 49% less memory (283B → 144B), 67% fewer allocations (9 → 3).
- **Cache key generation** - added strings.Builder pooling and eliminated strings.Join allocation, 43% faster (~3500ns → 1964ns), 1 fewer allocation.
- **Request ID generation** - replaced fmt.Sprintf with manual hex encoding in middleware.
- **ETag generation** - replaced fmt.Sprintf with manual hex encoding for static file ETag and dynamic response ETag.
- **Traceparent header** - replaced fmt.Sprintf + hex.EncodeToString with fixed-size buffer and manual hex encoding in proxy handler.

### Verification

- `go vet ./...` passes.
- `go test -p 1 ./...` passes.
- `npx tsc --noEmit` passes in `web/dashboard`.
- `npm run build` passes in `web/dashboard`.
- Benchmark suite: ContextAcquireRelease 76ns/op, CacheKeyGenerate 1964ns/op, VHostLookup 36ns/op.

## [0.0.49] - 2026-04-05

### Features

- **DNS-01 ACME challenge support** - ACME client now supports DNS-01 challenge for automated certificate issuance via Cloudflare, DigitalOcean, Hetzner, and Route53 DNS providers.
- **htaccess IfModule module checking** - IfModule directives now properly check whether the referenced Apache module is loaded, instead of always processing the block contents.
- **htaccess RewriteBase support** - mod_rewrite RewriteBase directive is now parsed and stored for use in rewrite rule processing.
- **Cache PurgeByTag across all layers** - PurgeByTag now correctly purges entries from all cache layers (L1 memory, L2 disk, L3 Redis).
- **Backup cron scheduling** - backups can now be scheduled using cron expressions (e.g., `0 2 * * *` for daily at 2 AM) in addition to simple interval.
- **htpasswd file BasicAuth** - BasicAuth middleware now supports reading credentials from htpasswd files with APR1-MD5, bcrypt, SHA1, and MD5 password formats.
- **Security headers** - additional security headers added: ReferrerPolicy, StrictTransportSecurity (HSTS), X-Content-Type-Options, XSS-Protection.
- **Mirror MaxBodyBytes configurable** - proxy mirror MaxBodyBytes is now configurable per domain instead of hardcoded 2MB.
- **System stats bar on all pages** - every dashboard page now shows real-time CPU, RAM, Disk usage, Load Average, and Uptime in a fixed top bar.

### Fixes

- **Self-update restart** - fixed self-update restart mechanism that was incorrectly sending SIGHUP to itself instead of using proper systemctl restart or re-exec.
- **TestHandleRequestBlockedUnknownHostHTTPS** - fixed pre-existing test failure after commit 6775695 changed unknown host handling to use fallback domain.
- **TLS self-signed certificate improvements** - self-signed certificates now use configurable validity period (default 24h) and random serial numbers.

### Verification

- `go vet ./...` passes.
- `go test -p 1 ./...` passes.
- `npx tsc --noEmit` passes in `web/dashboard`.
- `npm run build` passes in `web/dashboard`.
- Test coverage: 83% → 86.1%

## [0.0.48] - 2026-04-04

### Fixes

- **Server IP appearing under Unknown Domains** - requests to the server's own IP address (e.g., health checks) are now correctly served by the fallback domain instead of being recorded as unknown domains. Previously, when no exact or wildcard match existed, the server rejected requests before checking if a fallback domain was available.
- **ETag generation for dynamic cached responses** - added SHA256-based weak ETag for non-ESI cached responses that don't have one, enabling conditional request support (If-None-Match) for dynamic content.
- **ReDoS prevention in WAF SQL injection regex** - fixed catastrophic backtracking in `(--|;)\s*` pattern that could cause exponential slowdown on crafted input like `;        ;`. Changed to `\s+` (requires at least one whitespace).
- **RFC compliance improvements** - CORS preflight requests now correctly validate `Access-Control-Request-Method` header per spec; requests without this header are passed through instead of incorrectly returning 200. Also added proper 417 Expectation Failed response for clients sending `Expect: 100-continue` header.
- **Rate limiter memory leak** - added TTL-based eviction for stale entries in the locationLimiters sync.Map, preventing unbounded growth from infrequently-accessed rate limit keys.
- **Backup schedule UI fix** - backup settings from config.yaml are now correctly displayed in the Admin UI. Fixed ScheduleDetail struct and simplifyInterval to return human-readable formats (7d, 24h) instead of Go duration strings.
- **PHP restart tracking** - PHP services now correctly show as "running" after server restart. Fixed RegisterExistingDomain to set sentinel proc for unix socket addresses and added nil-proc fallback.
- **UFW IPv6 display** - firewall page no longer shows invalid duplicate IPv6 entries. Added V6 bool field to Rule struct and properly detects and strips `(v6)` suffix from UFW rules.
- **WriteHeader double-call prevention** - fixed TransformWriter that could call WriteHeader twice on same status code.
- **Partial proxy body fix** - upstream errors in buffered mode no longer result in partial response body being sent to client.
- **Silent Error() failure** - Error() calls after headers are written now correctly return early instead of silently failing.
- **Net.SplitHostPort errors** - improved handling of malformed RemoteAddr values with graceful fallback.
- **Mutex race in backup callback** - acquire mutex before reading onBackup callback to prevent race condition.

### Features

- **WAF bypass paths UI** - Security page in dashboard now allows configuring WAF bypass paths per domain, allowing certain paths to skip WAF inspection entirely.
- **WAF overhaul** - major improvements to Web Application Firewall to prevent false positives on legitimate traffic:
  - Content-Type aware body scanning: skips JSON, multipart, XML payloads
  - Removed `<script>` tag check from body patterns (legitimate in CMS editors, email templates)
  - Removed `sleep()` and `benchmark()` checks from body patterns (legitimate in code playgrounds)
  - Added per-domain WAF bypass paths support
- **Database Explorer** - native phpMyAdmin-like database exploration interface in dashboard.
- **Cloudflare integration page** - full DNS management interface for Cloudflare.
- **Cron preset options** - backup scheduling now supports preset intervals (hourly, daily, weekly).
- **Redis L3 cache support** - optional Redis cache layer behind L1 memory and L2 disk cache.
- **CI/CD improvements** - comprehensive GitHub Actions workflow with test coverage tracking.

### Verification

- `go vet ./...` passes.
- `go test -p 1 ./...` passes (note: some tests have goroutine cleanup issues with webhook workers that are pre-existing and unrelated to code changes).
- `npx tsc --noEmit` passes in `web/dashboard`.

## [0.0.38] - 2026-03-28

### Features

- **38 dashboard pages, 205+ API endpoints** - major dashboard expansion including Database Explorer, Cloudflare integration, WAF bypass paths UI.
- **Redis L3 cache** - optional Redis caching layer for distributed caching scenarios.
- **Comprehensive test coverage** - coverage improved from 78.4% to 83.8%.

### Fixes

- **Crash-proof concurrent access** - hot-path safety improvements throughout the codebase.
- **All GitHub issues resolved** - issues #3, #4, #5, #6, #7 fixed.

## [0.0.35-rc.1] - 2026-03-30

### Features

- **Domain + route Basic Auth management** - dashboard and API now support manageable Basic Auth at site root and per-location rules with multi-user credentials.

### Fixes

- **Location auth enforcement consistency** - location-matched requests now correctly apply effective Basic Auth policy (domain default or location override) before route dispatch.
- **Domain update merge semantics** - `PUT /api/v1/domains/{host}` now correctly handles `basic_auth`, `aliases`, and `locations` in merge mode when fields are intentionally cleared or disabled.
- **Dashboard modal/state stability** - removed several effect-driven state synchronization loops in Pin modal, deploy wizard, topology graph, and routes editor flows to prevent cascading render risks.
- **Error handling hardening** - analytics reset and migration load actions now surface API failures instead of silently swallowing exceptions.
- **Frontend lint/type hygiene** - cleared dashboard lint backlog and tightened several `unknown`/typed interfaces to reduce unsafe dynamic typing paths.

### Verification

- `go vet ./...` passes.
- `go test -p 1 ./...` passes.
- `npm run lint` passes in `web/dashboard`.
- `npm run build` passes in `web/dashboard`.

## [0.0.34] - 2026-03-30

### Fixes

- **Release workflow publish context** - `GH_REPO` is now explicitly set for `gh` CLI release steps, fixing the `fatal: not a git repository` failure path in tag-triggered release jobs.

### Improvements

- **Release pipeline validation** - release process verified end-to-end with the updated GitHub Actions stack and Node 24 runtime enforcement.
- **Dependency posture check** - direct Go dependencies and dashboard/docs frontend dependencies re-checked; project remains on latest compatible versions.

### Verification

- `go test -p 1 ./...` passes.
- `npm run build` passes in `web/dashboard`.
- `npm run build` passes in `docs/site`.

## [0.0.33] - 2026-03-30

### Improvements

- **GitHub Actions modernization** - CI, Docs and Release workflows upgraded to latest action majors (`checkout@v6`, `setup-node@v6`, `upload-artifact@v7`, `download-artifact@v8`, `deploy-pages@v5`).
- **Node runtime hardening** - workflows now force JavaScript actions to run on Node 24 to avoid deprecated Node 20 execution paths.
- **Release pipeline robustness** - release publishing migrated to `gh` CLI upload flow to avoid Node action runtime drift and duplicate-tag edge behavior.
- **Docs deploy reliability** - Pages artifact packaging now matches deploy-pages requirements with manual `tar` artifact upload.
- **Docs/README data refresh** - dashboard/API/package metrics refreshed to current values across docs site hero and README sections.

### Security

- **Frontend dependency refresh** - dashboard/docs dependencies updated to latest compatible versions; `npm audit` clean on both projects.

### Verification

- `go test -p 1 ./...` passes.
- CI runs: `23721368566`, `23721418078`, `23721490599` passed.
- Docs deploy runs: `23721368569`, `23721493076` passed.

## [0.0.32] - 2026-03-30

### Fixes

- **Terminal handler nil logger panic** - Linux terminal handler now guards logger calls, preventing nil-pointer panic paths when logger is not initialized.
- **CI stability** - `internal/admin` terminal handler test no longer fails in Linux CI due to the nil logger panic path.

### Verification

- `go test -p 1 ./...` passes.
- GitHub Actions CI run `23718438056` passed.

## [0.0.31] - 2026-03-29

### Fixes

- **PHP shutdown/restart race** - `StopDomain` / shutdown flows no longer trigger unintended auto-restart of domain PHP workers.
- **PHP process stop safety** - stale process entries are now handled safely in `StopFPM` and `StopAll` without nil dereference risk.
- **Conflict detection robustness** - conflict probing now supports `systemctl is-active` fallback and Apache service variants (`apache2` / `httpd`).
- **Install reliability** - CLI install flow now returns errors for failed `mkdir`, `systemctl`, and symlink/stat operations instead of silently continuing.
- **FastCGI response handling** - body read path simplified and hardened; empty/WSOD detection remains intact while removing dead/always-true branches.

### Improvements

- **Go 1.26 compatibility cleanup** - ACME JWS key-byte handling migrated away from deprecated ECDSA public key coordinate field usage in runtime and tests.
- **Static analysis hygiene** - non-test staticcheck warnings cleaned up across core packages.
- **Windows test portability** - test-only `echo` helper bootstrap added for CLI/PHP manager test suites where `echo` is not available as an executable.

### Verification

- `go test -p 1 ./...` passes on this branch after changes.

## [0.0.26] - 2026-03-28

### Major Features

- **ESI (Edge Side Includes)** — Fragment caching for HTML responses. Each `<esi:include>` has its own cache key and TTL. Enable per-domain: `cache.esi: true`
- **App Process Manager** — Node.js/Python/Ruby/Go process management. Auto-detect start commands, per-domain ports, crash auto-restart. Domain type: `app`
- **Web Terminal** — Browser-based shell via WebSocket-to-PTY (Linux). No external dependencies.
- **GeoIP Blocking** — Country-based access control per domain (block/allow ISO codes)
- **Resource Limits** — Per-domain CPU/memory/PID limits via Linux cgroups v2
- **SMTP Relay** — Transactional email via SMTP with TLS/STARTTLS

### Dashboard (38 pages)

- **Applications** page — List, start, stop, restart app processes with runtime badges
- **Terminal** page — Browser shell with Ctrl+C/D/L shortcuts
- **Domain Detail** — GeoIP block/allow + Resource Limits fields in Security tab

### Fixes

- **Auth middleware stale closure** — Config changes (API key, multi-user toggle) now take effect without restart
- **Auth token query param bug** — `?token=` was deleted before legacy auth could use it when multi-user was enabled. Fixed WebSocket terminal and SSE endpoints.
- **GeoIP external call** — Async lookup, no longer blocks request path
- **WebSocket DoS** — 64KB max frame size, close frame echo per RFC 6455
- **App manager race** — Double-check stopCh prevents zombie restarts
- **App cleanup on reload** — Removed domains' app processes are stopped
- **GeoIP chains on reload** — Rebuilt on config change (was missing)
- **CORS** — Added `X-Pin-Code` to allowed headers

### Improvements

- `logger.SafeGo()` panic recovery for critical goroutines
- PHP dropdown simplified, PHP Config batch save
- TypeScript: removed `as any` cast, proper `DomainDetail.ip` typing
- CLAUDE.md updated: 50 packages, 38 pages, 190+ API endpoints

## [0.0.25] - 2026-03-27

### Fixes

- **Backup restore** — Fixed DB dump not being restored from new backups. `CreateBackup` wrote `databases/native-all-databases.sql` but `RestoreBackup` only matched the old `databases/all-databases.sql` path. Now recognizes both for backward compatibility.

### Improvements

- **Global pin modal** — Auto-prompts on ANY page when API returns `pin_required`, not just specific pages
- **Dead code cleanup** — Removed unused Go code (vars, methods, test helpers), 18 unused dashboard API exports, and 3 unreferenced asset files. Net -84 lines.

## [0.0.24] - 2026-03-27

### Security

- **SQL injection protection** — Parameterized queries and input validation hardened across database operations
- **Pin bypass prevention** — Strengthened pin code verification for destructive operations
- **SFTP symlink guard** — Prevents symlink-based path traversal in SFTP chroot jails
- **PHP header blocking** — Blocks sensitive PHP headers from leaking to clients

## [0.0.23] - 2026-03-27

### Security

- **Pin code protection** — Destructive operations (delete domain, drop DB, firewall changes) now require a pin code. Auto-generated on init, shown in setup output.
- **PHP isolation** — Enforces `open_basedir` per-request via `PHP_ADMIN_VALUE`, sandboxing each domain
- **Firewall hardening** — Blocks `any` deny rules, protects ports 80/443/22/admin, validates domain root paths

## [0.0.22] - 2026-03-27

### New Features

- **update.sh** — One-line update script: detects version, downloads latest, replaces binary, auto-restarts systemd service
- **CLI auto-loads .env** — `uwas php list`, `uwas status` etc. now work without manually setting UWAS_ADMIN_KEY (auto-loads from `~/.uwas/.env`)

### Fixes

- **WP-CLI + PHP 8.5** — Separated stdout/stderr so deprecation warnings don't corrupt JSON output. Users, plugins, themes now display correctly.
- **Blocked unknown domains** — Now persisted to `blocked-hosts.txt`, survive restart
- **Settings save** — 15+ missing config keys added (multi-user auth, ACME, cache, backup, alerting)
- **PHP domains missing from PHP page** — `RegisterExistingDomain()` ensures config-based PHP domains appear after restart
- **PHP Config dropdown** — Deduplicated versions, input validation, preset descriptions
- **WordPress install** — Docker DB containers shown in host dropdown
- **Clone/staging** — Auto-creates domain config after cloning
- **Doctor** — Detects and auto-stops Apache/Nginx conflicts
- **Services** — PHP 8.1-8.5 FPM, Docker added; Redis/Postfix/Dovecot removed

### Improvements

- **Settings layout** — Toggles in highlighted row, fields in 2-column grid
- **About page** — Version, license, GitHub links, tech stack
- **Docker DB management** — Create/list/drop databases inside containers, export/import SQL
- **Backup includes Docker DBs** — All running Docker MySQL/MariaDB dumped in backup archive

## [0.0.20] - 2026-03-27

### New Features

- **Docker DB management** — Create/list/drop databases inside Docker containers via `docker exec`. Export (mysqldump) and import SQL. Dashboard UI with expandable container panels.
- **Backup includes Docker DBs** — Backup archives now dump all running Docker MySQL/MariaDB containers alongside native DB.
- **Self-update auto-restart** — `UpdateAndRestart()` downloads, replaces binary, and restarts via `systemctl restart uwas` or `syscall.Exec`.
- **Doctor: Apache/Nginx conflict detection** — Detects running Apache/Nginx, auto-stops with `--fix`.

### Fixes

- **Settings save fixed** — 15+ missing config keys added (multi-user auth, ACME on-demand, cache, backup S3/SFTP, alerting email, MCP).
- **PHP domains missing from PHP page** — `autoAssignPHP` skipped domains with working FPM address but never registered them in phpMgr. Now uses `RegisterExistingDomain()`.
- **PHP Config: version dropdown deduplicated** — No more 3x same version. Input validation added.
- **WordPress install: Docker DB in dropdown** — Shows Docker containers as database host options.
- **Clone/staging: auto-creates domain config** — Was only copying files + DB, no domain record.
- **Packages link fixed** — Uses React Router `Link` instead of `<a href>`.

### Improvements

- **Services page** — PHP 8.1-8.5 FPM individually listed, Docker added, Redis/Memcached/Postfix/Dovecot removed.
- **Settings tabs** — General, Security, Performance, Integrations.
- **Settings help text** — S3/SFTP/Telegram/Slack/HTTP3 setup guides.
- **About page** — Version, license, GitHub links, tech stack.

## [0.0.19] - 2026-03-27

### New Features

- **About page** — System > About: version info, GitHub/website links, AGPL-3.0 + commercial license cards, "What UWAS Replaces" table, tech stack
- **Docker installable** — Docker added to Packages page (`docker.io`). Database page shows install prompt when Docker is missing.
- **Clone auto-domain** — Clone/staging now auto-creates domain config (was only copying files + DB, no domain record)

### Improvements

- **Settings help text** — S3 endpoint examples (AWS/Wasabi/MinIO), SFTP descriptions, Telegram bot setup guide (@BotFather), Slack webhook instructions, HTTP/3 QUIC explanation, email SMTP fields added

## [0.0.17] - 2026-03-27

### Fixes

- **PHP assignment now works properly:**
  - Domain creation: user's FPM address from form is respected (was always ignored)
  - Auto-assign: prefers running FPM over CGI (was picking first detected)
  - PHP page assign: FPM address now persisted to domain config file (was lost on restart)
  - PHP page assign: auto-starts PHP process after assignment
  - Audit log records PHP assignments
- **WordPress install dropdown**: selects first domain WITHOUT WordPress (was selecting first PHP domain regardless)
- **Cache: PHP domains only cache static assets** (CSS/JS/images) — PHP output never cached
- **PHP status: CGI no longer shows FPM socket** — only FPM SAPI shows system socket

## [0.0.16] - 2026-03-27

### Fixes

- **PHP status: CGI no longer shows FPM socket** — Dashboard was showing the FPM socket for all PHP binaries (CGI, FPM, CLI). Now only FPM SAPI shows the system socket; CGI shows its own TCP port.

## [0.0.15] - 2026-03-26

### Critical Fix

- **POST blank pages FIXED (root cause)** — Compression middleware was swallowing redirect status codes. When PHP returned `302 + Location`, `WriteHeader(302)` was buffered but never flushed to the real ResponseWriter. Go defaulted to 200 → browser got `200 + Location + empty body` → didn't follow redirect → white page. Now redirects (3xx), 204, 304 are flushed immediately without compression buffering.
- **Content-Length stripped from PHP** — PHP's Content-Length conflicted with gzip compression. Now removed before forwarding; Go recalculates.

## [0.0.14] - 2026-03-26

### Critical Fix

- **`/wp-admin/` showing homepage instead of dashboard** — Domain config had `index_files: [index.html, index.htm]` without `index.php`. When resolving `/wp-admin/` directory, UWAS looked for `index.html` inside wp-admin (doesn't exist), fell back to root `/index.php` (homepage). Now PHP domains always include `index.php` in index file list regardless of config, and merge `php.index_files` into the lookup.

## [0.0.13] - 2026-03-26

### Critical Fix

- **WordPress redirects fixed** — PHP-FPM sends `Location` header without `Status: 302`. UWAS was forwarding as `200 + Location` — browsers don't follow redirects on 200, so pages appeared blank after form submissions (POST). Now auto-upgrades to 302 when Location header is present with status 200.

### Improvements

- **WSOD body detection** — Detects PHP responses with headers but empty body (fatal error with `display_errors=Off`). Returns 500 with diagnostic instead of blank page. Only triggers for GET/POST text/html 200 without Location header.
- **FastCGI handler cleanup** — Removed duplicate stderr read, extracted X-Accel-Redirect into helper, body read via `io.ReadAll` for reliable WSOD detection.
- **htaccess skip for .php** — Direct `.php` file requests now skip htaccess rewrite processing (unnecessary overhead, potential interference).

## [0.0.12] - 2026-03-26

### Critical Fix

- **PHP blank pages fixed** — `resp.Stdout()` was called AFTER `ParseHTTP()` which consumes the buffer. Every PHP response was incorrectly flagged as empty, returning 500 instead of the actual page. WordPress, wp-admin, POST forms — all affected. Root cause identified and fixed with single-line change.

### Security (8 fixes from full code audit)

- **SQL injection** — `escapeSQL()` was escaping in wrong order (quotes before backslashes), allowing quote escape. Fixed + added null byte stripping.
- **Command injection** — `/api/v1/cron/execute` had no permission check. Now admin-only.
- **Info disclosure** — PHP stderr was leaked to clients in HTML comments. Now server-side only.
- **Login brute-force** — Login endpoint bypassed rate limiter. Now rate-limited.
- **TLS data race** — `UpdateDomains()` had no mutex. Added `sync.RWMutex`.
- **wp-config.php** — Written with 0644 (world-readable). Now 0600.
- **Service injection** — `systemctl` commands accepted arbitrary names. Now allowlist-checked via `IsKnownService()`.
- **Session token leak** — Query param tokens stripped from URL after auth (prevents log/referer leakage).

### Security (4 additional hardening)

- **TOTP 2FA** — `pendingTOTP` was single global string. Now per-user map (concurrent setup safe).
- **SFTP passwords** — All domains shared the API key. Now per-domain via HMAC-SHA256 derivation.
- **Admin API TLS** — New `admin.tls_cert` / `admin.tls_key` config for encrypted admin traffic.
- **Admin timeout** — Write timeout increased from 10s to 5min (SSE, DB export, backup).

### Improvements

- **localhost:80 removed** — No longer created on init. Was dangerous (deleting it wiped `/var/www`).
- **localhost delete blocked** — Backend returns 403, dashboard hides delete button for localhost/127.0.0.1.
- **Monitor log noise** — Internal health checks (30s interval) no longer pollute access logs.
- **Self-update** — Falls back to `/releases` API when `/releases/latest` returns 404 (pre-releases).

### Tests

- WordPress URL resolution tests: `/wp-admin/`, `/wp-admin/post.php`, POST, pretty permalinks — all verified working.

## [0.0.11] - 2026-03-26

### Improvements

- **Install script** — Rewritten `install.sh` with proper binary name matching, version fallback, binary verification, colored output, and post-install guidance (systemd, dashboard URL)
- **README** — Added one-line install command (`curl | sh`), systemd install instructions, dashboard URL, build-from-source section
- **Docs site** — Updated subtitle (35 pages, hosting panel + cPanel replacement), feature descriptions

### Install

```bash
curl -fsSL https://raw.githubusercontent.com/uwaserver/uwas/main/install.sh | sh
```

Downloads the latest release binary for your platform (linux/darwin, amd64/arm64), verifies it runs, installs to `/usr/local/bin/uwas`.

## [0.0.10] - 2026-03-26

### Bug Fixes

- **SFTP path traversal (security)** — Reject all paths containing `..` before processing, prevents chroot escape on Linux
- **CI green** — Fixed SFTP, admin, and read-only dir tests for Linux; skipped CLI tests (signal handling); increased timeout to 600s
- **CI workflows** — Upgraded to `actions/checkout@v5`, `setup-go@v6`, `setup-node@v5` (Node.js 20 deprecation fix)
- **Stats updated** — README, CLAUDE.md, docs site: 35 pages, 170+ API endpoints, 45 test packages

## [0.0.9] - 2026-03-26

### Bug Fixes

- **WordPress admin routing** — Skip `.htaccess` rewrite for `/wp-admin`, `/wp-includes`, `/wp-content` paths (was rewriting admin URLs to front-page `index.php`)
- **wp-cli HTTP_HOST error** — Auto-detect site URL from directory structure and pass `--url` flag to wp-cli (fixes "Undefined array key HTTP_HOST" warning during core updates)
- **Cache bypass for .php** — `.php` requests are never cached (PHP output is always dynamic)
- **Domain deletion safety** — Protected paths expanded (`/var/www`, `/var/lib`, `/var/log`, etc.), require 4+ path components to delete parent, never delete webRoot itself
- **Default domain protection** — `localhost`, `localhost:80`, `127.0.0.1` cannot be deleted
- **Domain detail iframe removed** — Replaced non-functional iframe with clean URL bar + Visit/WP Admin buttons

## [0.0.8] - 2026-03-26

### Highlights

**Unified domain management, WordPress security hardening, installation task queue, PHP white-screen fix.** Every domain now has its own detail page with live preview, security toggles, WordPress management, analytics, and file access — all in one place.

### New Features

- **Domain Detail page** (`/domains/:host`) — unified per-domain management with 6 tabs:
  - **Overview**: live screenshot preview, quick stats (requests, bandwidth, errors, disk), 24h traffic chart, config info
  - **Settings**: domain config display with links to editor
  - **Security**: WAF toggle, hotlink protection, rate limiting, blocked paths, IP blacklist — all editable and saveable
  - **WordPress**: version info, plugin/theme management, security hardening, user/password management, DB optimization
  - **Analytics**: page views, unique IPs, top pages, top referrers
  - **Files**: disk usage, link to file manager
- **WordPress security hardening** — toggle XML-RPC, file editor, SSL admin, WP-Cron, directory listing; "Harden All" one-click
- **WordPress user management** — list users with roles, change any user's password from dashboard
- **WordPress DB optimization** — clean revisions, spam, trash, expired transients, optimize tables
- **Global install task manager** (`internal/install/`) — serialized apt/dpkg queue prevents concurrent lock conflicts
- **Installation progress persistence** — navigate away and back, install progress resumes automatically
- **Security page upgrade** — two tabs: Threat Monitor (stats + blocked requests) and Per-Domain Rules (WAF/rate-limit/IP ACL toggles)

### Bug Fixes

- **PHP white screen of death** — empty FastCGI response now returns 500 with diagnostic message instead of silent blank 200
- **WordPress plugin install failure** — `wp-content/upgrade` and `uploads` directories now created during install and fix-permissions
- **Cache bypass** — wp-admin, wp-login, wp-cron, wp-json, xmlrpc paths + woocommerce/comment_author cookies now bypass cache

### API Endpoints (new)

- `GET /api/v1/tasks` — list all active/recent installation tasks
- `GET /api/v1/tasks/{id}` — get task status and output
- `GET /api/v1/wordpress/sites/{domain}/users` — list WordPress users
- `POST /api/v1/wordpress/sites/{domain}/change-password` — change WP user password
- `GET /api/v1/wordpress/sites/{domain}/security` — get WP security status
- `POST /api/v1/wordpress/sites/{domain}/harden` — apply security hardening
- `POST /api/v1/wordpress/sites/{domain}/optimize-db` — clean and optimize database

### Stats

- **45 test packages**, all passing, 0 failures
- **9 new install manager tests** (serial execution, task lifecycle, concurrency safety)

## [0.0.7] - 2026-03-26

### Highlights

**Dual licensing, massive test coverage push, doctor & database hardening.** 50,000+ lines of new tests across 30+ packages, AGPL-3.0 + commercial dual license, MariaDB auto-repair, and multi-user auth improvements.

### License

- **Dual licensing** — AGPL-3.0 for open-source community use, commercial license available for enterprise/proprietary use
- Updated LICENSE, README, CONTRIBUTING, and docs site footer

### New Features

- **DB repair & force uninstall** — `POST /api/v1/database/repair`, `DELETE /api/v1/database/uninstall?force=true` for broken MariaDB installations
- **Doctor: MariaDB auto-repair** — Detects and fixes corrupt InnoDB tablespace, broken permissions, stale PID files
- **Doctor: system checks** — Memory usage, open file descriptors, NTP clock sync diagnostics
- **Login upgrade** — Multi-user auth flow with role-aware session handling
- **Settings: notification channels** — Configure webhook/Slack/Telegram/email notification destinations from dashboard

### Test Coverage (~50,000 new lines)

New test files and major expansions across 30+ packages:

- `internal/admin` — 3,528 lines: API endpoint coverage (domains, PHP, cache, backup, cron, firewall)
- `internal/cli` — 4,464 lines: all CLI commands (install, stop, conflicts, pidcheck, user)
- `internal/sftpserver` — 3,435 lines: SFTP protocol, chroot, permissions, SSH key auth
- `internal/phpmanager` — 3,038 lines: PHP detect, install, start/stop, config, auto-restart
- `internal/wordpress` — 2,646 lines: install, permissions, mu-plugin, wp-config generation
- `internal/server` — 5,149 lines: HTTP/HTTPS dispatch, middleware chain, graceful shutdown
- `internal/migrate` — 2,339 lines: clone, site migration, SSH transfer
- `internal/siteuser` — 1,118 lines: user CRUD, chroot setup, SSH key management
- `internal/auth` — 1,549 lines: RBAC, sessions, API keys, TOTP 2FA, persistence
- `internal/cronjob` — 1,449 lines: cron CRUD, execution, monitoring, failure alerts
- `internal/database` — 1,807 lines: MySQL/MariaDB management + Docker container tests
- `internal/doctor` — 1,559 lines: diagnostics, auto-fix, PHP/permissions/config/ports
- `internal/backup` — 1,357 lines: local/S3/SFTP backup + restore
- `internal/bandwidth` — 1,605 lines: throttle/block, daily/monthly limits
- `internal/tls` — 2,275 lines: SNI routing, ACME client, JWS signing, cert storage
- `internal/dnsmanager` — 2,261 lines: Cloudflare, DigitalOcean, Hetzner, Route53
- `internal/selfupdate` — 712 lines: GitHub release check, download, binary swap
- `internal/serverip` — 984 lines: interface detection, public IP lookup
- `internal/firewall` — 601 lines: UFW rule management
- `internal/notify` — 490 lines: webhook, Slack, Telegram, email channels
- `internal/handler/*` — 1,714 lines: FastCGI, proxy, static handler edge cases
- `internal/middleware` — 848 lines: chain composition, WAF, image optimization
- `internal/router` — 937 lines: vhost routing, unknown domain tracking
- `internal/config` — 829 lines: YAML parsing, Duration/ByteSize types, validation
- `internal/webhook` — 456 lines: event delivery, HMAC signing, retry
- `pkg/fastcgi` — 436 lines: binary protocol, connection pool
- `pkg/htaccess` — 393 lines: parser directives, IfModule, RewriteCond

### Bug Fixes

- **CLI install** — Fixed error handling in package installation flow
- **CLI stop** — Improved PID file cleanup on graceful shutdown
- **CLI conflicts** — Better port conflict detection and reporting
- **Cronjob monitor** — Fixed race condition in concurrent job execution tracking
- **Database manager** — Hardened connection error handling, added timeout for stale connections
- **DNS checker** — Fixed edge case in CNAME chain resolution
- **DNS providers** — Consistent error handling across Cloudflare, DigitalOcean, Hetzner, Route53
- **Doctor** — Expanded diagnostic checks with actionable fix suggestions
- **File manager** — Path traversal guard strengthened for symlink edge cases
- **Firewall** — Improved UFW rule parsing for complex CIDR ranges
- **Image optimization** — Added nil check for missing Accept header
- **Migrate/clone** — Fixed SSH key auth and database dump error propagation
- **Notify channels** — Fixed timeout handling for slow webhook endpoints
- **PHP manager** — Improved version detection and FPM socket path resolution
- **Self-update** — Fixed GitHub API rate limit handling and checksum verification
- **Server IP** — Improved interface filtering for virtual/docker bridges
- **Services** — Better systemd unit file parsing and status detection
- **Site user** — Fixed SSH key format validation and chroot directory permissions
- **TLS/ACME** — Improved retry logic for DNS-01 challenge propagation
- **WordPress** — Fixed wp-config.php generation for non-standard DB prefixes

### Stats

- **44 test packages**, all passing, 0 failures
- **50,000+** new lines of test code
- **30+** packages with expanded coverage
- **83 files** changed in this release

## [0.0.6] - 2026-03-23

### Highlights

**Dead code audit & feature activation.** 2,500+ lines of dead code removed, 9 config-backed features activated, 8 bugs fixed, daemon mode added.

### New Features

- **Daemon mode** — `uwas serve -d` starts server as background process (cross-platform)
- **Per-domain CORS** — `cors.enabled`, allowed origins/methods/headers per domain
- **Per-domain BasicAuth** — `basic_auth.enabled`, username/password per domain
- **Per-domain IP ACL** — `security.ip_whitelist` / `ip_blacklist` now enforced
- **Per-domain header transforms** — `headers.response_add` / `request_add` applied per request
- **Circuit breaker** — `proxy.circuit_breaker.threshold` trips after N failures, auto-recovery
- **Canary routing** — `proxy.canary.enabled` routes % of traffic to canary upstreams
- **Image optimization** — `image_optimization.enabled` serves pre-converted WebP/AVIF
- **Custom error pages** — `error_pages.404: /404.html` serves per-domain error pages
- **MCP API endpoints** — `GET /api/v1/mcp/tools`, `POST /api/v1/mcp/call` in admin API
- **Domain edit** — Edit button in dashboard domain table, pre-filled form with updateDomain API
- **PHP dropdown** — FPM address field auto-detects installed PHP versions

### Bug Fixes

- **Proxy retry bug** — `netErr.Timeout() || true` always retried; fixed to `return true` for all net.Error
- **Config editor crash** — Raw config API returned YAML but frontend expected JSON; wrapped in `{"content": "..."}`
- **Rate limiter blocked dashboard** — Public endpoints (health, dashboard) now exempt from rate limiting
- **SSE auth** — EventSource token via query param support added (browser can't set headers)
- **Dashboard toFixed crash** — Latency cards null-safe when stats fields undefined
- **Response header timing** — Per-domain headers set before handler dispatch, not deferred
- **E2e test locators** — Strict mode violations fixed with exact text matchers

### Dead Code Removed (~2,500 LOC)

- `internal/server/upgrade.go` — Unused GracefulRestart/DrainAndWait (duplicated shutdown logic)
- `internal/logger/accesslog.go` — Unused AccessLogger subsystem (server uses slog middleware)
- Old nginx migration code in `internal/cli/migrate.go` (superseded by `internal/migrate/`)
- Alerter methods DomainDown/CertExpiry/RecordRateLimit (implemented but never wired)
- Handler Name()/Description()/CanHandle() methods (never called from server dispatch)
- Analytics Record() wrapper, requestsInWindow, ActiveDomains() (test-only)
- Dead constants: StatusBypass, shardCount, ToolList struct
- Redundant CustomHeaders middleware (HeaderTransform already covers it)
- Frontend: unused PHP API functions, phantom react-router-dom dependency

### Improvements

- `go mod tidy` fixed mislabeled indirect deps (brotli, quic-go, x/crypto)
- All API wrapper functions exported in frontend api.ts (monitor, alerts, MCP, cache stats)
- Cache page uses api.ts wrapper instead of direct fetch
- CacheStatsData interface moved to shared api.ts
- CLAUDE.md updated with per-domain middleware docs, coverage stats
- 21+ new backend tests, 29 e2e tests passing

### Stats

- **1,718 tests** across 27 packages, 88.6% coverage
- **29/29 Playwright e2e tests** passing
- **0 JS errors** in dashboard
- **0 TODO/FIXME** remaining in codebase

## [0.0.5] - 2026-03-22

### Highlights

**1,728 tests, 93%+ average coverage, 0 failures.** 27 packages, 17k lines of Go source.

### New Features

- **Backup/Restore** — Local filesystem, S3 (AWS SigV4), SFTP over SSH; scheduled backups with auto-pruning
- **HTTP/3 (QUIC)** — via quic-go with Alt-Svc header advertisement
- **WebSocket Proxy** — TCP hijack + bidirectional tunneling for real-time apps
- **Audit Logging** — 500-entry ring buffer tracking all admin actions with timestamps/IPs
- **Latency Metrics** — p50/p95/p99/max percentiles via Prometheus endpoint
- **Slow Request Logging** — WARN-level log for requests exceeding configurable threshold
- **Per-domain PHP** — Multiple PHP versions per domain, auto-port assignment, php.ini editing
- **Nginx/Apache Migration** — `uwas migrate nginx/apache <file>` converts configs to UWAS YAML
- **W3C Trace Context** — traceparent header propagation through reverse proxy
- **Per-handler Metrics** — uwas_requests_by_handler{handler=static/php/proxy/redirect}
- **Connection Limiter** — Reject with 503 when at max capacity
- **System Info API** — GET /api/v1/system (Go version, OS, arch, CPUs, goroutines, memory)

### Dashboard (15 pages)

- **Backups page** — Create/restore/delete with provider selection + scheduling
- **Audit Log page** — Filterable action history with color-coded badges
- **Analytics enhanced** — Referrer tracking + user agent breakdown charts
- **Dashboard** — Latency cards (p50/p95/p99), dual-axis chart with p95 line
- **Settings** — Real system info (Go version, CPUs, goroutines, memory, GC)
- **Config Editor** — In-memory fallback when domain files don't exist

### Security Hardening

- **Admin API rate limiting** — 10 failed auths in 1 minute triggers 5-minute IP block
- **Config validation expanded** — 300+ lines: CIDRs, ports, URLs, regexes, enums, file existence
- **Slowloris protection** — ReadHeaderTimeout (10s), MaxHeaderBytes (1MB)
- **Graceful shutdown** — Connection draining with configurable grace period

### CLI / UX

- **First-run experience** — Auto-config creation in ~/.uwas/, interactive port setup
- **Startup banner** — ASCII art, version, listeners, features, dashboard URL
- **Zero-arg launch** — `uwas` without arguments auto-starts server

### Bug Fixes

- Domain create: SSL, proxy, redirect, WAF payload structures fixed
- Config editor: domain raw GET falls back to in-memory config
- Domain file path: port in hostnames sanitized for filesystem
- Analytics page crash: match actual API response format
- PHP-FPM HTTP_HOST: set from r.Host, not r.Header
- Cache bypass: wp-admin/wp-login session cookie detection

---

## [0.0.4] - 2026-03-22

### Highlights

UWAS is a feature-complete, production-ready web server that replaces
Apache + Nginx + Varnish + Caddy with a single 13MB Go binary.

**818 tests, 88% coverage, 0 failures.** WordPress 6.9.4 verified.

### Server

- Auto HTTPS with Let's Encrypt ACME client
- Built-in L1 memory + L2 disk cache with grace mode
- PHP-FPM via FastCGI with .htaccess support
- Reverse proxy with 5 load balancing algorithms
- Circuit breaker + health checks + retry logic
- A/B testing / canary routing with cookie stickiness
- Brotli + gzip on-the-fly compression
- URL rewrite engine (Apache mod_rewrite compatible)
- WAF (SQL injection, XSS, path traversal detection)
- Rate limiting (token bucket, per-IP)
- IP whitelist/blacklist (CIDR)
- Basic authentication per-path
- Security headers (HSTS, CSP, X-Frame, CORS)
- Request/response header transforms with variable substitution
- Automatic image optimization (WebP/AVIF serving)
- SPA mode + try_files + directory listing
- Custom error pages per domain
- ETag + 304 Not Modified + Range requests
- Pre-compressed file serving (.br, .gz)
- HTTP/2 via Go stdlib
- SIGHUP config reload (zero-downtime)
- Configurable listen addresses
- Trusted proxies for X-Forwarded-For
- Log rotation (size-based + SIGHUP reopen)
- Systemd service file
- Alerting (webhook + internal ring buffer)
- Uptime monitoring per domain
- Request mirroring (shadow traffic)

### Dashboard (React 19 + Tailwind 4.1)

- 11 pages: Login, Dashboard, Domains, Topology, Cache, Logs,
  Settings, Metrics, Analytics, Config Editor, Certificates
- Domain templates: WordPress, Static, Proxy, Redirect (one-click setup)
- Real-time stats via Server-Sent Events
- Cache management: charts, per-domain view, tag/domain/all purge
- YAML config editor with syntax validation
- SSL certificate timeline with expiry tracking
- Per-domain analytics with traffic charts
- Topology graph with React Flow

### CLI (15 commands)

- `serve`, `version`, `help`
- `config validate/test`
- `domain list/add/remove`
- `cache stats/purge`
- `status`, `reload`
- `migrate nginx/apache <file>`
- `backup`, `restore`

### API (22+ endpoints)

- Health, stats, config, domains CRUD, domain detail
- Cache stats/purge, logs, metrics, SSE live stats
- Certificates, analytics, monitor
- Config raw read/write, domain raw read/write
- Config export (YAML download)
- Alerts

### Configuration

- Single YAML file or split per-domain files (domains.d/)
- Include patterns (glob)
- Environment variable expansion with fallback
- Hot reload via SIGHUP or API

### Security (28 fixes from code review)

- Shared http.Transport (no connection leak)
- Config race mutex, admin CRUD mutex
- RealIP spoofing prevention
- On-demand TLS rate limiting
- Cache key collision fix (full canonical keys)
- Goroutine leak prevention (context-based)
- Request body limits, secret stripping
- WAF URL-decode bypass fix
- Open redirect fix, path traversal validation

### Docker

- Multi-stage Alpine build: 28.5MB image
- docker-compose: UWAS + PHP-FPM + MariaDB
- One-command VPS setup script

### Performance (AMD Ryzen 9 9950X3D)

- Static file: 7,000 req/sec
- Cache L1 hit: 75,000,000 ops/sec
- VHost routing: 70,000,000 ops/sec
- Middleware chain: 308,000 req/sec

## [0.0.3] - 2026-03-22

### Security

- **RealIP spoofing fix**: Proxy headers only trusted when direct connection is from a configured trusted proxy
- **On-demand TLS hardened**: OnDemandAsk URL validation + rate limit (10 certs/minute)
- **CORS restricted**: No more wildcard `*` origin — validates against dashboard/localhost origins only
- **Open redirect fixed**: HTTPS redirect uses canonical `domain.Host` instead of raw `Host` header
- **Dotfile protection**: Checks all path components, not just filename (blocks `/.git/config`)
- **Path traversal**: Fallback try_files path validated against document root
- **Config export sanitized**: Strips DNS credentials, PHP env vars, cache purge key
- **Admin API body limits**: All mutation endpoints limited to 1MB request body
- **WAF double-decode**: Checks URL-decoded query strings to catch encoded attacks

### Fixed

- **Transport leak**: Shared `http.Transport` across proxy requests (was creating one per request)
- **Config race condition**: RWMutex protects config during hot reload
- **Admin CRUD race**: RWMutex protects domain list during add/update/delete
- **Response capture OOM**: Limited to 10MB max body for caching (prevents memory exhaustion)
- **Cache key collision**: Uses full canonical key string (method|host|path|query|vary) instead of hash
- **Goroutine leaks**: Cache cleanup and rate limiter accept context.Context for proper shutdown
- **Disk cache accounting**: Scans existing files on startup to initialize byte counter
- **ACME challenge**: Polls correct challenge URL (was hardcoded to index 0)
- **ETag 304 from cache**: Conditional requests handled against cached ETag
- **Chunked POST**: FastCGI forwards chunked transfer-encoding bodies
- **io.Copy error**: Proxy logs upstream body copy failures
- **Memory aliasing**: Cache deserialize copies body slice

### Performance

- **htaccess caching**: Parsed once per domain root, not on every request
- **Rewrite precompilation**: Regex rules compiled at server init, not per request
- **Nonce pool capped**: ACME nonce pool limited to 10 entries
- **Request context zeroed**: Full struct zero on pool acquire prevents data leak

## [0.0.2] - 2026-03-22

### Added

- **Configurable listen addresses**: `http_listen` and `https_listen` fields in global config
- **Trusted proxies**: `trusted_proxies` CIDR list for X-Forwarded-For real IP extraction
- **.htaccess runtime import**: Parse and apply WordPress/Laravel .htaccess rewrites with proper -f/-d condition checks
- **Directory listing**: Per-domain `directory_listing: true` toggle with HTML table output
- **WAF URL decode**: WAF patterns now check both raw and URL-decoded query strings
- **Admin /health public**: Health endpoint no longer requires authentication
- **Config hot reload**: Live config reload via `POST /api/v1/reload` with document root change support
- **Install script**: `curl -fsSL https://uwaserver.com/install.sh | sh` for Linux/macOS
- **Benchmark suite**: Static file, vhost lookup, middleware chain, cache get/set benchmarks
- **Comprehensive integration tests**: Cache store/hit, rate limiting, multi-domain routing, backend failover, CORS, config reload

### Fixed

- .gitignore pattern `uwas` was blocking `cmd/uwas/` directory
- Dockerfile and CI workflows updated from Go 1.23 to Go 1.26
- GoReleaser docker build removed (binary-only releases)
- Gzip middleware now skips conditional requests (If-None-Match → 304 works correctly)
- Rate limiter correctly wired from per-domain security config

### Changed

- Server ports no longer hardcoded to :80/:443 — fully configurable
- Full middleware chain wired: recovery → request ID → real IP → security headers → gzip → rate limit → WAF → access log
- All documentation translated to English
- Logo and banner assets added

### Performance (AMD Ryzen 9 9950X3D)

- VHost routing: 70M ops/sec
- Cache L1 get: 75M ops/sec
- Middleware chain: 308K req/sec
- Static file serve: 10K req/sec

## [0.0.1] - 2026-03-21

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

[0.0.2]: https://github.com/uwaserver/uwas/releases/tag/v0.0.2
[0.0.1]: https://github.com/uwaserver/uwas/releases/tag/v0.0.1
