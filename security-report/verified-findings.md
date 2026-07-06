# Verified Security Findings — UWAS
> 
> **Status:** This scan was performed 2026-06-26. All findings have been
> reviewed and are **resolved** in the current codebase (v0.8.8, July 2026).
> See [SECURITY-REPORT.md](./SECURITY-REPORT.md) for the full status update
> with per-finding resolution tracking.
>

Phase-3 verification (sc-verifier). Every surviving finding below was re-confirmed
by re-reading the cited source, not trusted from the scanner output. Duplicates
across scanners were merged; framework/existing-control mitigations (pathsafe,
crypto/subtle, header-based auth, RE2 regex, Go net/http header sanitization,
WAF) were applied to confidence scoring.

## Summary
- Total raw findings from Phase 2 (excluding "no issues found" scanners): 54
- After duplicate merging (10 cross-scanner duplicates collapsed): 44 distinct
- After false-positive elimination / context downgrade: 44 retained (0 hard-eliminated; the scanners had already pruned FPs — several were down-scored to Info)
- Final verified findings: 35 actionable + 9 informational

## Confidence Distribution
- Confirmed (90-100): 0
- High Probability (70-89): 11
- Probable (50-69): 16
- Possible (30-49): 8
- Low Confidence (0-29): 0

## Cross-cutting precondition
Most authorization findings (the cross-tenant read/IDOR class and the
mass-assignment privesc) are reachable **only when `global.users.enabled = true`**
(multi-user RBAC with reseller/user roles). In the default single-API-key
deployment, `authMiddleware` injects a virtual admin for every request
(`internal/admin/api.go:283`), so every caller is admin and these are not
exploitable. UWAS ships multi-tenant RBAC as a first-class feature, so the
findings remain valid for that configuration. This precondition is reflected in
the confidence scores.

---

## CRITICAL

### VULN-001: Reseller can over-post `root` on domain update → arbitrary filesystem read/write (privilege escalation)
- **Severity:** Critical
- **Confidence:** 85/100 (High Probability)
- **Original Skill:** sc-mass-assignment (MASS-001) + sc-privilege-escalation (PRIVESC-001) [merged]
- **Vulnerability Type:** CWE-915 (Mass Assignment) / CWE-269 (Improper Privilege Management) / CWE-22
- **File:** internal/admin/handlers_domain.go:659-701 (non-admin denylist), :768 + :1142-1144 (`validateDomainUpdateConfig`→`ValidateDomainPartial`), internal/config/merge.go:55, internal/admin/handlers_files.go:78 (`authorizedDomainRoot`→`domainRootForFiles`)
- **Reachability:** Direct (HTTP `PUT /api/v1/domains/{host}` then file-manager endpoints)
- **Sanitization:** None on the update path (containment check exists only on the create path, handlers_domain.go:1129-1133)
- **Framework Protection:** None
- **Description:** `handleUpdateDomain` decodes the body into the persisted `config.Domain`, then for non-admins rejects only a fixed denylist: `ssl, security, cache, compression, basic_auth, resources, aliases, htaccess, locations, php.fpm_address`. The denylist **omits `root`** (also `type`, `proxy`, `redirect`, `ip`). `config.MergeDomain` applies `patch.Root` unconditionally (merge.go:55), and the update validator `validateDomainUpdateConfig` calls `config.ValidateDomainPartial`, which has no web-root containment check (the `pathsafe.IsWithinBase(webRoot, d.Root)` guard exists only in the create path). The file manager then uses the domain's `root` as its jail base via `domainRootForFiles`, with no re-assertion of web-root containment for non-admins.
- **Verification Notes:** Confirmed by reading handlers_domain.go:659-701 (no `root` in denylist), :768 (update uses `validateDomainUpdateConfig`), :1129-1133 (containment is create-only), and handlers_files.go:67-88 (`authorizedDomainRoot` returns the raw configured root). A reseller owning `a.com` sends `PUT /api/v1/domains/a.com {"root":"/"}`; pathsafe then treats the whole filesystem as in-jail, enabling read/write of `/etc/shadow`, the UWAS config (admin API key), other tenants' data, and `authorized_keys` → host compromise.
- **Remediation:** Replace the non-admin denylist with an allowlist; add the `pathsafe.IsWithinBase`/`IsWithinBaseResolved` containment check to the update path (in `validateDomainUpdateConfig` or post-merge); defense-in-depth: re-assert web-root containment in `authorizedDomainRoot` for non-admins.

---

## HIGH

### VULN-002: php.ini directive injection via unsanitized per-domain config value → PHP sandbox escape / RCE
- **Severity:** High
- **Confidence:** 85/100 (High Probability)
- **Original Skill:** sc-rce (RCE-001)
- **Vulnerability Type:** CWE-94 (Code Injection)
- **File:** internal/phpmanager/manager.go:534-554 (`SetDomainConfig`, value stored verbatim), :686-738 (`buildDomainINI`, overrides appended after security lines via `fmt.Sprintf("%s = %s")`), :324 (applied as `-c` master ini); reachable at internal/admin/handlers_php.go:502-534 (non-admin path at :511-518)
- **Reachability:** Direct (HTTP `PUT /api/v1/php/domains/{domain}/config`)
- **Sanitization:** Key blocklist only (`blockedPHPDirectives`); the **value** is never checked for newlines/control chars
- **Framework Protection:** None
- **Description:** `SetDomainConfig` blocks dangerous directive *keys* (open_basedir, disable_functions, allow_url_include, auto_prepend_file, …) but stores the *value* verbatim (manager.go:552). `buildDomainINI` writes each override with `fmt.Sprintf("%s = %s", k, overrides[k])` (manager.go:737) with no `\n`/`\r` filtering, and appends overrides **after** the UWAS-enforced `disable_functions`/`open_basedir`/`allow_url_include` lines, so PHP last-value-wins semantics let an injected line override them. The file is passed as `-c tmpINI` at PHP_INI_SYSTEM scope.
- **Verification Notes:** Confirmed at manager.go:540 (key-only check), :552 (verbatim store), :686-688 (security lines written first), :727-738 (overrides written last, unescaped) and handlers_php.go:511-518/534 (non-admin domain manager reaches the sink via `CanManageDomain`). PoC: `{"key":"memory_limit","value":"128M\ndisable_functions =\nopen_basedir = /\nauto_prepend_file = /var/www/<d>/shell.php"}` clears the sandbox, widens open_basedir to `/`, and auto-runs an uploaded PHP file on every request. Persisted to domain YAML. The `.htaccess php_value` path is NOT equivalent (PHP_INI_PERDIR, cannot override SYSTEM directives) — correctly noted by the scanner.
- **Remediation:** Reject values containing `\n`/`\r`/control chars in `SetDomainConfig`; validate values against the directive's expected type; quote on emit and build the ini with a structured escaped writer. Apply same to global `SetConfig`/`updateINI` (ini.go:145) for defense-in-depth.

### VULN-003: Reseller can over-post `type`/`proxy`/`redirect` on domain update → SSRF / route hijack
- **Severity:** High
- **Confidence:** 75/100 (High Probability)
- **Original Skill:** sc-mass-assignment (MASS-002)
- **Vulnerability Type:** CWE-915 → CWE-918 (SSRF)
- **File:** internal/admin/handlers_domain.go:659-701; internal/config/merge.go:49-51,115-120
- **Reachability:** Direct (`PUT /api/v1/domains/{host}`)
- **Sanitization:** None for `type`/`proxy`/`redirect` (not in non-admin denylist; `ValidateDomainPartial` does not restrict upstream targets)
- **Framework Protection:** Reverse-proxy upstreams ARE validated by the proxy SSRF policy at dispatch time (server_dispatch.go:295-300) — this is a partial mitigation that the proxy creation path enforces, reducing but not eliminating reachability.
- **Description:** Same incomplete denylist as VULN-001. A reseller converts an owned domain into `type: proxy` with `upstreams: [{url: http://169.254.169.254/...}]`, turning their domain into an SSRF primitive against cloud metadata / internal services.
- **Verification Notes:** Denylist omission confirmed (handlers_domain.go:659-701). Note `type=app` is separately rejected at handlers_domain.go:838. Net confidence held at 75 because the proxy dispatch path applies `IsProxyUpstreamSafe`; verify whether that policy blocks 169.254.169.254 by default (the SSRF scanner reports the proxy policy denies metadata/private ranges) — if so, real-world impact is limited to allowed-but-unintended internal hosts, but the route-hijack/redirect repurposing remains.
- **Remediation:** Allowlist non-admin-patchable fields; forbid `type`, `proxy`, `redirect`, `root`, `ip`, `app.*` for non-admins.

### VULN-004: Stored XSS via SVG preview in File Manager (token theft → full admin compromise)
- **Severity:** High
- **Confidence:** 72/100 (High Probability)
- **Original Skill:** sc-xss (XSS-001)
- **Vulnerability Type:** CWE-79 (Cross-site Scripting)
- **File:** internal/admin/handlers_files.go:303-333 (serves `.svg` as `image/svg+xml`, no `Content-Disposition`/`nosniff`/CSP); web/dashboard/src/pages/FileManager.tsx:181-209 (`window.open(blobURL)` without `noopener`)
- **Reachability:** Stored → triggered when an admin previews the file. Cross-user.
- **Sanitization:** None (raw bytes returned; no nosniff/attachment/CSP)
- **Framework Protection:** React JSX auto-escaping protects the rest of the dashboard, but does NOT apply here — the SVG is opened as a top-level same-origin document, bypassing React entirely.
- **Description:** `handleFileRead` returns `.svg` with `Content-Type: image/svg+xml`. The client fetches it with the auth header, wraps it in a same-origin `blob:` URL, and `window.open(url, '_blank')` (no `noopener`). An SVG with `<script>`/`onload` executes JS in the dashboard origin; the new same-origin context inherits `sessionStorage` (the bearer token `uwas_token`), enabling exfiltration and full control of the 250+ admin endpoints.
- **Verification Notes:** Confirmed server-side (handlers_files.go:323-331: svg→`image/svg+xml`, only Content-Type/Content-Length set, then `w.Write(data)`) and client-side (FileManager.tsx:198-200: `blob` → `createObjectURL` → `window.open(url,'_blank')` with no noopener). Token in sessionStorage confirmed (api.ts:5). A lower-priv RBAC user / SFTP user / hosted CMS can plant the SVG; a higher-priv admin clicking preview is compromised — genuine cross-user stored XSS.
- **Remediation:** Serve uploaded SVG with `Content-Disposition: attachment` + `X-Content-Type-Options: nosniff` (or `text/plain`); drop `.svg` from the client image-preview branch or render in a `sandbox`ed iframe without `allow-scripts`; add `noopener`; add a strict CSP to dashboard + file-content responses.

### VULN-005: Publicly-known default admin API key in docker-compose, with admin port published on 0.0.0.0
- **Severity:** High
- **Confidence:** 70/100 (High Probability)
- **Original Skill:** sc-docker (DOCK-001 + DOCK-003) [merged]
- **Vulnerability Type:** CWE-798 (Default Credentials) / CWE-668 (Exposure to Wrong Sphere)
- **File:** docker-compose.yml:7 (`"9443:9443"`), :13 (`UWAS_ADMIN_KEY=${UWAS_ADMIN_KEY:-please-change-this-admin-key}`)
- **Reachability:** Direct over the network if `docker compose up` is run without exporting `UWAS_ADMIN_KEY`
- **Sanitization:** N/A
- **Framework Protection:** The startup guard `internal/admin/api.go:233` refuses to bind a non-loopback listener only when the key is **empty** — a non-empty default passes, so the server boots with the guessable key. Confirmed at api.go:233-238.
- **Description:** The admin control plane is published on all interfaces (`9443:9443` binds 0.0.0.0) and falls back to the literal `please-change-this-admin-key` when the env var is unset. An attacker reaching :9443 with the known key gains full panel control (file manager, terminal, SFTP, DB, firewall).
- **Verification Notes:** Confirmed docker-compose.yml lines and the empty-only bind guard. Held at High (not Critical) because it requires the operator to skip setting the env var and to expose 9443; the inline comment warns against it but boot is not blocked.
- **Remediation:** Use `${UWAS_ADMIN_KEY:?set a strong admin key}` (fail-fast) or generate a random key in the entrypoint; bind admin to `127.0.0.1:9443:9443`; prefer Compose `secrets:`.

---

## MEDIUM

### VULN-006: TOTP code replay — replay protection designed but never wired up
- **Severity:** Medium
- **Confidence:** 80/100 (High Probability)
- **Original Skill:** sc-race (RACE-001) + sc-business-logic (BIZ-001) + sc-auth (AUTH-002) [merged]
- **Vulnerability Type:** CWE-294 (Auth Bypass by Capture-Replay) / CWE-362
- **File:** internal/admin/api.go:477; internal/admin/handlers_auth.go:370,416; internal/admin/totp.go:50 (returns matched step); internal/auth/manager.go:80 (`LastStep` field, never read/written)
- **Reachability:** Direct (per-request admin 2FA gate)
- **Sanitization:** N/A
- **Framework Protection:** None (the mechanism exists but is unused)
- **Description:** `ValidateTOTP` returns the matched time step expressly so callers can burn a used code, and `Session.LastStep` is documented "prevents replay", but all three callers do `valid, _ := ValidateTOTP(...)` and `LastStep` is never read/written. With `totpWindow = 1`, a captured 6-digit code is replayable for ~60-90s. A replay submits a *valid* code so it never increments the failure rate limiter.
- **Verification Notes:** Confirmed at api.go:477 (`valid, _ := ValidateTOTP(totpSecret, totpCode)`); confirmed `LastStep` exists only as a struct field. Three independent scanners flagged the same root cause.
- **Remediation:** After a successful validation, atomically record the returned step (per session/secret) under the session lock and reject any code whose step ≤ last accepted step.

### VULN-007: Raw-config viewer leaks TLS private key, 2FA recovery codes, and OAuth client secrets
- **Severity:** Medium
- **Confidence:** 85/100 (High Probability)
- **Original Skill:** sc-data-exposure (EXPOSE-001)
- **Vulnerability Type:** CWE-200 / CWE-522
- **File:** internal/admin/handlers_settings.go:264-274 (mask list) + maskYAMLValue impl
- **Reachability:** Direct (`GET /api/v1/config/raw`, admin-gated) — the Config Editor page calls it on every view
- **Sanitization:** Partial — exact-key prefix mask covers a subset only
- **Framework Protection:** None
- **Description:** The mask list is `api_key, pin_code, totp_secret, secret_key, password, secret_access_key, api_token, client_secret, telegram_token, slack_url, purge_key`. It does NOT cover: `tls_key` (admin TLS private key), `recovery_codes` (a YAML list — `maskYAMLValue` only rewrites inline values on a matching line, so the child `- <code>` items leak verbatim), and `google_client_secret`/`github_client_secret` (the `client_secret` key does not prefix-match these). The fully-sanitized `handleConfigExport` (lines 184-217) demonstrates the intended set; the raw viewer diverges.
- **Verification Notes:** Confirmed the exact mask list at handlers_settings.go:264-274. Recovery codes are a 2FA-bypass credential; TLS key compromises panel transport. Admin-only, hence Medium not High, but it is a real credential-handling/exposure gap (browser memory, history, screen-share, proxy logs).
- **Remediation:** Reuse the export sanitization set; marshal a deep-copied secret-stripped `config.Config` rather than regex-masking raw text; special-case YAML list values.

### VULN-008: Origin-validation prefix-match bypass weakens CORS reflection and CSRF fallback
- **Severity:** Medium
- **Confidence:** 78/100 (High Probability that the bug is real; impact bounded)
- **Original Skill:** sc-cors (CORS-001) + sc-csrf (CSRF-001) [merged; cross-ref sc-websocket]
- **Vulnerability Type:** CWE-346 (Origin Validation Error) / CWE-1385 / CWE-942
- **File:** internal/admin/handlers_auth.go:152-170 (matchers 155-158); consumed at internal/admin/api.go:289-296 (CORS reflection) and api.go:499-510 (CSRF origin fallback)
- **Reachability:** Direct
- **Sanitization:** Flawed — `strings.HasPrefix(lower, "http://localhost")` etc.
- **Framework Protection:** Header-based auth (no cookies) and **no `Access-Control-Allow-Credentials`** materially bound the impact (confirmed: no Set-Cookie auth anywhere in internal/admin). The terminal WebSocket uses a stricter exact-host `CheckOrigin` and is NOT affected.
- **Description:** `isAllowedOrigin` matches any origin whose host *begins with* `localhost`/`127.0.0.1` — e.g. `http://localhost.evil.com` — so an attacker-registered domain passes, getting reflected into `Access-Control-Allow-Origin` and satisfying the secondary CSRF origin/referer check. Because the primary auth is a header bearer token the browser will not auto-attach, this erodes a defense-in-depth layer rather than yielding a working CSRF/data-theft today; it becomes Critical if cookie auth or ACAC are ever introduced.
- **Verification Notes:** Confirmed the `HasPrefix` matchers at handlers_auth.go:155-158 and that no ACAC header is emitted. Two scanners agreed (one rated it 75, the other 42 due to the header-auth mitigation); merged severity Medium, confidence reflects "real bug, bounded impact."
- **Remediation:** Parse the origin and compare `u.Hostname()` exactly against `localhost`/`127.0.0.1`/`::1` (optional port). Never use `HasPrefix` for origin allowlisting.

### VULN-009: Missing per-domain authorization on DNS records read (cross-tenant zone disclosure)
- **Severity:** Medium
- **Confidence:** 72/100 (High Probability)
- **Original Skill:** sc-authz (AUTHZ-001) + sc-api-security (API-001) [merged]
- **Vulnerability Type:** CWE-862 (Missing Authorization) / CWE-639
- **File:** internal/admin/handlers_dns.go:73-91
- **Reachability:** Direct (`GET /api/v1/dns/{domain}/records`)
- **Sanitization:** N/A — no `requireAdmin` and no `requireDomainAccess`
- **Framework Protection:** None on this handler
- **Description:** `handleDNSRecords` takes `{domain}` from the path and uses the server-wide DNS provider token to list the zone's records, with no authorization. The write siblings (`handleDNSRecordCreate` etc.) call `requireAdmin` **and** `requireDomainAccess` (confirmed at handlers_dns.go:94,98), so the read endpoint is strictly less protected. In multi-user mode any authenticated user enumerates any zone's A/MX/TXT (incl. SPF/DKIM/ACME tokens).
- **Verification Notes:** Confirmed: handleDNSRecords (lines 73-91) has zero auth calls; handleDNSRecordCreate at line 93-99 starts with `requireAdmin`+`requireDomainAccess`.
- **Remediation:** Add `if !s.requireDomainAccess(w, r, domain, "dns.read") { return }` at the top of `handleDNSRecords`.

### VULN-010: `handleDomainDebug` exposes any domain's filesystem path, dir listing, PHP PID — no per-domain authz
- **Severity:** Medium
- **Confidence:** 70/100 (High Probability)
- **Original Skill:** sc-data-exposure (EXPOSE-002)
- **Vulnerability Type:** CWE-200 / CWE-285
- **File:** internal/admin/handlers_domain_health.go:19-106 (route routes.go:62)
- **Reachability:** Direct (`GET /api/v1/domains/{host}/debug`)
- **Sanitization:** None — no `requireAdmin`, no `CanManageDomain`
- **Framework Protection:** Authenticated (passes authMiddleware) but no per-tenant scoping, unlike sibling `handleDomainHealth` (filters by `user.Domains`) and `handleDomainRawGet` (`CanManageDomain`)
- **Description:** Returns `root`, PHP-FPM address, `os.ReadDir(domainCfg.Root)` listing, PHP PID, and cert issuer for any host. In multi-user mode a low-priv user learns other tenants' absolute paths and web-root contents (useful for traversal/LFI follow-ups).
- **Verification Notes:** Confirmed handler reads `host` from path and returns `root`/`php_fpm_address`/web-root listing with no authz check (lines 19-48).
- **Remediation:** Add `CanManageDomain` guard for non-admins or gate the whole endpoint behind `requireAdmin`.

### VULN-011: No minimum password length / weak password policy
- **Severity:** Medium
- **Confidence:** 75/100 (High Probability)
- **Original Skill:** sc-auth (AUTH-001)
- **Vulnerability Type:** CWE-521 (Weak Password Requirements)
- **File:** internal/auth/manager.go:281 (`CreateUser`); also handlers_auth.go:611-614 (bootstrap admin), :782 (user create), manager.go:563 (UpdateUser), :689 (ChangePassword)
- **Reachability:** Direct (auth HTTP endpoints)
- **Sanitization:** Only non-empty check
- **Framework Protection:** Per-username lockout (5/15min) + per-IP rate limit slow but do not prevent guessing trivially weak passwords
- **Description:** The only password check is "non-empty", including for the first admin (bootstrap). Combined with the public login endpoint, weak passwords enable account takeover.
- **Remediation:** Enforce minimum length (≥12) and basic complexity/breached-password check in CreateUser/ChangePassword/UpdateUser.

### VULN-012: Brute-force lockout check-then-increment is not atomic (concurrent burst bypass)
- **Severity:** Medium
- **Confidence:** 65/100 (Probable)
- **Original Skill:** sc-race (RACE-002)
- **Vulnerability Type:** CWE-362 / CWE-307
- **File:** internal/auth/manager.go:348-376 (`Authenticate`; `isLockedOut` :249, `recordFailedAttempt` :266)
- **Reachability:** Direct (login endpoint)
- **Description:** `isLockedOut` and `recordFailedAttempt` each take/release `loginAttemptsMu` independently, with the slow bcrypt compare in between holding no lock. N concurrent wrong-password requests for one username all pass the lockout check before any records a failure, admitting a burst larger than the 5-attempt ceiling. (The per-IP limiter in audit.go IS atomic, which bounds single-source abuse — partial mitigation.)
- **Remediation:** Combine check-and-increment into one critical section, or use a per-username in-flight gate.

### VULN-013: Default DB root/user passwords in docker-compose
- **Severity:** Medium
- **Confidence:** 65/100 (Probable)
- **Original Skill:** sc-docker (DOCK-002) + sc-secrets (SECRET-001) [merged]
- **Vulnerability Type:** CWE-798 / CWE-1392
- **File:** docker-compose.yml:19,39,42
- **Description:** MariaDB defaults to `uwas_root` / `uwas_wp` when env vars unset; the uwas container also uses `root` + the default password for restore. Bounded because the `db` service publishes no host port (internal bridge only) — confirmed no `ports:` on `db`.
- **Remediation:** Use `${DB_ROOT_PASSWORD:?required}` / Compose secrets; create a least-privilege app DB user instead of using root.

### VULN-014: Template injection of `github.ref_name` into `run:` shell (release workflow)
- **Severity:** Medium
- **Confidence:** 55/100 (Probable)
- **Original Skill:** sc-ci-cd (CICD-001)
- **Vulnerability Type:** CWE-94 (Expression Injection)
- **File:** .github/workflows/release.yml:104,129
- **Description:** `${{ github.ref_name }}` is expanded into the build/publish shell before execution. Git ref names permit shell metacharacters (`$( )` `` ` `` `;` etc.; `${IFS}` for spaces), so a crafted tag can execute on the runner, which holds `contents: write` + GITHUB_TOKEN. Requires repo write to push the tag (compromised-maintainer / privilege-escalation scenario, not anonymous), hence Medium.
- **Remediation:** Pass via `env: REF_NAME: ${{ github.ref_name }}` and reference `${REF_NAME}` in the shell; optionally validate against `^v[0-9]+\.[0-9]+\.[0-9]+`.

### VULN-015: `ci.yml` has no top-level `permissions:` block (excessive default token scope)
- **Severity:** Medium
- **Confidence:** 65/100 (Probable)
- **Original Skill:** sc-ci-cd (CICD-002)
- **Vulnerability Type:** CWE-732
- **File:** .github/workflows/ci.yml (no `permissions:` anywhere)
- **Description:** `release.yml` and `docs.yml` declare explicit `permissions:`; `ci.yml` does not, so its GITHUB_TOKEN inherits the repo/org default while running PR-supplied build/test scripts. GitHub restricts fork-PR tokens to read-only, which holds this to Medium.
- **Remediation:** Add `permissions: { contents: read }` at top level; grant narrower write per-job only where needed.

### VULN-016: Self-service password change via `PUT /auth/users/{username}` bypasses current-password verification
- **Severity:** Medium
- **Confidence:** 58/100 (Probable)
- **Original Skill:** sc-business-logic (BIZ-002)
- **Vulnerability Type:** CWE-620 (Unverified Password Change)
- **File:** internal/admin/handlers_auth.go:831-833,847 → internal/auth/manager.go:563-569
- **Description:** The dedicated change-password endpoint requires/verifies `current_password`, but `handleUserUpdateAuth` accepts a `password` field for the caller's own account with no current-password check, then `UpdateUser` invalidates sessions. A hijacked session is thereby upgraded to persistent takeover (attacker sets a new password without knowing the old one and re-logs-in). Inconsistent control between two routes for the same action.
- **Remediation:** Reject `password` in `handleUserUpdateAuth` for self-service (require the dedicated endpoint) or require+verify `current_password`.

---

## LOW

### VULN-017: Cron monitor exposes other tenants' job command/output
- **Severity:** Low — **Confidence:** 65/100 (Probable)
- **Skill:** sc-authz (AUTHZ-002) + sc-api-security (API-002) [merged] — **CWE-639**
- **File:** internal/admin/handlers_cron.go:14-34
- `GET /api/v1/cron/monitor` (`GetAllStatus`) and `/{host}` (`GetDomainStatus`) return all/any domain's cron `JobStatus` (Command/Schedule/Output) with no domain scoping; the write/execute siblings are admin-gated. Remediation: filter by `canAccessDomain`; gate `{host}` with `requireDomainAccess`.

### VULN-018: Bandwidth stats readable across tenants
- **Severity:** Low — **Confidence:** 65/100 (Probable)
- **Skill:** sc-authz (AUTHZ-003) + sc-api-security (API-003) [merged] — **CWE-639**
- **File:** internal/admin/handlers_bandwidth.go:12-33
- `GET /api/v1/bandwidth` and `/{host}` return per-domain usage unscoped, while `handleBandwidthReset` correctly enforces `requireDomainAccess`. Remediation: filter list by `canAccessDomain`; add `requireDomainAccess` to the get.

### VULN-019: Per-domain analytics readable across tenants
- **Severity:** Low — **Confidence:** 60/100 (Probable)
- **Skill:** sc-authz (AUTHZ-004) — **CWE-639**
- **File:** internal/admin/api.go:877 (analytics handlers take no auth context)
- `GET /api/v1/analytics` / `/{host}` expose every/any domain's traffic analytics to any authenticated user. Remediation: wrap with a per-domain authorization closure or filter by `canAccessDomain`.

### VULN-020: Uptime monitor, alerts, and per-domain stats unscoped
- **Severity:** Low — **Confidence:** 55/100 (Probable)
- **Skill:** sc-authz (AUTHZ-005) — **CWE-639**
- **File:** internal/admin/api.go:887 (`handleMonitor`), :898 (`handleAlerts`), :853 (`handleStatsDomains`)
- Return uptime/alert/request state for all domains unfiltered. Remediation: filter by `canAccessDomain` or restrict to admins.

### VULN-021: Fine-grained RBAC permission model is dead code; `user` role == `reseller` capabilities
- **Severity:** Low — **Confidence:** 60/100 (Probable)
- **Skill:** sc-authz (AUTHZ-006) — **CWE-863**
- **File:** internal/auth/manager.go:101 (`rolePermissions`/`HasPermission` referenced only in interface + tests)
- Enforcement is binary (`requireAdmin` vs `CanManageDomain` membership-only), so a documented read-only `user` can write to/delete any assigned domain. Remediation: enforce `HasPermission` in write handlers, or remove the model and document that any assigned domain grants full management.

### VULN-022: Session token / API key accepted in URL `?token=` query param (legacy fallback)
- **Severity:** Low — **Confidence:** 70/100 (High Probability)
- **Skill:** sc-session (SESS-002) — **CWE-598**
- **File:** internal/admin/api.go:395-416,430-434
- A live session token or long-lived API key in `?token=` leaks to history/Referer/proxy logs *before* it is stripped from `r.URL`. The correct single-use 30s `?ticket=` flow already exists. Remediation: remove the `?token=` fallback; require tickets for SSE/WebSocket.

### VULN-023: `users.session_ttl` config silently ignored — lifetime hardcoded to 24h
- **Severity:** Low — **Confidence:** 80/100 (High Probability)
- **Skill:** sc-session (SESS-001) — **CWE-613**
- **File:** internal/auth/manager.go:398 (`ExpiresAt: time.Now().Add(24*time.Hour)`); config.go:86 (dead field)
- Operators setting `session_ttl` get no effect; the documented control is non-functional. Remediation: thread `SessionTTL` into the manager and use it (clamp, default 24h).

### VULN-024: Admin PIN has no brute-force protection
- **Severity:** Low — **Confidence:** 55/100 (Probable)
- **Skill:** sc-auth (AUTH-004) — **CWE-307**
- **File:** internal/admin/handlers_auth.go:485
- Failed PIN attempts are only audit-logged, not fed to `recordAuthFailure`/lockout; the PIN is often a short numeric code. Requires a valid session/key first, hence Low. Remediation: route PIN failures through a counter with lockout; enforce a minimum PIN length.

### VULN-025: Username enumeration via login timing oracle
- **Severity:** Low — **Confidence:** 55/100 (Probable)
- **Skill:** sc-auth (AUTH-003) — **CWE-208**
- **File:** internal/auth/manager.go:353
- Non-existent user returns before bcrypt; existing user pays the bcrypt cost — a measurable timing difference (the message is correctly generic). Remediation: run a dummy bcrypt compare against a decoy hash on the no-user path.

### VULN-026: Username-keyed login lockout enables targeted account-lockout DoS
- **Severity:** Low — **Confidence:** 48/100 (Possible)
- **Skill:** sc-business-logic (BIZ-003) — **CWE-645**
- **File:** internal/auth/manager.go:249-270,349-358
- Five bad attempts/15min lock a username for everyone including the legitimate user; a distributed attacker can keep a known operator locked out. Availability-only. Remediation: per-(username,IP) buckets or progressive delays; do not count once the password matches.

### VULN-027: Bootstrap admin creation is a check-then-act race (TOCTOU)
- **Severity:** Low — **Confidence:** 45/100 (Possible)
- **Skill:** sc-business-logic (BIZ-004) — **CWE-367**
- **File:** internal/admin/handlers_auth.go:596-622
- The "zero users" check and `CreateUser(RoleAdmin)` are not under one lock, so two concurrent first-run requests can both create admin accounts. Narrow first-run-only window. Remediation: a single atomic `CreateFirstAdmin` under the write lock.

### VULN-028: Admin dashboard SPA + file-content served without frame/CSP headers
- **Severity:** Low — **Confidence:** 65/100 (Probable)
- **Skill:** sc-clickjacking (CLICK-001) + sc-lang-typescript (TS-001) [merged] — **CWE-1021 / CWE-922**
- **File:** internal/admin/routes.go:410-426; internal/admin/api.go:246 (handler chain omits `SecurityHeaders`)
- The dashboard HTML/assets carry no `X-Frame-Options`/CSP. Exploitability is limited because auth is a sessionStorage bearer token (framed instance loads unauthenticated), so this is defense-in-depth — but a CSP would also be the second layer that limits VULN-004's XSS blast radius. Remediation: add `X-Frame-Options: DENY` + strict CSP (`frame-ancestors 'none'; script-src 'self'; object-src 'none'`) to dashboard responses.

### VULN-029: Admin PIN transmitted in WebSocket URL query string
- **Severity:** Low — **Confidence:** 45/100 (Possible)
- **Skill:** sc-websocket (WS-001) — **CWE-598**
- **File:** web/dashboard/src/lib/api.ts:1600; internal/admin/handlers_auth.go:497 (`requirePin` accepts `?pin=`)
- The long-lived PIN is placed in the terminal WS URL, partly defeating the ticket design. Requires log/observer access (admin listener has no default access log). Remediation: bind the PIN into the ticket at mint time, or accept it via `Sec-WebSocket-Protocol`.

### VULN-030: Per-location rate-limiter map grows unbounded (no background sweeper)
- **Severity:** Low — **Confidence:** 58/100 (Probable)
- **Skill:** sc-rate-limiting (RATE-001) — **CWE-401 / CWE-770**
- **File:** internal/server/server_dispatch.go:208-258 (state server.go:154-155)
- Eviction is opportunistic (only when the same key reloads), so one-shot rotating IPs (IPv6 /64, botnet) accrete permanent entries → gradual memory exhaustion, against the very population the limiter defends. Only active when a domain configures per-location `rate_limit`. Remediation: add a janitor goroutine (mirror `middleware.RateLimiter.cleanupLoop`) or key by bounded subnet with LRU cap.

### VULN-031: On-demand domain health check lacks the SSRF guard the background monitor uses
- **Severity:** Low — **Confidence:** 58/100 (Probable)
- **Skill:** sc-ssrf (SSRF-001) — **CWE-918**
- **File:** internal/admin/handlers_domain_health.go:172,201,237-245
- `handleDomainHealth` fetches `http(s)://<domain.Host>/` with no `IsWebhookURLSafe`/loopback/metadata check and no `SafeDialControl`, unlike `internal/monitor/monitor.go:119-126`. `IsValidHostname` accepts bare IPs/`localhost`, so a domain whose Host is `169.254.169.254` turns the endpoint into a semi-blind internal scanner (returns Code/Ms/Error). Admin-gated; non-admins limited to assigned domains. Remediation: apply `config.IsWebhookURLSafe` before fetch and wire `config.SafeDialControl` into the client transport.

### VULN-032: `http.DefaultClient` used without timeout/context in Cloudflare admin handlers
- **Severity:** Low — **Confidence:** 50/100 (Probable)
- **Skill:** sc-lang-go — **CWE-1088 / CWE-400**
- **File:** internal/admin/handlers_cloudflare.go:201,236,342; handlers_cloudflare_zones.go:43,50,123,125
- `http.NewRequest` + `http.DefaultClient.Do` (zero timeout, no request context). A stalled/hostile upstream hangs the goroutine; client disconnect does not cancel. Admin-triggered. Remediation: use a package `*http.Client{Timeout}` + `NewRequestWithContext(r.Context(),...)`.

### VULN-033: Compose services lack runtime hardening
- **Severity:** Low — **Confidence:** 70/100 (High Probability)
- **Skill:** sc-docker (DOCK-004) — **CWE-250 / CWE-732 / CWE-400**
- **File:** docker-compose.yml:1-45
- No `no-new-privileges`, `cap_drop: [ALL]`, `read_only`, `pids_limit`, or resource limits on any service. The uwas image runs non-root, limiting impact. Remediation: add the listed hardening options.

### VULN-034: Base images not pinned by digest
- **Severity:** Low — **Confidence:** 55/100 (Probable)
- **Skill:** sc-docker (DOCK-005) — **CWE-1104 / CWE-829**
- **File:** Dockerfile:2,23 (`golang:1.26-alpine3.24`, `alpine:3.24`)
- Version tags are mutable; pin to `@sha256:...` for reproducibility/supply-chain integrity.

### VULN-035: Per-domain reverse-proxy can disable upstream TLS verification
- **Severity:** Low — **Confidence:** 40/100 (Possible)
- **Skill:** sc-crypto (CRYPTO-001) + sc-lang-go (INFO) [merged] — **CWE-295**
- **File:** internal/handler/proxy/handler.go:81-82 (config proxy.go:18-22)
- `proxy.insecure_skip_verify` (default false, `#nosec`-annotated opt-in) accepts any upstream cert when an operator enables it — MITM on the UWAS→backend hop. Intentional feature for self-signed origins. Remediation: keep default false; prefer a per-domain `RootCAs` bundle; warn/audit when enabled.

---

## INFORMATIONAL (confidence < 50, by-design or hardening-only — retained, not eliminated)

- **INFO-1 (UPLOAD-001, conf 35):** File-manager upload performs no server-side type validation and writes into the executable web root (handlers_files.go:416-434). By-design cPanel-equivalent behavior, gated by per-domain RBAC and the PHP sandbox; no privilege boundary crossed. Optional: per-domain extension allowlist for low-trust deployments.
- **INFO-2 (UPLOAD-002, conf 30):** cPanel tar extraction has per-file 10GB cap but no aggregate size/entry-count cap (migrate/cpanel.go:69-104). Admin+PIN only; disk-exhaustion-by-own-admin. Add cumulative ceiling.
- **INFO-3 (RATE-002, conf 55):** No default global/per-domain rate limit on public web traffic (server.go:699-704). Matches nginx/Caddy norms; login/2FA ARE rate-limited. Ship a conservative default.
- **INFO-4 (CRYPTO-002, conf 30):** WordPress download integrity uses SHA1 (installer.go:261-299) — integrity check over HTTPS from same origin; transport is the real guarantee. wordpress.org publishes only sha1/md5.
- **INFO-5 (WS-002, conf 35):** Interactive root shell over `ws://` on non-TLS deployments (api.ts:1595). Client correctly upgrades to `wss:` on HTTPS — operator TLS-config risk. Optionally refuse upgrade when `r.TLS == nil`.
- **INFO-6 (CICD-003, conf 40):** First-party GitHub actions pinned to mutable major tags, not commit SHAs. Pin to SHAs + Dependabot.
- **INFO-7 (CICD-004, conf 45):** `govulncheck`/`staticcheck` installed from `@latest` in CI. Pin tool versions.
- **INFO-8 (DOCK-006, conf 40):** `.dockerignore` omits `.env`/`*.key`/`*.pem`. Final image copies only the binary + entrypoint + docker/uwas.yaml, so risk is limited to builder layers. Add patterns for defense-in-depth.
- **INFO-9 (cmdi defense-in-depth, n/a):** `internal/deploy/deploy.go:529 validateShellCommand` forbids `&&`/`||` but not a lone `&`, unlike `validateBuildCommand`. Admin-only command runner (no trust boundary crossed); align validators for consistency.

---

## Eliminated / Not Reported (no reachable vulnerability — scanner agreement)

The following scanner classes returned "no issues found" and were verified as having
no reachable attack surface: sc-cmdi (all exec is array-form or admin-only +
validated), sc-deserialization (Go yaml.v3/json/xml are data-only), sc-graphql
(no GraphQL), sc-header-injection (Go net/http + explicit CRLF stripping), sc-jwt
(opaque server-side tokens, no JWT verification), sc-ldap (no LDAP), sc-nosqli
(length-prefixed RESP, no Mongo/ES), sc-open-redirect (admin-config or
vhost-validated targets only), sc-path-traversal (centralized symlink-aware
`pathsafe` containment), sc-sqli (no database/sql; identifier allowlist + value
escaping, admin-only), sc-ssti (no template engine), sc-xxe (encoding/xml is
XXE-safe; trusted inputs only). These were confirmed and intentionally produce no
findings.

Duplicate findings collapsed during merge (10): PRIVESC-001→VULN-001;
BIZ-001+AUTH-002→VULN-006; CSRF-001→VULN-008; API-001→VULN-009; API-002→VULN-017;
API-003→VULN-018; SECRET-001→VULN-013; lang-go-INFO(TLS)→VULN-035;
TS-001→VULN-028; DOCK-003→VULN-005.
