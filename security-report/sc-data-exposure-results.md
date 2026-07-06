# sc-data-exposure results
> 
> **Status:** This scan was performed 2026-06-26. All findings have been
> reviewed and are **resolved** in the current codebase (v0.8.8, July 2026).
> See [SECURITY-REPORT.md](./SECURITY-REPORT.md) for the full status update
> with per-finding resolution tracking.
>

Summary: Two reachable information-disclosure issues found — an incomplete secret-masking control in the raw-config viewer that leaks TLS private keys, 2FA recovery codes and OAuth client secrets to the dashboard, and a domain "debug" endpoint that performs no per-domain authorization and exposes other tenants' filesystem paths and directory listings. Several strong defenses were also observed (access-log query/Referer redaction, dotfile rejection in the static handler, fully sanitized config export, sanitized `/api/v1/config`, no source maps, no pprof/expvar exposure, default error pages contain no internals).

---

## Finding EXPOSE-001: Raw config viewer masks TOTP secret but leaks TLS private key, 2FA recovery codes, and OAuth client secrets

- Title: Incomplete secret masking in `handleConfigRawGet`
- Severity: Medium
- Confidence: 85
- File: internal/admin/handlers_settings.go:241-277 (mask list 264-274; masking impl 572-585)
- Vulnerability Type: CWE-200 (Information Disclosure) / CWE-522 (Insufficiently Protected Credentials)

Evidence — the endpoint reads the live config file and asserts "Secrets ... are masked with asterisks" (line 240), but the mask list only covers a subset:

```go
for _, key := range []string{
    "api_key", "pin_code", "totp_secret", "secret_key", "password",
    "secret_access_key", "api_token", "client_secret",
    "telegram_token", "slack_url", "purge_key",
} {
    content = maskYAMLValue(content, key)
}
```

`maskYAMLValue` only redacts a line whose trimmed text starts with `key + ":"` and only rewrites the inline value on that same line:

```go
if strings.HasPrefix(trimmed, key+":") {
    idx := strings.Index(line, key+":")
    result.WriteString(line[:idx] + key + `: "********"`)
}
```

Secret fields that are persisted to the main config (`s.config.Global.Admin`, confirmed at handlers_auth.go:387 and handlers_settings.go:57/78) and are NOT covered:

- `tls_key` (admin-panel TLS private key PEM) — config/admin.go:11 — no matching mask key.
- `recovery_codes` (2FA recovery codes that bypass TOTP) — config/admin.go:12. This is a YAML list; `recovery_codes:` carries no inline value, so the code items on the following `- <code>` lines are never touched. The codes leak verbatim.
- `google_client_secret` / `github_client_secret` (OAuth secrets) — config/admin.go:21,23. The mask key `client_secret` does not prefix-match `google_client_secret:` / `github_client_secret:`, so both pass through.

Why it's exploitable: The Config Editor dashboard page calls `GET /api/v1/config/raw`, so on every view these secrets are serialized into the JSON response and transmitted to the browser (memory, history, shared-screen, proxy logs, browser dev tools). The masking control exists precisely to stop this; the fully-sanitized export at handleConfigExport (lines 184-217 strip `APIKey`, `PinCode`, `TOTPSecret`, `TLSKey`, `RecoveryCodes`, OAuth secrets, etc.) demonstrates the intended set, and the raw viewer diverges from it. Recovery codes are a 2FA-bypass credential and the TLS key compromises panel transport security. Reachable by any `admin`-role caller (`requireAdmin`), so this is primarily a defense-in-depth / credential-handling gap rather than a privilege break, hence Medium.

Remediation: Reuse the export sanitization set for the raw viewer. Add `tls_key`, `google_client_secret`, `github_client_secret` to the mask list and special-case YAML list values (`recovery_codes`) so the child `- item` lines are redacted. Better: marshal a deep-copied, secret-stripped `config.Config` (as handleConfigExport does) rather than regex-masking raw text.

---

## Finding EXPOSE-002: `handleDomainDebug` exposes any domain's filesystem path, directory listing, and PHP PID with no per-domain authorization

- Title: Domain debug endpoint leaks cross-tenant filesystem/runtime info
- Severity: Medium
- Confidence: 70
- File: internal/admin/handlers_domain_health.go:19-106 (route: internal/admin/routes.go:62)
- Vulnerability Type: CWE-200 (Information Disclosure) / CWE-285 (Improper Authorization)

Evidence — the handler takes the host from the path and returns sensitive details with no `requireAdmin` and no `CanManageDomain`/owned-domain filtering:

```go
func (s *Server) handleDomainDebug(w http.ResponseWriter, r *http.Request) {
    host := r.PathValue("host")
    ...
    result["root"] = domainCfg.Root
    result["php_fpm_address"] = domainCfg.PHP.FPMAddress
    result["web_root_global"] = webRoot
    ...
    entries, _ := os.ReadDir(domainCfg.Root)   // full file listing of the web root
    ...
    result["php_pid"] = inst.PID
    result["cert_issuer"] = certInfo.Issuer
}
```

Contrast with peers that do enforce tenant scoping: `handleDomainHealth` (same file, lines 120-138) filters `targets` by `user.Domains` for non-admins, and `handleDomainRawGet` (handlers_domain.go:1274-1282) calls `s.authMgr.CanManageDomain`. `handleDomainDebug` has neither.

Why it's exploitable: All `/api/v1/*` routes pass through `authMiddleware` (api.go:246/274), so the endpoint is authenticated — but in multi-user mode (`global.users.enabled`) a low-privilege `user`/`reseller` role authenticated with their own API key or session can request `GET /api/v1/domains/{any-host}/debug` for domains they do not own and learn the absolute filesystem root, a directory listing of the document root, the PHP-FPM socket/address, the running PHP-FPM PID, and the certificate issuer of other tenants. This is cross-tenant information disclosure useful for further attacks (path discovery for traversal/LFI, knowing other tenants' app layout). When multi-user mode is disabled the caller is always admin, so impact is limited to deployments using RBAC — hence Medium / confidence 70.

Remediation: Add the same authorization guard used by sibling handlers, e.g. at the top of `handleDomainDebug`:
```go
if user, ok := auth.UserFromContext(r.Context()); ok && user.Role != auth.RoleAdmin {
    if !s.authMgr.CanManageDomain(user, host) {
        jsonError(w, "forbidden", http.StatusForbidden); return
    }
}
```
or gate the whole endpoint behind `requireAdmin` if debug is intended to be admin-only.

---

## Defenses observed (no finding)

- internal/middleware/accesslog.go: query params (`token`, `key`, `code`, `password`, `secret`, `access_token`, `auth`, `signature`, ...) and Referer are redacted before logging — good CWE-532 control.
- internal/handler/static/handler.go:33-60: rejects dotfile path components (`.git`, `.env`, `.htpasswd`) with 403; listing.go also skips dotfiles.
- internal/admin/handlers_settings.go:166-237 (`handleConfigExport`) and api.go:857 (`handleConfig`) return fully secret-stripped output.
- internal/middleware/headers.go:29: deletes `X-Powered-By`.
- web/dashboard/vite.config.ts: no `build.sourcemap` enabled (defaults to off), so no production source maps shipped.
- No `net/http/pprof`/`expvar` registration found in cmd/ or the admin mux.
- internal/server/errors.go: default error pages are static and reveal no stack traces or paths.
- Settings GET (handlers_settings.go:373/392/393) masks `api_key`, `slack_url`, `telegram_token` with `maskSecret`.
