# sc-ssrf results

**Summary:** UWAS has strong, centralized SSRF defenses across its outbound HTTP paths (webhooks, notifications, uptime monitor, reverse proxy). One real defense-in-depth gap remains: the on-demand domain health-check endpoint fetches `http(s)://<domain.Host>/` with no loopback/private/metadata guard and no dial-time IP control, unlike every other comparable client in the tree.

---

## Defenses observed (context)

UWAS centralizes SSRF policy in `internal/config/validate.go`:

- `SafeDialControl` (validate.go:22) — a `net.Dialer.Control` hook that re-checks the *actual* dial IP, closing the DNS-rebinding TOCTOU window. Wired into the **webhook** client (`internal/webhook/manager.go:264`) and **notify** client (`internal/notify/channels.go:42-47`).
- `IsWebhookURLSafe` / `IsProxyUpstreamSafe` / `IsPrivateProxyUpstreamSafe` / `IsHostSafe` — pre-flight allow/deny against loopback, private, link-local, cloud-metadata (`169.254.169.254`), documentation and unspecified ranges.
- Redirect re-validation: webhook (`manager.go`) and notify (`channels.go:32`) `CheckRedirect` re-runs the SSRF check on every hop so a `302 Location: http://169.254.169.254/` cannot be smuggled in.
- Admin webhook create/test endpoints validate URLs before persistence (`internal/admin/webhook_handlers.go:67,154`).
- Reverse-proxy / location-proxy upstreams are validated with the proxy policy (`internal/server/server_dispatch.go:295-300`).
- Background uptime monitor guards each probe (`internal/monitor/monitor.go:123`, `monitorURLSafetyCheck = config.IsWebhookURLSafe`).
- GeoIP external lookup validates the IP with `net.ParseIP` before concatenating it into the fixed `ip-api.com` URL (`internal/middleware/geoip.go:311`); host is constant.
- All other outbound clients (Cloudflare API, DNS providers, self-update, cloudflare IP-list) target hardcoded/constant base hosts — not user-controlled.

---

## Findings

### SSRF-001: On-demand domain health check lacks SSRF guard applied to the background monitor
- **Severity:** Low
- **Confidence:** 60
- **File:** internal/admin/handlers_domain_health.go:201 (client built at :172; URL built in `domainHealthURL` :237-245)
- **CWE:** CWE-918 (Server-Side Request Forgery)
- **Description:** `handleDomainHealth` (route `GET /api/v1/domains/health`, routes.go:63) builds `http(s)://<dom.Host>/` for each configured domain and fetches it with `client.Get(url)`. The client (handlers_domain_health.go:172) has **no** `SafeDialControl` and the code applies **none** of the `IsWebhookURLSafe` / loopback / private / cloud-metadata checks that the otherwise-identical background monitor explicitly added in `internal/monitor/monitor.go:119-126` ("a typo or stale entry (e.g. Host: 169.254.169.254) from turning the monitor into an internal-network scanner").
- **Why reachable / exploitable:** `config.IsValidHostname` (validate.go:479-498) accepts bare IPv4 literals (`169.254.169.254`) and single labels (`localhost`), so a domain whose `Host` is an internal address passes validation. When that domain exists in config, the health endpoint connects to it and returns per-target `Code` (status), `Ms` (latency) and `Error` — i.e. semi-blind SSRF usable as an internal port/host scanner and to confirm cloud-metadata reachability. `CheckRedirect` returns `ErrUseLastResponse`, so redirects are not followed (limits chaining). Primary actor is an authenticated admin (the endpoint is admin-API-gated); non-admin users are filtered to their assigned domains (`allowedDomains`, lines 119-138), so a reseller can only trigger it for domains whose `Host` an admin assigned to them, narrowing real-world reach. The same `domainHealthURL` is also called for the per-domain health view.
- **Remediation:** Apply the same guard the monitor uses before the fetch — e.g. `if err := config.IsWebhookURLSafe(url); err != nil { mark down/skip }` — and additionally wire `config.SafeDialControl` into the health client's `Transport.DialContext` (matching webhook/notify) to also defeat DNS rebinding. The `apps://`-derived loopback upstream branch (`appDomainHealthURL`) should remain exempt since it intentionally targets local app listen addresses.

---

## Not vulnerable (verified, excluded)

- `internal/admin/handlers_apps_deploy.go:385` (`probeAppHealth`) — host is hardcoded `127.0.0.1:<def.Port>`; path validated by `validateHealthPath`; intentional loopback probe.
- `internal/middleware/geoip.go:315` — IP validated, fixed remote host.
- `internal/selfupdate/updater.go`, `internal/cloudflare/*`, `internal/dnsmanager/*`, `internal/serverip/detect.go` — constant/operator-fixed base hosts; path segments only.
- Reverse proxy / location proxy — upstreams validated via proxy SSRF policy at dispatch and config time.
