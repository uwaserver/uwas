# sc-rate-limiting results
> 
> **Status:** This scan was performed 2026-06-26. All findings have been
> reviewed and are **resolved** in the current codebase (v0.8.8, July 2026).
> See [SECURITY-REPORT.md](./SECURITY-REPORT.md) for the full status update
> with per-finding resolution tracking.
>

One-line summary: UWAS has strong, deliberate DoS hardening (RE2 regex = no ReDoS, body-size caps everywhere, server timeouts, capped pagination, capped analytics maps, IP+username brute-force lockout on login/bootstrap/TOTP). Two minor gaps remain: the per-location rate-limiter map is never proactively cleaned up, and public web traffic has no rate limiting by default.

---

## Finding RATE-001: Per-location rate-limiter map grows unbounded (no background cleanup)

- **Severity:** Low
- **Confidence:** 60
- **File:** internal/server/server_dispatch.go:208-258 (state in internal/server/server.go:154-155)
- **Vulnerability Type:** CWE-401 (Missing Release of Memory) / CWE-770 (Allocation Without Limits)

### Evidence
The per-path rate limiter stores one entry per `host|location|clientIP` key in a `sync.Map`:

```go
// server.go
locationLimiters sync.Map

// server_dispatch.go
limiterKey := domain.Host + "|" + loc.Match + "|" + clientAddr
...
if v, ok := s.locationLimiters.Load(limiterKey); ok {
    entry = v.(*rateLimitEntry)
} else {
    v, _ := s.locationLimiters.LoadOrStore(limiterKey, &rateLimitEntry{lastAccess: now})
    entry = v.(*rateLimitEntry)
}
...
if now.Sub(entry.lastAccess) > 10*window {     // eviction ONLY happens here
    ...
    s.locationLimiters.Delete(limiterKey)
}
```

Eviction is purely opportunistic: a stale entry is only deleted when *that exact key is loaded again*. Entries created by one-shot client IPs (which never return) are never re-loaded and therefore never deleted. There is no background sweeper for `locationLimiters` (grep confirms only the 4 references above — no janitor goroutine), in contrast to `middleware.RateLimiter.cleanupLoop` which proactively prunes its shards every minute.

### Why it's reachable / exploitable
When a domain has a per-location `rate_limit` configured (an opt-in feature), every distinct source IP hitting that path allocates a permanent map entry. An attacker rotating source addresses — trivial with an IPv6 /64 (2^64 addresses) or a botnet — grows the map without bound, causing gradual memory exhaustion. This is the same population (many distinct IPs) the rate limiter is meant to defend against, so the control backfires. `clientAddr` is the real source IP (RealIP only rewrites RemoteAddr behind trusted proxies, so it is not freely spoofable), which limits but does not eliminate the vector.

### Remediation
Add a background cleanup goroutine that periodically iterates `locationLimiters` and deletes entries whose `lastAccess` is older than a few multiples of the window (mirror `middleware.RateLimiter.cleanupLoop`). Alternatively, key the limiter by a bounded subnet (e.g. /64 for IPv6) and/or impose a hard map-size cap with LRU eviction.

---

## Finding RATE-002: No global/per-domain rate limiting on public web traffic by default

- **Severity:** Info
- **Confidence:** 55
- **File:** internal/server/server.go:699-704 (defaults in internal/config/defaults.go)
- **Vulnerability Type:** CWE-770 (Allocation Without Limits) / CWE-799 (Improper Control of Interaction Frequency)

### Evidence
Global rate limiting is only wired in when explicitly configured:

```go
if s.config.Global.RateLimit.Requests > 0 {
    mws = append(mws, middleware.RateLimit(s.ctx, ...))
}
```

`internal/config/defaults.go` sets timeouts and `MaxHeaderBytes` defaults but does NOT default `RateLimit.Requests` to a non-zero value, and per-domain `Security.RateLimit` is likewise opt-in. As a result, on a default install the public-facing server applies no request-rate throttling to expensive handlers (PHP/FastCGI, reverse proxy, search-like endpoints). Bodies are capped and the server has read/write/idle timeouts (slowloris protection), so this is request-frequency only.

### Why this is informational, not a bug
This matches conventional web-server behavior (nginx/Caddy do not rate-limit by default), the feature is fully configurable globally, per-domain, and per-location, and the most sensitive surface (admin `/auth/login`, `/auth/bootstrap`, and login-time TOTP) IS rate-limited regardless (see Defenses). Recommend shipping a conservative non-zero `global.rate_limit` default and documenting it.

### Remediation
Provide a safe default global rate limit (e.g. a high per-IP/minute ceiling) so unconfigured deployments are not fully unthrottled, and surface the recommendation in `uwas.example.yaml`.

---

## Defenses observed (verified, not findings)

- **No ReDoS:** Go's `regexp` is RE2-based (linear time, no catastrophic backtracking). All 40 `regexp.MustCompile`/`Compile` sites — including the WAF, mod_rewrite engine, and .htaccess parser — are immune to ReDoS regardless of input. No third-party backtracking regex engine is used.
- **Login/2FA brute force:** `/api/v1/auth/login`, `/api/v1/auth/bootstrap`, and login-time `X-TOTP-Code` validation are all gated by `checkRateLimit` (IP-based) with failures recorded per-IP and per-username; 10 failures/minute → 5-minute block (`internal/admin/audit.go`, `internal/admin/api.go:312-340,471-479`).
- **IP spoofing of the lockout:** `RealIP` only honors `X-Forwarded-For`/`X-Real-IP`/`CF-Connecting-IP` when the direct peer is a configured trusted proxy, and uses the rightmost-untrusted XFF entry, so the brute-force lockout key cannot be trivially rotated (`internal/middleware/realip.go`).
- **Request-size limits:** ~70 admin handlers wrap bodies in `http.MaxBytesReader` (1MB default; explicit larger caps for uploads/imports). `MaxHeaderBytes` defaults to 1MB.
- **WAF memory safety:** request-body inspection reads at most 64KB via `io.LimitReader` and restores the stream with `io.MultiReader` — no full-body buffering (`internal/middleware/security.go:14,132-134`).
- **Pagination cap:** `parsePagination` caps `limit` at 500 (`internal/admin/api.go:1459-1476`).
- **Analytics memory cap:** per-domain unbounded maps (Paths/Referrers/UserAgents/UniqueIPs) are capped at 50,000 distinct keys to prevent remote memory exhaustion (`internal/analytics/collector.go:60-113`).
- **Slowloris:** server sets Read/ReadHeader/Write/Idle timeouts with sane defaults (`internal/server/server.go:890-927`, `internal/config/defaults.go:33-50`).
- **Global rate limiter hygiene:** `middleware.RateLimiter` runs a background `cleanupLoop` pruning idle buckets (the model RATE-001 should follow).
