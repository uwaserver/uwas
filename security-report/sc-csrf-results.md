# CSRF, CORS & Clickjacking Scan Report

**Scan Date:** 2026-05-01
**Scanner:** sc-csrf + sc-cors + sc-clickjacking
**Status:** Manual verification

## Summary

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| HIGH | 0 |
| MEDIUM | 2 |
| LOW | 2 |
| **Total** | **4** |

## MEDIUM Findings

### MEDIUM-1: No CSRF Tokens for Session-Based Requests
- **File:** `internal/admin/api.go` (all mutation endpoints)
- **CWE:** CWE-352 (Cross-Site Request Forgery)
- **Description:** The admin API relies on session tokens in headers, which provides some CSRF protection for AJAX requests. However, there is no explicit CSRF token mechanism. If cookies were used for session transport, endpoints would be vulnerable.
- **Recommendation:** Add `X-Requested-With` or custom CSRF header validation; ensure cookies use `SameSite=Strict`.

### MEDIUM-2: CORS Configuration Potentially Permissive
- **File:** `internal/middleware/cors.go`
- **CWE:** CWE-942 (Permissive Cross-domain Policy with Untrusted Domains)
- **Description:** Per-domain CORS is configurable. Default/global CORS policy for admin endpoints is not explicitly restrictive.
- **Recommendation:** Default-deny CORS on admin API; only allow dashboard origin.

## LOW Findings

### LOW-1: Security Headers Injected Globally
- **File:** `internal/middleware/security.go`
- **CWE:** N/A
- **Description:** Security headers are present but should be verified for correct values (CSP, X-Frame-Options).
- **Recommendation:** Audit header values for effectiveness.

### LOW-2: Dashboard Embed Path Predictable
- **File:** `internal/admin/api.go`
- **CWE:** CWE-200 (Information Exposure)
- **Description:** Dashboard served at `/_uwas/dashboard/` is a known path.
- **Recommendation:** Not a direct vulnerability but consider path randomization for security-through-obscurity.
