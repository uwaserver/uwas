# Upgrading UWAS

Operator-facing notes for upgrades that change runtime behavior. For the full
list of changes per release, see [CHANGELOG.md](CHANGELOG.md).

---

## Upgrading to v0.11.23

Drop-in.

1. **Deploy listen timeout** is 45s (was 3s). Slow Node/`npm start` apps
   should no longer false-rollback after a successful fetch/build.
2. **Telegram** alert footer shows `hostname · IP`.

No config or data migration.

---

## Upgrading to v0.11.22

Drop-in. App `env` keys keep the order you saved (no alphabetical reshuffle).
Existing YAML maps load in document order going forward.

No config or data migration.

---

## Upgrading to v0.11.21

Drop-in.

1. **Deploy** recovers from leftover `.git/**/*.lock` (e.g. `shallow.lock`)
   and streams live logs in the panel (5m client timeout).
2. **Auto-detected Node builds** run `install` then `build` as separate
   steps — custom `build_cmd` still must not contain `&&` / `|` / `;`.

No config or data migration.

---

## Upgrading to v0.11.20

Drop-in.

1. **Firewall Rules table** no longer lists `# uwas-autoblock` denys (still in
   Auto-Block panel).
2. **Enable** now allows **443/udp** for HTTP/3 (QUIC). Already-enabled hosts
   keep existing rules — re-enable or add `443/udp` manually if needed.

No config or data migration.

---

## Upgrading to v0.11.19

Drop-in. If the firewall panel is missing the bottom IPv4 `DENY any → any` but
still has an IPv6 twin (from an older move bug), open Firewall or click Refresh
— the IPv4 deny is re-added automatically.

No config or data migration.

---

## Upgrading to v0.11.18

Drop-in. Firewall reorder and display fixes only:

1. **↑↓ move** no longer risks deleting the numbered default DENY (delete-first
   move; default deny stays at the bottom and cannot be moved).
2. **Protocol** column shows `any` when empty (source-only / any-port rules).

No config or data migration.

---

## Upgrading to v0.11.17

Drop-in for most hosts. Firewall panel behavior changes slightly:

1. **Enable** now ensures one numbered `DENY any → any` at the bottom (plus the
   UFW default incoming deny policy). If you already had a blanket deny, it is
   not duplicated.
2. **Empty port** in Add Rule means any port. Allow with both port and source
   empty is rejected in the UI (would open the host completely).
3. Re-enabling after disable cleans duplicate allow/deny rows left by earlier
   builds.

---

## Upgrading to v0.11.16

Mostly drop-in. Review if you use the WAF body scanner, firewall panel, remote
MySQL, or app deploy build commands.

### Action may be required

1. **WAF now inspects JSON and multipart bodies.** Legitimate APIs that send
   strings matching SQLi/XSS/PHP patterns in JSON fields or form-data may get
   blocked where they previously passed. Tune WAF rules or exclude those
   paths if needed.

2. **App deploy `build_cmd`** rejects shell chaining (`&&`, `|`, `;`, backticks,
   etc.). Split into a single safe command or a script file invoked without
   metacharacters.

3. **Firewall enable** sets `ufw default deny incoming` (policy) and skips
   duplicate allow rows. Existing numbered rules are unchanged; use Source IP
   and ↑↓ reorder in the panel if you want tighter allow-above-deny ordering.

4. **Remote MySQL** with an active firewall prompts to open `3306/tcp` — decline
   if you only reach MySQL over a private network / tunnel.

---

## Upgrading to v0.11.15

Security hardening release. Most deployments are drop-in; review the items
below if you use multi-user RBAC, location proxies, PHP FastCGI, 2FA, or built-in
SFTP.

### Action may be required

1. **If login fails with “Invalid API key or server unavailable” after a
   Settings / Config Editor save,** open `uwas.yaml` and restore
   `global.admin.api_key` (a corrupted value looks like `****xxxx` or
   `********`). Then restart or reload. v0.11.15 prevents this class of
   overwrite going forward.

2. **Non-admin domain updates** can no longer change `root`, `type`, `proxy`,
   `ip`, `app`, `redirect`, `internal_aliases`, `access_log`, or
   `webhook_secret`. Admins are unchanged.

3. **2FA recovery-code generation** requires a valid TOTP code when TOTP is
   already enabled. Using a recovery code disables TOTP until re-enrolled.

4. **PHP `open_basedir`** is tighter: the parent of the document root is only
   allowed for common framework public directories. Sites that relied on reading
   siblings outside those layouts may need an explicit, reviewed
   `PHP_ADMIN_VALUE` (filtered values that weaken the sandbox are ignored).

5. **Built-in SFTP will not start** if `global.admin.api_key` is empty — set a
   key first.

6. **Location `proxy_pass`** blocks private/link-local targets via dial
   controls and no longer trusts client-supplied `X-Forwarded-For` chains.

---

## Upgrading to v0.11.8

**Behavior change:** `canonical_host` (the panel's "primary URL") is now
enforced. It was stored but ignored, so both the apex and www answered 200. A
domain with `canonical_host: www` now 301-redirects the apex to www (and
`canonical_host: apex` redirects www to the apex), preserving path and query.
If you set a primary URL and relied on both hostnames serving directly, the
non-primary now redirects. Domains with no `canonical_host` set are unaffected.

Between v0.11.1 and v0.11.8 the notable operator-facing changes (full detail in
CHANGELOG.md): auto-block and the liveness watchdog can be configured from
Settings and enabling the firewall now allows UWAS's own ports with a 60-second
auto-rollback (v0.11.5); the bot guard no longer blocks crawlers from
robots.txt, sitemaps or /.well-known/ (v0.11.7); and a per-domain rate-limit
429 now reports the configured window as Retry-After (v0.11.1).

---

## Upgrading to v0.11.1

No action required. Auto-block and the watchdog can now be configured from
Settings → Security instead of hand-editing `uwas.yaml`, and the Firewall page
gains a live panel of active blocks. As the panel notes, enabling or disabling
either feature — and changing any threshold — still takes effect only after a
service restart; the panel persists the values, `systemctl restart uwas`
applies them.

One behaviour change: a per-domain rate-limit rejection now sends
`Retry-After` matching the configured window instead of a fixed 60 seconds.
A client that honoured the old value will back off for the real window now
(e.g. 10s for a 10s window), which is shorter, not longer.

---

## Upgrading to v0.11.0

Two new subsystems, both **off by default**. Upgrading changes nothing until you
turn them on — but neither is useful left off if you are exposed to floods, and
the watchdog has a footgun worth reading before you enable it.

### Action required to get the new protection

1. **Autoblock is opt-in.** Add `global.autoblock.enabled: true`. Start with
   `dry_run: true` for a day of representative traffic, then read what it would
   have done:

   ```bash
   journalctl -u uwas | grep 'autoblock (dry-run'
   ```

   Anything in there that is a real visitor means a threshold is too tight or
   an address belongs in `whitelist`. Only then set `dry_run: false`. Enabling
   enforcement blind is how an autoblocker takes a site off the internet —
   thresholds wrong in the tight direction are worse than the attack.

   `firewall_sync: true` additionally pushes blocks to ufw/iptables. Without it
   a blocked IP still completes a TCP handshake before being dropped.

2. **The watchdog and `WatchdogSec` must be enabled together.** With
   `WatchdogSec` set in the unit and `global.watchdog.enabled` false, UWAS
   never sends a ping and **systemd restarts a perfectly healthy server every
   60 seconds.** The installer comments `WatchdogSec` out when it finds no
   `watchdog:` block in `uwas.yaml`; if you enable the probe later, uncomment
   it in `/etc/systemd/system/uwas.service.d/10-resilience.conf` and run
   `systemctl daemon-reload`.

3. **Behind Cloudflare or another CDN, confirm the edge ranges are known.**
   `global.cloudflare.ip_ranges` and `global.trusted_proxies` are whitelisted
   automatically. If neither is populated, every connection looks like it comes
   from the CDN edge and autoblock will eventually block one — taking the site
   offline for every visitor that edge serves. Sync the ranges from the panel,
   or list them under `global.autoblock.whitelist`, before enabling
   enforcement.

   With `proxy_protocol: true`, connection-level detection is disabled outright
   (a warning is logged at startup) because every connection arrives from the
   load balancer. Only WAF/rate/404 signals apply. A TLS handshake flood in
   that topology has to be stopped at the load balancer.

### Behavior changes (no action needed)

- **The service now restarts on any exit, not just a failing one.**
  `Restart=always` replaces `Restart=on-failure`, so a process killed by the
  OOM killer comes back the way a crash does. `StartLimitBurst=10` over 5
  minutes replaces systemd's default of 5 in 10 seconds, which used to give up
  permanently and turn a recoverable overload into a lasting outage.
- **`LimitNOFILE` rises to 1048576.** A connection flood exhausts file
  descriptors before anything else and shows up as `accept: too many open
  files` with a listener that has stopped accepting.
- **The installer writes a systemd drop-in** at
  `/etc/systemd/system/uwas.service.d/10-resilience.conf` instead of rewriting
  `uwas.service`. Existing operator edits to the unit are preserved. If you
  have your own `Restart=` or `LimitNOFILE` and want them to win, put them in a
  drop-in that sorts after `10-`.
- **`POST /api/v1/database/docker` returns 400 instead of 503 for a malformed
  body.** It previously reported a Docker daemon problem for bad JSON or a
  missing `root_pass`. Any client keying on the 503 for these cases should read
  400 now.
- **SFTP and SSH pick up two upstream DoS fixes** (`golang.org/x/crypto`
  v0.56.0). No configuration change.

---

## Upgrading to v0.10.1

Six things the panel reported as working and were not. Two change behavior you
may have been relying on.

### Action may be required

1. **`directory_listing` no longer replaces the homepage.** With listings on,
   `GET /` served a file listing even when `index.html` existed. It now serves
   the index and lists only directories that have none. If you were relying on
   the listing appearing at a path that also has an index file, remove the
   index file.

2. **`stale_while_revalidate` now does something.** It was inert, so turning it
   on changed nothing. With it on, an entry past its TTL is served stale for up
   to `grace_ttl` — 24 hours by default — while a refresh runs behind it. Check
   that `grace_ttl` is a window you actually want before enabling it.

### Behavior changes (no action needed)

- **Alerts now reach Slack, Telegram and email.** Those channels were stored
  and never delivered to. If you configured them and forgot, they will start
  sending.
- **The WAF blocks boolean SQL tautologies** (`' OR '1'='1`). Anchored on a
  quote or a numeric equality in the query string, so ordinary queries are
  unaffected.
- **Request metrics halve.** `uwas_requests_total`, the panel's request stat
  and the MCP report were double-counting; existing graphs will show a step
  down that is a correction, not a traffic drop.
- **The panel stops claiming `.htaccess` is enabled everywhere.** It reads
  `mode: import` now instead of any non-empty value, and an invalid mode is
  rejected at config load rather than silently meaning "off".

---

## Upgrading to v0.10.0

Three silent failures fixed and the first measured performance change. Nothing
here requires action, but four things behave differently on the first request
after upgrading.

### Behavior changes (no action needed)

- **Static responses now carry `Cache-Control`.** UWAS previously sent none at
  all, leaving browsers to guess. HTML and extensionless URLs now get
  `no-cache`, which means the browser revalidates through the ETag and gets a
  304 rather than heuristically reusing a page it should not have. Every other
  static file is untouched by default. If a domain already sets Cache-Control
  through `locations[].cache_control`, `cache.rules[].cache_control` or
  `headers`, that value still wins — the new setting only fills a gap.

- **Small static files are held in memory.** A file cache under
  `global.static_cache` keeps files up to 512KB, capped at 64MB total, and
  revalidates an entry against size and mtime before reuse. Editing a file on
  disk is picked up as before. Set `global.static_cache.enabled: false` to
  turn it off, or lower `max_bytes` on a memory-constrained host.

- **New static domains are created with the cache on.** Adding a domain
  through the API or panel now writes `cache.enabled: true` unless the request
  says otherwise, including an explicit `cache: {enabled: false}`. Existing
  domains are untouched, and php, proxy and redirect domains are unaffected.

- **A domain is reported down only after two consecutive failed checks.** One
  slow or bot-challenged reply used to flip the badge immediately, which is
  routine for a site behind a CDN. Recovery still shows on the first good
  check. Health checks also run up to 8 domains at a time instead of strictly
  one after another, and send a browser-shaped User-Agent that keeps the
  `UWAS-Monitor` token.

### Worth knowing

- **The first per-domain cache purge after upgrading may miss older entries.**
  Purging one domain now matches an implicit `site:<host>` tag that entries
  carry from this release onward. Anything cached by the previous version has
  no such tag; those entries fall out on TTL. Purge all if you need them gone
  immediately.

- **Git push webhooks configured with GitHub's default content type now
  work.** They were answered with `415 Unsupported Media Type` before reaching
  the handler, with the failure visible only in GitHub's delivery log. If you
  changed a webhook to `application/json` to work around this, it will keep
  working — both content types are accepted.

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
