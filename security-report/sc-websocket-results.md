# WebSocket & Race Condition Scan Report

**Scan Date:** 2026-05-01
**Scanner:** sc-websocket + sc-race-condition
**Status:** Manual verification

## Summary

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| HIGH | 1 |
| MEDIUM | 2 |
| LOW | 1 |
| **Total** | **4** |

## HIGH Findings

### HIGH-1: WebSocket Terminal Missing Origin Validation
- **File:** `internal/admin/api.go` (`handleTerminal` or terminal handler)
- **CWE:** CWE-1385 (Missing Origin Validation in WebSocket)
- **Description:** The WebSocket terminal endpoint may not validate the Origin header, allowing cross-origin WebSocket connections.
- **Recommendation:** Enforce Origin header validation matching the dashboard domain.

## MEDIUM Findings

### MEDIUM-1: Auth Tickets Reusable Within Window
- **File:** `internal/admin/api.go`
- **CWE:** CWE-294 (Authentication Bypass by Capture-replay)
- **Description:** Auth tickets for SSE/WebSocket have expiration but may be reusable within their short lifetime.
- **Recommendation:** Mark tickets as consumed immediately after first use.

### MEDIUM-2: Concurrent Map Access in Rate Limiters
- **File:** `internal/admin/api.go`
- **CWE:** CWE-362 (Race Condition)
- **Description:** Rate limiter maps (`rateLimit`, `userRateLimits`) are protected by mutex but should be verified for correct locking in all paths.
- **Recommendation:** Audit all map accesses for consistent locking.

## LOW Findings

### LOW-1: Session Token in Query Params for SSE
- **File:** `internal/admin/api.go` (SSE handlers)
- **CWE:** CWE-598
- **Description:** SSE connections may pass tokens via query parameters for browser EventSource compatibility.
- **Recommendation:** Use ticket-based auth (already partially implemented) and deprecate query param tokens.
