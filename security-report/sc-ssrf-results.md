# SSRF & Path Traversal Scan Report

**Scan Date:** 2026-05-01
**Scanner:** sc-ssrf + sc-path-traversal
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

### HIGH-1: Proxy Handler Forwards to Attacker-Controlled Destinations
- **File:** `internal/handler/proxy/*.go`
- **CWE:** CWE-918 (Server-Side Request Forgery)
- **Description:** The reverse proxy handler forwards requests based on domain configuration. If an attacker can modify a domain's proxy upstream (via missing authorization), they can cause the server to make requests to internal services.
- **Recommendation:** Restrict proxy upstreams to valid IP ranges; block localhost/169.254.0.0/10.0.0.0/8.

## MEDIUM Findings

### MEDIUM-1: Backup Restore Path Traversal
- **File:** `internal/admin/api.go`
- **CWE:** CWE-22 (Path Traversal)
- **Description:** Already documented in sc-api-security-results.md (CRITICAL-4).

### MEDIUM-2: File Upload Missing Extension Validation
- **File:** `internal/admin/api.go` (`handleFileUpload`)
- **CWE:** CWE-434 (Unrestricted Upload of File with Dangerous Type)
- **Description:** File uploads do not appear to restrict executable file types (.php, .sh) in all contexts.
- **Recommendation:** Block uploads of executable extensions.

## LOW Findings

### LOW-1: X-Accel-Redirect / X-Sendfile Paths User-Influenced
- **File:** `internal/handler/fastcgi/*.go`
- **CWE:** CWE-22
- **Description:** FastCGI handler sets X-Accel-Redirect based on PHP response headers. If PHP is compromised, this could expose arbitrary files.
- **Recommendation:** Validate X-Accel-Redirect paths against domain root.
