# sc-api-security results
> 
> **Status:** This scan was performed 2026-06-26. All findings have been
> reviewed and are **resolved** in the current codebase (v0.8.8, July 2026).
> See [SECURITY-REPORT.md](./SECURITY-REPORT.md) for the full status update
> with per-finding resolution tracking.
>

Summary: The UWAS admin API is generally well-guarded (per-request auth middleware, CSRF origin checks, per-domain `requireDomainAccess`, admin-only `requireAdmin`, PIN gating, HMAC-verified webhooks, secret masking). However several **read** endpoints that have **write** siblings enforcing per-domain authorization are themselves NOT domain-scoped, so a non-admin (`user`/`reseller`) tenant can read other tenants' resources (Broken Object Level Authorization). All findings require multi-user mode (`global.users.enabled`) with a non-admin role to be exploitable — in legacy single-API-key mode every caller is admin, and write paths remain protected.

---

## Finding API-001: DNS zone records readable across tenants (missing per-domain authorization)

- **Severity:** Medium
- **Confidence:** 60
- **File:** internal/admin/handlers_dns.go:73
- **CWE:** CWE-639 (Authorization Bypass Through User-Controlled Key)

Evidence:
```go
func (s *Server) handleDNSRecords(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	cf := s.getDNSProvider()      // global provider creds (admin-configured)
	...
	zone, err := cf.FindZoneByDomain(domain)   // no requireDomainAccess(domain)
	records, err := cf.ListRecords(zone.ID)
	jsonResponse(w, map[string]any{"zone_id": zone.ID, "zone": zone.Name, "records": records})
}
```

Why exploitable: `GET /api/v1/dns/{domain}/records` performs no `requireDomainAccess`/`canAccessDomain` check, unlike its mutating siblings in the same file which all gate on the domain (`handleDNSRecordCreate` line 98, `handleDNSRecordUpdate` line 161, `handleDNSRecordDelete` line 134, `handleDNSSync` line 195). In multi-user mode any authenticated `user`/`reseller` can request the records of an arbitrary domain (`GET /api/v1/dns/competitor.example.com/records`) and receive the full zone record set — A/MX/TXT/CNAME, including infra details and any secrets stored in TXT records — using the globally-configured provider token. This is excessive data exposure / BOLA.

Remediation: Add `if !s.requireDomainAccess(w, r, domain, "dns.list") { return }` to `handleDNSRecords` (and `handleDNSCheck` if it should be scoped), matching the write handlers.

---

## Finding API-002: Cron monitor status (commands + output) readable across tenants

- **Severity:** Low
- **Confidence:** 55
- **File:** internal/admin/handlers_cron.go:14-34
- **CWE:** CWE-639 (Authorization Bypass Through User-Controlled Key)

Evidence:
```go
func (s *Server) handleCronMonitorList(w http.ResponseWriter, r *http.Request) {
	...
	statuses := s.cronMonitor.GetAllStatus()   // ALL domains, no role/domain scoping
	jsonResponse(w, statuses)
}
func (s *Server) handleCronMonitorDomain(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	statuses := s.cronMonitor.GetDomainStatus(host)   // arbitrary host, no requireDomainAccess
	jsonResponse(w, statuses)
}
```
`JobStatus` (internal/cronjob/monitor.go:37) carries `Command`, `Schedule`, and `Output` fields.

Why exploitable: `GET /api/v1/cron/monitor` and `GET /api/v1/cron/monitor/{host}` are reachable by any authenticated user with no admin/domain check (the related `POST /api/v1/cron/execute` is admin-gated, and `POST/DELETE /api/v1/cron` use `requireAdmin`). A non-admin tenant can read other tenants' cron command lines and captured command **output**, which frequently contain file paths, tokens, or DB credentials passed on the command line.

Remediation: Gate `handleCronMonitorList` with `requireAdmin`, and `handleCronMonitorDomain` with `requireDomainAccess(host, ...)`; for non-admins, `GetAllStatus` results should be filtered to domains the user can manage.

---

## Finding API-003: Bandwidth usage readable across tenants (missing per-domain authorization)

- **Severity:** Low
- **Confidence:** 55
- **File:** internal/admin/handlers_bandwidth.go:12-33
- **CWE:** CWE-285 (Improper Authorization)

Evidence:
```go
func (s *Server) handleBandwidthList(w http.ResponseWriter, r *http.Request) {
	statuses := s.bwMgr.GetAllStatus()     // every domain, unscoped
	jsonResponse(w, statuses)
}
func (s *Server) handleBandwidthGet(w http.ResponseWriter, r *http.Request) {
	host := r.PathValue("host")
	status := s.bwMgr.GetStatus(host)      // arbitrary host, no check
	jsonResponse(w, status)
}
```

Why exploitable: `GET /api/v1/bandwidth` and `GET /api/v1/bandwidth/{host}` are open to any authenticated user, while the write sibling `handleBandwidthReset` (line 37) correctly enforces `requireDomainAccess`. A non-admin tenant can read traffic/usage figures for any domain on the server. Lower sensitivity than API-001/002 (numeric usage only) but the same authorization-consistency defect.

Remediation: Scope `handleBandwidthGet` with `requireDomainAccess(host, ...)` and filter `handleBandwidthList` output to the caller's manageable domains (or require admin).

---

## Defenses observed (intentionally not reported)

- Central `authMiddleware` (internal/admin/api.go:274) authenticates every request; refuses to bind a credential-less API to a non-loopback address (api.go:233).
- CSRF protection via `X-Requested-With`/Origin/Referer checks on all mutating methods plus expensive GETs (api.go:485-512); `requireJSONMiddleware` enforces JSON content-type.
- Per-domain write authorization via `requireDomainAccess`/`authorizedDomainRoot` (file manager, WordPress, DNS write, bandwidth reset, SSH keys).
- `requireAdmin` on DB, Docker-DB, SQL explorer, software lifecycle (delegated through `handleSoftwareComposeAction`), services, firewall, packages, MCP, settings, raw config, backups, logs/SSE.
- App deploy webhook is unauthenticated by design but HMAC-SHA256 (GitHub) / constant-time token (GitLab) verified (handlers_apps_webhook.go:450), with constant-time comparison.
- TOTP/recovery, `requirePin` (constant-time) on destructive ops (DB drop, docker remove, user delete, cron delete, terminal).
- Settings GET masks secrets (handlers_settings.go), config export zeroes secrets, pagination capped at 500 (api.go:1466).
- User update/create cannot mass-assign `role` (no Role field in update DTO); reseller creation gated by config flag; self-update restricted to own record.
