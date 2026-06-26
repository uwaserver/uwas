# sc-csrf — CSRF Scan Results

**Summary:** The UWAS admin API uses header-based authentication (tokens in `sessionStorage` sent via `Authorization: Bearer` / `X-Session-Token`, never cookies), which makes it fundamentally CSRF-immune; multiple defense-in-depth layers exist. One genuine origin-validation bug weakens the secondary CSRF/CORS origin check but is not independently exploitable for CSRF given the header-based auth model.

---

## Auth & CSRF model observed (defenses present)

UWAS does **not** use cookie-based session authentication for its control plane. Confirmed:

- **No auth cookies.** The dashboard stores its token in `sessionStorage` (`web/dashboard/src/lib/api.ts:5`) and attaches it explicitly as `Authorization: Bearer <key>` or `X-Session-Token: <token>` headers (`api.ts:30-31`). The only `http.Cookie` reads in Go are proxy sticky-session affinity cookies (`internal/handler/proxy/balancer.go:107`, `canary.go:49`), which set `SameSite=Lax` and are unrelated to auth.
- **Header-based auth is CSRF-immune.** A cross-origin attacker page cannot read the victim's `sessionStorage` and the browser never auto-attaches `Authorization`/`X-Session-Token` headers, so a forged request lands unauthenticated (`internal/admin/api.go:447-451`, returns 401).
- **Defense-in-depth layer 1 — JSON content-type enforcement.** `requireJSONMiddleware` (`internal/admin/api.go:1204-1220`) rejects POST/PUT/PATCH without `Content-Type: application/json`, blocking simple HTML-form CSRF (forms cannot set that content type without a CORS preflight).
- **Defense-in-depth layer 2 — explicit CSRF origin check.** State-changing methods (and "expensive" GETs like `/export`, `/backup`, `/download`) require `X-Requested-With: XMLHttpRequest` or a same-origin `Origin`/`Referer` (`internal/admin/api.go:485-512`).
- **Defense-in-depth layer 3 — restrictive CORS.** Admin CORS reflects only the dashboard's own origin/localhost and never sets `Access-Control-Allow-Credentials` (`internal/admin/api.go:288-296`). Per-domain proxy CORS (`internal/middleware/cors.go`) only reflects explicitly-allowed origins and suppresses credentials on wildcard.
- Public unauthenticated endpoints (`/api/v1/health`, deploy webhooks, login/bootstrap) are not CSRF-relevant (no authenticated victim state to abuse).

---

## Findings

### CSRF-001 — Permissive `localhost` prefix match in `isAllowedOrigin` weakens CSRF/CORS origin check
- **Severity:** Low
- **Confidence:** 42
- **File:** `internal/admin/handlers_auth.go:152-160`
- **Vulnerability Type:** CWE-1385 (Insufficient Verification of Origin) / CWE-352 (CSRF, defense-in-depth)
- **Description:** `isAllowedOrigin` accepts any origin that *starts with* `http://localhost` / `https://localhost` / `http://127.0.0.1` / `https://127.0.0.1`:
  ```go
  if strings.HasPrefix(lower, "http://localhost") ||
     strings.HasPrefix(lower, "https://localhost") ||
     strings.HasPrefix(lower, "http://127.0.0.1") ||
     strings.HasPrefix(lower, "https://127.0.0.1") {
      return true
  }
  ```
  An attacker-controlled origin such as `http://localhost.evil.com` (or `http://127.0.0.1.evil.com`) satisfies `HasPrefix(..., "http://localhost")` and is treated as same-origin. This function backs both the secondary CSRF origin check (`api.go:501-505`) and the reflected CORS `Access-Control-Allow-Origin` (`api.go:289-296`).
- **Why impact is limited / why it is NOT a working CSRF:** The control plane's primary authentication is header-based. Even if an attacker bypasses the origin check from `localhost.evil.com`, the forged request carries no `Authorization`/`X-Session-Token` header (browser won't attach it; attacker JS can't read the victim's `sessionStorage`), so it is rejected at `api.go:447`. The admin CORS response also never emits `Access-Control-Allow-Credentials`, so reflected ACAO cannot be leveraged to read authenticated responses cross-origin. The bug therefore erodes a defense-in-depth layer rather than opening a directly exploitable CSRF.
- **Remediation:** Match origins exactly. Parse the origin URL and compare host to `localhost`/`127.0.0.1`/`::1` (with optional port), e.g.:
  ```go
  u, err := url.Parse(origin)
  if err != nil || u.Host == "" { return false }
  host := u.Hostname()
  if host == "localhost" || host == "127.0.0.1" || host == "::1" { return true }
  ```
  Avoid `strings.HasPrefix` for host/origin allowlisting.
- **References:** https://cwe.mitre.org/data/definitions/352.html , https://cwe.mitre.org/data/definitions/1385.html

---

## Notes / non-issues considered

- `requireJSONMiddleware` exempts `/upload` and `/import` paths from the JSON content-type check (`api.go:1206-1211`) and does not cover DELETE. These remain protected by the header-based auth plus the `X-Requested-With`/same-origin CSRF check in `authMiddleware`, so no CSRF gap.
- The "no credentials configured" bypass (`api.go:283-287`) skips auth and CSRF entirely, but `Start()` refuses to bind a non-loopback listener in that mode (`api.go:233-238`), so it is not remotely reachable.
- Proxy sticky-session cookies (`balancer.go`, `canary.go`) are backend-affinity only, `SameSite=Lax`, not authentication — no CSRF relevance.
