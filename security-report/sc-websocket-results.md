# sc-websocket results

**Summary:** The WebSocket attack surface (browser terminal → PTY bridge, reverse-proxy WS tunnel) is well defended — origin validation, single-use ticket auth, admin RBAC, and pin gating are all present and correctly ordered. No critical or high-severity, reachable WebSocket vulnerabilities were found. Two low-severity hardening gaps are noted.

---

## Scope reviewed

WebSocket servers / handlers:
- `internal/terminal/handler.go` + `internal/terminal/pty_linux.go` — first-party WS→PTY shell bridge (the only WebSocket *server* UWAS implements; hand-rolled, no gorilla dep).
- `internal/handler/proxy/handler.go:489` `serveWebSocket` — reverse-proxy WS tunnel to operator-configured backends.
- Auth/ticket plumbing: `internal/admin/handlers_auth.go` (`handleAuthTicket`/`redeemTicket`/`requirePin`/`requireAdmin`), `internal/admin/api.go` (auth middleware, ticket redemption at lines 369-417).
- Client: `web/dashboard/src/lib/api.ts:1594` `terminalWSURL`, `web/dashboard/src/pages/Terminal.tsx`.

## Defenses observed (why most candidate bugs are NOT exploitable)

- **Origin validation IS present** on the terminal (`Handler.CheckOrigin`, handler.go:31). With no `AllowedOrigin` configured (the default) it enforces same-origin via `reqOrigin.Host == r.Host`; an empty Origin is rejected. Browsers cannot forge the `Host` header to equal an attacker `Origin`, so cross-site WebSocket hijacking via a browser fails.
- **Authentication is NOT cookie-based.** The terminal requires a short-lived (30s), single-use ticket (`redeemTicket`, deleted on redemption) that can only be minted by an already-authenticated caller presenting a Bearer/session token (`handleAuthTicket`). Because no ambient credential (cookie) is attached by the browser, classic CSWSH (missing-origin + cookie-auth) is structurally impossible here even if the origin check were weaker.
- **Auth ordering is correct:** global auth middleware → `requireAdmin` → `requirePin` all run *before* `UpgradeWebSocket` hijacks the connection (`routes.go:197-202`, `pty_linux.go:31-39`).
- **Frame DoS bounded:** `maxWSPayload = 64KB` rejects oversized frames (handler.go:151).
- **Proxy WS tunnel** runs after vhost lookup and per-domain middleware (IP ACL / BasicAuth / WAF), and forwards to an operator-configured backend; Go's stdlib rejects CR/LF in inbound header values, so no header/CRLF injection into the upstream handshake. Origin enforcement for proxied apps is correctly the backend's responsibility.
- **Client transport selection is correct:** `wss:` is used whenever the page is `https:` (`api.ts:1595`).

## Findings (low severity, hardening)

### WS-001 — Pin secondary credential transmitted in WebSocket URL query string
- **Severity:** Low
- **Confidence:** 45
- **File:** `web/dashboard/src/lib/api.ts:1600` (client) / `internal/admin/handlers_auth.go:497` (server accepts `?pin=`)
- **CWE:** CWE-598 (Information Exposure Through Query Strings)
- **Evidence:**
  - Client: `if (pin) params.set('pin', pin);` then `…/api/v1/terminal?ticket=…&pin=…`
  - Server: `requirePin` falls back to `provided = r.URL.Query().Get("pin")` because "WebSocket connections can't set headers".
- **Why it matters:** The whole point of the ticket system (per the `handleAuthTicket` comment) is to keep secrets out of URLs because URLs leak into access logs, proxy logs, and history. The `pin_code` is a *long-lived* secondary credential (unlike the 30s single-use ticket) yet is still placed in the query string, partially defeating that design. Anyone who can read request URLs on the path (reverse proxy / access log) recovers the standing pin.
- **Exploitability caveat:** Requires log/observer access; the admin listener does not appear to emit an access log by default, and `Terminal.tsx` deliberately avoids echoing the URL on screen. Hence low.
- **Remediation:** Pass the pin the same way as the token — fold it into the ticket at mint time (bind pin verification server-side when issuing the ticket) so neither secret ever appears in the URL; or accept the pin via `Sec-WebSocket-Protocol` subprotocol header (the one header browsers *can* set on `new WebSocket`).

### WS-002 — Interactive root shell can traverse an unencrypted ws:// connection on non-TLS deployments
- **Severity:** Low (Info)
- **Confidence:** 35
- **File:** `web/dashboard/src/lib/api.ts:1595`; `internal/terminal/handler.go:66-77`
- **CWE:** CWE-319 (Cleartext Transmission of Sensitive Information)
- **Evidence:** `const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';` — when the panel is served over plain HTTP (a documented deployment mode; see the `CheckOrigin` comment "UWAS may be deployed on plain HTTP"), the browser terminal — a full interactive shell running as the server user — is tunneled over cleartext `ws://`.
- **Why it matters:** Keystrokes and shell output (credentials, tokens) for a privileged session cross the network unencrypted on HTTP deployments.
- **Exploitability caveat:** The client correctly upgrades to `wss:` on HTTPS, so this is purely an operator TLS-configuration risk; the code does the right thing relative to the page protocol. Informational.
- **Remediation:** Document that the web terminal must only be used over HTTPS; optionally refuse the WS upgrade (server-side) when `r.TLS == nil` unless an explicit `allow_insecure_terminal` flag is set.

## Out-of-scope note (not counted as a WebSocket finding)

`isAllowedOrigin` (`internal/admin/handlers_auth.go:152-160`) uses `strings.HasPrefix(lower, "http://localhost")` / `"http://127.0.0.1"`, which also matches hostnames like `http://localhost.attacker.com`. This is an origin-validation weakness used by the CORS reflector (api.go:290) and the CSRF check (api.go:501), **not** by the terminal WebSocket (which uses the stricter exact-host `CheckOrigin`). It is reported here only for cross-reference; it belongs to the CORS/CSRF scanners and, because admin auth is header-based rather than cookie-based, its practical impact is limited.
