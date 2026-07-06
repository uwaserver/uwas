# sc-business-logic results
> 
> **Status:** This scan was performed 2026-06-26. All findings have been
> reviewed and are **resolved** in the current codebase (v0.8.8, July 2026).
> See [SECURITY-REPORT.md](./SECURITY-REPORT.md) for the full status update
> with per-finding resolution tracking.
>

Summary: UWAS is a hosting control panel, not an e-commerce app, so classic price/coupon/cart manipulation does not apply. The auth/RBAC/2FA workflows are largely well-built (per-username lockout, IP rate limiting, mass-assignment stripping for non-admin domain create, domain-ownership checks via `requireDomainAccess`/`CanManageDomain`, constant-time secret compares, login responses that do not leak account existence). A few genuine business-logic / workflow-bypass weaknesses remain, the strongest being missing TOTP replay protection that is explicitly designed-for but never wired up.

---

## Finding: BIZ-001 — TOTP code replay: replay protection designed but never enforced
- **Severity:** Medium
- **Confidence:** 78
- **File:** internal/admin/api.go:477 (also internal/admin/handlers_auth.go:370, :416)
- **Vulnerability Type:** CWE-294 (Authentication Bypass by Capture-replay) / CWE-308
- **Description:** `ValidateTOTP` returns the matched time step precisely so callers can de-dupe used codes — its own doc comment says: "Return the step that actually matched ... so callers can enforce replay protection across the skew window" (internal/admin/totp.go:46-49). Every one of the three callers discards it: `valid, _ := ValidateTOTP(...)`. No store of consumed `(secret, step)` pairs exists. A 6-digit code is therefore accepted repeatedly throughout its validity window (±1 step → up to ~90s) instead of being burned on first use.

  Evidence (internal/admin/api.go:477):
  ```go
  valid, _ := ValidateTOTP(totpSecret, totpCode)
  if !valid { ... }
  ```
- **Why exploitable:** The second factor is global admin TOTP applied to every state-changing admin request (api.go:469). A code captured once (TLS-terminating proxy, browser/devtools/extension, shoulder-surf, history of a copy-pasted header, or a replayed request that an attacker can resend) can be reused for the rest of its window to satisfy the 2FA gate, defeating the single-use guarantee 2FA is supposed to provide. IP rate limiting does not help because a replay submits a *valid* code and never increments the failure counter.
- **Remediation:** Capture the returned step and reject already-seen steps: keep a small map/set of recently consumed `(secret-id, step)` entries (TTL ≈ 2×period) and fail validation if the step was already used. Apply to all three call sites.
- **References:** https://cwe.mitre.org/data/definitions/294.html

---

## Finding: BIZ-002 — Unverified password change bypasses current-password requirement
- **Severity:** Low
- **Confidence:** 62
- **File:** internal/admin/handlers_auth.go:831-833 (handler) → internal/auth/manager.go:563-569 (UpdateUser)
- **Vulnerability Type:** CWE-620 (Unverified Password Change)
- **Description:** The dedicated change-password endpoint (`POST /auth/users/{username}/password`, handlers_auth.go:467-474) correctly forces non-admin users to supply `current_password` and verifies it via `ChangePassword`. The general update endpoint (`PUT /auth/users/{username}`, handleUserUpdateAuth) accepts a `password` field for the caller's own account and applies it through `UpdateUser` with **no current-password check**:
  ```go
  if req.Password != nil { updates.Password = *req.Password }   // handlers_auth.go:831-833
  // ... currentUser.Username == username path, then:
  s.authMgr.UpdateUser(username, updates)                       // :847
  ```
  This is an inconsistent control: the same security-relevant action (set my password) is guarded in one route and unguarded in the other.
- **Why exploitable:** Requires an already-authenticated/hijacked session or stolen session token. With one, an attacker sets a new password (no knowledge of the old one needed). `UpdateUser` then invalidates existing sessions (manager.go:584), so the attacker re-logs-in with the password they just chose and the legitimate user is locked out — turning a transient session compromise into persistent account takeover. The current-password requirement on the other endpoint exists precisely to stop this.
- **Remediation:** In `handleUserUpdateAuth`, reject `password` for self-service (require the dedicated endpoint), or require and verify `current_password` before allowing a self password change for non-admins.
- **References:** https://cwe.mitre.org/data/definitions/620.html

---

## Finding: BIZ-003 — Username-keyed login lockout enables targeted account-lockout DoS
- **Severity:** Low
- **Confidence:** 50
- **File:** internal/auth/manager.go:249-270 (isLockedOut / recordFailedAttempt), :349-358 (Authenticate)
- **Vulnerability Type:** CWE-645 (Overly Restrictive Account Lockout) / abuse vector
- **Description:** Failed logins are counted per **username** (`m.loginAttempts[username]`), and after 5 failures in 15 min `isLockedOut` rejects all logins for that username — including the legitimate user with the correct password. An attacker who knows (or guesses) an admin/reseller username can keep that account locked out indefinitely by submitting bad passwords every window.
- **Why exploitable:** Login itself does not leak account existence (good — both unknown user and bad password return "invalid credentials", manager.go:358/376), but usernames are often known/enumerable elsewhere (admin, bootstrap-chosen names, audit data). IP rate limiting (api.go:336) throttles a single source, but a distributed/rotating-IP attacker can still drive the per-username counter to lock a specific operator out of the panel. Impact is availability only, hence Low.
- **Remediation:** Pair the username counter with a successful-credential exception (do not count toward lockout once the password matches), prefer per-(username,IP) buckets, or apply progressive delays instead of a hard username-wide block. Ensure the IP-based control is the primary brute-force defense.
- **References:** https://cwe.mitre.org/data/definitions/645.html

---

## Finding: BIZ-004 — Bootstrap admin creation is a check-then-act race (TOCTOU)
- **Severity:** Low
- **Confidence:** 45
- **File:** internal/admin/handlers_auth.go:596-622 (handleAuthBootstrap)
- **Vulnerability Type:** CWE-367 (Time-of-check Time-of-use) / workflow race
- **Description:** The unauthenticated bootstrap endpoint gates on `len(s.authMgr.ListUsers()) != 0` (→ 409) and `apiKey != ""` (→ 403), then calls `CreateUser(... RoleAdmin ...)`. The "no users yet" check and the create are not performed under a single lock, so two concurrent requests with different usernames can both pass the check and both create admin accounts during the first-run window.
- **Why exploitable:** Only reachable in the legitimate first-run state (multi-user enabled, no legacy API key, zero users). An attacker who reaches the panel before/at the same time as the operator can race in their own admin account. Narrow window and preconditions, so Low.
- **Remediation:** Perform the "zero users" check and the admin creation atomically inside the auth manager under its write lock (a single `CreateFirstAdmin` that fails if any user already exists), rather than check-in-handler / create-later.
- **References:** https://cwe.mitre.org/data/definitions/367.html

---

## Defenses observed (no finding)
- Login does not enable account enumeration: identical "invalid credentials" for unknown user vs. wrong password (manager.go:358, :376).
- Non-admin domain create strips sensitive fields (mass-assignment protection) and enforces `CanManageDomain` (handlers_domain.go:188-217); DNS/PHP/files/bandwidth/SFTP mutations gate on `requireDomainAccess`.
- TOTP brute force is bounded by IP rate limiting checked before auth (api.go:336) plus `recordAuthFailure` on bad codes (api.go:479).
- 2FA disable/setup require a valid current TOTP code; setup refuses to overwrite an active secret (handlers_auth.go:305-308, 411-420).
- Constant-time comparison for API keys, PIN, recovery codes, TOTP; sessions invalidated on password change / disable (manager.go:584).
- Bandwidth/quota enforcement uses `> 0` guards so absent/negative limits simply disable enforcement rather than mis-behaving (bandwidth/manager.go:141-171).
