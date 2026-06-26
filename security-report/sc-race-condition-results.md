# sc-race-condition results

Summary: Two real, reachable atomicity/race issues found in the auth path — (1) TOTP one-time codes are never marked consumed so they are replayable within their validity window, and (2) the login lockout check and the failed-attempt increment are not atomic, letting a single concurrent burst bypass the 5-attempts/15-min brute-force limit. Most other shared state (bandwidth counters, rate limiter, tickets, recovery codes, domain CRUD, config maps) is correctly guarded.

---

## Finding RACE-001: TOTP code never consumed — replayable within validity window (missing atomic single-use)

- Severity: Medium
- Confidence: 75
- File: internal/admin/totp.go:35 (ValidateTOTP) ; enforced at internal/admin/api.go:477 ; also internal/admin/handlers_auth.go:370, internal/admin/handlers_auth.go:416 ; designed-but-unused field internal/auth/manager.go:80
- CWE: CWE-294 (Authentication Bypass by Capture-Replay) / CWE-362 (concurrent reuse)

Evidence:

`ValidateTOTP` deliberately returns the matched time step so callers can enforce single-use, and `Session.LastStep` exists for exactly that purpose:

```go
// internal/auth/manager.go:80
LastStep  int64 `json:"last_step,omitempty"` // TOTP step (Unix/30) last used — prevents replay
```

```go
// internal/admin/totp.go:50 — returns the matched step "so callers can enforce
// replay protection across the skew window"
if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
    return true, int64(counter)
}
```

But every caller discards the step and nothing ever reads/writes `LastStep`:

```go
// internal/admin/api.go:477 (per-request 2FA gate for legacy admin auth)
valid, _ := ValidateTOTP(totpSecret, totpCode)
// internal/admin/handlers_auth.go:370 (2fa/verify)
valid, _ := ValidateTOTP(secret, req.Code)
// internal/admin/handlers_auth.go:416 (2fa/disable)
valid, _ := ValidateTOTP(secret, req.Code)
```

`grep -rn LastStep internal/ --include='*.go'` returns only the struct declaration — the replay-protection mechanism is wired nowhere.

Why it is exploitable: with `totpWindow = 1` (±1 period), a captured/observed 6-digit code stays valid for ~60–90 seconds and can be submitted an unlimited number of times in that window — sequentially or concurrently. An attacker who captures one code (shoulder-surf, phishing proxy, TLS-terminating MITM, logging proxy, or a leaked replayed request) can issue further authenticated admin API calls before the window closes. The intended control (mark the step used, reject reuse) was designed but never enforced. The concurrent-reuse case is a classic check-then-act atomicity gap (same OTP accepted by N simultaneous requests).

Remediation: After a successful `ValidateTOTP`, atomically record the returned step (per session/per admin-secret) and reject any code whose step is `<=` the last accepted step. Persist `LastStep` under the existing session lock so the compare-and-update is atomic across concurrent requests.

---

## Finding RACE-002: Brute-force lockout is not atomic — concurrent burst bypasses the 5-attempt limit

- Severity: Medium
- Confidence: 70
- File: internal/auth/manager.go:349 (isLockedOut check) and internal/auth/manager.go:357/370/375 (recordFailedAttempt), within `Authenticate`
- CWE: CWE-362 (Race Condition) / CWE-307 (Improper Restriction of Excessive Authentication Attempts)

Evidence:

```go
// internal/auth/manager.go:348
func (m *Manager) Authenticate(username, password string) (*Session, error) {
    if m.isLockedOut(username) {            // (A) acquires loginAttemptsMu, releases it
        return nil, errors.New("too many failed attempts; try again later")
    }
    ...
    if err := bcrypt.CompareHashAndPassword(...); err != nil {
        m.recordFailedAttempt(username)     // (B) re-acquires loginAttemptsMu later
        return nil, errors.New("invalid credentials")
    }
```

`isLockedOut` (line 249) and `recordFailedAttempt` (line 266) each take/release `loginAttemptsMu` independently, and the slow `bcrypt.CompareHashAndPassword` runs *between* them with no lock held. The check (A) and the increment (B) are therefore not a single atomic transaction.

Why it is exploitable: an attacker who fires N concurrent login requests with wrong passwords for the same username will have all N pass the `isLockedOut` check at (A) before any of them reaches `recordFailedAttempt` at (B), because none has recorded a failure yet. All N proceed to a full password comparison. The intended ceiling of `maxLoginAttempts = 5` per `loginLockoutWindow = 15m` is bypassed for the size of a single concurrent batch (lockout only engages for *later* requests once the records land). This weakens credential brute-force protection on the login endpoint (multi-user `Authenticate`, reachable from the admin login route). bcrypt cost throttles per-request CPU but does not bound the number of guesses admitted in one burst.

Remediation: Make check-and-increment atomic. Either (a) increment a provisional attempt counter under one lock acquisition before doing the bcrypt compare and decrement on success, or (b) hold a per-username gate so that only one in-flight attempt is evaluated at a time, or (c) record the attempt first then check the threshold under the same lock. Combining the lockout check and the failure record into a single critical section closes the window.

---

## Areas reviewed and found safe (no finding)

- internal/admin/handlers_auth.go redeemTicket: single-use ticket delete is atomic under `ticketMu` (check-and-delete in one critical section).
- internal/admin/handlers_settings.go handleUseRecoveryCode: recovery-code compare-and-remove held entirely under `configMu.Lock()` — no double-use race.
- internal/bandwidth/manager.go: usage counters use `atomic.Int64` plus per-usage mutex; concurrent updates safe.
- internal/middleware/ratelimit.go and internal/admin/audit.go recordFailure/recordAuthFailure: guarded by dedicated mutexes (`rlMu`), check+increment in one critical section.
- internal/admin/handlers_domain.go domain create/update: duplicate-hostname check and mutation held under `configMu.Lock()` together — no TOCTOU on duplicate detection.
- internal/middleware/imageopt.go convertImageReal: double-checked locking after `convertMu.Lock()`; cache write is idempotent.
- internal/handler/proxy/balancer.go round-robin counter: `atomic.Uint64`.
