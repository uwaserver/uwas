# SQL Injection & Command Injection Scan Report

**Scan Date:** 2026-05-01
**Scanner:** sc-sqli + sc-cmdi + sc-rce
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

### HIGH-1: Database Explorer Query Endpoint Accepts Raw SQL
- **File:** `internal/admin/api.go` (`handleDBExploreQuery`)
- **CWE:** CWE-89 (SQL Injection)
- **Description:** The database explorer endpoint accepts raw SQL queries from authenticated users. While auth is required, any authenticated user (including low-privilege) can execute arbitrary SQL.
- **Recommendation:** Restrict to read-only queries; implement query allowlisting; use separate read-only DB user.

## MEDIUM Findings

### MEDIUM-1: Command Execution in Package Installer
- **File:** `internal/install/*.go`
- **CWE:** CWE-78 (OS Command Injection)
- **Description:** Package installation uses `os/exec.Command` with package names. If package names are not strictly validated, command injection is possible.
- **Recommendation:** Use parameterized package managers; validate package names against strict allowlist.

### MEDIUM-2: Shell Command Execution in PHP Manager
- **File:** `internal/phpmanager/*.go`
- **CWE:** CWE-78
- **Description:** PHP installation and management execute shell commands. Input validation on version strings and paths should be verified.
- **Recommendation:** Validate all inputs against strict patterns before shell execution.

## LOW Findings

### LOW-1: Database Import/Export Uses Command Line Tools
- **File:** `internal/admin/api.go` (DB import/export handlers)
- **CWE:** CWE-78
- **Description:** Database import/export may use `mysqldump` or `mysql` CLI tools with user-influenced database names.
- **Recommendation:** Validate database names strictly; use database-native APIs where possible.
