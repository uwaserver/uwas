# Go Security Deep Scan Report

**Scan Date:** 2026-05-01
**Scanner:** sc-lang-go
**Status:** Completed via manual verification

## Summary

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| HIGH | 1 |
| MEDIUM | 2 |
| LOW | 1 |
| **Total** | **4** |

## HIGH Findings

### HIGH-1: In-Memory Session Storage Not Persisted Across Restarts
- **File:** `internal/auth/manager.go`
- **Line:** 104-112
- **CWE:** CWE-522 (Insufficiently Protected Credentials)
- **Description:** Sessions are stored in an in-memory map (`map[string]*Session`). Server restart invalidates all active sessions, and memory dumps could expose session tokens.
- **Recommendation:** Persist sessions to disk (encrypted) or Redis with TTL.

## MEDIUM Findings

### MEDIUM-1: JWT Secret Generated But Never Used
- **File:** `internal/auth/manager.go`
- **Line:** 117-128
- **CWE:** CWE-665 (Improper Initialization)
- **Description:** `jwtSecret` is generated at startup but never read or used for JWT operations. The system uses opaque session tokens instead. This is dead code but could confuse future maintainers.
- **Recommendation:** Remove unused `jwtSecret` field or implement JWT-based auth.

### MEDIUM-2: Global API Key Stored in Plaintext in Config
- **File:** `internal/auth/manager.go`
- **Line:** 110
- **CWE:** CWE-798 (Hardcoded Credentials)
- **Description:** The global admin API key is passed as a plaintext string to `NewManager` and stored in memory. Though not hardcoded in source, it resides in config files.
- **Recommendation:** Hash the global API key similarly to per-user API keys.

## LOW Findings

### LOW-1: Race Condition Potential in Session LastStep Update
- **File:** `internal/auth/manager.go`
- **Line:** 200+ (Authenticate method)
- **CWE:** CWE-362 (Race Condition)
- **Description:** Session `LastStep` field is updated without mutex lock during TOTP verification, though impact is low (TOTP replay within same 30s window).
- **Recommendation:** Lock session during TOTP step update.
