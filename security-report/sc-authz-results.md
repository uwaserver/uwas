# sc-authz — Authorization Flaw Scan (IDOR / Broken Access Control)

**Summary:** The admin API is broadly well-guarded — write/state-changing and system-level endpoints consistently call `requireAdmin` or `requireDomainAccess`/`CanManageDomain`, and the multi-user management endpoints correctly prevent self-role-escalation. However, several **read-only** endpoints that expose **per-domain data are not scoped to the caller's assigned domains**, allowing a non-admin (reseller/user) tenant to read other tenants' data (horizontal IDOR / missing authorization). Separately, the fine-grained `rolePermissions`/`HasPermission` model is **dead code** — actual enforcement is binary (admin vs. domain-scoped), so the documented read-only `user` role can in fact write to any domain assigned to it.

> **Scope note / exploit precondition:** All findings below are only reachable when `global.users.enabled = true` (multi-user RBAC) with `reseller`/`user` accounts. In the default single-API-key deployment, `authMiddleware` injects a virtual `admin` for every request (`internal/admin/api.go:283`), so these are not exploitable there. UWAS ships multi-tenant RBAC as a first-class feature, so these remain valid for that configuration.

---

## Finding AUTHZ-001 — Missing authorization on DNS records read (cross-zone disclosure)

- **Severity:** Medium
- **Confidence:** 75
- **File:** internal/admin/handlers_dns.go:73
- **CWE:** CWE-862 (Missing Authorization)

**Evidence:**
```go
func (s *Server) handleDNSRecords(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	cf := s.getDNSProvider()          // shared, server-wide provider token
	...
	zone, err := cf.FindZoneByDomain(domain)
	records, err := cf.ListRecords(zone.ID)
	jsonResponse(w, map[string]any{"zone_id": zone.ID, "zone": zone.Name, "records": records})
}
```

**Why exploitable:** `GET /api/v1/dns/{domain}/records` performs **no** `requireAdmin` and **no** `requireDomainAccess` check, unlike its write siblings `handleDNSRecordCreate/Update/Delete` (handlers_dns.go:93/126/156) which all call both. Any authenticated non-admin user can supply an arbitrary `{domain}` and have the server use its configured (Cloudflare/Route53/etc.) provider credentials to enumerate the full DNS record set of any zone in the provider account — including records for domains belonging to other tenants or unrelated infrastructure (mail servers, internal hostnames, ACME/SPF/DKIM TXT tokens). The asymmetry with the protected write handlers indicates an oversight, not intent.

**Remediation:** Add `if !s.requireDomainAccess(w, r, domain, "dns.read") { return }` (matching the create/update/delete handlers) at the top of `handleDNSRecords`.

---

## Finding AUTHZ-002 — Cron monitor exposes other tenants' job status/output

- **Severity:** Low
- **Confidence:** 70
- **File:** internal/admin/handlers_cron.go:14
- **CWE:** CWE-639 (Authorization Bypass Through User-Controlled Key)

**Evidence:**
```go
func (s *Server) handleCronMonitorList(...)   { ... statuses := s.cronMonitor.GetAllStatus(); jsonResponse(w, statuses) }
func (s *Server) handleCronMonitorDomain(...) { host := r.PathValue("host"); ... s.cronMonitor.GetDomainStatus(host) ... }
```

**Why exploitable:** `GET /api/v1/cron/monitor` returns every domain's cron execution records, and `GET /api/v1/cron/monitor/{host}` returns any host's records, with no `canAccessDomain`/`requireDomainAccess` filter. The sibling `handleCronExecute` (handlers_cron.go:36) is correctly admin-gated. Cron `JobStatus` can contain command strings and captured stdout/stderr, which may include sensitive data for domains the caller does not own.

**Remediation:** Filter `GetAllStatus()` results through `s.canAccessDomain(r, host)`; gate `handleCronMonitorDomain` with `s.requireDomainAccess(w, r, host, "cron.monitor")`.

---

## Finding AUTHZ-003 — Bandwidth stats readable across tenants

- **Severity:** Low
- **Confidence:** 70
- **File:** internal/admin/handlers_bandwidth.go:12
- **CWE:** CWE-639 (IDOR)

**Evidence:**
```go
func (s *Server) handleBandwidthList(...) { statuses := s.bwMgr.GetAllStatus(); jsonResponse(w, statuses) }
func (s *Server) handleBandwidthGet(...)  { host := r.PathValue("host"); status := s.bwMgr.GetStatus(host); jsonResponse(w, status) }
```

**Why exploitable:** `GET /api/v1/bandwidth` and `GET /api/v1/bandwidth/{host}` return per-domain bandwidth usage/limits for all/any host with no domain-access check, while the mutating `handleBandwidthReset` (handlers_bandwidth.go:35) correctly calls `requireDomainAccess`. A non-admin tenant can read other tenants' traffic volumes.

**Remediation:** Filter `GetAllStatus()` by `s.canAccessDomain`; add `requireDomainAccess` to `handleBandwidthGet`.

---

## Finding AUTHZ-004 — Per-domain analytics readable across tenants

- **Severity:** Low
- **Confidence:** 70
- **File:** internal/admin/api.go:877
- **CWE:** CWE-639 (IDOR)

**Evidence:**
```go
allHandler, hostHandler := a.Handler()
s.mux.HandleFunc("GET /api/v1/analytics", allHandler)
s.mux.HandleFunc("GET /api/v1/analytics/{host}", hostHandler)
```
The analytics `Collector.Handler()` (internal/analytics) takes no auth context and performs no `canAccessDomain` check (the package does not even import `internal/auth`).

**Why exploitable:** `GET /api/v1/analytics` returns traffic analytics for every domain and `GET /api/v1/analytics/{host}` returns any host's analytics, to any authenticated user. Cross-tenant disclosure of traffic patterns, top URLs, referrers, visitor counts.

**Remediation:** Scope the analytics handlers to the caller's domains — either wrap them with a per-domain authorization closure in `SetAnalytics`, or pass the request user into the collector and filter `{host}` / the all-domains list by `canAccessDomain`.

---

## Finding AUTHZ-005 — Uptime monitor & alerts unscoped (all-domain disclosure)

- **Severity:** Low
- **Confidence:** 60
- **File:** internal/admin/api.go:887
- **CWE:** CWE-639 (IDOR)

**Evidence:**
```go
func (s *Server) handleMonitor(...) { jsonResponse(w, s.monitor.Results()) }   // api.go:887
func (s *Server) handleAlerts(...)  { jsonResponse(w, s.alerter.Alerts()) }    // api.go:898
```

**Why exploitable:** `GET /api/v1/monitor` and `GET /api/v1/alerts` return uptime/alert state for all configured domains with no per-tenant filtering, so a non-admin user sees every domain's health and alert history (including down/error states and the hostnames themselves). Same class as AUTHZ-002/003/004. Also applies to `GET /api/v1/stats/domains` (`handleStatsDomains`, api.go:853) which returns per-domain request stats unfiltered.

**Remediation:** Filter results by `canAccessDomain`, or restrict these aggregate views to admins.

---

## Finding AUTHZ-006 — Fine-grained permission model is dead code; `user` role can write to assigned domains

- **Severity:** Low
- **Confidence:** 60
- **File:** internal/auth/manager.go:101
- **CWE:** CWE-863 (Incorrect Authorization)

**Evidence:**
```go
var rolePermissions = map[Role][]Permission{
	RoleAdmin:    { ...all... },
	RoleReseller: { PermDomainRead, PermDomainCreate, PermDomainUpdate, PermDomainDelete, PermUserRead, PermSystemRead, PermCertManage },
	RoleUser:     { PermDomainRead, PermSystemRead },   // documented read-only
}
func (m *Manager) HasPermission(role Role, perm Permission) bool { ... }
```
`HasPermission`/`rolePermissions` are referenced only in the `AuthManager` interface declaration (internal/admin/api.go:144) and tests — **no handler ever calls them.** Actual enforcement is binary: `requireAdmin` (manager.go via handlers) or `CanManageDomain` (manager.go:502), the latter checking only domain membership, not the role's permission set.

**Why it matters:** A `RoleUser` account — documented/intended as read-only (`PermDomainRead` only) — can in practice update domain config (`handleUpdateDomain`), delete domains (`handleDeleteDomain`), edit raw domain YAML, write/delete files, and run WordPress mutations for any domain in its `Domains` list, because those handlers gate solely on `CanManageDomain`. `reseller` and `user` thus have identical capabilities, defeating the role distinction the permission table advertises.

**Remediation:** Either enforce `HasPermission` (e.g. require `PermDomainUpdate`/`PermDomainDelete` in the relevant write handlers in addition to `CanManageDomain`) or remove the unused permission model and document that any assigned domain grants full domain-management rights.

---

## Defenses observed (not flaws)

- Global `authMiddleware` (api.go:274) enforces auth before all non-public routes; constant-time API-key compare; per-IP brute-force rate limiting; CSRF origin check on mutations and expensive GETs; token/ticket stripped from URL after use.
- File manager, WordPress, PHP-per-domain, SSH-keys, bandwidth-reset, DNS write, and domain CRUD (`handleUpdateDomain`/`handleDeleteDomain`/`handleDomainDetail`/`handleDomainRawGet`/`handleDomainRawPut`) all correctly enforce `requireDomainAccess`/`CanManageDomain` for non-admins.
- System-level endpoints (services, firewall, packages, doctor, update, config raw/export, MCP, settings, apps, cron add/delete, cron execute) all require admin; sensitive ones additionally require a PIN.
- Multi-user management endpoints prevent privilege escalation: role is not updatable via `handleUserUpdateAuth`, only admins set `domains`, only admins create users, users cannot delete themselves, password-change is self-or-admin.
</content>
</invoke>
