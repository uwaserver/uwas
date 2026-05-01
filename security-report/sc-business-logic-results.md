# Security Scan Report: Business Logic & JWT

**Scanner:** sc-business-logic + sc-jwt  
**Project:** UWAS v0.0.54  
**Date:** 2026-05-01  
**Scope:** `internal/admin/api.go`, `internal/admin/handlers_hosting.go`, `internal/admin/handlers_app.go`, `internal/admin/webhook_handlers.go`, `internal/auth/manager.go`  

---

## Executive Summary

| Category | Critical | High | Medium | Low |
|----------|----------|------|--------|-----|
| Business Logic Flaws | 3 | 6 | 5 | 2 |
| JWT Issues | 0 | 0 | 1 | 1 |
| **Total** | **3** | **6** | **6** | **3** |

**Key Finding:** UWAS does not use JWT tokens (session-based auth). The `jwtSecret` field is unused. The primary risk is **missing authorization checks** -- over 80 API endpoints authenticate the user but never verify role or permissions, allowing any authenticated user (including `RoleUser`) to perform administrative actions.

---

## CRITICAL

### C1. Missing Authorization on System-Level Endpoints -- Any Authenticated User Can Control Server

**File:** `internal/admin/handlers_hosting.go`  
**Lines:** 691-773 (firewall), 906-1369 (database), 1304-1339 (services), 1961-2047 (packages)  
**Severity:** Critical  
**CWE:** CWE-862 (Missing Authorization)

**Description:** The following destructive/system-level handlers authenticate the user via `authMiddleware` but perform **no role or permission check**. Any authenticated user (including the lowest-privilege `RoleUser`) can execute these operations:

- `handleFirewallAllow` (695) -- open firewall ports
- `handleFirewallDeny` (717) -- close firewall ports  
- `handleFirewallEnable` (757) -- enable firewall
- `handleDBCreate` (930) -- create databases
- `handleDBDrop` (955) -- drop databases (requires pin, but no role check)
- `handleDBChangePassword` (1007) -- change DB user passwords
- `handleDBExport` (1030) -- export database dumps
- `handleDBImport` (1048) -- import arbitrary SQL
- `handleDBStart/Stop/Restart` (1344-1369) -- control MySQL service
- `handleServiceStart/Stop/Restart` (1312-1339) -- control any systemd service
- `handlePackageInstall` (1961) -- install/remove system packages via apt

**Impact:** Complete server compromise by any authenticated user.

**Recommended Fix:** Add a `requireAdmin()` helper that checks `user.Role == auth.RoleAdmin` and apply it to all system-level endpoints. Example:

```go
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
    user, ok := auth.UserFromContext(r.Context())
    if !ok || user.Role != auth.RoleAdmin {
        jsonError(w, "admin required", http.StatusForbidden)
        return false
    }
    return true
}
```

---

### C2. Missing Authorization on Settings/Config Endpoints -- Any Authenticated User Can Rewrite Configuration

**File:** `internal/admin/api.go`  
**Lines:** 3139-3199 (`handleConfigRawPut`), 3267-3420 (`handleSettingsPut`), 3735-3855 (`handleDomainRawPut`)  
**Severity:** Critical  
**CWE:** CWE-862 (Missing Authorization)

**Description:** Three critical config endpoints have **no role check** and **no pin check**:

1. `handleConfigRawPut` (3139) -- writes raw YAML to the main config file. Can change admin API key, pin code, TOTP secret, backup credentials, webhook secrets.
2. `handleSettingsPut` (3267) -- updates global settings via flat key-value pairs. Can change `global.admin.api_key`, `global.backup.s3.secret_key`, `global.alerting.telegram_token`, and all other sensitive settings.
3. `handleDomainRawPut` (3735) -- writes raw YAML to individual domain configs. Can change SSL certs, proxy upstreams, PHP settings, basic auth credentials.

**Impact:** Any authenticated user can escalate privileges by changing the admin API key, disabling security features, or exfiltrating secrets.

**Recommended Fix:** Require `RoleAdmin` on all three endpoints. Add pin-code requirement to `handleConfigRawPut` and `handleDomainRawPut`.

---

### C3. Missing Authorization on Backup Endpoints -- Any Authenticated User Can Restore/Delete Backups

**File:** `internal/admin/api.go`  
**Lines:** 3907-4044 (`handleBackupCreate`, `handleBackupDomain`, `handleBackupRestore`, `handleBackupDelete`)  
**Severity:** Critical  
**CWE:** CWE-862 (Missing Authorization)

**Description:** All backup handlers authenticate but do not check roles:

- `handleBackupCreate` (3907) -- create full system backup
- `handleBackupDomain` (3952) -- backup specific domain
- `handleBackupRestore` (4011) -- restore from backup (no pin check)
- `handleBackupDelete` (4045) -- delete backup files (no pin check)

**Impact:** Any authenticated user can restore arbitrary backups (potentially overwriting production data) or delete all backups.

**Recommended Fix:** Require `RoleAdmin` + pin code for restore and delete operations.

---

## HIGH

### H1. Missing Authorization on Site Migration/Clone/Import Endpoints

**File:** `internal/admin/handlers_hosting.go`  
**Lines:** 2051-2309 (`handleMigrate`, `handleClone`, `handleMigrateCPanel`)  
**Severity:** High  
**CWE:** CWE-862 (Missing Authorization)

**Description:** Three migration endpoints have no role check:

- `handleMigrate` (2051) -- migrates a site from a remote SSH host. Can read arbitrary remote files.
- `handleClone` (2185) -- clones a domain to a new domain. Can copy any site's files and database.
- `handleMigrateCPanel` (2261) -- imports a cPanel backup archive. Can create new domains and databases.

**Impact:** Any authenticated user can clone arbitrary sites, import foreign backups, or trigger remote migrations.

**Recommended Fix:** Require `RoleAdmin` or domain-specific ownership verification.

---

### H2. Missing Authorization on Docker DB Endpoints

**File:** `internal/admin/handlers_hosting.go`  
**Lines:** 1115-1288 (`handleDockerDB*` handlers)  
**Severity:** High  
**CWE:** CWE-862 (Missing Authorization)

**Description:** Docker database container handlers lack role checks:

- `handleDockerDBCreate` (1135) -- create new Docker DB containers
- `handleDockerDBStart/Stop` (1169-1185) -- control containers
- `handleDockerDBCreateDatabase` (1213) -- create databases inside containers
- `handleDockerDBExport/Import` (1254-1288) -- export/import database dumps

Only `handleDockerDBRemove` (1187) and `handleDockerDBDropDatabase` (1240) require pin codes, but neither checks roles.

**Impact:** Any authenticated user can create, control, and access Docker database containers.

**Recommended Fix:** Require `RoleAdmin` for all Docker DB operations.

---

### H3. Missing Authorization on Self-Update Endpoint

**File:** `internal/admin/handlers_hosting.go`  
**Lines:** 872-902 (`handleUpdate`)  
**Severity:** High  
**CWE:** CWE-862 (Missing Authorization)

**Description:** `handleUpdate` requires a pin code but does not check the user's role. Any authenticated user who knows the pin can trigger a binary self-update and server restart.

**Impact:** Low-privilege users can update the server binary if they know the pin.

**Recommended Fix:** Add `requireAdmin(w, r)` before pin check.

---

### H4. Missing Authorization on DNS Management Endpoints

**File:** `internal/admin/handlers_hosting.go`  
**Lines:** 1443-1591 (`handleDNSRecords`, `handleDNSRecordCreate`, `handleDNSRecordDelete`, `handleDNSRecordUpdate`, `handleDNSSync`)  
**Severity:** High  
**CWE:** CWE-862 (Missing Authorization)

**Description:** All DNS record management endpoints authenticate but do not check roles. Only `handleDNSRecordDelete` requires a pin. Any authenticated user can create, update, or sync DNS records for any domain.

**Impact:** DNS hijacking by any authenticated user.

**Recommended Fix:** Require `RoleAdmin` for DNS operations.

---

### H5. Global 2FA Bypass When Multi-User Auth Is Enabled

**File:** `internal/admin/api.go`  
**Lines:** 730-750  
**Severity:** High  
**CWE:** CWE-287 (Improper Authentication)

**Description:** The TOTP 2FA check in `authMiddleware` is guarded by `!multiUserEnabled`:

```go
if totpSecret != "" && user.Role == auth.RoleAdmin &&
    !strings.HasPrefix(r.URL.Path, "/api/v1/auth/2fa/") &&
    !multiUserEnabled {   // <-- 2FA only when multi-user is OFF
```

When multi-user auth is enabled (`global.users.enabled = true`), the global admin TOTP is completely bypassed. Admin users authenticated via API key or session token are not required to provide a TOTP code, even if `global.admin.totp_secret` is configured.

**Impact:** 2FA provides no protection in multi-user mode.

**Recommended Fix:** Remove `!multiUserEnabled` condition or implement per-user TOTP for multi-user mode.

---

### H6. Session Token Exposure via URL Query Parameters

**File:** `internal/admin/api.go`  
**Lines:** 662-682, 696-699  
**Severity:** High  
**CWE:** CWE-598 (Use of GET Request Method With Sensitive Query Strings)

**Description:** `authMiddleware` accepts session tokens and API keys via URL query parameters (`?token=...` and `?ticket=...`). Tokens passed in URLs are logged in:

- Server access logs
- Browser history
- Referrer headers sent to third parties
- Proxy logs

While the code strips the token from the URL after auth (line 676-679), the token has already been exposed in the request line.

**Impact:** Session/API key leakage leading to account takeover.

**Recommended Fix:** Remove the `token` query parameter fallback entirely. Only accept tokens in `Authorization` header or `X-Session-Token` header. Keep `ticket` (single-use) for SSE/WebSocket only.

---

## MEDIUM

### M1. Missing Authorization on PHP Management Endpoints

**File:** `internal/admin/api.go`  
**Lines:** 1111-1708 (all `handlePHP*` handlers)  
**Severity:** Medium  
**CWE:** CWE-862 (Missing Authorization)

**Description:** All PHP management handlers (install, start, stop, restart, config, domain assign/unassign) authenticate but do not check roles. Any authenticated user can install PHP versions, start/stop FPM pools, and change PHP configuration.

**Recommended Fix:** Require `RoleAdmin` for PHP installation; require domain ownership for per-domain PHP operations.

---

### M2. Missing Authorization on Cron Job Endpoints

**File:** `internal/admin/handlers_hosting.go`  
**Lines:** 638-687 (`handleCronList`, `handleCronAdd`, `handleCronDelete`)  
**Severity:** Medium  
**CWE:** CWE-862 (Missing Authorization)

**Description:** Cron job handlers lack role checks. Any authenticated user can list, add, or delete system cron jobs. `handleCronDelete` requires a pin but no role check.

**Impact:** Privilege escalation via arbitrary command execution through cron.

**Recommended Fix:** Require `RoleAdmin` for cron operations.

---

### M3. Missing Authorization on Notification/Branding Endpoints

**File:** `internal/admin/handlers_hosting.go`  
**Lines:** 1373-1392 (`handleNotifyTest`), 2657-2708 (`handleNotifyPrefs*`, `handleBranding*`)  
**Severity:** Medium  
**CWE:** CWE-862 (Missing Authorization)

**Description:** Notification and branding endpoints have no role checks:

- `handleNotifyTest` -- send test notifications using configured channels
- `handleNotifyPrefsPut` -- change alerting/webhook settings
- `handleBrandingPut` -- change white-label branding

**Recommended Fix:** Require `RoleAdmin`.

---

### M4. Unused Permission System -- `HasPermission` Never Called

**File:** `internal/auth/manager.go`  
**Lines:** 308-320 (`HasPermission`), `internal/admin/*.go` (all handlers)  
**Severity:** Medium  
**CWE:** CWE-284 (Improper Access Control)

**Description:** The auth package defines a comprehensive permission system (`PermDomainRead`, `PermDomainCreate`, `PermUserRead`, etc.) and a `HasPermission(role, perm)` method, but **no handler in the admin API ever calls it**. The role-based access control exists only in data structures, not in enforcement.

**Impact:** The permission system provides a false sense of security. Administrators may believe users are restricted by permissions when they are not.

**Recommended Fix:** Implement a permission-checking middleware or helper and apply it to all endpoints. Example:

```go
func (s *Server) requirePermission(w http.ResponseWriter, r *http.Request, perm auth.Permission) bool {
    user, ok := auth.UserFromContext(r.Context())
    if !ok || !s.authMgr.HasPermission(user.Role, perm) {
        jsonError(w, "forbidden", http.StatusForbidden)
        return false
    }
    return true
}
```

---

### M5. Recovery Code Generation Without Authentication

**File:** `internal/admin/handlers_hosting.go`  
**Lines:** 2604-2621 (`handleGenRecoveryCodes`)  
**Severity:** Medium  
**CWE:** CWE-862 (Missing Authorization)

**Description:** `handleGenRecoveryCodes` generates new 2FA recovery codes and stores them in config. It has **no role check and no pin check**. Any authenticated user can regenerate recovery codes, invalidating the previous set and locking out the real admin.

**Recommended Fix:** Require `RoleAdmin` + pin code.

---

### M6. Unused `jwtSecret` Field -- Dead Code with Misleading Security Implication

**File:** `internal/auth/manager.go`  
**Lines:** 111, 117-120, 128  
**Severity:** Medium  
**CWE:** CWE-1109 (Use of Same Variable for Multiple Purposes)

**Description:** `auth.Manager` generates and stores a 32-byte `jwtSecret` on initialization, but this field is **never read anywhere in the codebase**. The system uses session-based authentication with in-memory tokens, not JWT. The presence of `jwtSecret` may mislead security reviewers into believing JWT is used, potentially masking the actual session-based auth model.

**Recommended Fix:** Remove the `jwtSecret` field and its generation to eliminate confusion.

---

## LOW

### L1. `PATCH` Method Not Covered by CSRF Protection

**File:** `internal/admin/api.go`  
**Lines:** 755-770  
**Severity:** Low  
**CWE:** CWE-352 (Cross-Site Request Forgery)

**Description:** The CSRF protection in `authMiddleware` only checks `POST`, `PUT`, and `DELETE` methods. `PATCH` requests bypass the `X-Requested-With` and origin validation. While no handlers currently use `PATCH`, future endpoints added with `PATCH` would be unprotected.

**Recommended Fix:** Add `PATCH` to the method check: `if r.Method == "POST" || r.Method == "PUT" || r.Method == "DELETE" || r.Method == "PATCH"`.

---

### L2. `handleUserCreate` (SFTP) Missing Domain Authorization

**File:** `internal/admin/api.go`  
**Lines:** 3023-3060  
**Severity:** Low  
**CWE:** CWE-862 (Missing Authorization)

**Description:** `handleUserCreate` creates SFTP users for a domain but does not verify that the requesting user is authorized for that domain. Any authenticated user can create SFTP users for any domain.

**Recommended Fix:** Add `requireDomainAccess(w, r, req.Domain, "sftp.create")` check.

---

### L3. `handleUserList` (SFTP) Missing Authorization

**File:** `internal/admin/api.go`  
**Lines:** 3015-3021  
**Severity:** Low  
**CWE:** CWE-862 (Missing Authorization)

**Description:** `handleUserList` returns all SFTP users without any authorization check. Any authenticated user can enumerate all SFTP accounts.

**Recommended Fix:** Filter the list to only show users for domains the caller can access, or require `RoleAdmin`.

---

## JWT-Specific Findings

### JWT-1. No JWT Implementation Found

**Status:** Informational / Architecture Note  
**Severity:** Medium (for scan completeness)

**Description:** Despite the presence of `jwtSecret` in `auth.Manager`, UWAS does **not implement JWT authentication**. All authentication is session-based using opaque random tokens stored in memory. Therefore, classic JWT vulnerabilities (algorithm confusion, none algorithm, key confusion, expired token bypass, etc.) are **not applicable**.

**Files examined:** `internal/auth/manager.go`, `internal/admin/api.go`, `internal/server/server.go`

**Evidence:**
- No JWT library imports (no `github.com/golang-jwt/jwt`, no `jwt-go`)
- No JWT parsing/verification code
- `jwtSecret` field is written at lines 117-120 but never read
- Session tokens are 32-byte random base64 strings (line 560-566), not JWTs

---

### JWT-2. Session Token Passed in URL Query Parameter

**File:** `internal/admin/api.go`  
**Lines:** 662-682  
**Severity:** Low  
**CWE:** CWE-598 (Use of GET Request Method With Sensitive Query Strings)

**Description:** While not a JWT issue per se, the session token can be passed via `?token=` query parameter for SSE/WebSocket connections. This is the functional equivalent of JWT token leakage via URL -- the token appears in logs, browser history, and referrer headers.

**Recommended Fix:** Use the single-use `ticket` mechanism exclusively for SSE/WebSocket and remove the `token` query parameter fallback.

---

## Summary Table

| ID | Severity | Finding | File | Line | CWE |
|----|----------|---------|------|------|-----|
| C1 | Critical | Missing authz on system endpoints (firewall, DB, services, packages) | `handlers_hosting.go` | 691-2047 | CWE-862 |
| C2 | Critical | Missing authz on config endpoints (raw config, settings, domain raw) | `api.go` | 3139-3855 | CWE-862 |
| C3 | Critical | Missing authz on backup endpoints (restore/delete without pin) | `api.go` | 3907-4044 | CWE-862 |
| H1 | High | Missing authz on migration/clone/import endpoints | `handlers_hosting.go` | 2051-2309 | CWE-862 |
| H2 | High | Missing authz on Docker DB endpoints | `handlers_hosting.go` | 1115-1288 | CWE-862 |
| H3 | High | Missing authz on self-update (pin only, no role check) | `handlers_hosting.go` | 872-902 | CWE-862 |
| H4 | High | Missing authz on DNS management endpoints | `handlers_hosting.go` | 1443-1591 | CWE-862 |
| H5 | High | Global 2FA bypassed when multi-user auth enabled | `api.go` | 730-750 | CWE-287 |
| H6 | High | Session token exposure via URL query parameter | `api.go` | 662-699 | CWE-598 |
| M1 | Medium | Missing authz on PHP management endpoints | `api.go` | 1111-1708 | CWE-862 |
| M2 | Medium | Missing authz on cron job endpoints | `handlers_hosting.go` | 638-687 | CWE-862 |
| M3 | Medium | Missing authz on notification/branding endpoints | `handlers_hosting.go` | 1373-2708 | CWE-862 |
| M4 | Medium | Permission system defined but never enforced | `auth/manager.go` | 308-320 | CWE-284 |
| M5 | Medium | Recovery code generation without authz/pin | `handlers_hosting.go` | 2604-2621 | CWE-862 |
| M6 | Medium | Unused `jwtSecret` field (dead code) | `auth/manager.go` | 111-128 | CWE-1109 |
| L1 | Low | PATCH method not covered by CSRF protection | `api.go` | 755-770 | CWE-352 |
| L2 | Low | SFTP user create missing domain authz | `api.go` | 3023-3060 | CWE-862 |
| L3 | Low | SFTP user list missing authz | `api.go` | 3015-3021 | CWE-862 |
| JWT-1 | Medium | No JWT implementation found (informational) | `auth/manager.go` | 111-128 | N/A |
| JWT-2 | Low | Session token in URL query parameter | `api.go` | 662-682 | CWE-598 |

---

## Positive Observations

1. **Password hashing:** bcrypt with default cost, correctly implemented.
2. **Token generation:** 32-byte `crypto/rand`, panic on error.
3. **Timing-safe comparisons:** `subtle.ConstantTimeCompare` used for API keys and passwords.
4. **Session expiration:** Sessions have 24-hour expiration and are validated.
5. **Single-use tickets:** SSE/WebSocket auth uses short-lived single-use tickets.
6. **Rate limiting:** Per-IP and per-username rate limiting on auth failures.
7. **CSRF protection:** `X-Requested-With` + origin validation for state-changing methods.
8. **Path traversal protection:** `pathsafe` package with symlink resolution.
9. **Domain access checks:** `canAccessDomain()` and `requireDomainAccess()` exist and are used for some endpoints.
10. **Pin code protection:** Destructive operations (delete domain, drop DB, uninstall, etc.) require pin code.
11. **Self-deletion prevention:** Users cannot delete their own account.
12. **Role restriction on user creation:** Only admin can create users, and only `user`/`reseller` roles are allowed.
13. **No mass assignment:** `handleSettingsPut` uses explicit key allowlist; `handleUserUpdateAuth` uses pointer fields for selective updates.

---

*End of Report*
