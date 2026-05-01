# Cryptography Misuse Scan Report

**Scan Date:** 2026-05-01
**Scanner:** sc-crypto
**Status:** Manual verification

## Summary

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| HIGH | 0 |
| MEDIUM | 1 |
| LOW | 1 |
| **Total** | **2** |

## MEDIUM Findings

### MEDIUM-1: In-Memory JWT Secret Not Persisted
- **File:** `internal/auth/manager.go`
- **Line:** 117-119
- **CWE:** CWE-321 (Use of Hard-coded Cryptographic Key) - partial
- **Description:** JWT secret is generated fresh on every restart. While this means JWTs (if used) would invalidate on restart, it also means the secret is unpredictable but ephemeral. Not a direct vulnerability since JWT is unused.
- **Recommendation:** Remove dead code or persist secret securely.

## LOW Findings

### LOW-1: API Key Hash Uses SHA256 Instead of bcrypt
- **File:** `internal/auth/manager.go`
- **Line:** 177
- **CWE:** CWE-916 (Use of Password Hash With Insufficient Computational Effort)
- **Description:** API keys are hashed with SHA256 (fast) rather than bcrypt (slow). API keys are high-entropy random strings, making brute force impractical, but bcrypt would provide defense-in-depth.
- **Recommendation:** Use bcrypt for API key hashing if performance allows.
