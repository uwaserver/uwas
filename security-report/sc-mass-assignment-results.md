# sc-mass-assignment results

Summary: One real mass-assignment / over-posting weakness found — the non-admin denylist on `PUT /api/v1/domains/{host}` is incomplete and the update validation path drops the document-root containment check, letting a reseller over-post `root` (arbitrary file read → admin compromise) and `type`/`proxy` (SSRF). Most other JSON-decode sites use explicit DTO structs and are safe.

---

## Finding MASS-001: Reseller can over-post `root` on domain update → arbitrary file read

- **Severity:** Critical
- **Confidence:** 80
- **File:** `internal/admin/handlers_domain.go:659-701` (denylist) + `internal/admin/handlers_domain.go:1142-1144` (update validation) + `internal/config/merge.go:55-57`
- **CWE:** CWE-915 (Improperly Controlled Modification of Dynamically-Determined Object Attributes)

### Evidence
`handleUpdateDomain` decodes the request body straight into the persisted model `config.Domain` (`json.Unmarshal(body, &d)`), then for non-admin callers applies a **denylist** of "sensitive" fields:

```go
// internal/admin/handlers_domain.go:658
// Mass-assignment protection: non-admin users cannot modify sensitive fields.
if currentUser != nil && currentUser.Role != auth.RoleAdmin {
    sensitive := []string{}
    if hasSSL { ... }            // ssl
    if hasSecurity { ... }       // security
    if hasCache { ... }          // cache
    if hasCompression { ... }    // compression
    if hasBasicAuth { ... }      // basic_auth
    if hasResources { ... }      // resources
    if hasAliases { ... }        // aliases
    if hasHtaccess { ... }       // htaccess
    if hasLocations { ... }      // locations
    // php.fpm_address
    ...
}
```

The denylist covers `ssl, security, cache, compression, basic_auth, resources, aliases, htaccess, locations, php.fpm_address`. It does **not** cover `root`, `type`, `proxy`, `redirect`, `ip`, `php.env`, `php.index_files`, `php.max_upload`, all of which `config.MergeDomain` happily merges from the patch:

```go
// internal/config/merge.go:55
if patch.Root != "" {
    merged.Root = patch.Root
}
```

Worse, the update path validates with `validateDomainUpdateConfig` → `config.ValidateDomainPartial`, which has **no** web-root containment check:

```go
// internal/admin/handlers_domain.go:1142
func validateDomainUpdateConfig(d *config.Domain) error {
    return config.ValidateDomainPartial(d)
}
```

The containment check (`pathsafe.IsWithinBase(webRoot, d.Root)`) exists **only** on the create path in `validateDomainConfig` (`handlers_domain.go:1129-1133`), not on update.

The static handler then serves files relative to whatever `domain.Root` is, confining requests *within* that root:
```go
// internal/handler/static/handler.go:245,306-310
docRoot := domain.Root
base, baseErr := pathsafe.CachedBase(docRoot)
fullPath := filepath.Join(docRoot, filepath.Clean("/"+resolved))
```

### Why it's exploitable
A `reseller` is a lower-trust tier (`internal/auth/manager.go:502 CanManageDomain` — resellers may only manage their assigned domains). For a domain they legitimately own, e.g. `a.com`:

```
PUT /api/v1/domains/a.com
{ "root": "/" }
```

`root` is not in the denylist, `MergeDomain` applies it, `ValidateDomainPartial` does not reject it (no containment check on update), and it is persisted. Because `pathsafe` only confines requests *within* the configured root, a root of `/` makes the **entire filesystem** "within root":

```
GET https://a.com/etc/passwd
GET https://a.com/<path-to-uwas-config>.yaml   # leaks Global.Admin.APIKey → full admin
```

This escalates a reseller to arbitrary file read of the whole server, including the UWAS config holding the admin API key — i.e., full privilege escalation.

### Remediation
- Replace the denylist with an **allowlist** of fields a non-admin may patch; reject any other key present in the raw JSON.
- Add the web-root containment check to the update path too (call the same `pathsafe.IsWithinBase(webRoot, d.Root)` guard inside `validateDomainUpdateConfig`/after merge), so even admins cannot point a served domain outside the web root by accident.

---

## Finding MASS-002: Reseller can over-post `type`/`proxy`/`redirect` on domain update → SSRF / route hijack

- **Severity:** High
- **Confidence:** 75
- **File:** `internal/admin/handlers_domain.go:659-701` + `internal/config/merge.go:49-51,115-120`
- **CWE:** CWE-915 (with downstream CWE-918 SSRF)

### Evidence
Same incomplete denylist as MASS-001. `MergeDomain` applies attacker-controlled `Type`, `Proxy`, and `Redirect`:

```go
// internal/config/merge.go:49
if patch.Type != "" { merged.Type = patch.Type }
...
// :115
if len(patch.Proxy.Upstreams) > 0 { merged.Proxy = patch.Proxy }
if patch.Redirect.Target != "" { merged.Redirect = patch.Redirect }
```

None of `type`, `proxy`, `redirect` appear in the non-admin sensitive-field list, and `ValidateDomainPartial` does not restrict upstream targets.

### Why it's exploitable
A reseller converts their own domain into a reverse proxy aimed at internal/metadata endpoints:

```
PUT /api/v1/domains/a.com
{ "type": "proxy", "proxy": { "upstreams": [ { "url": "http://169.254.169.254/latest/meta-data/" } ] } }
```

Subsequent requests to `a.com` are proxied to the cloud metadata service / internal services the server can reach — a server-side request forgery primitive driven by a low-trust user. Setting `redirect`/`type=redirect` likewise lets a reseller repurpose routing semantics they should not control.

(Note: `type=app` is separately rejected at `handlers_domain.go:838`, and the `app.*` fields, while also merge-able and not in the denylist, are largely inert because apps are launched via the admin-only `/api/v1/apps` API. `app` over-posting is therefore low-impact on its own and folded into the same root cause.)

### Remediation
Same as MASS-001: allowlist non-admin-patchable fields; explicitly forbid `type`, `proxy`, `redirect`, `root`, `ip`, and `app.*` for non-admin callers.

---

## Areas checked and found safe (defenses observed)

- **Auth / RBAC user management** (`handlers_auth.go:736-855`): `handleUserCreateAuth` and `handleUserUpdateAuth` decode into explicit anonymous DTO structs. Update **cannot** set `role` at all (no field), `domains` requires admin, and `role` on create is validated against the enum and gated by `AllowResller`. No mass-assignment role escalation. Good DTO pattern.
- **2FA / login / bootstrap / change-password** (`handlers_auth.go`): all use field-scoped anonymous structs, not model binding.
- **Apps create/update** (`handlers_apps.go:160,276`): decode full `apps.App` model but are guarded by `requireAdmin` — admins setting `command`/`env` is expected behavior, not a privilege boundary crossing.
- **Settings / firewall / dns / database / backup / cron / php / cloudflare handlers**: use explicit request structs with named fields; not bound to persisted models without filtering.
- The domain update handler does apply correct RBAC for ownership (`CanManageDomain`) and rename, plus body size limits (`MaxBytesReader`) and hostname validation — the gap is specifically the incomplete field denylist + missing root containment on the update path.
