# Upgrading UWAS

Operator-facing notes for upgrades that change runtime behavior. For the full
list of changes per release, see [CHANGELOG.md](CHANGELOG.md).

---

## Upgrading to v0.8.8

v0.8.8 is a stability and bug-fix release with 26 fixes across the engine,
backup, DNS, cron, middleware, PHP, WordPress, and selfupdate subsystems.
Most changes are transparent bug fixes, but review the items below before
upgrading.

### Action may be required

1. **Cron job timeout now enforced (24h ceiling).** A cron job that hangs
   indefinitely will be killed after 24 hours and its overlap guard released,
   allowing the next scheduled run to proceed. Most setups are unaffected,
   but if you deliberately run cron jobs longer than 24 hours they will now
   be killed.

2. **Crontab read failure now surfaces as an error.** Previously, a transient
   `crontab -l` failure (permission error, fork failure) was silently treated
   as "no crontab" — the crontab would be overwritten with only UWAS's own
   entry, destroying every unrelated cron job on the system. Now the error is
   surfaced and the write aborts. If you see cron job management errors after
   upgrading, verify crontab permissions.

3. **Route53 DNS signing always uses us-east-1.** Route53 is a global AWS
   service and must be signed against us-east-1 regardless of the provider's
   configured region. If you use Route53 with a non-us-east-1 region and your
   setup depended on the previous (incorrect) signing behaviour, verify DNS
   records after upgrading.

4. **Cache: Content-Encoding stripped from cached responses.** The cache now
   strips `Content-Encoding` and `Content-Length` from stored responses so the
   compress middleware re-derives them correctly on each hit. Previously a
   cached response could be served with the wrong encoding header. If you use
   the cache, consider purging it after upgrading to avoid stale entries.

### Behavior changes (no action needed)

- **Backup: day-31 cron schedules now fire.** The calendar-walk algorithm was
  fixed to use real month lengths (previously day 31 never occurred, so
  schedules on the 31st silently never ran).
- **Backup: full-backup retention no longer deletes per-domain backups.**
  Pruning now only targets `uwas-backup-*` files; `uwas-domain-*` backups
  keep their own lifecycle.
- **Cloudflare: zone/record listing now paginates.** Previous code returned
  only the first page (50 zones / 100 records), so record lookups past page 1
  silently failed. Duplicate A records created as a result will not be
  automatically cleaned up.
- **Selfupdate: checksum verification now works.** The previous .sha256 URL
  scheme always 404'd; verification was silently skipped. The new code fetches
  `SHA256SUMS` from the release and validates the binary.
- **WordPress installer: previously silent errors now surface.** HTTP errors,
  download failures, and move failures are now reported instead of silently
  claiming success.
- **Admin 2FA endpoints require admin role.** Non-admin users cannot set up,
  verify, or disable 2FA even when authenticated (multi-user mode only).

---

## Upgrading to v0.8.7

v0.8.7 is a security release. It is a drop-in upgrade for the **default
single-API-key deployment**, but it tightens several defaults and enforces the
multi-user permission model. Review the items below before upgrading,
especially if you run with `global.users.enabled: true` or deploy via
docker-compose.

### Action may be required

1. **docker-compose / `.env` now fail fast on missing secrets.**
   `docker compose up` now **errors out** instead of starting with placeholder
   credentials if any of these are unset:
   - `UWAS_ADMIN_KEY`
   - `DB_ROOT_PASSWORD`
   - `DB_PASSWORD`

   Set them in your `.env` (see `.env.example`). Generate strong values with
   `openssl rand -hex 24`. This prevents accidentally running with the old
   shipped defaults.

2. **Placeholder admin keys are rejected on a public listener.**
   If the admin listener is bound to a non-loopback address and
   `global.admin.api_key` is a well-known placeholder (e.g.
   `please-change-this-admin-key`, `changeme`, `admin`), the server now
   **refuses to start**. Set a strong, unique key, or bind the admin listener to
   `127.0.0.1` / `::1`.

3. **The `user` role is now read-only (multi-user mode only).**
   The declared RBAC model is now enforced: an account with the `user` role can
   **read** its assigned domains but can no longer create, update, or delete
   them (those return `403`). If you relied on `user`-role accounts managing
   domains, move them to the `reseller` role (which retains domain
   create/update/delete). `admin` is unaffected.

4. **Minimum password length is now 12 characters (multi-user mode only).**
   Bootstrap, user creation, password change, and admin password reset reject
   passwords shorter than 12 characters. **Existing passwords keep working** —
   this applies only when a password is set or changed.

5. **Custom SSE/WebSocket clients: `?token=` is no longer accepted.**
   The legacy `?token=<session-or-api-key>` query-parameter auth fallback was
   removed (it leaked credentials to logs/history/Referer). Use the single-use
   ticket flow: `POST /api/v1/auth/ticket`, then connect with
   `?ticket=<ticket>`. The bundled dashboard already does this — only custom
   integrations are affected.

6. **Custom terminal clients: the admin PIN is bound into the ticket.**
   The PIN is no longer read from the WebSocket URL (`?pin=`) in authenticated
   deployments. Send the PIN via the `X-Pin-Code` header on the
   `POST /api/v1/auth/ticket` request; the resulting ticket carries PIN
   verification. (`?pin=` still works only in the no-auth bypass mode.) The
   bundled dashboard already does this.

### Behavior changes (no action needed)

- **`global.users.session_ttl` is now honored.** It was previously ignored
  (sessions were hardcoded to 24h). If you had set it, sessions will now use the
  configured lifetime — verify the value is what you intend.
- **Login lockout is now per-(username, IP).** A flood from one IP no longer
  locks a user out from other IPs. Per-source brute-force is still capped.
- **Admin PIN failures are rate-limited.** Repeated wrong PINs now trip the
  per-IP lockout (previously only audit-logged).
- **File manager: SVG files download instead of previewing.** SVG can carry
  scripts; raster images still preview in a new tab.
- **Dashboard responses carry a strict Content-Security-Policy** and
  `X-Frame-Options: DENY`. If you embed the dashboard in a frame or inject
  custom external scripts, they will be blocked.
- **Per-domain `php.ini` overrides reject newlines/control characters** in keys
  and values (closes a sandbox-escape vector).

### Recommended (optional)

- **Enable a global rate limit** as a DoS backstop for unknown domains and the
  admin API. It is off by default; see the commented `global.rate_limit` block
  in `uwas.example.yaml`.
