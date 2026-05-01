# Authentication Flaws Scan Report

**Scan Date:** 2026-05-01
**Scanner:** sc-auth
**Status:** Consolidated from sc-business-logic findings

## Summary

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| HIGH | 2 |
| MEDIUM | 2 |
| LOW | 1 |
| **Total** | **5** |

## HIGH Findings

### HIGH-1: No Brute Force Protection on Login Endpoint
- **File:** `internal/admin/api.go`
- **Line:** ~4700 (`handleLogin`)
- **CWE:** CWE-307 (Improper Restriction of Excessive Authentication Attempts)
- **Description:** The login endpoint has per-IP rate limiting but no per-username brute force protection. Attackers can distribute attempts across multiple IPs.
- **Recommendation:** Implement per-username account lockout after N failed attempts.

### HIGH-2: Session Tokens Passable via URL Query Parameters
- **File:** `internal/admin/api.go`
- **Line:** ~150 (`authTicket` handling) and SSE handlers
- **CWE:** CWE-598 (Use of GET Request Method With Sensitive Query Strings)
- **Description:** While tickets are used for SSE/WebSocket, the underlying session token mechanism may accept tokens via query params in some paths, causing leakage in browser history and server logs.
- **Recommendation:** Enforce header-only token transmission.

## MEDIUM Findings

### MEDIUM-1: No Password Complexity Requirements
- **File:** `internal/auth/manager.go`
- **Line:** 135-148 (`CreateUser`)
- **CWE:** CWE-521 (Weak Password Requirements)
- **Description:** Password validation only checks non-emptiness. No minimum length, complexity, or breach detection.
- **Recommendation:** Enforce minimum 12 characters, mixed case, digits, symbols.

### MEDIUM-2: Timing Attack on User Existence Check
- **File:** `internal/auth/manager.go`
- **Line:** 192-198 (`Authenticate`)
- **CWE:** CWE-204 (Observable Response Discrepancy)
- **Description:** The code takes different paths depending on whether the user exists (early return vs bcrypt comparison). This leaks user existence via timing.
- **Recommendation:** Always perform bcrypt comparison (with dummy hash) even when user doesn't exist.

## LOW Findings

### LOW-1: No Account Lockout Policy
- **File:** `internal/auth/manager.go`
- **CWE:** CWE-307
- **Description:** No mechanism to disable accounts after repeated failed logins.
- **Recommendation:** Implement progressive delay or temporary lockout.
