# sc-privilege-escalation — Results
> 
> **Status:** This scan was performed 2026-06-26. All findings have been
> reviewed and are **resolved** in the current codebase (v0.8.8, July 2026).
> See [SECURITY-REPORT.md](./SECURITY-REPORT.md) for the full status update
> with per-finding resolution tracking.
>

Summary: One real privilege-escalation issue found — a non-admin (reseller) can escape the per-domain file-manager jail to the whole filesystem by repointing a managed domain's `root` via `PUT /api/v1/domains/{host}`, because the update path's mass-assignment blocklist omits `root` and the update validator does not enforce root-under-webroot (the create path does). RBAC, role storage, and the user CRUD surface are otherwise solid.

---

## Finding: PRIVESC-001 — Reseller can repoint domain `root` to arbitrary path → full-filesystem access via file manager

- **Severity:** High
- **Confidence:** 80
- **File:** internal/admin/handlers_domain.go:659-701 (mass-assignment blocklist) and internal/admin/handlers_domain.go:768 (`validateDomainUpdateConfig`)
- **Vulnerability Type:** CWE-269 (Improper Privilege Management); related CWE-22 (Path Traversal / containment escape)

### Description
Domain updates run through `handleUpdateDomain`. For non-admin callers a "mass-assignment protection" block rejects a fixed list of sensitive fields:

```go
// internal/admin/handlers_domain.go:659
if currentUser != nil && currentUser.Role != auth.RoleAdmin {
    sensitive := []string{}
    if hasSSL { ... }
    if hasSecurity { ... }
    if hasCache { ... }
    if hasCompression { ... }
    if hasBasicAuth { ... }
    if hasResources { ... }
    if hasAliases { ... }
    if hasHtaccess { ... }
    if hasLocations { ... }
    // php.fpm_address ...
}
```

The list does **not** include `root` (nor `type`). The merge then applies the attacker-supplied root unconditionally:

```go
// internal/config/merge.go:55
if patch.Root != "" {
    merged.Root = patch.Root
}
```

Crucially, the update path validates with `validateDomainUpdateConfig` (→ `config.ValidateDomainPartial`, handlers_domain.go:768 / 1142), which does **not** perform the web-root containment check. That check exists only in the create path's `validateDomainConfig`:

```go
// internal/admin/handlers_domain.go:1129  (create path only)
if d.Root != "" && d.Type != "redirect" {
    if !pathsafe.IsWithinBase(webRoot, d.Root) || !pathsafe.IsWithinBaseResolved(webRoot, d.Root) {
        return fmt.Errorf("root path must be under %s (got %s)", webRoot, d.Root)
    }
}
```

The file manager then treats the domain's `root` as the jail base. A reseller is authorized for their own domain (`requireDomainAccess` → `CanManageDomain` returns true), and `domainRootForFiles`/`authorizedDomainRoot` (internal/admin/handlers_files.go:67-88) hand that root straight to the file-read/write/delete/upload handlers.

### Exploit path
1. Admin enables `global.users.enabled` + `global.users.allow_reseller`, creates a reseller, and assigns it a domain `shop.example.com` (normal reseller workflow).
2. Reseller authenticates (per-user API key or session) and sends:
   `PUT /api/v1/domains/shop.example.com` with body `{"root":"/"}` (or `/etc`, `/root`, `/home/othertenant`).
   - `root` is not in the non-admin blocklist → accepted.
   - `validateDomainUpdateConfig` does not reject out-of-webroot roots → persisted.
3. Reseller now calls the file-manager endpoints they are already authorized for, e.g.
   `GET /api/v1/files/shop.example.com/read?path=etc/shadow`,
   `PUT /api/v1/files/shop.example.com/write`,
   `DELETE /api/v1/files/shop.example.com/delete`.
   Because the jail base is now `/`, pathsafe containment is satisfied for any absolute target — the reseller gains arbitrary read/write/delete as the UWAS process (commonly root).

### Impact
Vertical privilege escalation from the confined "reseller" role to full filesystem read/write as the server process. Enables reading `/etc/shadow`, the UWAS config (admin API key, TOTP secret), other tenants' data, and writing to startup scripts/SSH `authorized_keys` for code execution — i.e. complete host compromise.

### Remediation
- Add `root` (and `type`, `ip`, `upstreams`/proxy targets) to the non-admin sensitive-field blocklist in `handleUpdateDomain` so non-admins cannot change them, mirroring the create-path field stripping in `handleAddDomain`.
- Apply the web-root containment check to the **update** path too: have `validateDomainUpdateConfig` (or the merged result before persist) enforce `pathsafe.IsWithinBase(webRoot, merged.Root)` exactly like `validateDomainConfig`.
- Defense in depth: in `authorizedDomainRoot`, re-assert that the resolved root is within `global.web_root` for non-admin callers before serving file operations.

### References
- https://cwe.mitre.org/data/definitions/269.html

---

## Defenses observed (no finding)
- **Admin authz is enforced per-handler** via `requireAdmin` (internal/admin/handlers_auth.go:513) across the dangerous surface (terminal, database, firewall, services, packages, migrate, backups, settings, config raw, MCP, software install). Spot-checked many: terminal route requires admin + pin (routes.go:197-202); cron add/delete/execute require admin (handlers_files.go:496-536, handlers_cron.go:36-44).
- **No role-from-request mass assignment in user CRUD.** `handleUserUpdateAuth` (handlers_auth.go:795) has no `role` field in its request struct, so a user cannot self-elevate; `handleUserCreateAuth` (handlers_auth.go:736) is admin-only and gates the reseller role behind config; domain reassignment is admin-only.
- **No JWT role-claim trust.** Sessions are random server-side tokens looked up in a map (`ValidateSession`, auth/manager.go:462); API keys are SHA-256 hashed and compared with `crypto/subtle`. Role is always re-read from the stored user, not from a client-presented claim.
- **No default/hardcoded admin credentials.** The "no credentials → virtual admin" fallback (api.go:283) refuses to bind on a non-loopback address (api.go:233-238) and only logs a warning on loopback.
- **Create path is protected.** `validateDomainConfig` enforces root-under-webroot (handlers_domain.go:1129), which is why this issue is reachable only through the update path.
- `handleConfig` (api.go:857) returns a sanitized config with no secrets despite lacking an explicit admin gate.
