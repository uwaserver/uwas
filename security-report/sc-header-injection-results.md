# sc-header-injection results

No issues found by sc-header-injection.

## Summary

No credible HTTP header injection / response splitting / Host-header-poisoning
vulnerabilities were found in user-reachable code paths. The codebase relies on
Go's `net/http` (which collapses `\r`/`\n` to spaces in header values on write)
and adds its own explicit CRLF-stripping and whitelist sanitization on the few
paths where user-influenced data reaches a header value.

## Scope traced

- `internal/server/` response/header/redirect handling (`server_dispatch.go`,
  `server.go`, `server_htaccess.go`, `header_vars.go`)
- `internal/admin/` download/export handlers (`handlers_files.go`,
  `handlers_database.go`, `handlers_settings.go`), CORS (`api.go`), auth
  (`handlers_auth.go`)
- `internal/handler/fastcgi/handler.go` (PHP response header forwarding,
  X-Accel-Redirect / X-Sendfile)
- `internal/handler/proxy/` (canary/balancer sticky cookies, location proxy
  header copy)
- `internal/handler/static/` (Content-Type / ETag / Content-Encoding)
- `internal/middleware/` (basicauth WWW-Authenticate, compress, imageopt,
  ratelimit Retry-After)
- `internal/terminal/handler.go` (manual WebSocket upgrade response write)

## Defenses observed (why candidates were dismissed)

1. **Stdlib sanitization.** Go's `net/http` replaces `\r` and `\n` with spaces in
   header values when writing the response and validates header values in the
   client, so CRLF injection via `w.Header().Set/Add` is mitigated framework-wide.
   The inbound `r.Host` and request headers are parsed/validated by Go's server,
   so they cannot themselves carry raw CRLF.

2. **Explicit CRLF stripping for header-transform variables.**
   `internal/server/header_vars.go:26` `safeHeaderValue()` strips `\r`/`\n` via
   `strings.Map`, and `substituteHeaderVars()` wraps every `$host`, `$uri`,
   `$remote_addr`, `$request_id` expansion plus the final result. Used at
   `server_dispatch.go:438,448,451` for request/response header transforms.

3. **Content-Disposition uses whitelist sanitization.**
   `internal/admin/handlers_database.go:216-222` maps the DB name to
   `[A-Za-z0-9._-]` (everything else → `_`) before placing it in the filename.
   `handlers_settings.go:235` uses a constant `uwas.yaml`. `handlers_files.go`
   download sets only a static `Content-Type` from a fixed switch.

4. **Reflected CORS origin is validated, not blindly echoed.**
   `internal/admin/api.go:289-295` only sets `Access-Control-Allow-Origin: <origin>`
   after `isAllowedOrigin()` (`handlers_auth.go:152-170`) confirms the origin is
   localhost or exactly the admin listener's own `scheme://Host`.

5. **Redirects go through `http.Redirect`.** Location-level
   (`server_dispatch.go:278`), domain redirect (`server_dispatch.go:835`), and
   HTTP→HTTPS (`server.go:1000`) all use `http.Redirect`, which sanitizes the
   Location value. Redirect targets (`domain.Redirect.Target`,
   `loc.Redirect`) are admin-configured, not request-controlled.

6. **Cookies set via `http.SetCookie`.** Sticky-session cookies
   (`proxy/balancer.go:119`, `proxy/canary.go:85`) use `http.SetCookie` with
   values sourced from config (`backendHost`) or the constant `"true"`, and
   `http.SetCookie` sanitizes invalid bytes.

7. **WebSocket upgrade manual write is safe.**
   `internal/terminal/handler.go:112-116` builds the raw `101 Switching Protocols`
   response with `\r\n`, but the only interpolated value is `accept =
   computeAcceptKey(key)`, a SHA-1 + base64 digest that cannot contain CRLF.

8. **FastCGI/proxy header forwarding.** PHP/CGI response headers
   (`fastcgi/handler.go:183-187`) and location-proxy upstream headers
   (`server_dispatch.go:324-328`) are parsed into `http.Header` and re-emitted via
   `Header().Add`, so Go sanitizes them on write; upstreams here are the operator's
   own trusted backends.

## Notes

- `internal/server/domainlog.go:107` and the apps/migrate `WriteString` calls write
  to log/output files, not HTTP response headers; any log-injection concern is out
  of scope for this skill.
