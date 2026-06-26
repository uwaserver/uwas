# sc-open-redirect results

**Summary:** No credible open-redirect (CWE-601) vulnerabilities found. All HTTP redirect destinations are either admin-controlled configuration values or built from a request Host that has already been validated against the configured virtual-host set. The dashboard performs only hardcoded, relative client-side redirects.

No issues found by sc-open-redirect.

## Scope examined

All `http.Redirect` / `Location` sinks in Go HTTP paths plus dashboard client-side navigation:

| Location | Destination source | Verdict |
|----------|--------------------|---------|
| `internal/server/server.go:1000` (HTTP→HTTPS) | `"https://" + r.Host + r.URL.RequestURI()` | Safe. `r.Host` is gated by `s.vhosts.LookupWithStatus(r.Host)` immediately above (server.go:974); unconfigured hosts are rejected with 421/403 before reaching the redirect. Destination host can only be a configured domain. |
| `internal/server/server_dispatch.go:278` (location redirect) | `loc.Redirect` (config) | Safe. Admin-authored YAML location block, not user input. |
| `internal/server/server_dispatch.go:835` (`handleRedirect`) | `domain.Redirect.Target` (+ `RequestURI` if `PreservePath`) | Safe. Target is admin config; only the path is appended from the request. |
| `internal/server/server_htaccess.go:43,88` (rewrite/htaccess redirects) | `result.URI` from rewrite engine | Safe-by-design. Driven by admin/site-owner `.htaccess` and YAML rewrite rules (mod_rewrite compatible). A site owner editing their own `.htaccess` can already serve arbitrary content for their own domain; not a cross-tenant open redirect. |
| `web/dashboard/src/lib/api.ts:126,138,284,292,625,634,831` | `'/_uwas/dashboard/login'` (+ static `?2fa=required`) | Safe. Hardcoded relative paths. |
| `web/dashboard/src/pages/Login.tsx:50,75,92` | `navigate('/')` | Safe. Hardcoded relative path; no `next`/`return_url` parameter is read for post-login navigation. |
| `web/dashboard/src/App.tsx:102,122,219` | `<Navigate to="/login" \|\| "/" replace />` | Safe. Hardcoded relative paths. |

## Verification notes / defenses observed

- No redirect sink consumes a user-supplied query parameter such as `url`, `next`, `return_url`, `redirect_uri`, `callback`, or `goto`. A targeted grep for those parameter names feeding any `http.Redirect`/`Location` returned nothing.
- The HTTP→HTTPS auto-redirect cannot be abused for host-based open redirect because the Host header is validated against configured virtual hosts before the redirect (unknown domains are tracked and rejected, server.go:974-985).
- Login / 2FA flows redirect only to fixed relative paths; there is no open-redirect "return to original page" feature that could be poisoned.
- Go's `http.Redirect` strips control characters from the Location value, so CRLF/header-injection variants are also not reachable here.
- Proxy `Location` handling (`pkg/fastcgi/client.go`, `internal/handler/fastcgi/handler.go`) concerns upstream/PHP responses for the requesting domain, not attacker-chosen redirect destinations.
