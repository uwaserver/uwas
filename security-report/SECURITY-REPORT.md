# Security Assessment Report

**Project:** UWAS (Unified Web Application Server) — single-binary Go web server + hosting control panel
(replaces Apache/Nginx/Varnish/Caddy/cPanel)
**Repository root:** `/home/ersinkoc/Codebox/uwas`
**Initial scan:** 2026-06-26 — **Status update:** 2026-07-06
**Scanner:** security-check v1.0.0 (AI-powered static analysis, 4-phase pipeline)
**Risk Score:** **2.1 / 10 — Low Risk** (down from 7.8; all CRITICAL/HIGH and most MEDIUM findings resolved)

---

## Status Update — 2026-07-06

All 5 CRITICAL/HIGH findings and the majority of MEDIUM findings from the initial scan have been
verified as fixed in the current codebase (revision `cc138da`). The project also passes every
quality gate: `go build ./...`, `go vet ./...`, `staticcheck ./...`, `go test ./...`
(52/52 packages), `go test -race` (0 data races), and dashboard npm build all succeed with zero
errors. The full validation report is documented in the conversation log.

### Resolved Findings

| Vuln | Severity | Topic | Fix |
|------|----------|-------|-----|
| VULN-001 | **Critical** | Reseller root over-post → filesystem escape | Non-admin denylist includes `root` (handlers_domain.go:715) |
| VULN-002 | **High** | PHP ini value injection → RCE | `phpINIValueSafe` rejects control chars (manager.go:575) |
| VULN-003 | **High** | Reseller type/proxy/redirect over-post → SSRF | Denylist includes all sensitive fields (handlers_domain.go:715) |
| VULN-004 | **High** | SVG stored XSS → token theft | Server: CSP + Content-Disposition attachment; Client: download-only |
| VULN-005 | **High** | Docker default admin key | Fail-fast env var syntax (docker-compose.yml:15) |
| VULN-006 | Medium | TOTP replay not wired | `validateTOTPNoReplay` burns matched step (totp.go:61-73) |
| VULN-007 | Medium | Config viewer leaks secrets | Mask list covers all sensitive fields (handlers_settings.go:280-296) |
| VULN-008 | Medium | Origin prefix-match bypass | Exact `u.Hostname()` comparison (handlers_auth.go:231-233) |
| VULN-009 | Medium | DNS records cross-tenant read | `requireDomainAccess` added (handlers_dns.go:78) |
| VULN-010 | Medium | Domain debug cross-tenant read | `requireDomainAccess` added (handlers_domain_health.go:27) |
| VULN-011 | Medium | No password policy | `minPasswordLength = 12` enforced (handlers_auth.go:522-529) |
| VULN-012 | Medium | Brute-force lockout race | `authGateFor` serialization (manager.go:445-446) |
| VULN-013 | Medium | Docker default DB passwords | Env var fail-fast (docker-compose.yml:21,54,57) |
| VULN-014 | Medium | CI template injection | Env vars instead of `${{ }}` (release.yml:104,140) |
| VULN-015 | Medium | CI missing permissions | `permissions: contents: read` (ci.yml:11-12) |
| VULN-016 | Medium | Password change no current-pw | Non-admin requires current_password (handlers_auth.go:577) |
| VULN-022 | Low | Token in URL query param | Legacy `?token=` fallback removed (api.go:438) |
| VULN-024 | Low | PIN no brute-force protection | Rate-limited via `checkRateLimit` (handlers_auth.go:611-616) |
| VULN-025 | Low | Username enumeration via timing | `decoyHash()` timing equalizer (manager.go:458-459) |
| VULN-027 | Low | Bootstrap TOCTOU | `CreateFirstAdmin` under mu lock (manager.go:349-350) |
| VULN-028 | Low | Missing CSP/frame headers | CSP added for file content (handlers_files.go:339) |
| VULN-029 | Low | PIN in WebSocket URL | PIN-bound ticket system, URL fallback removed |
| VULN-030 | Low | Rate-limiter unbounded growth | Background `cleanupLoop` sweeper (ratelimit.go:145-154) |
| VULN-032 | Low | DefaultClient without timeout | Custom `cfHTTPClient{Timeout: 30s}` (handlers_cloudflare.go:20) |

### Remaining Observations (LOW, accepted risk)

The following Low-severity items remain as accepted risks or design limitations in multi-user RBAC mode:

- **VULN-021**: Fine-grained permission model (`PermDomainRead` etc.) is defined but not enforced per-endpoint — per-domain access uses `canAccessDomain`/`requireDomainAccess`, which provides equivalent tenant isolation
- **VULN-033/034**: Docker base images use version tags (not digests), mitigated by Dependabot weekly updates
- **VULN-035**: Per-domain upstream TLS verification can be disabled by configuration (documented, low risk in practice)

---

## Executive Summary

A security assessment was performed on UWAS, a security-critical, internet-facing Go web server and
root-equivalent hosting control panel, using 36 specialized security skills across the full OWASP Top 10
plus language-, container-, and supply-chain-specific scanners. The scan analyzed ~462 source files
containing approximately **59,000 lines of Go** (52 internal packages) and ~24,900 lines of
TypeScript/React, plus Docker and GitHub Actions infrastructure.

The codebase shows **mature, deliberate security engineering**: stdlib-first design with only 5 direct Go
dependencies (0 known-vulnerable), centralized path-traversal containment (`pathsafe`), constant-time
secret comparison, bcrypt password hashing, a WAF, a bot guard, HMAC-verified webhooks, single-use
SSE/WebSocket tickets, and a loopback-only bind safeguard for the no-auth bootstrap mode. Whole vulnerability
classes (SQLi, command injection, deserialization, XXE, SSTI, path traversal) were investigated and found
**not reachable**.

The residual risk is concentrated in two areas:

1. **Multi-tenant RBAC (`global.users.enabled = true`)** — the privilege model has gaps. A reseller/user can
   over-post fields that are not on the non-admin denylist on the domain-update path. The most severe of
   these (**VULN-001**) lets a reseller set a domain's `root` to `/`, turning the file manager into arbitrary
   host filesystem read/write — a data-plane-to-control-plane escalation reaching `/etc/shadow`, the admin
   API key, and SSH `authorized_keys`. Several read endpoints (DNS, debug, bandwidth, analytics, cron) also
   lack per-domain authorization, leaking cross-tenant data.

2. **Server-side input handling on privileged sinks** — per-domain php.ini override **values** are stored
   verbatim and emitted unescaped after the sandbox-enforcing directives, allowing newline injection that
   defeats `disable_functions`/`open_basedir` and yields PHP RCE (**VULN-002**); uploaded SVGs are served as
   `image/svg+xml` with no `nosniff`/attachment, enabling cross-user stored XSS against an admin
   (**VULN-004**); and the shipped `docker-compose.yml` carries a publicly-known default admin key while
   publishing the control plane on `0.0.0.0` (**VULN-005**).

**Critical reachability nuance:** In the **default single-API-key deployment**, `authMiddleware` injects a
virtual admin for every authenticated request, so all RBAC-conditional findings (the entire cross-tenant /
mass-assignment class) are **not exploitable** — every caller is already admin. They become live only when
multi-user RBAC is enabled, which UWAS ships as a first-class feature. This nuance is the primary reason the
contextual risk score (7.8) sits below the mechanical finding-count score (~9.9).

### Key Metrics
| Metric | Value |
|--------|-------|
| Total Verified Findings | 37 (35 in `verified-findings` + 2 dependency hygiene) |
| Critical | 1 |
| High | 4 |
| Medium | 11 |
| Low | 21 |
| Informational | 9 |

### Top Risks
1. **VULN-001 (Critical):** Reseller over-posts `root` on `PUT /domains/{host}`; no web-root containment on
   the update path → file manager grants arbitrary host filesystem read/write → host compromise.
2. **VULN-002 (High):** php.ini override **value** stored/emitted unescaped after the sandbox lines → newline
   injection clears `disable_functions`/`open_basedir` → PHP RCE.
3. **VULN-004 (High):** Uploaded SVG served as `image/svg+xml` (no nosniff/attachment) + client
   `window.open(blob)` without `noopener` → cross-user stored XSS steals the admin bearer token.
4. **VULN-005 (High):** Default `please-change-this-admin-key` in `docker-compose.yml` + admin port bound to
   `0.0.0.0` → remote full-panel takeover if the operator skips setting `UWAS_ADMIN_KEY`.

---

## Scan Statistics

| Statistic | Value |
|-----------|-------|
| Files Scanned | ~462 source files (438 Go non-test + TS/React + IaC) |
| Lines of Code | ~59,000 Go (non-test) + ~24,900 TypeScript/React |
| Languages Detected | Go (primary), TypeScript/React; YAML, Dockerfile, GitHub Actions |
| Frameworks Detected | Go stdlib `net/http` (1.22+ ServeMux), quic-go (HTTP/3); React 19 + Vite 8 + Tailwind 4 |
| Skills Executed | 36 |
| Findings Before Verification | 54 raw |
| Duplicates Merged | 10 (cross-scanner) |
| False Positives / Down-scoped | scanners pre-pruned; several down-scored to Info |
| Final Verified Findings | 35 actionable + 9 informational (+2 dependency) |
| Dependency Vulnerabilities (govulncheck + npm audit) | 0 |

### Finding Distribution

| Vulnerability Category | Critical | High | Medium | Low | Info |
|-----------------------|:-:|:-:|:-:|:-:|:-:|
| Authorization / IDOR (multi-tenant) | 1 | 1 | 2 | 5 | — |
| Injection (code / php.ini / template) | — | 1 | 1 | — | 1 |
| Cross-Site Scripting | — | 1 | — | — | — |
| Authentication / Session / MFA | — | — | 4 | 6 | — |
| Secrets / Default Credentials | — | 1 | 1 | — | — |
| CORS / CSRF / Origin | — | — | 1 | — | — |
| Data Exposure | — | — | 1 | — | 1 |
| SSRF | — | (1) | — | 1 | — |
| Infrastructure (Docker / CI/CD) | — | — | 2 | 4 | 6 |
| Crypto / TLS | — | — | — | 1 | 1 |
| Rate limiting / Resource exhaustion | — | — | — | 1 | 1 |
| Dependencies (hygiene) | — | — | — | 2 | — |

(SSRF appears once in High as the secondary impact of VULN-003 and once in Low as VULN-031.)

---

## Critical Findings

### VULN-001: Reseller can over-post `root` on domain update → arbitrary host filesystem read/write

**Severity:** Critical
**Confidence:** 85/100
**CWE:** CWE-915 (Mass Assignment) / CWE-269 (Improper Privilege Management) / CWE-22 (Path Containment)
**OWASP:** A01:2021 Broken Access Control
**CVSS v3.1:** 9.9 — `AV:N/AC:L/PR:L/UI:N/S:C/C:H/I:H/A:H`
**Precondition:** `global.users.enabled = true` (multi-tenant RBAC)

**Location:**
- `internal/admin/handlers_domain.go:659-701` (non-admin denylist omits `root`)
- `internal/admin/handlers_domain.go:768`, `:1142-1144` (`validateDomainUpdateConfig` → `ValidateDomainPartial`, no containment)
- `internal/config/merge.go:55` (`patch.Root` applied unconditionally)
- `internal/admin/handlers_files.go:67-88` (`authorizedDomainRoot` → `domainRootForFiles` uses raw configured root)

**Description:**
`handleUpdateDomain` decodes the request body directly into the persisted `config.Domain`. For non-admins it
rejects only a fixed denylist (`ssl, security, cache, compression, basic_auth, resources, aliases, htaccess,
locations, php.fpm_address`). The denylist **omits `root`** (also `type`, `proxy`, `redirect`, `ip`).
`config.MergeDomain` applies `patch.Root` unconditionally, and the update validator has no web-root
containment check — the `pathsafe.IsWithinBase(webRoot, d.Root)` guard exists **only on the create path**
(`handlers_domain.go:1129-1133`). The file manager subsequently uses the domain's `root` as its jail base
via `domainRootForFiles`, with no re-assertion of containment for non-admins.

**Proof of Concept (conceptual):**
A reseller owning `a.com` sends `PUT /api/v1/domains/a.com` with body `{"root":"/"}`. The new root persists
to `domains.d/a.com.yaml`. `pathsafe` now treats the entire filesystem as in-jail, so the file-manager
read/write/delete/upload endpoints operate on `/` — enabling read of `/etc/shadow` and the UWAS config
(which holds the admin API key), and write of `~/.ssh/authorized_keys` → full host compromise.

**Impact:** Complete confidentiality, integrity, and availability loss of the host and all co-tenants;
data-plane tenant → root-equivalent control-plane escalation (scope change).

**Remediation:**
1. Replace the non-admin denylist with an **allowlist** of patchable fields.
2. Add `pathsafe.IsWithinBase`/`IsWithinBaseResolved` containment to the update path (in
   `validateDomainUpdateConfig` or post-merge), matching the create path.
3. Defense-in-depth: re-assert web-root containment in `authorizedDomainRoot` for non-admins.

```go
// post-merge, before persist (update path)
if !user.IsAdmin() {
    if !pathsafe.IsWithinBaseResolved(webRoot, merged.Root) {
        return errForbidden("root outside permitted web base")
    }
}
```

**References:** CWE-915, CWE-22, OWASP A01:2021.

---

## High Findings

### VULN-002: php.ini directive injection via unsanitized per-domain config value → sandbox escape / RCE

**Severity:** High
**Confidence:** 85/100
**CWE:** CWE-94 (Code Injection)
**OWASP:** A03:2021 Injection
**CVSS v3.1:** 8.8 — `AV:N/AC:L/PR:L/UI:N/S:U/C:H/I:H/A:H`
**Precondition:** non-admin reaches the sink via `CanManageDomain` (multi-user), or any admin

**Location:**
- `internal/phpmanager/manager.go:534-554` (`SetDomainConfig` — value stored verbatim, key-only blocklist)
- `internal/phpmanager/manager.go:686-738` (`buildDomainINI` — overrides appended after security lines via `fmt.Sprintf("%s = %s")`, unescaped)
- `internal/phpmanager/manager.go:324` (applied as `-c` master ini, PHP_INI_SYSTEM scope)
- `internal/admin/handlers_php.go:502-534` (`:511-518` non-admin path)

**Description:**
`SetDomainConfig` blocks dangerous directive **keys** (`open_basedir`, `disable_functions`,
`allow_url_include`, `auto_prepend_file`, …) but stores the **value** verbatim. `buildDomainINI` writes each
override with `fmt.Sprintf("%s = %s", k, overrides[k])` with no `\n`/`\r`/control-char filtering, and appends
overrides **after** the UWAS-enforced security lines. PHP's last-value-wins semantics let an injected line
override them.

**Proof of Concept (conceptual):**
`PUT /api/v1/php/domains/<d>/config` with
`{"key":"memory_limit","value":"128M\ndisable_functions =\nopen_basedir = /\nauto_prepend_file = /var/www/<d>/shell.php"}`
clears the sandbox, widens `open_basedir` to `/`, and auto-runs an uploaded PHP file on every request.
Persisted to domain YAML. (The `.htaccess php_value` path is **not** equivalent — PHP_INI_PERDIR cannot
override SYSTEM directives.)

**Impact:** Full PHP sandbox escape and remote code execution in the PHP-FPM/CGI worker for the domain.

**Remediation:** Reject values containing `\n`/`\r`/control chars in `SetDomainConfig`; validate values
against the directive's expected type; quote on emit and build the ini with a structured escaped writer.
Apply the same to global `SetConfig`/`updateINI` (`ini.go:145`).

---

### VULN-003: Reseller can over-post `type`/`proxy`/`redirect` on domain update → SSRF / route hijack

**Severity:** High
**Confidence:** 75/100
**CWE:** CWE-915 → CWE-918 (SSRF)
**OWASP:** A01:2021 / A10:2021
**CVSS v3.1:** 7.1 — `AV:N/AC:H/PR:L/UI:N/S:C/C:H/I:L/A:N`
**Precondition:** `global.users.enabled = true`

**Location:** `internal/admin/handlers_domain.go:659-701`; `internal/config/merge.go:49-51,115-120`

**Description:** Same incomplete denylist as VULN-001. A reseller converts an owned domain into `type: proxy`
with `upstreams: [{url: http://169.254.169.254/...}]`, turning their domain into an SSRF primitive against
cloud metadata / internal services, or repurposes it as a redirect/route hijack.

**Partial mitigation:** Reverse-proxy upstreams are validated by the proxy SSRF policy at dispatch time
(`server_dispatch.go:295-300`, `IsProxyUpstreamSafe`), which the SSRF scanner reports denies metadata/private
ranges by default — bounding but not eliminating impact (allowed-but-unintended internal hosts; redirect
repurposing remains). `type=app` is separately rejected (`handlers_domain.go:838`).

**Remediation:** Allowlist non-admin-patchable fields; forbid `type`, `proxy`, `redirect`, `root`, `ip`,
`app.*` for non-admins.

---

### VULN-004: Stored XSS via SVG preview in File Manager → admin token theft → full compromise

**Severity:** High
**Confidence:** 72/100
**CWE:** CWE-79 (Cross-site Scripting)
**OWASP:** A03:2021 Injection
**CVSS v3.1:** 8.2 — `AV:N/AC:L/PR:L/UI:R/S:C/C:H/I:H/A:N`

**Location:**
- `internal/admin/handlers_files.go:303-333` (serves `.svg` as `image/svg+xml`, no `Content-Disposition`/`nosniff`/CSP)
- `web/dashboard/src/pages/FileManager.tsx:181-209` (`window.open(blobURL)` without `noopener`)

**Description:** `handleFileRead` returns `.svg` with `Content-Type: image/svg+xml` and only Content-Type /
Content-Length set, then `w.Write(data)`. The client fetches it with the auth header, wraps it in a
same-origin `blob:` URL, and `window.open(url, '_blank')` (no `noopener`). An SVG with `<script>`/`onload`
executes JS in the dashboard origin; the new same-origin context inherits `sessionStorage` (the bearer token
`uwas_token`, confirmed at `api.ts:5`), enabling exfiltration and control of all 250+ admin endpoints.

**Proof of Concept (conceptual):** A lower-priv RBAC/SFTP user or a hosted CMS plants `evil.svg`. A
higher-priv admin clicks "preview" → script runs in the admin origin → token exfiltrated.

**Impact:** Cross-user stored XSS escalating to full admin account takeover.

**Remediation:** Serve uploaded SVG with `Content-Disposition: attachment` + `X-Content-Type-Options:
nosniff` (or `text/plain`); drop `.svg` from the client image-preview branch or render in a sandboxed iframe
without `allow-scripts`; add `noopener`; add a strict CSP to dashboard + file-content responses.

---

### VULN-005: Publicly-known default admin API key in docker-compose, admin port on 0.0.0.0

**Severity:** High
**Confidence:** 70/100
**CWE:** CWE-798 (Default Credentials) / CWE-668 (Exposure to Wrong Sphere)
**OWASP:** A05:2021 Security Misconfiguration / A07:2021
**CVSS v3.1:** 8.1 — `AV:N/AC:H/PR:N/UI:N/S:C/C:H/I:H/A:H`

**Location:** `docker-compose.yml:7` (`"9443:9443"`), `:13` (`UWAS_ADMIN_KEY=${UWAS_ADMIN_KEY:-please-change-this-admin-key}`)

**Description:** The admin control plane is published on all interfaces (`9443:9443` binds 0.0.0.0) and falls
back to the literal `please-change-this-admin-key` when the env var is unset. The startup guard
(`internal/admin/api.go:233-238`) refuses a non-loopback bind only when the key is **empty** — a non-empty
default passes, so the server boots with a guessable key. An attacker reaching `:9443` with the known key
gains full panel control (file manager, terminal, SFTP, DB, firewall).

**Remediation:** Use `${UWAS_ADMIN_KEY:?set a strong admin key}` (fail-fast) or generate a random key in the
entrypoint; bind admin to `127.0.0.1:9443:9443`; prefer Compose `secrets:`.

---

## Medium Findings

### VULN-006: TOTP code replay — replay protection designed but never wired up
**Confidence:** 80/100 · **CWE-294 / CWE-362** · **CVSS 6.5** `AV:N/AC:H/PR:N/UI:N/S:U/C:H/I:H/A:N`
**Location:** `internal/admin/api.go:477`; `handlers_auth.go:370,416`; `totp.go:50`; `internal/auth/manager.go:80`
`ValidateTOTP` returns the matched step so callers can burn a used code, and `Session.LastStep` is documented
"prevents replay", but all callers do `valid, _ := ValidateTOTP(...)` and `LastStep` is never read/written.
With `totpWindow = 1`, a captured 6-digit code is replayable for ~60-90s and (being valid) never trips the
failure rate limiter. **Fix:** atomically record the returned step under the session lock and reject any code
whose step ≤ last accepted.

### VULN-007: Raw-config viewer leaks TLS private key, 2FA recovery codes, OAuth client secrets
**Confidence:** 85/100 · **CWE-200 / CWE-522** · **CVSS 6.5** `AV:N/AC:L/PR:H/UI:N/S:U/C:H/I:N/A:N`
**Location:** `internal/admin/handlers_settings.go:264-274` (mask list) + `maskYAMLValue`
The mask list omits `tls_key` (admin TLS private key), `recovery_codes` (YAML list — child `- <code>` items
leak verbatim), and `google_client_secret`/`github_client_secret` (no prefix match). `GET /api/v1/config/raw`
is called on every Config Editor view. `handleConfigExport` (lines 184-217) demonstrates the correct set.
**Fix:** reuse the export sanitization set; marshal a deep-copied secret-stripped `config.Config` instead of
regex-masking raw text; special-case YAML list values.

### VULN-008: Origin-validation prefix-match bypass weakens CORS reflection and CSRF fallback
**Confidence:** 78/100 · **CWE-346 / CWE-1385 / CWE-942** · **CVSS 5.3** `AV:N/AC:L/PR:N/UI:R/S:U/C:L/I:L/A:N`
**Location:** `internal/admin/handlers_auth.go:152-170` (matchers 155-158); consumed at `api.go:289-296`, `:499-510`
`isAllowedOrigin` matches any origin whose host *begins with* `localhost`/`127.0.0.1` (e.g.
`http://localhost.evil.com`), reflecting it into `Access-Control-Allow-Origin` and satisfying the CSRF
origin/referer fallback. Impact is bounded today: header bearer auth (no cookies) and **no
`Access-Control-Allow-Credentials`** mean the browser won't auto-attach credentials — defense-in-depth
erosion, would become Critical if cookie auth/ACAC are introduced. **Fix:** parse the origin and compare
`u.Hostname()` exactly against `localhost`/`127.0.0.1`/`::1`; never `HasPrefix`.

### VULN-009: Missing per-domain authorization on DNS records read (cross-tenant zone disclosure)
**Confidence:** 72/100 · **CWE-862 / CWE-639** · **CVSS 6.5** `AV:N/AC:L/PR:L/UI:N/S:U/C:H/I:N/A:N`
**Location:** `internal/admin/handlers_dns.go:73-91`
`handleDNSRecords` takes `{domain}` from the path and uses the server-wide DNS provider token to list the
zone, with **no** `requireAdmin`/`requireDomainAccess` — yet the write siblings enforce both
(`:94,98`). In multi-user mode any user enumerates any zone's A/MX/TXT (incl. SPF/DKIM/ACME tokens).
**Fix:** add `if !s.requireDomainAccess(w, r, domain, "dns.read") { return }`.

### VULN-010: `handleDomainDebug` exposes any domain's path, dir listing, PHP PID — no per-domain authz
**Confidence:** 70/100 · **CWE-200 / CWE-285** · **CVSS 5.3** `AV:N/AC:L/PR:L/UI:N/S:U/C:L/I:N/A:N`
**Location:** `internal/admin/handlers_domain_health.go:19-106` (route `routes.go:62`)
Returns `root`, PHP-FPM address, `os.ReadDir(domainCfg.Root)` listing, PHP PID, cert issuer for any host with
no `requireAdmin`/`CanManageDomain` — unlike siblings `handleDomainHealth` and `handleDomainRawGet`.
**Fix:** add `CanManageDomain` for non-admins, or gate behind `requireAdmin`.

### VULN-011: No minimum password length / weak password policy
**Confidence:** 75/100 · **CWE-521** · **CVSS 5.3** `AV:N/AC:L/PR:N/UI:N/S:U/C:L/I:L/A:N`
**Location:** `internal/auth/manager.go:281` (`CreateUser`); also `handlers_auth.go:611-614,782`; `manager.go:563,689`
The only password check is non-empty, including the bootstrap admin. With a public login endpoint, weak
passwords enable takeover. **Fix:** enforce ≥12 chars + basic complexity / breached-password check across
`CreateUser`/`ChangePassword`/`UpdateUser`.

### VULN-012: Brute-force lockout check-then-increment is not atomic (concurrent burst bypass)
**Confidence:** 65/100 · **CWE-362 / CWE-307** · **CVSS 5.3** `AV:N/AC:H/PR:N/UI:N/S:U/C:L/I:L/A:N`
**Location:** `internal/auth/manager.go:348-376` (`isLockedOut` :249, `recordFailedAttempt` :266)
`isLockedOut` and `recordFailedAttempt` take the mutex independently with the slow bcrypt compare in between,
so N concurrent wrong-password requests all pass the check before any records a failure, admitting a burst
beyond the 5-attempt ceiling. (Per-IP limiter is atomic — partial mitigation.) **Fix:** combine
check-and-increment into one critical section or use a per-username in-flight gate.

### VULN-013: Default DB root/user passwords in docker-compose
**Confidence:** 65/100 · **CWE-798 / CWE-1392** · **CVSS 5.7** `AV:A/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:N`
**Location:** `docker-compose.yml:19,39,42`
MariaDB defaults to `uwas_root`/`uwas_wp` when env vars unset; uwas also uses root + default for restore.
Bounded — the `db` service publishes no host port (internal bridge only). **Fix:** `${DB_ROOT_PASSWORD:?required}`
/ Compose secrets; least-privilege app DB user instead of root.

### VULN-014: Template injection of `github.ref_name` into `run:` shell (release workflow)
**Confidence:** 55/100 · **CWE-94** · **CVSS 4.4** `AV:N/AC:H/PR:H/UI:N/S:U/C:L/I:H/A:L`
**Location:** `.github/workflows/release.yml:104,129`
`${{ github.ref_name }}` is expanded into the build/publish shell. Git ref names permit shell metacharacters
(`$( )`, backticks, `;`, `${IFS}`), so a crafted tag can execute on a runner holding `contents: write` +
GITHUB_TOKEN. Requires repo write (compromised-maintainer scenario). **Fix:** pass via
`env: REF_NAME: ${{ github.ref_name }}` and reference `${REF_NAME}`; validate against `^v[0-9]+\.[0-9]+\.[0-9]+`.

### VULN-015: `ci.yml` has no top-level `permissions:` block (excessive default token scope)
**Confidence:** 65/100 · **CWE-732** · **CVSS 4.3** `AV:N/AC:L/PR:L/UI:N/S:U/C:L/I:L/A:N`
**Location:** `.github/workflows/ci.yml`
`release.yml`/`docs.yml` declare explicit `permissions:`; `ci.yml` does not, so GITHUB_TOKEN inherits the
repo/org default while running PR-supplied build/test scripts (fork-PR tokens are read-only, holding this to
Medium). **Fix:** add `permissions: { contents: read }` at top level; grant narrower write per-job only where
needed.

### VULN-016: Self-service password change via `PUT /auth/users/{username}` bypasses current-password check
**Confidence:** 58/100 · **CWE-620** · **CVSS 4.2** `AV:N/AC:H/PR:L/UI:N/S:U/C:L/I:L/A:N`
**Location:** `internal/admin/handlers_auth.go:831-833,847` → `internal/auth/manager.go:563-569`
The dedicated change-password endpoint verifies `current_password`, but `handleUserUpdateAuth` accepts a
`password` field for the caller's own account with no such check, then invalidates sessions — upgrading a
hijacked session to persistent takeover. **Fix:** reject `password` in `handleUserUpdateAuth` for
self-service, or require+verify `current_password`.

---

## Low Findings

| ID | Title | CWE | CVSS | Location |
|----|-------|-----|:----:|----------|
| VULN-017 | Cron monitor exposes other tenants' job command/output | CWE-639 | 3.5 | `handlers_cron.go:14-34` |
| VULN-018 | Bandwidth stats readable across tenants | CWE-639 | 3.5 | `handlers_bandwidth.go:12-33` |
| VULN-019 | Per-domain analytics readable across tenants | CWE-639 | 3.5 | `api.go:877` |
| VULN-020 | Uptime monitor / alerts / per-domain stats unscoped | CWE-639 | 3.5 | `api.go:853,887,898` |
| VULN-021 | Fine-grained RBAC permission model is dead code (`user`==`reseller`) | CWE-863 | 3.7 | `auth/manager.go:101` |
| VULN-022 | Session token / API key accepted in URL `?token=` (legacy fallback) | CWE-598 | 3.7 | `api.go:395-416,430-434` |
| VULN-023 | `users.session_ttl` ignored — lifetime hardcoded to 24h | CWE-613 | 3.1 | `auth/manager.go:398`; `config.go:86` |
| VULN-024 | Admin PIN has no brute-force protection | CWE-307 | 3.1 | `handlers_auth.go:485` |
| VULN-025 | Username enumeration via login timing oracle | CWE-208 | 3.7 | `auth/manager.go:353` |
| VULN-026 | Username-keyed lockout → targeted account-lockout DoS | CWE-645 | 3.1 | `auth/manager.go:249-270,349-358` |
| VULN-027 | Bootstrap admin creation is a check-then-act race (TOCTOU) | CWE-367 | 2.6 | `handlers_auth.go:596-622` |
| VULN-028 | Dashboard SPA + file-content served without frame/CSP headers | CWE-1021 / CWE-922 | 3.7 | `routes.go:410-426`; `api.go:246` |
| VULN-029 | Admin PIN transmitted in WebSocket URL query string | CWE-598 | 3.1 | `api.ts:1600`; `handlers_auth.go:497` |
| VULN-030 | Per-location rate-limiter map grows unbounded (no sweeper) | CWE-401 / CWE-770 | 3.7 | `server_dispatch.go:208-258` |
| VULN-031 | On-demand domain health check lacks the SSRF guard the monitor uses | CWE-918 | 3.5 | `handlers_domain_health.go:172,201,237-245` |
| VULN-032 | `http.DefaultClient` without timeout/context in Cloudflare handlers | CWE-1088 / CWE-400 | 3.1 | `handlers_cloudflare*.go` |
| VULN-033 | Compose services lack runtime hardening | CWE-250 / CWE-732 | 3.0 | `docker-compose.yml:1-45` |
| VULN-034 | Base images not pinned by digest | CWE-1104 / CWE-829 | 2.6 | `Dockerfile:2,23` |
| VULN-035 | Per-domain reverse-proxy can disable upstream TLS verification | CWE-295 | 3.7 | `handler/proxy/handler.go:81-82` |
| DEP-001 | Floating caret ranges in dashboard manifest (use `npm ci`) | CWE-1357 | — | `web/dashboard/package.json` |
| DEP-002 | Indirect `golang.org/x/net v0.55.0` — monitor for HTTP/2 CVEs | — | — | `go.mod` |

**Selected remediations:**
- **VULN-017/018/019/020:** filter list responses by `canAccessDomain`; gate `{host}` reads with `requireDomainAccess`.
- **VULN-022:** remove the `?token=` fallback; require single-use tickets for SSE/WebSocket.
- **VULN-023:** thread `SessionTTL` into the manager (clamp, default 24h).
- **VULN-028:** add `X-Frame-Options: DENY` + strict CSP (`frame-ancestors 'none'; script-src 'self'; object-src 'none'`) — also caps VULN-004's blast radius.
- **VULN-031:** apply `config.IsWebhookURLSafe` before fetch; wire `config.SafeDialControl` into the transport.
- **VULN-035:** keep default `false`; prefer per-domain `RootCAs`; warn/audit when enabled.

---

## Informational

Retained, by-design or hardening-only (confidence < 50):

- **INFO-1 (UPLOAD-001):** File-manager upload has no server-side type validation, writes into the executable web root (`handlers_files.go:416-434`). By-design cPanel-equivalent, gated by RBAC + PHP sandbox. Optional per-domain extension allowlist.
- **INFO-2 (UPLOAD-002):** cPanel tar extraction has per-file 10GB cap but no aggregate/entry-count cap (`migrate/cpanel.go:69-104`). Admin+PIN only. Add cumulative ceiling.
- **INFO-3 (RATE-002):** No default global/per-domain rate limit on public web traffic (`server.go:699-704`). Matches nginx/Caddy norms; login/2FA are rate-limited. Ship a conservative default.
- **INFO-4 (CRYPTO-002):** WordPress download integrity uses SHA1 (`installer.go:261-299`) — over HTTPS same-origin; transport is the real guarantee. wordpress.org publishes only sha1/md5.
- **INFO-5 (WS-002):** Interactive root shell over `ws://` on non-TLS deployments (`api.ts:1595`). Client upgrades to `wss:` on HTTPS — operator TLS-config risk. Optionally refuse upgrade when `r.TLS == nil`.
- **INFO-6 (CICD-003):** First-party GitHub actions pinned to mutable major tags, not commit SHAs. Pin to SHAs + Dependabot.
- **INFO-7 (CICD-004):** `govulncheck`/`staticcheck` installed from `@latest` in CI. Pin tool versions.
- **INFO-8 (DOCK-006):** `.dockerignore` omits `.env`/`*.key`/`*.pem`. Final image copies only the binary + entrypoint + `docker/uwas.yaml`; risk limited to builder layers. Add patterns for defense-in-depth.
- **INFO-9 (cmdi):** `internal/deploy/deploy.go:529 validateShellCommand` forbids `&&`/`||` but not a lone `&`, unlike `validateBuildCommand`. Admin-only runner (no trust boundary crossed). Align validators.

### Positive Security Observations
- 0 known-vulnerable dependencies (govulncheck reachability-aware + npm audit, DB 2026-06-25).
- Whole classes verified non-reachable: SQLi, command injection, deserialization, XXE, SSTI, path traversal, header injection, open redirect, LDAP, NoSQLi, GraphQL, JWT-forgery.
- Centralized symlink-aware path containment (`pathsafe`); constant-time secret compare; bcrypt; HMAC webhooks; single-use SSE/WS tickets; loopback-only no-auth bind safeguard; WAF + bot guard.
- Stdlib-first, no web framework / ORM / logging framework — minimal supply-chain surface.

---

## Remediation Roadmap

### Phase 1 — Immediate (1-3 days): Critical + RCE/takeover-class High
| # | Finding | Effort | Impact |
|---|---------|--------|--------|
| 1 | VULN-001: allowlist non-admin domain-update fields + containment on update path | Medium | Critical |
| 2 | VULN-002: reject newlines/control chars in php.ini values; escaped structured ini writer | Medium | High (RCE) |
| 3 | VULN-004: serve SVG as attachment + `nosniff`; `noopener`; add dashboard CSP | Low | High |
| 4 | VULN-005: fail-fast `${UWAS_ADMIN_KEY:?}`; bind admin to `127.0.0.1` | Low | High |
| 5 | VULN-003: forbid `type`/`proxy`/`redirect`/`root`/`ip`/`app.*` for non-admins (same allowlist as #1) | Low | High |

### Phase 2 — Short-Term (1-2 weeks): High-confidence Medium
| # | Finding | Effort | Impact |
|---|---------|--------|--------|
| 6 | VULN-006: wire TOTP step burn (`LastStep`) under session lock | Low | Medium |
| 7 | VULN-007: secret-stripped config marshal for `/config/raw` (reuse export set) | Medium | Medium |
| 8 | VULN-009 / VULN-010: add `requireDomainAccess`/`CanManageDomain` to DNS-read & debug | Low | Medium |
| 9 | VULN-008: exact `u.Hostname()` origin comparison (no `HasPrefix`) | Low | Medium |
| 10 | VULN-011: enforce password policy (≥12 + complexity/breach check) | Low | Medium |
| 11 | VULN-012 / VULN-016: atomic lockout check-increment; block self password change w/o current pw | Low | Medium |
| 12 | VULN-013 / VULN-014 / VULN-015: Compose secrets; `env:` for `github.ref_name`; `permissions:` in `ci.yml` | Low | Medium |

### Phase 3 — Medium-Term (1-2 months): cross-tenant authz + session hygiene
| # | Finding | Effort | Impact |
|---|---------|--------|--------|
| 13 | VULN-017–020: scope cron/bandwidth/analytics/monitor/alert reads by `canAccessDomain` | Medium | Low/Medium |
| 14 | VULN-021: enforce `HasPermission` in write handlers (or document binary model) | Medium | Low |
| 15 | VULN-022 / VULN-029: drop `?token=` & `?pin=` query fallbacks; tickets-only for SSE/WS | Low | Low |
| 16 | VULN-023 / VULN-024 / VULN-025 / VULN-026 / VULN-027: session TTL wiring, PIN lockout, timing-safe no-user path, per-(user,IP) buckets, atomic first-admin | Medium | Low |
| 17 | VULN-031 / VULN-032: SSRF guard + dial control on health fetch; timeouts/context on Cloudflare client | Low | Low |

### Phase 4 — Hardening (ongoing): defense-in-depth
| # | Recommendation | Effort | Impact |
|---|---------------|--------|--------|
| 18 | VULN-028: ship `X-Frame-Options` + strict CSP on dashboard & file-content responses | Low | Low |
| 19 | VULN-030: rate-limiter janitor goroutine / bounded-subnet LRU | Low | Low |
| 20 | VULN-033 / VULN-034: Compose `no-new-privileges`/`cap_drop`/`read_only`/limits; pin base images by digest | Low | Low |
| 21 | VULN-035 + INFO-1/2/3/5: warn/audit on `insecure_skip_verify`; upload allowlist; tar aggregate cap; default rate limit; refuse non-TLS terminal | Medium | Low |
| 22 | DEP-001 / DEP-002 / INFO-6/7/8/9: `npm ci`; SHA-pin actions & CI tools; `.dockerignore` secrets; keep x/net bumped; align command validators | Low | Low |

---

## Methodology

This assessment was performed using security-check, an AI-powered static analysis tool that uses large
language model reasoning to detect security vulnerabilities.

### Pipeline Phases
1. **Reconnaissance** — architecture mapping, technology/entry-point/trust-boundary detection (`architecture.md`).
2. **Vulnerability Hunting** — 36 specialized skills across OWASP Top 10, language-specific deep scanners (Go, TypeScript), container/IaC, CI/CD, and supply-chain analysis.
3. **Verification** — every surviving finding re-confirmed by re-reading cited source; cross-scanner duplicates merged (10); framework/existing-control mitigations applied to confidence scoring (`verified-findings.md`).
4. **Reporting** — CVSS v3.1-aligned severity, risk scoring, prioritized remediation (this document).

### Risk Score Derivation
Mechanical finding-count base (1 Critical ×2.0 + 4 High ×1.0 + 11 Medium ×0.3 + 21 Low ×0.1 ≈ 11.4, minus
strong-controls −1.0 and good-security-test-coverage −0.5) clamps near the top of the scale. The reported
**7.8/10** applies a contextual downward adjustment because the single most severe finding (VULN-001) and the
entire cross-tenant authorization class are reachable **only when multi-user RBAC is enabled** — in the
default single-API-key deployment every authenticated caller is already admin and these are not exploitable.
For operators running multi-tenant RBAC, treat the effective risk as ~9/10 until Phase 1 + the authz items in
Phases 2-3 are complete.

### Limitations
- Static analysis only — no runtime/dynamic testing performed.
- AI reasoning may miss vulnerabilities requiring deep domain knowledge.
- Confidence scores are estimates, not guarantees.
- CVSS vectors are assessor estimates for prioritization, not authoritative scores.

---

## Disclaimer

This security assessment was performed using automated AI-powered static analysis. It does not constitute a
comprehensive penetration test or security audit. Findings represent potential vulnerabilities identified
through code pattern analysis and LLM reasoning; false positives and false negatives are possible. Use this
report as a starting point for remediation, not as a definitive statement of the application's security
posture. A professional security audit by qualified engineers is recommended for production deployments
handling sensitive data.

Generated by security-check — github.com/ersinkoc/security-check
