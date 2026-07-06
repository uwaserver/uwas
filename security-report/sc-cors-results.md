# sc-cors results
> 
> **Status:** This scan was performed 2026-06-26. All findings have been
> reviewed and are **resolved** in the current codebase (v0.8.8, July 2026).
> See [SECURITY-REPORT.md](./SECURITY-REPORT.md) for the full status update
> with per-finding resolution tracking.
>

Summary: One CORS/origin-validation weakness found — `isAllowedOrigin` uses prefix matching for localhost/127.0.0.1, letting attacker-controlled hostnames (e.g. `localhost.evil.com`) pass origin validation. Impact is bounded because the admin API never emits `Access-Control-Allow-Credentials` and uses header-based (not cookie) auth.

---

## Finding CORS-001: Broken origin validation via prefix match (localhost/127.0.0.1)

- **Title:** CORS / CSRF origin validation bypass — `strings.HasPrefix` on localhost matches attacker domains
- **Severity:** Medium
- **Confidence:** 75
- **File:** internal/admin/handlers_auth.go:152-170 (matchers at lines 155-158); consumed in internal/admin/api.go:289-296 (CORS reflection) and internal/admin/api.go:499-510 (CSRF origin fallback)
- **CWE:** CWE-346 (Origin Validation Error) / CWE-942 (Permissive Cross-domain Policy)

### Evidence
```go
// internal/admin/handlers_auth.go:152
func isAllowedOrigin(origin string, r *http.Request) bool {
    lower := strings.ToLower(origin)
    if strings.HasPrefix(lower, "http://localhost") ||
        strings.HasPrefix(lower, "https://localhost") ||
        strings.HasPrefix(lower, "http://127.0.0.1") ||
        strings.HasPrefix(lower, "https://127.0.0.1") {
        return true
    }
    ...
}
```
The allow-listed origin is then reflected verbatim:
```go
// internal/admin/api.go:289
if origin := r.Header.Get("Origin"); origin != "" {
    if isAllowedOrigin(origin, r) {
        w.Header().Set("Access-Control-Allow-Origin", origin)   // reflected
        ...
    }
}
```
The same function is the fallback for CSRF origin/referer checks:
```go
// internal/admin/api.go:501
sameOrigin := origin != "" && isAllowedOrigin(origin, r)
```

### Why it is reachable / exploitable
`strings.HasPrefix(lower, "http://localhost")` matches any origin whose host merely *begins* with `localhost`/`127.0.0.1`, including attacker-registered domains such as `http://localhost.evil.com`, `http://127.0.0.1.evil.com`, or `http://localhostevil.com`. An attacker can register/resolve such a hostname publicly, host a page there, and the browser will send `Origin: http://localhost.evil.com`. That value:
1. passes `isAllowedOrigin` and is reflected into `Access-Control-Allow-Origin` on the admin API, and
2. satisfies the CSRF origin/referer fallback at api.go:501-504, defeating that defense-in-depth layer for state-changing requests that omit `X-Requested-With`.

### Mitigations already present (limit impact)
- The admin CORS responder (api.go:289-296) **does not set `Access-Control-Allow-Credentials`**, and admin authentication is entirely header-based (`Authorization: Bearer`, `X-Session-Token`) or query ticket — there is **no cookie-based session** (no `Set-Cookie` anywhere in `internal/admin/`). A cross-origin attacker page therefore cannot attach the victim's credentials, so the reflected ACAO does not yield authenticated data theft, and the CSRF path is not auto-credentialed either.
- The primary CSRF gate is the `X-Requested-With: XMLHttpRequest` header (api.go:497), which cannot be forged cross-origin without a preflight; the origin check is only a secondary path.

These mitigations are why this is rated Medium (origin-validation defect in a security-critical admin path that weakens the CSRF fallback) rather than Critical. Should cookie auth or `Access-Control-Allow-Credentials` ever be introduced, this becomes Critical.

### Remediation
Validate the origin's host exactly instead of using `HasPrefix`. Parse the origin and compare the host (allowing an optional port):
```go
u, err := url.Parse(origin)
if err != nil || u.Host == "" { return false }
host := u.Hostname() // strips port
if host == "localhost" || host == "127.0.0.1" || host == "::1" { return true }
```
Apply the same exact-host comparison for the dashboard origin.

---

## Other CORS surfaces reviewed (no issue)

- **internal/middleware/cors.go (per-domain CORS):** `isOriginAllowed` only returns true for an exact `*` or case-insensitive exact match — no reflection of unvalidated origins. Crucially, credentials are suppressed when a wildcard is configured (`if cfg.AllowCredentials && !isWildcard`), preventing the classic wildcard+credentials anti-pattern. When `*` is configured it reflects the request origin but withholds credentials — acceptable for non-credentialed access. AllowedOrigins is admin-configured (config/domain.go:216), not attacker-controlled.
- No `Access-Control-Allow-Credentials: true` is emitted anywhere with a reflected/wildcard origin.
- Dashboard frontend uses no cookies / `withCredentials` (grep of web/dashboard/src/lib empty).
