# Authorization Flaws Scan Report

**Scan Date:** 2026-05-01
**Scanner:** sc-authz
**Status:** Consolidated from sc-api-security and sc-business-logic findings

## Summary

| Severity | Count |
|----------|-------|
| CRITICAL | 1 |
| HIGH | 6 |
| MEDIUM | 4 |
| LOW | 2 |
| **Total** | **13** |

## CRITICAL Findings

### CRITICAL-1: Authorization Architecture Completely Missing
- **File:** `internal/admin/api.go`, `internal/auth/manager.go`
- **Lines:** All handler functions, `rolePermissions` map (84-101)
- **CWE:** CWE-284 (Improper Access Control), CWE-269 (Improper Privilege Management)
- **Description:** The `auth.Manager` defines a complete RBAC permission system (`RoleAdmin`, `RoleReseller`, `RoleUser`, `rolePermissions` map, `HasPermission()` method) but **NO HTTP handler ever calls `HasPermission()`**. The entire authorization layer is present but unused. Any authenticated user can perform any action.
- **Recommendation:** Immediately add `requireAdmin()` and `requireRole()` middleware. Wire `HasPermission()` into every handler.

## HIGH Findings

See sc-api-security-results.md for detailed endpoint-by-endpoint breakdown of missing authorization checks on:
- Firewall management
- Service control
- Database management
- Backup/restore
- Package installation
- Certificate upload
- Raw config write
