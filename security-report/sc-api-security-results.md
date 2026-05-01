# UWAS API Security & Rate-Limiting Scan Report

**Scan Date:** 2026-05-01
**Scanner:** sc-api-security + sc-rate-limiting
**Scope:** `internal/admin/*.go`, `internal/middleware/*.go`, `internal/server/server.go`, `internal/config/config.go`
**Focus Areas:**
1. Missing rate limiting on sensitive endpoints
2. Mass assignment vulnerabilities
3. Missing input validation
4. Verbose error responses
5. Missing pagination on list endpoints
6. Unauthenticated access to admin endpoints
7. API version handling
8. Content-Type enforcement
9. Missing request size limits
10. IDOR (Insecure Direct Object Reference) vulnerabilities

---

## Summary

| Severity | Count |
|----------|-------|
| CRITICAL | 7 |
| HIGH | 12 |
| MEDIUM | 14 |
| LOW | 6 |
| **Total** | **39** |

---

## CRITICAL Findings

### CRITICAL-1: Missing Admin Role Checks on Critical System Endpoints
- **File:** `internal/admin/api.go`
- **Lines:** Multiple (see list below)
- **CWE:** CWE-269 (Improper Privilege Management), CWE-285 (Improper Authorization)
- **Description:** Numerous administrative endpoints that control system-level resources (firewall, services, packages, databases, backups, cron jobs, global config) only check `requireAuth` (any authenticated user) or `requireDomainAccess` (domain owner) but do NOT verify the user has the `admin` role. This allows regular `user` and `reseller` roles to perform destructive system operations.
- **Affected Endpoints:**
  - `handleFirewallAllow` / `handleFirewallDeny` / `handleFirewallDelete` / `handleFirewallEnable` / `handleFirewallDisable` — no admin check
  - `handleServiceStart` / `handleServiceStop` / `handleServiceRestart` — no admin check
  - `handlePackageInstall` — no admin check
  - `handleDBCreate` / `handleDBDrop` / `handleDBStart` / `handleDBStop` / `handleDBRestart` — no ownership/admin check
  - `handleCronAdd` / `handleCronDelete` — no admin check
  - `handleBackupCreate` / `handleBackupRestore` / `handleBackupDelete` — no admin check
  - `handleConfigRawPut` — no admin check (raw YAML write)
  - `handleCloudflareConnect` — no admin check
  - `handleCertUpload` — no domain access check at all
- **Recommendation:** Add `requireAdmin` middleware or explicit role checks (`s.requireRole(w, r, "admin")`) to all system-level endpoints.

### CRITICAL-2: Raw YAML Config Write Without Validation or Admin Check
- **File:** `internal/admin/api.go`
- **Line:** ~3139 (`handleConfigRawPut`)
- **CWE:** CWE-94 (Code Injection), CWE-269 (Improper Privilege Management)
- **Description:** The `PUT /api/v1/config/raw` endpoint accepts raw YAML and writes it directly to the main configuration file. There is no admin role check, no schema validation, no backup before overwrite, and no syntax validation before persisting. A regular authenticated user can corrupt the entire server configuration.
- **Recommendation:** Require admin role; validate YAML syntax before persisting; create automatic backup; restrict which fields can be modified.

### CRITICAL-3: Database Create/Drop Without Ownership Verification
- **File:** `internal/admin/api.go` / `internal/admin/handlers_hosting.go`
- **Lines:** ~3500-3600 (DB handlers)
- **CWE:** CWE-639 (Authorization Bypass Through User-Controlled Key), CWE-284 (Improper Access Control)
- **Description:** Database creation and deletion endpoints do not verify that the requesting user owns the target database or has admin privileges. A regular user could drop databases belonging to other users or the system.
- **Recommendation:** Implement database ownership tracking and verify ownership before create/drop operations.

### CRITICAL-4: Backup Restore Allows Arbitrary File Overwrite
- **File:** `internal/admin/api.go`
- **Line:** ~4000 (`handleBackupRestore`)
- **CWE:** CWE-22 (Path Traversal), CWE-434 (Unrestricted Upload of File with Dangerous Type)
- **Description:** Backup restore operations do not sufficiently validate the backup archive contents. A malicious backup could overwrite arbitrary files outside the intended restore path, especially if path traversal sequences exist in archive entries.
- **Recommendation:** Implement strict path traversal checks during archive extraction; validate archive signatures; sandbox restore operations.

### CRITICAL-5: Service Control Endpoints Open to Non-Admin Users
- **File:** `internal/admin/api.go`
- **Lines:** ~4200-4250 (service handlers)
- **CWE:** CWE-269 (Improper Privilege Management)
- **Description:** System service start/stop/restart endpoints are accessible to any authenticated user. This allows denial of service by stopping critical services (e.g., database, web server) and potential privilege escalation if services can be started with attacker-controlled configurations.
- **Recommendation:** Restrict service control endpoints to admin role only.

### CRITICAL-6: Package Installation Without Admin Check
- **File:** `internal/admin/api.go`
- **Line:** ~4150 (`handlePackageInstall`)
- **CWE:** CWE-269 (Improper Privilege Management), CWE-78 (OS Command Injection)
- **Description:** The package installation endpoint allows any authenticated user to trigger system package installations. Depending on the underlying implementation, this could lead to arbitrary package installation or command injection.
- **Recommendation:** Restrict to admin role only; validate package names against strict allowlist.

### CRITICAL-7: Certificate Upload Missing Domain Access Check
- **File:** `internal/admin/api.go`
- **Line:** ~2486 (`handleCertUpload`)
- **CWE:** CWE-284 (Improper Access Control), CWE-639 (Authorization Bypass)
- **Description:** The SSL certificate upload endpoint does not verify that the authenticated user has access to the domain for which they are uploading a certificate. This could allow users to upload certificates for domains they do not own, potentially enabling phishing or MITM attacks.
- **Recommendation:** Add `requireDomainAccess` or `requireAdmin` check before processing certificate uploads.

---

## HIGH Findings

### HIGH-1: Missing Pagination on List Endpoints
- **File:** `internal/admin/api.go`, `internal/admin/handlers_app.go`, `internal/admin/handlers_hosting.go`
- **Lines:** Multiple
- **CWE:** CWE-770 (Allocation of Resources Without Limits or Throttling)
- **Description:** Most list endpoints return all records without pagination, which can lead to excessive memory usage and denial of service when datasets grow large.
- **Affected Endpoints:**
  - `handleAudit` (~line 2970) — returns up to 500 audit entries (ring buffer, bounded but no client pagination)
  - `handleAppList` (handlers_app.go:19) — returns all app instances
  - `handleDeployList` (handlers_app.go:243) — returns all deployment statuses
  - `handleDomainList` (~line 2500) — returns all domains
  - `handleUserList` (~line 3015) — returns all SFTP users
  - `handleDBList` (~line 3480) — returns all databases
  - `handleBackupList` (~line 3950) — returns all backups
  - `handleCronList` (~line 3800) — returns all cron jobs
  - `handleFirewallList` (~line 3700) — returns all firewall rules
  - `handleServiceList` (~line 4180) — returns all services
  - `handlePackageList` (~line 4120) — returns all packages
- **Recommendation:** Implement cursor-based or offset/limit pagination on all list endpoints. Return a maximum of 50-100 items per request.

### HIGH-2: Mass Assignment in Domain Update
- **File:** `internal/admin/api.go`
- **Line:** ~2539 (`handleUpdateDomain`)
- **CWE:** CWE-915 (Improperly Controlled Modification of Dynamically-Determined Object Attributes)
- **Description:** The domain update endpoint uses merge mode by default, allowing partial updates. However, in replace mode (`?replace=true`), the entire domain configuration can be overwritten. Sensitive fields like `App.Env`, `TLS`, `PinCode`, and `Locations` can be modified without proper validation. There is no field-level access control based on user roles.
- **Recommendation:** Implement field-level allowlists based on user roles; validate all incoming fields; prevent modification of critical fields (e.g., domain ownership) by non-admins.

### HIGH-3: Mass Assignment in Settings Update
- **File:** `internal/admin/api.go`
- **Line:** ~3267 (`handleSettingsPut`)
- **CWE:** CWE-915 (Improperly Controlled Modification of Dynamically-Determined Object Attributes)
- **Description:** The global settings update endpoint accepts arbitrary key-value pairs and applies them to the global configuration. There is no validation of which settings can be modified by which role, and no schema validation for values. This could allow modification of security-critical settings like `Admin.APIKey`, rate limits, or TLS settings.
- **Recommendation:** Implement a strict allowlist of settings per role; validate all values against expected types and ranges.

### HIGH-4: Missing Content-Type Enforcement on API Endpoints
- **File:** `internal/admin/api.go`
- **Lines:** All POST/PUT/PATCH handlers
- **CWE:** CWE-20 (Improper Input Validation), CWE-436 (Interpretation Conflict)
- **Description:** API endpoints that expect JSON bodies do not enforce `Content-Type: application/json`. This can lead to CSRF bypasses in some browser contexts and may cause unexpected behavior if clients send form-encoded or multipart data.
- **Recommendation:** Add middleware or per-handler checks to require `Content-Type: application/json` for all mutation endpoints.

### HIGH-5: Verbose Error Messages Leak Internal Information
- **File:** `internal/admin/api.go`, `internal/admin/handlers_app.go`, `internal/admin/handlers_hosting.go`
- **Lines:** Throughout (e.g., `jsonError(w, err.Error(), ...)`)
- **CWE:** CWE-209 (Generation of Error Message Containing Sensitive Information), CWE-200 (Information Exposure)
- **Description:** Many endpoints return raw error messages from underlying libraries, file system operations, or database drivers. These messages can leak internal paths, database schemas, library versions, or implementation details useful to attackers.
- **Examples:**
  - `jsonError(w, err.Error(), ...)` used extensively
  - File operation errors expose absolute paths
  - Database errors expose SQL syntax or schema details
- **Recommendation:** Log detailed errors internally but return generic error messages to clients. Map internal errors to safe, user-friendly messages.

### HIGH-6: Missing Request Size Limits on Sensitive Endpoints
- **File:** `internal/admin/api.go`
- **Lines:** Multiple
- **CWE:** CWE-770 (Allocation of Resources Without Limits or Throttling), CWE-400 (Uncontrolled Resource Consumption)
- **Description:** While some endpoints (file upload, deploy, webhook) have size limits, many mutation endpoints lack request body size limits. This includes:
  - `handleSettingsPut` (~line 3267) — no size limit
  - `handleUpdateDomain` (~line 2539) — no size limit
  - `handleConfigRawPut` (~line 3139) — no size limit
  - `handleCronAdd` (~line 3800) — no size limit
  - `handleDBExploreQuery` (~line 2383) — query size not limited
- **Recommendation:** Apply `http.MaxBytesReader` to all endpoints that read request bodies. Set appropriate limits per endpoint (e.g., 1MB for settings, 64KB for cron jobs).

### HIGH-7: IDOR in SFTP User Management
- **File:** `internal/admin/api.go`
- **Lines:** ~3015-3050 (`handleUserList`, `handleUserCreate`)
- **CWE:** CWE-639 (Authorization Bypass Through User-Controlled Key), CWE-285 (Improper Authorization)
- **Description:** SFTP user listing and creation endpoints do not verify admin privileges. A regular user could list all SFTP users across all domains or create SFTP users for domains they do not own, gaining unauthorized access.
- **Recommendation:** Restrict SFTP user management to admin role or verify domain ownership for user-targeted operations.

### HIGH-8: IDOR in Cron Job Management
- **File:** `internal/admin/api.go`
- **Lines:** ~3800-3850 (cron handlers)
- **CWE:** CWE-639 (Authorization Bypass Through User-Controlled Key)
- **Description:** Cron job management endpoints do not verify that the user has access to the domain associated with the cron job. Users can create, delete, or view cron jobs for domains they do not own.
- **Recommendation:** Add domain ownership checks to all cron job endpoints.

### HIGH-9: IDOR in Backup Management
- **File:** `internal/admin/api.go`
- **Lines:** ~3950-4050 (backup handlers)
- **CWE:** CWE-639 (Authorization Bypass Through User-Controlled Key)
- **Description:** Backup endpoints do not verify domain ownership or admin role. Users can create, restore, or delete backups for domains they do not own, potentially accessing sensitive data or overwriting production sites.
- **Recommendation:** Add domain ownership checks to all backup endpoints; restrict restore/delete to admin or domain owner.

### HIGH-10: Firewall Management Without Admin Check
- **File:** `internal/admin/api.go`
- **Lines:** ~3700-3750 (firewall handlers)
- **CWE:** CWE-269 (Improper Privilege Management)
- **Description:** Firewall rule management (allow, deny, delete, enable, disable) is accessible to any authenticated user. This allows non-admin users to modify system firewall rules, potentially blocking legitimate traffic or opening ports for attacks.
- **Recommendation:** Restrict all firewall endpoints to admin role only.

### HIGH-11: Cloudflare Connection Without Admin Check
- **File:** `internal/admin/api.go`
- **Line:** ~4870 (`handleCloudflareConnect`)
- **CWE:** CWE-269 (Improper Privilege Management), CWE-284 (Improper Access Control)
- **Description:** The Cloudflare DNS connection endpoint does not require admin privileges. Any authenticated user can modify global DNS integration settings, potentially hijacking domain resolution or exfiltrating API tokens.
- **Recommendation:** Restrict to admin role only.

### HIGH-12: No API Versioning
- **File:** `internal/admin/api.go`
- **Lines:** Route registration (~line 191)
- **CWE:** CWE-1109 (Use of Same Variable for Multiple Purposes)
- **Description:** The API uses `/api/v1/` prefix but there is no mechanism for versioning or deprecation. Breaking changes to endpoints will affect all clients simultaneously. There is no version negotiation or sunset policy.
- **Recommendation:** Implement proper API versioning strategy with deprecation headers; maintain backward compatibility for at least one major version.

---

## MEDIUM Findings

### MEDIUM-1: Rate Limiting Only Applied to Auth Endpoints
- **File:** `internal/admin/audit.go`
- **Lines:** ~106 (`checkRateLimit`)
- **CWE:** CWE-770 (Allocation of Resources Without Limits or Throttling)
- **Description:** The built-in rate limiter in `audit.go` only tracks failed authentication attempts. Successful requests to admin endpoints are not rate-limited per-user, allowing brute-force attacks on administrative operations, enumeration attacks, or resource exhaustion.
- **Recommendation:** Apply per-user rate limiting to all sensitive endpoints (domain CRUD, deploy, backup, config changes).

### MEDIUM-2: Global Rate Limit Uses First Domain's Config
- **File:** `internal/server/server.go`
- **Lines:** ~619-628
- **CWE:** CWE-770 (Allocation of Resources Without Limits or Throttling)
- **Description:** The global middleware chain uses the first domain's rate limit configuration for all incoming requests to unknown domains. This is inconsistent and may provide insufficient protection for admin endpoints or high-traffic sites.
- **Recommendation:** Implement dedicated rate limit configuration for admin endpoints and global fallback, independent of domain configuration.

### MEDIUM-3: Missing Input Validation on Domain Names
- **File:** `internal/admin/api.go`
- **Lines:** ~2539, ~2600 (domain handlers)
- **CWE:** CWE-20 (Improper Input Validation)
- **Description:** While there is some domain validation, the regex and checks may not catch all invalid or malicious domain names. Internationalized domain names (IDN) and very long domain names may bypass validation.
- **Recommendation:** Enforce strict domain name validation per RFC 1035; limit length to 253 characters; validate labels separately.

### MEDIUM-4: Missing Input Validation on File Paths
- **File:** `internal/admin/handlers_hosting.go`
- **Lines:** ~560 (`handleFileUpload`), file manager handlers
- **CWE:** CWE-20 (Improper Input Validation), CWE-22 (Path Traversal)
- **Description:** File manager and upload handlers rely on `pathsafe` package for path traversal protection, but manual review is needed to ensure all paths are sanitized. Some endpoints construct paths directly from user input without explicit sanitization.
- **Recommendation:** Audit all file path constructions; ensure `pathsafe` is used consistently; add defense-in-depth with chroot jails.

### MEDIUM-5: Deploy Webhook Uses Global API Key as Secret
- **File:** `internal/admin/handlers_app.go`
- **Lines:** ~262-313 (`handleDeployWebhook`)
- **CWE:** CWE-798 (Use of Hard-coded Credentials), CWE-287 (Improper Authentication)
- **Description:** The deploy webhook uses the global admin API key as the webhook secret. If the API key is compromised, webhooks can be forged. Additionally, there is no per-domain webhook secret configuration.
- **Recommendation:** Generate unique per-domain webhook secrets; rotate secrets independently of API keys; support signature-based verification for all providers.

### MEDIUM-6: Webhook Verification Has Redundant Empty Secret Check
- **File:** `internal/admin/handlers_app.go`
- **Lines:** ~290-313
- **CWE:** CWE-561 (Dead Code)
- **Description:** The code checks `if secret != ""` after already returning an error when `secret == ""`. This creates unreachable code and indicates logic that could be simplified.
- **Recommendation:** Remove the redundant `if secret != ""` check; the earlier check already handles empty secrets.

### MEDIUM-7: Audit Log Exposes IP Addresses Without Consent Mechanism
- **File:** `internal/admin/audit.go`
- **Lines:** ~55-70 (`RecordAudit`)
- **CWE:** CWE-532 (Insertion of Sensitive Information into Log File)
- **Description:** The audit log records client IP addresses without hashing or anonymization. In jurisdictions with privacy regulations (GDPR), this may violate data retention policies.
- **Recommendation:** Hash or anonymize IP addresses in audit logs; provide configuration for full IP retention vs anonymized.

### MEDIUM-8: Missing CSRF Protection on State-Changing Endpoints
- **File:** `internal/admin/api.go`
- **Lines:** ~553 (`authMiddleware`)
- **CWE:** CWE-352 (Cross-Site Request Forgery)
- **Description:** While CSRF is partially mitigated by `X-Requested-With` header checks and Origin validation, there is no dedicated CSRF token mechanism for state-changing operations. This leaves a window for CSRF attacks if the header-based protections are bypassed.
- **Recommendation:** Implement CSRF tokens for all state-changing mutations; validate tokens on every POST/PUT/PATCH/DELETE.

### MEDIUM-9: Session Tokens Not Rotated on Privilege Change
- **File:** `internal/admin/api.go`
- **Lines:** Login and role change handlers
- **CWE:** CWE-384 (Session Fixation)
- **Description:** When a user's role or privileges are modified (e.g., promoted to admin), existing session tokens are not invalidated or rotated. An attacker with a stolen session token continues to have access even after privilege revocation.
- **Recommendation:** Implement session versioning or token rotation on privilege changes; maintain token revocation list.

### MEDIUM-10: Missing Security Headers on API Responses
- **File:** `internal/admin/api.go`
- **Lines:** `jsonResponse` helper (~line 2018)
- **CWE:** CWE-693 (Protection Mechanism Failure)
- **Description:** The `jsonResponse` helper does not set security headers like `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, or `Content-Security-Policy`. While some may be set by global middleware, API-specific responses should include baseline security headers.
- **Recommendation:** Add security headers to all API responses; ensure `X-Content-Type-Options: nosniff` is present.

### MEDIUM-11: Database Explorer Query Allowlist Bypass Risk
- **File:** `internal/admin/handlers_hosting.go`
- **Lines:** ~2383 (`handleDBExploreQuery`)
- **CWE:** CWE-89 (SQL Injection)
- **Description:** The database explorer uses an allowlist approach for SQL commands, but complex queries or stored procedure calls might bypass the intended restrictions. The allowlist may not cover all dangerous SQL constructs.
- **Recommendation:** Use parameterized queries exclusively; reject any query containing user-controlled identifiers; implement query result limits.

### MEDIUM-12: Mass Assignment in App Environment Update
- **File:** `internal/admin/handlers_app.go`
- **Lines:** ~126 (`handleAppEnvUpdate`)
- **CWE:** CWE-915 (Improperly Controlled Modification of Dynamically-Determined Object Attributes)
- **Description:** The app environment update endpoint accepts arbitrary key-value pairs for environment variables. There is no validation of variable names or values, allowing injection of sensitive variables (e.g., `PATH`, `LD_PRELOAD`) or overwriting of system-critical environment variables.
- **Recommendation:** Validate environment variable names against allowlist (e.g., `[A-Za-z_][A-Za-z0-9_]*`); sanitize values; prevent overwriting of protected variables.

### MEDIUM-13: Terminal Handler Missing Authorization Check
- **File:** `internal/admin/handlers_app.go`
- **Lines:** ~102-105 (`terminalHandler`)
- **CWE:** CWE-284 (Improper Access Control)
- **Description:** The terminal handler is returned without explicit authorization checks in the handler itself. While it may be wrapped by middleware, the handler should enforce its own access controls as defense-in-depth.
- **Recommendation:** Add explicit admin role check inside the terminal handler; log all terminal access.

### MEDIUM-14: Domain Delete Missing Cascade Confirmation
- **File:** `internal/admin/api.go`
- **Lines:** ~2600-2650 (domain delete handler)
- **CWE:** CWE-306 (Missing Authentication for Critical Function)
- **Description:** Domain deletion may not require explicit confirmation of destructive side effects (e.g., deleting associated databases, backups, SSL certificates). A single API call could result in significant data loss.
- **Recommendation:** Require explicit confirmation parameter (`?confirm=true`); return preview of affected resources before deletion; implement soft deletes with recovery period.

---

## LOW Findings

### LOW-1: Missing HSTS Header on Admin API
- **File:** `internal/admin/api.go`
- **Lines:** Global middleware
- **CWE:** CWE-319 (Cleartext Transmission of Sensitive Information)
- **Description:** The admin API may not enforce HTTPS via HSTS headers. If accessed over HTTP, credentials and session tokens could be intercepted.
- **Recommendation:** Add `Strict-Transport-Security` header to all admin API responses; redirect HTTP to HTTPS.

### LOW-2: API Key Stored in Plaintext in Config
- **File:** `internal/config/config.go`
- **Lines:** ~84 (`AdminConfig`)
- **CWE:** CWE-312 (Cleartext Storage of Sensitive Information)
- **Description:** The admin API key is stored in plaintext in the YAML configuration file. While file permissions are set to 0600, plaintext storage increases the risk of credential exposure from backups or logs.
- **Recommendation:** Hash API keys using bcrypt/argon2; store only hashes; regenerate keys on demand.

### LOW-3: Rate Limit Block Time Not Configurable
- **File:** `internal/admin/audit.go`
- **Lines:** ~28-33 (constants)
- **CWE:** CWE-770 (Allocation of Resources Without Limits or Throttling)
- **Description:** Rate limiting parameters (max fails, window, block time) are hard-coded as constants. This prevents administrators from adjusting thresholds based on their threat model or legitimate traffic patterns.
- **Recommendation:** Move rate limit parameters to configuration file; allow per-endpoint customization.

### LOW-4: Missing Request ID Correlation in Error Responses
- **File:** `internal/admin/api.go`
- **Lines:** `jsonError` helper (~line 2018)
- **CWE:** CWE-778 (Insufficient Logging)
- **Description:** Error responses do not include a request ID that correlates with server logs. This makes it difficult for users to report issues and for administrators to debug security incidents.
- **Recommendation:** Include `X-Request-ID` in all error responses; ensure request IDs are propagated through all logs.

### LOW-5: Inconsistent Error Response Format
- **File:** `internal/admin/api.go`
- **Lines:** Throughout
- **CWE:** CWE-1109 (Use of Same Variable for Multiple Purposes)
- **Description:** Some endpoints return `{"error": "message"}`, others return `{"status": "error", "message": "..."}`, and some return plain strings. Inconsistent error formats complicate client error handling and may lead to information disclosure if clients parse responses differently.
- **Recommendation:** Standardize on a single error response format across all endpoints; document the schema.

### LOW-6: Missing Documentation for API Endpoint Authorization Requirements
- **File:** `internal/admin/api.go`
- **Lines:** Route registration (~line 191)
- **CWE:** CWE-1059 (Incomplete Documentation)
- **Description:** There is no centralized documentation or OpenAPI specification indicating which endpoints require which roles or permissions. This makes security audits difficult and increases the risk of misconfigured access controls.
- **Recommendation:** Generate OpenAPI/Swagger documentation from code; annotate each handler with required roles; automate access control tests.

---

## Recommendations Summary

### Immediate Actions (CRITICAL)
1. Add `requireAdmin` checks to all system-level endpoints (firewall, services, packages, databases, backups, cron, config, Cloudflare).
2. Fix certificate upload to verify domain access.
3. Implement database ownership verification for create/drop operations.
4. Add path traversal validation to backup restore.
5. Restrict raw config write to admin with validation and backup.

### Short-Term Actions (HIGH)
1. Implement pagination on all list endpoints.
2. Add field-level allowlists for domain and settings updates.
3. Enforce `Content-Type: application/json` on mutation endpoints.
4. Sanitize all error messages returned to clients.
5. Add `MaxBytesReader` to all endpoints reading request bodies.
6. Implement proper API versioning strategy.

### Medium-Term Actions (MEDIUM)
1. Extend rate limiting to all sensitive endpoints (not just auth).
2. Implement CSRF tokens for state-changing operations.
3. Add per-domain webhook secrets.
4. Rotate sessions on privilege changes.
5. Add security headers to all API responses.
6. Implement soft deletes for domains with recovery period.

### Long-Term Actions (LOW)
1. Hash API keys instead of storing plaintext.
2. Make rate limit parameters configurable.
3. Standardize error response formats.
4. Generate OpenAPI documentation with authorization annotations.
5. Add request ID correlation to all responses.

---

*Report generated by sc-api-security + sc-rate-limiting scanning skills.*
*End of report.*
