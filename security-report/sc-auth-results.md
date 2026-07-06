# sc-auth results — Authentication flaw scan (UWAS)
> 
> **Status:** This scan was performed 2026-06-26. All findings have been
> reviewed and are **resolved** in the current codebase (v0.8.8, July 2026).
> See [SECURITY-REPORT.md](./SECURITY-REPORT.md) for the full status update
> with per-finding resolution tracking.
>

**Summary:** The core authentication is well-built (bcrypt password hashing, per-username brute-force lockout, `crypto/subtle` constant-time comparisons, session invalidation on password change/disable, single-use SSE/WS tickets, CSRF origin checks, loopback-only guard when unauthenticated). No authentication bypass, no plaintext password storage, and no hardcoded admin credentials were found in reachable code paths. The findings below are real but lower-severity policy / hardening gaps.

Defenses observed (mitigations, not findings):
- Passwords hashed with `bcrypt.DefaultCost` (`internal/auth/manager.go:302,564,702`); SFTP rejects legacy plaintext (`internal/sftpserver/server.go:806`).
- Per-username lockout: 5 failures / 15 min (`internal/auth/manager.go:242-263`) plus per-IP rate limiting in the admin middleware (`internal/admin/audit.go:128`).
- Constant-time compares for API key, PIN, and TOTP (`internal/admin/api.go:435`, `handlers_auth.go:503`, `totp.go:50`).
- `requestIP` uses `RemoteAddr` only, so `X-Forwarded-For` cannot spoof the rate-limit key (`internal/admin/audit.go:259`).
- Admin API refuses to bind to a non-loopback address when no credentials are configured (`internal/admin/api.go:233`).

---

## Finding AUTH-001: No minimum password length / weak password policy
- **Severity:** Medium
- **Confidence:** 85
- **File:** internal/auth/manager.go:281
- **CWE:** CWE-521 (Weak Password Requirements)
- **Evidence:**
  ```go
  func (m *Manager) CreateUser(username, email, password string, role Role, domains []string) (*User, error) {
      if username == "" || password == "" {
          return nil, errors.New("username and password required")
      }
  ```
  The only password check is non-empty. The same gap exists in the HTTP entry points that feed it: `handleAuthBootstrap` (creates the first **admin**, `internal/admin/handlers_auth.go:611-614`), `handleUserCreateAuth` (`handlers_auth.go:782`), `handleUserUpdateAuth`/`UpdateUser` (`manager.go:563`), and `ChangePassword` (`manager.go:689`). Only the unrelated WordPress flow enforces a length (`handlers_software_store`/`handlers_wordpress.go:327`).
- **Why exploitable:** When multi-user auth is enabled an admin (or self-service user) can set a 1-character password. Combined with the public, rate-limited but otherwise open `/api/v1/auth/login`, weak passwords are realistically guessable, leading to account takeover. The lockout (5/15 min) slows but does not prevent guessing of trivially weak passwords.
- **Remediation:** Enforce a minimum length (>=12) and ideally a basic complexity/breached-password check in `CreateUser`/`ChangePassword`/`UpdateUser` before hashing.

## Finding AUTH-002: TOTP codes are replayable within their validity window
- **Severity:** Low
- **Confidence:** 75
- **File:** internal/admin/api.go:477
- **CWE:** CWE-294 (Authentication Bypass by Capture-replay)
- **Evidence:**
  ```go
  valid, _ := ValidateTOTP(totpSecret, totpCode)   // api.go:477 — matched step discarded
  ```
  `ValidateTOTP` returns the matched step specifically so callers can prevent replay, and `auth.Session.LastStep` is declared with the comment "TOTP step (Unix/30) last used — prevents replay" (`internal/auth/manager.go:80`). However, a repo-wide search shows `LastStep` is **never read or written anywhere**, and every caller discards the returned step (`api.go:477`, `handlers_auth.go:370`, `handlers_auth.go:416`). The accepted window is +/-1 period (`totp.go:20`).
- **Why exploitable:** A 6-digit code captured from a request (proxy log, shoulder-surf, network capture, malicious browser extension) can be replayed against the admin API for up to ~90 seconds, defeating the single-use property MFA is supposed to provide.
- **Remediation:** Wire the matched step into per-session/per-admin state (the already-declared `LastStep`) and reject codes whose step is `<=` the last accepted step.

## Finding AUTH-003: Username enumeration via login timing oracle
- **Severity:** Low
- **Confidence:** 60
- **File:** internal/auth/manager.go:353
- **CWE:** CWE-208 (Observable Timing Discrepancy) / CWE-204
- **Evidence:**
  ```go
  user, exists := m.users[username]
  if !exists {
      m.mu.RUnlock()
      m.recordFailedAttempt(username)
      return nil, errors.New("invalid credentials")   // returns WITHOUT running bcrypt
  }
  ...
  if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {  // slow path only for existing users
  ```
  The HTTP layer correctly returns a generic "invalid credentials" message (no message-based enumeration, `handlers_auth.go:555`), but the response time differs sharply: a non-existent user returns immediately while an existing user pays the bcrypt cost.
- **Why exploitable:** An unauthenticated attacker can distinguish valid usernames by measuring login latency, narrowing a credential-stuffing / password-guessing target set.
- **Remediation:** Perform a dummy `bcrypt.CompareHashAndPassword` against a fixed decoy hash when the user does not exist so both paths take comparable time.

## Finding AUTH-004: Admin PIN has no brute-force protection
- **Severity:** Low
- **Confidence:** 55
- **File:** internal/admin/handlers_auth.go:485
- **CWE:** CWE-307 (Improper Restriction of Excessive Authentication Attempts)
- **Evidence:**
  ```go
  if subtle.ConstantTimeCompare([]byte(provided), []byte(pin)) != 1 {
      s.recordAuditR(r, "pin.failed", r.URL.Path, false)   // audited, but NOT fed to recordAuthFailure / rate limiter
      jsonError(w, "invalid_pin", http.StatusForbidden)
      return false
  }
  ```
  `requirePin` gates destructive operations (user delete, DB drop, file delete, terminal — e.g. `routes.go:198`). Failed PIN attempts are only audit-logged; they are not counted by `recordAuthFailure`/`checkRateLimit`, and there is no lockout. The PIN is an operator-set string (`internal/config/admin.go:8`) with no enforced length/complexity and is commonly a short numeric code.
- **Why exploitable:** An attacker who has already obtained a valid admin session or API key (but not the out-of-band PIN) can brute-force a short PIN with unlimited attempts to unlock the most destructive endpoints. Requires prior authentication, hence Low.
- **Remediation:** Route PIN failures through `recordAuthFailure` (or a dedicated counter) with lockout, and enforce a minimum PIN length.
