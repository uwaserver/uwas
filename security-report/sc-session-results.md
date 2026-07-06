# sc-session — Session Management Flaw Scan (UWAS)
> 
> **Status:** This scan was performed 2026-06-26. All findings have been
> reviewed and are **resolved** in the current codebase (v0.8.8, July 2026).
> See [SECURITY-REPORT.md](./SECURITY-REPORT.md) for the full status update
> with per-finding resolution tracking.
>

**Summary:** UWAS uses a token-based session model (server-generated 256-bit random tokens, sent via `X-Session-Token` header, stored client-side in `sessionStorage`) with solid core hygiene — new token per login (no fixation), server-side invalidation on logout/password-change/disable, 0600 on-disk persistence, brute-force lockout, and single-use short-lived tickets to keep tokens out of URLs. Two real but low-severity weaknesses were traced: a configured session-lifetime that is silently ignored, and a legacy code path that still accepts the raw session token in a URL query parameter.

---

## Finding SESS-001: `users.session_ttl` configuration is silently ignored — session lifetime hardcoded to 24h

- **Severity:** Low
- **Confidence:** 85
- **File:** `internal/auth/manager.go:398` (sink); `internal/config/config.go:86` (dead config field)
- **CWE:** CWE-613 (Insufficient Session Expiration)

**Evidence:**
```go
// internal/config/config.go:86
SessionTTL   int  `yaml:"session_ttl"`    // Session TTL in hours (default 24)

// internal/auth/manager.go:391-399  (Authenticate)
session := &Session{
    ...
    CreatedAt: time.Now(),
    ExpiresAt: time.Now().Add(24 * time.Hour),   // hardcoded
}
```
`grep` confirms `SessionTTL` / `session_ttl` is **read nowhere** in the codebase outside its struct declaration — no default assignment, no wiring into `auth.NewManager` or `Authenticate`.

**Why it matters:** An operator hardening the deployment (e.g. `session_ttl: 1` to shrink the hijacking/idle window) gets no effect: every session still lives a fixed 24 hours of absolute lifetime with no idle timeout. The documented control is non-functional, giving a false sense of security. Impact is limited because 24h is itself a defensible default and sessions are invalidated on logout/password change/disable.

**Remediation:** Thread `config.Global.Users.SessionTTL` into the auth `Manager` (e.g. pass to `NewManager`, clamp to a sane range, default 24h when ≤0) and use it in `Authenticate` instead of the literal. Consider adding an idle/sliding timeout in `ValidateSession` if shorter inactivity windows are desired.

---

## Finding SESS-002: Raw session token / API key accepted in URL query parameter (`?token=`) legacy fallback

- **Severity:** Low
- **Confidence:** 70
- **File:** `internal/admin/api.go:396-416` (multi-user fallback); `internal/admin/api.go:430-434` (legacy API-key fallback)
- **CWE:** CWE-598 (Use of GET Request Method With Sensitive Query Strings) / session token in URL

**Evidence:**
```go
// internal/admin/api.go:395-416
// Also check token query param for SSE/WebSocket (legacy fallback)
if !authenticated {
    if token := r.URL.Query().Get("token"); token != "" {
        if session, err := s.authMgr.ValidateSession(token); err == nil { ... }
        if !authenticated {
            if u, err := s.authMgr.AuthenticateAPIKey(token); err == nil { ... }
        }
        if authenticated {
            q := r.URL.Query(); q.Del("token"); r.URL.RawQuery = q.Encode()
        }
    }
}
```

**Why it matters:** A live session token (or a long-lived API key) supplied via `?token=` is exposed in browser history, the `Referer` header, any upstream/CDN access logs, and proxy logs *before* the server strips it from `r.URL`. The project already ships the correct fix — the single-use, 30-second `ticket` system (`handleAuthTicket`/`redeemTicket`) — specifically to avoid this leak; the `?token=` path is described in-code as a "legacy fallback" and undercuts that design. Passing an **API key** this way is worse, as keys do not expire.

**Mitigations observed (why this is Low, not Medium):** the token is removed from `r.URL` after auth; the preferred ticket flow is implemented and used by the dashboard; CSRF/origin checks apply to state-changing requests.

**Remediation:** Remove the `?token=` fallback entirely and require the `?ticket=` flow for SSE/WebSocket. If a transition period is needed, log a deprecation warning and gate it behind an explicit opt-in config flag.

---

## Defenses observed (no finding)

- **No session fixation:** tokens are server-generated `crypto/rand` 32-byte values (`generateToken`, `manager.go:810`); client-supplied IDs are never adopted. A fresh token is minted on every `Authenticate`.
- **Server-side invalidation:** `Logout` deletes the token and persists; `UpdateUser`/`ChangePassword`/`DeleteUser` call `invalidateUserSessionsLocked` on password change or disable (`manager.go:584-603,710,649`).
- **Expiry enforced everywhere:** `ValidateSession`, `CleanupSessions`, and load/save all drop expired sessions; a 1h background pruner bounds memory.
- **Secret storage:** `sessions.json`, `users.json`, `auth.json` written with 0600 in a 0700 dir via temp+rename (`persist.go`).
- **Client storage:** dashboard uses `sessionStorage` (per-tab, cleared on close) rather than `localStorage` (`web/dashboard/src/lib/api.ts:5`).
- **Token transport:** sent in `X-Session-Token` header, not a cookie — cookie attribute checks (HttpOnly/Secure/SameSite) are N/A; CSRF is handled via `X-Requested-With`/origin checks.
- **Brute-force protection:** per-username lockout (5 attempts / 15 min) plus per-IP rate limiting before auth.
- **Tickets:** short-lived (30s), single-use, atomic-delete on redeem — keeps real tokens out of URLs for SSE/WebSocket.

## Note (informational, not scored)

`requirePin` (`internal/admin/handlers_auth.go:497-498`) accepts the admin PIN via `?pin=` query param for WebSocket use, with the same URL-leak property as SESS-002 but for a secondary control. Same remediation direction (prefer ticket/header).
