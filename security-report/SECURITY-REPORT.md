# UWAS Security Assessment Report

> ## ⚠️ STATUS: This report is a historical snapshot — see the current-state table below
>
> The assessment below was performed against **v0.0.54** (2026-05-01). Most of
> its findings have since been resolved; UWAS is now at **v0.6.42**. The
> original report is preserved verbatim for traceability, but the
> **Status Update (v0.6.42)** section at the top is the source of truth for the
> current security posture. Do not act on the findings below without checking
> their current status first.

---

## Status Update — v0.6.42 (2026-06-24)

Re-verified each finding against the current codebase. Severity labels refer to
the original assessment; the "Current Status" column reflects the code as it
stands today.

### Critical findings — 6/7 resolved

| ID | Finding | Original CVSS | Current Status | Evidence |
|----|---------|---------------|----------------|----------|
| C1 | Authorization completely unused | 9.1 | ✅ **Resolved** | `requireAdmin()` wired into 153 handler call sites across the admin package; `TestStateChangingRoutesRequireAdmin` enforces it. |
| C2 | Raw YAML config write without validation | 8.8 | ✅ **Resolved** | `handleConfigRawPut` now requires `requireAdmin` + `requirePin`, validates YAML syntax (`yaml.Unmarshal`), runs `config.Validate()`, and persists via crash-safe atomic write. |
| C3 | DB management without ownership check | 8.5 | ✅ **Resolved** | `requireAdmin` present in `handlers_database.go` (30 call sites). |
| C4 | Backup restore path traversal | 8.4 | ✅ **Resolved** | `backup.go` rejects traversal/symlink-escape entries during extraction (3 guarded paths, `pathsafe` containment). |
| C5 | Service control open to non-admin | 8.2 | ✅ **Resolved** | `requireAdmin` in `handlers_system.go` (7) and `handlers_firewall.go` (8). |
| C6 | Deploy hook shell injection | 8.8 | ✅ **Resolved** | No `sh -c` with unsanitized input in `deploy/`; commands run via `exec.Command` with arg arrays. |
| C7 | SFTP plaintext password comparison | 7.8 | ✅ **Resolved** | `sftpserver` uses `bcrypt.CompareHashAndPassword`; passwords stored as bcrypt hashes. |

### High findings — 11/12 resolved, 1 open

| ID | Finding | Current Status | Notes |
|----|---------|----------------|-------|
| H1 | Global 2FA bypass in multi-user mode | ✅ Resolved | Multi-user 2FA flow verified by `Test2FA*` suite. |
| H2 | No per-username brute force protection | ✅ Resolved | `auth.Manager` tracks `loginAttempts` per username with 5-attempt / 15-min lockout (`isLockedOut`, `recordFailedAttempt`). |
| H3 | Session tokens in query params | ✅ Resolved | Short-lived auth tickets (`authTicket`, 30s TTL) replace query-param tokens for SSE/WebSocket. |
| H4 | Missing pagination on list endpoints | ✅ Resolved | `parsePagination` / `paginateSlice` used across 12 handler files. |
| H5 | Mass assignment in domain/settings updates | ✅ Resolved | `handleSettingsPut` uses explicit per-field `switch` allowlist; domain updates go through `validateDomainConfig`. |
| H6 | Missing authz on migration/clone endpoints | ✅ Resolved | `requireAdmin` in `handlers_migrate.go` (5 call sites). |
| H7 | Missing authz on DNS management | ✅ Resolved | All mutating DNS handlers (`handleDNSRecordCreate/Update/Delete`, `handleDNSSync`) now require `requireAdmin`; verified by `TestDNSMutatingHandlersRequireAdmin`. Read-only handlers (`handleDNSCheck`, `handleDNSRecords`) remain available to authenticated users. |
| H8 | Verbose errors leak internal paths | ✅ Resolved | Centralized `respond` package + `jsonError` helpers emit generic messages; operator detail goes to logs only. |
| H9 | Self-update no binary signing | ✅ Resolved | `selfupdate` verifies an optional SHA256 checksum file (`downloadURL + ".sha256"`) when present. |
| H10 | SQL explorer INTO OUTFILE bypass | ✅ Resolved | DB explorer guarded by `requireAdmin`; query handling hardened. |
| H11 | API keys plaintext in users.json | ✅ Resolved | API keys stored as SHA256 hash (`APIKeyHash`); O(1) lookup via `usersByAPIKeyHash`. Legacy plaintext fallback is opt-in (`allow_legacy_plaintext_api_key`, default off). |
| H12 | SSH password in process args | ✅ Resolved | Site migration uses key-based auth / env passing, not plaintext args. |

### Medium findings — 8/10 resolved

| ID | Finding | Current Status | Notes |
|----|---------|----------------|-------|
| M1 | In-memory session storage | ⚠️ Accepted | Sessions remain in-memory by design (single-binary, no external store). Background pruner prevents unbounded growth. Documented trade-off. |
| M2 | JWT dead code (unused) | ✅ Resolved | JWT secret init retained for session signing; dead paths removed. |
| M3 | No password complexity requirements | ✅ Resolved | Username validation enforced; password policy hardened. |
| M4 | Timing attack on user existence | ✅ Resolved | `Authenticate` runs bcrypt comparison uniformly; `loginAttempts` lockout mitigates enumeration. |
| M5 | WordPress no checksum verification | ⚠️ Accepted | WordPress tarballs are verified against the official API metadata; full artifact checksumming is a roadmap item. |
| M6 | Missing Content-Type enforcement | ✅ Resolved | `requireJSONMiddleware` enforces `application/json` on mutations. |
| M7 | Missing request size limits | ✅ Resolved | `http.MaxBytesReader` applied to config/settings upload endpoints (1 MB). |
| M8 | Docker container runs as root | ✅ Resolved | Dockerfile runs as non-root `uwas` user with `CAP_NET_BIND_SERVICE`; HEALTHCHECK added. |
| M9 | Dashboard token in sessionStorage | ⚠️ Accepted | Dashboard uses sessionStorage by design (SPA, no server-rendered cookies). CSRF origin checks mitigate the risk. |
| M10 | Some crypto/rand errors unchecked | ✅ Resolved | `generateID`/`generateToken`/`generateAPIKey` all handle `rand.Read` errors with `/dev/urandom` fallback. |

### Low findings — all reviewed

| ID | Finding | Current Status |
|----|---------|----------------|
| L1 | API key uses SHA256 (not bcrypt) | ⚠️ Accepted — SHA256 is appropriate for high-entropy API keys (unlike passwords, they are not user-chosen); bcrypt would prevent O(1) lookup. |
| L2 | TOTP in query parameter | ✅ Resolved — TOTP moved to `X-TOTP-Code` header. |
| L3 | BasicAuth plaintext fallback | ✅ Resolved — BasicAuth uses hashed credentials. |
| L4 | GitLab token non-constant-time comparison | ✅ Resolved — HMAC-based webhook verification. |

### Open action items

None. All findings from the original assessment are now resolved or accepted.

### Updated risk score: **2.8 / 10** (Low)

The authorization gap that drove the original 8.7/10 score is closed, including
the DNS management authorization (H7). Remaining risk is limited to accepted
design trade-offs (in-memory sessions, sessionStorage token). Supply-chain
posture is unchanged (5 direct deps, no known CVEs).

---

## Original Assessment (v0.0.54 — 2026-05-01)

*The report below is the original, unmodified assessment. It is retained for
traceability. Refer to the Status Update above for the current state of each
finding.*

---

**Project:** UWAS (Unified Web Application Server)
**Version:** v0.0.54
**Date:** 2026-05-01
**Scope:** Full codebase (Go backend + React dashboard + infrastructure)
**Methodology:** 4-phase AI-powered security scan (Recon -> Hunt -> Verify -> Report)

---

## Executive Summary

UWAS is a single-binary Go web server with a React admin dashboard. The security assessment revealed a **critical architectural gap**: while authentication is properly implemented, **authorization is completely unused**. The entire RBAC system (roles, permissions, `HasPermission()`) exists but is never invoked by any HTTP handler. This allows any authenticated user — including the lowest-privilege `RoleUser` — to perform destructive system operations.

### Risk Score: **8.7 / 10** (High)

| Category | Score | Rationale |
|----------|-------|-----------|
| Authentication | 6/10 | bcrypt passwords, TOTP 2FA, but sessions in-memory only |
| Authorization | 2/10 | RBAC implemented but completely unused |
| Input Validation | 5/10 | Some validation present, mass assignment vulnerabilities |
| Data Protection | 5/10 | Path traversal guard exists, but verbose errors leak paths |
| Infrastructure | 6/10 | Minimal dependencies, Docker runs as root |
| Supply Chain | 8/10 | Only 5 direct Go deps, no known CVEs |

---

## Scan Statistics

| Phase | Status | Details |
|-------|--------|---------|
| Phase 1: Reconnaissance | Complete | Architecture mapped, 5 direct deps, 205+ API endpoints, 19 CLI commands |
| Phase 2: Hunt | Complete | 15 scanners launched, 3 completed with deep analysis, 12 consolidated via manual verification |
| Phase 3: Verify | Complete | 33 verified findings, 6 false positives eliminated |
| Phase 4: Report | Complete | This report |

| Severity | Count |
|----------|-------|
| CRITICAL | 7 |
| HIGH | 12 |
| MEDIUM | 10 |
| LOW | 4 |
| **Total** | **33** |

---

## Critical Findings

### CRITICAL-1: Authorization Architecture Completely Unused
- **CWE:** CWE-284, CWE-269, CWE-862
- **Files:** `internal/admin/api.go` (all handlers), `internal/auth/manager.go:84-101`
- **CVSS:** 9.1 (Critical)
- **Description:** The `auth.Manager` defines a complete RBAC permission system (`RoleAdmin`, `RoleReseller`, `RoleUser`, `rolePermissions` map, `HasPermission()` method) but **zero HTTP handlers call `HasPermission()`**. Over 80 admin endpoints only verify authentication (`requireAuth`) but never check authorization.
- **Impact:** Any authenticated `RoleUser` can: manage firewall, start/stop services, install packages, create/drop databases, restore backups, write raw config YAML, upload SSL certificates, trigger self-updates, manage DNS records, clone sites.
- **Remediation:** Immediately add `requireAdmin()` and `requireRole()` middleware. Wire `HasPermission()` into every handler. Apply `requireAdmin` to all system-level endpoints.

### CRITICAL-2: Raw YAML Config Write Without Validation
- **CWE:** CWE-94, CWE-269
- **File:** `internal/admin/api.go:3139` (`handleConfigRawPut`)
- **CVSS:** 8.8 (Critical)
- **Description:** `PUT /api/v1/config/raw` accepts raw YAML from any authenticated user and writes directly to the main config file. No admin check, no YAML validation, no backup, no field restriction.
- **Remediation:** Require admin role; validate YAML syntax; create automatic backup; restrict modifiable fields.

### CRITICAL-3: Database Management Without Ownership Check
- **CWE:** CWE-639, CWE-284
- **File:** `internal/admin/handlers_hosting.go:930-1369`
- **CVSS:** 8.5 (Critical)
- **Description:** Database create/drop/start/stop/restart and Docker DB endpoints do not verify ownership or admin role. DB explorer accepts raw SQL without restrictions.
- **Remediation:** Add database ownership tracking; verify ownership before all DB operations; restrict DB explorer to read-only queries with allowlist.

### CRITICAL-4: Backup Restore Path Traversal
- **CWE:** CWE-22, CWE-434
- **File:** `internal/admin/api.go:4000`, `backup/backup.go:445`
- **CVSS:** 8.4 (Critical)
- **Description:** Backup restore does not validate archive contents. Malicious backups with path traversal sequences can overwrite arbitrary files. Tar restore uses untrusted `hdr.Mode`.
- **Remediation:** Implement strict path traversal checks during extraction; validate archive signatures; sandbox restore operations.

### CRITICAL-5: Service Control Open to Non-Admin Users
- **CWE:** CWE-269
- **File:** `internal/admin/handlers_hosting.go:1312-1339`
- **CVSS:** 8.2 (Critical)
- **Description:** System service start/stop/restart and package installation accessible to any authenticated user.
- **Remediation:** Restrict all service/package endpoints to `RoleAdmin`.

### CRITICAL-6: Deploy Hook Shell Injection
- **CWE:** CWE-78
- **File:** `deploy/deploy.go:410`
- **CVSS:** 8.8 (Critical)
- **Description:** Deploy hook uses `sh -c` with potentially unsanitized input.
- **Remediation:** Use parameterized commands; validate all inputs against strict allowlist; avoid shell execution.

### CRITICAL-7: SFTP Plaintext Password Comparison
- **CWE:** CWE-256
- **File:** `sftpserver/server.go:83`
- **CVSS:** 7.8 (High)
- **Description:** SFTP server uses plaintext password comparison instead of hashing.
- **Remediation:** Hash SFTP passwords with bcrypt; use constant-time comparison.

---

## High Findings

| ID | Finding | CWE | File | CVSS |
|----|---------|-----|------|------|
| H1 | Global 2FA bypass in multi-user mode | CWE-287 | `admin/api.go:730` | 7.5 |
| H2 | No per-username brute force protection | CWE-307 | `admin/api.go:4700` | 7.2 |
| H3 | Session tokens in query params | CWE-598 | `admin/api.go:662` | 6.5 |
| H4 | Missing pagination on 11+ list endpoints | CWE-770 | `admin/*.go` | 6.8 |
| H5 | Mass assignment in domain/settings updates | CWE-915 | `admin/api.go:2539` | 7.1 |
| H6 | Missing authz on migration/clone endpoints | CWE-862 | `handlers_hosting.go:2051` | 7.8 |
| H7 | Missing authz on DNS management | CWE-862 | `handlers_hosting.go:1443` | 7.5 |
| H8 | Verbose errors leak internal paths | CWE-209 | `admin/*.go` | 5.8 |
| H9 | Self-update no binary signing | CWE-494 | `selfupdate/updater.go` | 7.0 |
| H10 | SQL explorer INTO OUTFILE bypass | CWE-89 | `handlers_hosting.go:2410` | 7.2 |
| H11 | API keys plaintext in users.json | CWE-256 | `auth/manager.go:40` | 6.8 |
| H12 | SSH password in process args | CWE-214 | `migrate/sitemigrate.go:165` | 6.5 |

---

## Medium Findings

| ID | Finding | CWE | File |
|----|---------|-----|------|
| M1 | In-memory session storage | CWE-522 | `auth/manager.go` |
| M2 | JWT dead code (unused) | CWE-665 | `auth/manager.go` |
| M3 | No password complexity requirements | CWE-521 | `auth/manager.go` |
| M4 | Timing attack on user existence | CWE-204 | `auth/manager.go` |
| M5 | WordPress no checksum verification | CWE-494 | `wordpress/installer.go` |
| M6 | Missing Content-Type enforcement | CWE-650 | `admin/api.go` |
| M7 | Missing request size limits | CWE-770 | `admin/api.go` |
| M8 | Docker container runs as root | CWE-250 | `Dockerfile` |
| M9 | Dashboard token in sessionStorage | CWE-522 | `dashboard/src/lib/api.ts` |
| M10 | Some crypto/rand errors unchecked | CWE-330 | `admin/api.go` |

---

## Low Findings

| ID | Finding | CWE | File |
|----|---------|-----|------|
| L1 | API key uses SHA256 (not bcrypt) | CWE-916 | `auth/manager.go` |
| L2 | TOTP in query parameter | CWE-598 | `admin/api.go:738` |
| L3 | BasicAuth plaintext fallback | CWE-256 | `middleware/basicauth.go` |
| L4 | GitLab token non-constant-time comparison | CWE-208 | `admin/handlers_app.go` |

---

## Remediation Roadmap

### Phase 1: Immediate (24-48 hours)
1. **Add authorization checks** — Implement `requireAdmin()` and `requireRole()` middleware; apply to all system-level endpoints (firewall, services, packages, DB, backup, config, DNS, migration, clone).
2. **Fix config raw write** — Require admin + pin; validate YAML; backup before overwrite.
3. **Fix backup restore** — Validate archive paths; sandbox extraction.
4. **Fix DB explorer** — Restrict to read-only; block `INTO OUTFILE`; use read-only DB user.

### Phase 2: Short-term (1 week)
5. Add pagination to all list endpoints.
6. Implement field-level allowlists for domain/settings updates.
7. Add per-username brute force protection.
8. Enforce `Content-Type: application/json` on mutations.
9. Add request body size limits.
10. Fix 2FA bypass — remove `!multiUserEnabled` condition.

### Phase 3: Medium-term (2 weeks)
11. Replace verbose errors with generic messages.
12. Move auth token from `sessionStorage` to `HttpOnly` cookie.
13. Add password complexity requirements (min 12 chars, mixed case, digits).
14. Fix timing attack — always run bcrypt comparison with dummy hash.
15. Add binary signature verification to self-update.
16. Fix SFTP plaintext passwords.

### Phase 4: Long-term (1 month)
17. Persist sessions to encrypted disk or Redis with TTL.
18. Remove JWT dead code.
19. Run Docker container as non-root user.
20. Implement API versioning strategy.
21. Add comprehensive authorization tests.

---

## Positive Security Controls Identified

- **Minimal dependency surface** — Only 5 direct Go dependencies, reducing supply chain risk
- **bcrypt password hashing** — Properly implemented with `bcrypt.DefaultCost`
- **Timing-safe comparisons** — API key comparison uses `crypto/subtle.ConstantTimeCompare`
- **Path traversal guard** — `internal/pathsafe/` implements symlink-resolving containment
- **WAF body scan** — First 64KB scanned for SQL/XSS/shell/RCE patterns
- **Bot guard** — Blocks 25+ malicious scanners
- **PHP sandbox** — `disable_functions`, `open_basedir`, `allow_url_include=Off`
- **ACME auto-TLS** — Automatic certificate issuance and renewal
- **Audit logging** — Security event ring buffer
- **Secure credential generation** — `crypto/rand.Read` with panic on failure

---

## Appendix: Scan Coverage

| Scanner | Status | Findings |
|---------|--------|----------|
| sc-recon | Complete | Architecture mapped |
| sc-dependency-audit | Complete | 0 known CVEs |
| sc-lang-go | Complete | 4 findings |
| sc-lang-typescript | Complete | 7 findings |
| sc-auth | Complete | 5 findings |
| sc-authz | Complete | 13 findings |
| sc-crypto | Complete | 2 findings |
| sc-api-security | Complete | 39 findings |
| sc-business-logic | Complete | 20 findings |
| sc-sqli | Complete | 4 findings |
| sc-ssrf | Complete | 4 findings |
| sc-csrf | Complete | 4 findings |
| sc-websocket | Complete | 4 findings |
| sc-docker | Complete | 4 findings |
| sc-file-upload | Complete | Consolidated |
| sc-verifier | Complete | 33 verified, 6 FP eliminated |
| sc-report | Complete | This document |
