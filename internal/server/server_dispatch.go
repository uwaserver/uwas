package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/uwaserver/uwas/internal/admin"
	"github.com/uwaserver/uwas/internal/build"
	"github.com/uwaserver/uwas/internal/cache"
	"github.com/uwaserver/uwas/internal/config"
	proxyhandler "github.com/uwaserver/uwas/internal/handler/proxy"
	"github.com/uwaserver/uwas/internal/handler/static"
	"github.com/uwaserver/uwas/internal/middleware"
	"github.com/uwaserver/uwas/internal/pathmatch"
	"github.com/uwaserver/uwas/internal/pathsafe"
	"github.com/uwaserver/uwas/internal/router"
)

// graceTTL is how long past its TTL an entry may still be served while a
// refresh runs behind it.
//
// Zero unless stale_while_revalidate is on, and that gate is the whole point:
// grace_ttl defaults to 24 hours, so applying it unconditionally would hand
// day-old content to visitors of every cached site. Entries stored before the
// flag is turned on keep a zero grace and expire the old way, which is the
// conservative direction for a value read off the entry rather than the
// config at serve time.
func (s *Server) graceTTL() time.Duration {
	s.cfgMu().RLock()
	defer s.cfgMu().RUnlock()
	if !s.config.Global.Cache.StaleWhileRevalidate {
		return 0
	}
	if secs := s.config.Global.Cache.GraceTTL; secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}

// defaultCacheTTL reads global.cache.default_ttl under the config lock:
// reload() overwrites the live config in place, so an unlocked read from the
// request path raced with it (F20700).
func (s *Server) defaultCacheTTL() int {
	s.cfgMu().RLock()
	defer s.cfgMu().RUnlock()
	return s.config.Global.Cache.DefaultTTL
}

// cacheTagsFor returns the domain's configured cache tags plus an implicit
// site: tag naming the domain that produced the entry.
//
// The implicit tag is what makes "purge this domain" work. The dashboard used
// to synthesize `site:<host>` client-side and purge by it, but nothing ever
// wrote that tag, so the purge matched nothing, reported success and left the
// cache untouched. Writing it here — through cache.SiteTag, the same
// normalization the cache key uses — is what closes that gap.
//
// The entry also carries the site: tag of the domain's configured host. It
// was tagged only with the host the request named, so purging a domain by its
// own name missed every entry cached under an alias or the canonical www host
// (example.com redirecting to www.example.com purged 0 entries).
//
// The result is always a fresh slice: appending to domain.Cache.Tags would
// write into the shared config slice.
func cacheTagsFor(domain *config.Domain, host string) []string {
	tags := make([]string, 0, len(domain.Cache.Tags)+2)
	tags = append(tags, domain.Cache.Tags...)
	reqTag := cache.SiteTag(host)
	if own := cache.SiteTag(domain.Host); own != reqTag {
		tags = append(tags, own)
	}
	return append(tags, reqTag)
}

// recordSecurityBlock feeds the security-stats counters (and, through the
// stats observer, the autoblocker) when a per-domain guard rejects a request.
// rate-limit and hotlink blocks were never recorded, so the Rate Limit and
// Hotlink counters on the dashboard and Security page sat at 0 no matter how
// many requests they turned away — and the autoblocker's rate signal was never
// fed.
func (s *Server) recordSecurityBlock(ctx *router.RequestContext, r *http.Request, reason string) {
	if s.securityStats == nil {
		return
	}
	ip := ctx.RemoteIP
	if ip == "" {
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			ip = host
		} else {
			ip = r.RemoteAddr
		}
	}
	s.securityStats.Record(ip, r.URL.Path, reason, r.UserAgent())
}

// enforceDomainRateLimit applies the domain's rate limit. It reports false,
// having answered 429 and fed the autoblocker, when the request is over it.
func (s *Server) enforceDomainRateLimit(ctx *router.RequestContext, r *http.Request, guards domainGuards) bool {
	if guards.rateLimit == nil || guards.rateLimit.Allow(guards.rateLimit.Key(r)) {
		return true
	}
	// Report the configured window, not a fixed minute. The global and
	// per-location limiters already do; this one did not, so a domain
	// limiting per 10s told well-behaved clients — crawlers included —
	// to back off six times longer than the limit actually lasts.
	// Add ±25% jitter to spread retry bursts and prevent thundering herd.
	window := int(guards.rateLimit.Window().Seconds())
	jitter := window / 4
	if jitter < 1 {
		jitter = 1
	}
	retry := window - jitter + int(time.Now().UnixNano()%int64(2*jitter+1))
	if retry < 1 {
		retry = 1
	}
	s.recordSecurityBlock(ctx, r, "rate")
	ctx.Response.Header().Set("Retry-After", strconv.Itoa(retry))
	ctx.Response.WriteHeader(http.StatusTooManyRequests)
	ctx.Response.Write([]byte("429 Too Many Requests"))
	return false
}

// proxyOwnsHost reports whether host is a configured reverse-proxy site, whose
// upstream owns every path under it.
func (s *Server) proxyOwnsHost(host string) bool {
	d, ok := s.vhosts.LookupWithStatus(host)
	return ok && d != nil && d.Type == "proxy"
}

func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	// An ESI sub-request runs inside its parent's request: it takes no
	// connection slot of its own and is not counted as a second request.
	esiSub, _ := r.Context().Value(esiSubrequestKey{}).(bool)

	// Connection limiter: reject with 503 when at capacity.
	if s.connLimiter != nil && !esiSub {
		select {
		case s.connLimiter <- struct{}{}:
			defer func() { <-s.connLimiter }()
		default:
			renderErrorPage(w, http.StatusServiceUnavailable)
			return
		}
	}

	// Health check on main port (no auth, fast path). The bare /healthz alias
	// is not the server's on a proxy site's own Host: there it is the upstream
	// application's liveness endpoint, and answering "ok" for it hid both the
	// application's page and an upstream that was down (F28201).
	if r.URL.Path == "/.well-known/health" || (r.URL.Path == "/healthz" && !s.proxyOwnsHost(r.Host)) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")

		s.cfgMu().RLock()
		domainCount := len(s.config.Domains)
		s.cfgMu().RUnlock()

		uptimeSecs := int64(time.Since(s.metrics.StartTime).Seconds())
		resp := `{"status":"ok","uptime_secs":` + strconv.FormatInt(uptimeSecs, 10) + `,"domains":` + strconv.Itoa(domainCount) + `}`
		_, _ = w.Write([]byte(resp))
		return
	}

	// Strip internal ESI header from external requests to prevent ESI bypass/leak.
	if !esiSub {
		r.Header.Del("X-ESI-Subrequest")
	}

	ctx := router.AcquireContext(w, r)
	defer router.ReleaseContext(ctx)

	ctx.Response.Header().Set("Server", "UWAS/"+build.Version)

	// Advertise HTTP/3 support via Alt-Svc header
	if altSvc := s.altSvcHeader(); altSvc != "" {
		ctx.Response.Header().Set("Alt-Svc", altSvc)
	}

	if r.TLS != nil {
		ctx.IsHTTPS = true
	}

	// Metrics + log tracking
	start := time.Now()
	// Per-domain stats are keyed by the vhost that served the request, set
	// after the lookup below. The raw Host header is client-chosen: keying by
	// it gave every wildcard subdomain, alias and arbitrary HTTPS Host its own
	// record (unbounded growth, split stats).
	statsHost := ""
	s.metrics.ActiveConns.Add(1)
	defer func() {
		s.metrics.ActiveConns.Add(-1)
		if esiSub {
			return
		}
		// RecordRequest owns RequestsTotal — it increments the counter and the
		// matching per-status bucket together. An extra Add here counted every
		// request twice, so uwas_requests_total, the panel's request stat and
		// the MCP report all read double the real traffic, while
		// RequestsByCode stayed correct and disagreed with their sum.
		s.metrics.RecordRequest(ctx.Response.StatusCode())
		s.metrics.RecordLatency(time.Since(start))
		s.metrics.BytesSent.Add(ctx.Response.BytesWritten())
		if statsHost != "" {
			s.metrics.RecordDomain(statsHost, ctx.Response.StatusCode(), ctx.Response.BytesWritten())
		}

		// Record to admin log ring buffer (skip internal health checks and monitor)
		// Matched as a substring: the monitor sends a browser-shaped User-Agent
		// to get past CDN bot rules and only keeps UWAS-Monitor as a token
		// inside it.
		isMonitor := strings.Contains(r.UserAgent(), "UWAS-Monitor")
		if s.admin != nil && !isMonitor && r.Host != "localhost:80" && r.Host != "localhost" {
			elapsed := time.Since(start)
			remoteIP := normalizedRemoteIP(r)
			s.admin.RecordLog(admin.LogEntry{
				Time:       start,
				Host:       r.Host,
				Method:     r.Method,
				Path:       r.URL.Path,
				Status:     ctx.Response.StatusCode(),
				Bytes:      ctx.Response.BytesWritten(),
				DurationMS: float64(elapsed.Microseconds()) / 1000.0,
				Duration:   elapsed.Round(time.Millisecond).String(),
				RemoteAddr: r.RemoteAddr,
				Remote:     remoteIP,
				UserAgent:  r.UserAgent(),
			})
		}

		// Record bandwidth usage
		if s.bwMgr != nil {
			// Account against the vhost that served the request, not the raw
			// Host header: the manager keys configured text, so a Unicode
			// domain asked for by its punycode name was never counted and its
			// limit never engaged (F12900).
			bwHost := r.Host
			if statsHost != "" {
				bwHost = statsHost
			}
			// A HEAD response carries no body: handlers still write one
			// (error pages, PHP output) and net/http discards it, so the
			// writer's byte count is not traffic. Billing it let a stream of
			// HEADs fill the quota and block the domain for free (F13200).
			sent := ctx.Response.BytesWritten()
			if r.Method == http.MethodHead {
				sent = 0
			}
			blocked, _ := s.bwMgr.Record(bwHost, sent)
			_ = blocked // blocking is applied on next request via pre-handler check
		}

		// Record analytics
		if s.analytics != nil && statsHost != "" {
			s.analytics.RecordFull(statsHost, r.URL.Path, r.RemoteAddr,
				r.Referer(), r.UserAgent(),
				ctx.Response.StatusCode(), ctx.Response.BytesWritten())
		}

		// Record for alerting (error spike detection)
		if s.alerter != nil {
			s.alerter.RecordRequest(ctx.Response.StatusCode() >= 500)
		}

		// Slow request logging
		elapsed := time.Since(start)
		if s.metrics.SlowThreshold > 0 && elapsed >= s.metrics.SlowThreshold {
			s.logger.Warn("slow request",
				"host", r.Host,
				"method", r.Method,
				"path", r.URL.Path,
				"status", ctx.Response.StatusCode(),
				"duration", elapsed.String(),
				"bytes", ctx.Response.BytesWritten(),
			)
		}
	}()

	// Virtual host lookup
	domain := s.vhosts.Lookup(r.Host)
	if domain == nil {
		renderErrorPage(ctx.Response, http.StatusNotFound)
		return
	}

	ctx.VHostName = domain.Host
	ctx.DocumentRoot = domain.Root
	statsHost = domain.Host

	if code := s.clientCertRefusal(domain, r); code != 0 {
		renderErrorPage(ctx.Response, code)
		return
	}

	// Canonical hostname redirect: honour the domain's primary-URL preference
	// before serving, so a request on the non-canonical host (e.g. the apex
	// when www is primary) 301s to the canonical one instead of answering 200.
	if loc := canonicalRedirectLocation(domain, r); loc != "" {
		// The redirect returns before the domain's guards run, so without this
		// a flood aimed at the non-canonical host (the apex, when www is
		// primary) never met the rate limit: every request got its 301, none
		// was counted, and the autoblocker never saw the flooding IPs.
		if !s.enforceDomainRateLimit(ctx, r, s.guardsFor(domain.Host)) {
			return
		}
		ctx.Response.Header().Set("Location", loc)
		ctx.Response.WriteHeader(http.StatusMovedPermanently)
		return
	}

	if s.rejectNonCloudflareOrigin(ctx.Response, r, domain) {
		return
	}

	// Maintenance mode: serve 503 with Retry-After for all non-allowed IPs
	if domain.Maintenance.Enabled {
		clientAddr := ctx.RemoteIP
		if clientAddr == "" {
			clientAddr = normalizedRemoteIP(r)
		}
		if !maintenanceAllows(domain.Maintenance.AllowedIPs, clientAddr) {
			ctx.Response.Header().Set("Content-Type", "text/html; charset=utf-8")
			if domain.Maintenance.RetryAfter > 0 {
				ctx.Response.Header().Set("Retry-After", strconv.Itoa(domain.Maintenance.RetryAfter))
			}
			ctx.Response.WriteHeader(http.StatusServiceUnavailable)
			msg := domain.Maintenance.Message
			if msg == "" {
				msg = "<html><body style=\"font-family:sans-serif;text-align:center;padding:50px\"><h1>Under Maintenance</h1><p>We'll be back shortly.</p></body></html>"
			}
			ctx.Response.Write([]byte(msg))
			return
		}
	}

	// Bandwidth limit check: block requests to domains that exceeded their limit
	if s.bwMgr != nil && domain.Bandwidth.Enabled && s.bwMgr.IsBlocked(domain.Host) {
		ctx.Response.Header().Set("Retry-After", "3600")
		s.renderDomainErrorFor(ctx.Request, ctx.Response, http.StatusServiceUnavailable, domain)
		return
	}

	// Per-domain WAF (Content-Type aware, with bypass path support).
	// Predicate-form guard precompiled at config load (refactor.md P2/P3);
	// no per-request closure allocation, no one-shot handler wrap.
	// Snapshot all per-domain guards in a single routeMu.RLock to avoid
	// 5 separate lock acquisitions per request.
	guards := s.guardsFor(domain.Host)
	if guards.waf != nil {
		if !guards.waf(ctx.Response, r) {
			return
		}
	}

	// Per-domain security headers (CSP, COEP, COOP, CORP, Permissions-Policy, HSTS, etc.)
	if sh := domain.SecurityHeaders; sh != (config.SecurityHeadersConfig{}) {
		sh.SetHeaders(ctx.Response.Header())
	}

	// Per-domain blocked paths. Enforced here, ahead of the location handlers,
	// rather than in the global SecurityGuard: that guard only knows the
	// built-in list, so one domain's list no longer blocks the same path on
	// every other domain (F2140).
	// Checked on the path as sent and on its canonical form: "/private//x" and
	// "/private/./x" are served as "/private/x" and must meet the same entry
	// (F2533).
	canonPath := pathmatch.Clean(r.URL.Path)
	for _, blocked := range domain.Security.BlockedPaths {
		if strings.Contains(r.URL.Path, blocked) || strings.Contains(canonPath, blocked) {
			s.renderDomainErrorFor(ctx.Request, ctx.Response, http.StatusForbidden, domain)
			return
		}
	}

	// Per-domain IP ACL (whitelist/blacklist) — predicate form (P2/P3).
	// The domain-wide access guards run ahead of the location handlers: a
	// location's redirect / proxy_pass / root answers the request itself, so
	// guards placed after the loop never saw it and an ip_whitelist, geo rule
	// or rate limit did not cover those paths (F3402).
	if guards.ipACL != nil {
		if !guards.ipACL(ctx.Response, r) {
			return
		}
	}

	// Per-domain GeoIP blocking — predicate form (P2/P3).
	if guards.geo != nil {
		if !guards.geo(ctx.Response, r) {
			return
		}
	}

	// Per-domain rate limiting
	if !s.enforceDomainRateLimit(ctx, r, guards) {
		return
	}

	basicAuthChecked := false

	// A browser sends a CORS preflight without credentials, so basic auth below
	// answered it 401 and the request it was asking about could never be made
	// from a page: a domain with both basic_auth and cors was unusable from a
	// browser (F15901). The preflight carries no body and changes nothing; it
	// is answered here, after the WAF, IP, geo, rate-limit and blocked-path
	// guards, and every other request still meets the CORS guard after auth.
	corsDone := false
	if guards.cors != nil && r.Method == http.MethodOptions &&
		r.Header.Get("Origin") != "" && r.Header.Get("Access-Control-Request-Method") != "" {
		corsDone = true
		if !guards.cors(ctx.Response, r) {
			return
		}
	}

	// Per-path location overrides (headers, cache-control, proxy, redirect, static root)
	for _, loc := range domain.Locations {
		if !matchLocation(r.URL.Path, loc.Match) {
			continue
		}

		// Per-path request timeout. locations[].request_timeout was dead
		// configuration: defined, merged and echoed back by the API, and no
		// runtime path read it.
		//
		// The deadline goes on the request context, so everything downstream
		// inherits it — the location proxy below, PHP dispatch (which builds
		// its FastCGI deadline from ctx.Request's context), and any handler
		// that honours cancellation. The loop breaks after this iteration
		// (first match wins, as in nginx), so exactly one location's timeout
		// can ever apply.
		if loc.RequestTimeout.Duration > 0 {
			timedCtx, cancel := context.WithTimeout(r.Context(), loc.RequestTimeout.Duration)
			// handleRequest returns when the request is done, so this releases
			// the timer at the right moment.
			defer cancel()
			r = r.WithContext(timedCtx)
			ctx.Request = r
		}

		// Apply headers + cache-control
		for k, v := range loc.Headers {
			ctx.Response.Header().Set(k, v)
		}
		if loc.CacheControl != "" {
			stampCacheControl(ctx, loc.CacheControl)
		}

		// Per-path rate limit
		if loc.RateLimit != nil && loc.RateLimit.Requests > 0 {
			clientAddr := ctx.RemoteIP
			if clientAddr == "" {
				clientAddr, _, _ = net.SplitHostPort(r.RemoteAddr)
				if clientAddr == "" {
					clientAddr = r.RemoteAddr
				}
			}
			limiterKey := domain.Host + "|" + loc.Match + "|" + clientAddr
			window := loc.RateLimit.Window.Duration
			if window == 0 {
				window = time.Minute
			}
			// One time.Now() per matching rate-limited request (was two), and
			// no per-request rateLimitEntry alloc when the entry already
			// exists in the map. LoadOrStore previously eagerly built the
			// entry even on a hit; now we Load first and fall back to
			// LoadOrStore only on miss. Refs: refactor.md P11.
			now := time.Now()
			var entry *rateLimitEntry
			if v, ok := s.locationLimiters.Load(limiterKey); ok {
				entry = v.(*rateLimitEntry)
			} else {
				v, _ := s.locationLimiters.LoadOrStore(limiterKey, &rateLimitEntry{lastAccess: now})
				entry = v.(*rateLimitEntry)
			}
			entry.mu.Lock()
			// Evict stale entry (>10x window since last access) and treat as new.
			if now.Sub(entry.lastAccess) > 10*window {
				entry.windowStart = now
				entry.count = 1
				entry.lastAccess = now
				entry.mu.Unlock()
				s.locationLimiters.Delete(limiterKey)
			} else {
				if now.Sub(entry.windowStart) > window {
					entry.windowStart = now
					entry.count = 0
				}
				entry.count++
				exceeded := entry.count > int64(loc.RateLimit.Requests)
				entry.lastAccess = now
				entry.mu.Unlock()
				if exceeded {
					s.recordSecurityBlock(ctx, r, "rate")
					// Round up: truncating sent clients back before the window ended (F25801).
					retrySecs := int((window + time.Second - 1) / time.Second)
					if retrySecs < 1 {
						retrySecs = 1
					}
					ctx.Response.Header().Set("Retry-After", strconv.Itoa(retrySecs))
					s.renderDomainErrorFor(ctx.Request, ctx.Response, http.StatusTooManyRequests, domain)
					return
				}
			}
		}

		// Location-level redirect (e.g. /old-path → https://example.com/new-path)
		// Per-path Basic Authentication.
		// If location basic_auth is set, it overrides domain basic_auth for this match.
		locationBasicAuth := domain.BasicAuth
		if loc.BasicAuth != nil {
			locationBasicAuth = *loc.BasicAuth
		}
		if !enforceBasicAuth(ctx.Response, r, domain.Host, locationBasicAuth) {
			return
		}
		basicAuthChecked = true

		// A location's redirect / proxy_pass / root answers the request itself and
		// returns before the hotlink check below, so images served from a
		// location root could be embedded from any site (F3600).
		if !s.hotlinkAllowed(ctx, r, domain) {
			return
		}

		// A location answers the request itself and returns before the
		// domain-level header transforms below, so request_add/request_remove
		// never reached its upstream and response_add/response_remove never
		// reached its client (F4201).
		applyDomainRequestHeaders(r, domain)
		if loc.Redirect != "" {
			applyDomainResponseHeaders(ctx.Response.Header(), r, domain)
			code := loc.RedirectCode
			if code == 0 {
				code = http.StatusMovedPermanently
			}
			http.Redirect(ctx.Response, r, loc.Redirect, code)
			return
		}

		// Location-level proxy_pass (e.g. /api/ → http://127.0.0.1:4000)
		if loc.ProxyPass != "" {
			// The escaped form keeps an encoded "?"/"#" in the path; the
			// decoded one is re-read as query/fragment once concatenated. A
			// match without a trailing slash ("/api") leaves a remainder that
			// need not start with "/", and "http://up" + "@host/x" would make
			// the configured upstream userinfo and the client's host the
			// authority, so the remainder is always rooted (F2620).
			path, pathOK := cleanProxyPassPath(r.URL.EscapedPath())
			if !pathOK {
				s.renderDomainErrorFor(ctx.Request, ctx.Response, http.StatusBadRequest, domain)
				return
			}
			if loc.StripPrefix && !strings.HasPrefix(loc.Match, "~") {
				path = strings.TrimPrefix(path, strings.TrimSuffix(loc.Match, "/"))
				if !strings.HasPrefix(path, "/") {
					path = "/" + path
				}
			}
			targetURL := loc.ProxyPass + path
			if r.URL.RawQuery != "" {
				targetURL += "?" + r.URL.RawQuery
			}
			ssrfCheck := config.IsProxyUpstreamSafe
			if domain.Proxy.AllowPrivateUpstreams {
				ssrfCheck = config.IsPrivateProxyUpstreamSafe
			}
			if err := ssrfCheck(targetURL); err != nil {
				s.logger.Warn("location proxy SSRF blocked", "match", loc.Match, "target", loc.ProxyPass+path, "error", err)
				s.renderDomainErrorFor(ctx.Request, ctx.Response, http.StatusForbidden, domain)
				return
			}
			// Simple single-backend proxy for location blocks
			// The idle timer below cancels this context when the upstream goes
			// quiet; the client has no overall Timeout (F7504).
			reqCtx, cancelReq := context.WithCancel(r.Context())
			defer cancelReq()
			proxyReq, err := http.NewRequestWithContext(reqCtx, r.Method, targetURL, r.Body)
			if err != nil {
				s.renderDomainErrorFor(ctx.Request, ctx.Response, http.StatusBadGateway, domain)
				return
			}
			// The server's body type hides its length from NewRequest, which
			// would send it chunked; keep the client's Content-Length for
			// upstreams that require one (F7502).
			if r.Body != nil && r.Body != http.NoBody && r.ContentLength > 0 {
				proxyReq.ContentLength = r.ContentLength
			}
			for k, vv := range r.Header {
				for _, v := range vv {
					proxyReq.Header.Add(k, v)
				}
			}
			// Launder hop-by-hop headers — including fields NAMED by Connection —
			// with the same implementation the domain proxy uses (RFC 9110
			// §7.6.1). The static-list-only filter used here previously let a
			// client smuggle "Connection: X-Foo" + "X-Foo: v" to the upstream.
			teTrailers := proxyhandler.AcceptsTrailers(proxyReq.Header)
			proxyhandler.RemoveHopByHop(proxyReq.Header)
			if teTrailers {
				// TE is hop-by-hop except for "trailers", which gRPC needs (F25800).
				proxyReq.Header.Set("Te", "trailers")
			}
			// X-Forwarded-For: overwrite with the peer address (same as domain
			// proxy). Do not preserve client-supplied prior hops — backends that
			// trust the leftmost entry would see attacker-chosen identity.
			clientIP := r.RemoteAddr
			if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
				clientIP = host
			}
			proxyReq.Header.Set("X-Forwarded-For", clientIP)
			proxyReq.Header.Set("X-Forwarded-Host", r.Host)
			// The domain proxy writes X-Real-IP / X-Forwarded-Proto itself and
			// strips the forwarding headers it never sets; a location proxy left
			// the client's own copies in place, so a backend preferring
			// Forwarded or X-Real-IP saw an attacker-chosen identity (F3900).
			for _, k := range []string{"Forwarded", "X-Forwarded-Port", "X-Forwarded-Prefix",
				"X-Forwarded-Server", "X-Forwarded-Ssl", "X-Forwarded-Scheme"} {
				proxyReq.Header.Del(k)
			}
			proxyReq.Header.Set("X-Real-IP", clientIP)
			if ctx.IsHTTPS {
				proxyReq.Header.Set("X-Forwarded-Proto", "https")
			} else {
				proxyReq.Header.Set("X-Forwarded-Proto", "http")
			}
			resp, err := locationProxyClient(domain.Proxy.AllowPrivateUpstreams, locationProxyTimeout).Do(proxyReq)
			if err != nil {
				// A request_timeout that fired is a gateway timeout, not a bad
				// gateway: the upstream was reachable, UWAS gave up waiting.
				status := http.StatusBadGateway
				if isDeadline(err) {
					status = http.StatusGatewayTimeout
				}
				// Client.Do wraps the failure in a *url.Error whose text embeds
				// the full request URL, including the visitor's query string
				// (tokens, signed URLs). The access log redacts that query, so
				// log only the cause (F2110).
				logErr := err
				var ue *url.Error
				if errors.As(err, &ue) {
					logErr = ue.Err
				}
				s.logger.Error("location proxy error", "match", loc.Match, "target", loc.ProxyPass,
					"status", status, "error", logErr)
				s.renderDomainErrorFor(ctx.Request, ctx.Response, status, domain)
				return
			}
			defer resp.Body.Close()
			idle := time.AfterFunc(locationProxyTimeout, cancelReq)
			defer idle.Stop()
			resp.Body = &idleResetBody{ReadCloser: resp.Body, reset: func() { idle.Reset(locationProxyTimeout) }}
			// Vary names headers the response already varies on (compression's
			// Accept-Encoding, CORS's Origin); the upstream's own Vary must add
			// to them, not replace them, or a shared cache serves a gzip body
			// to a client that did not ask for it (F4200).
			priorVary := append([]string(nil), ctx.Response.Header().Values("Vary")...)
			for k, vv := range resp.Header {
				// Preserve all upstream values (e.g. Set-Cookie) but replace the
				// middleware default so security headers are never duplicated.
				ctx.Response.Header()[k] = append([]string(nil), vv...)
			}
			mergeVaryNames(ctx.Response.Header(), priorVary)
			applyDomainResponseHeaders(ctx.Response.Header(), r, domain)
			// Same laundering on the response side: the upstream's Connection
			// header and the fields it names must not reach the client.
			proxyhandler.RemoveHopByHop(ctx.Response.Header())
			ctx.Response.WriteHeader(resp.StatusCode)
			// Unknown-length and event-stream bodies are flushed per write, as
			// the domain proxy does, so SSE and long-poll data is not held in
			// the write buffer while the upstream stays open (F7503).
			var dst io.Writer = ctx.Response
			if resp.ContentLength == -1 || isEventStreamHeader(resp.Header) {
				dst = flushingWriter{ctx.Response}
			}
			if _, err := io.Copy(dst, resp.Body); err != nil {
				s.logger.Warn("location proxy: response body copy failed",
					"host", r.Host, "path", r.URL.Path, "error", err)
			}
			return
		}

		// Location-level static root (e.g. /docs/ → /var/www/docs)
		if loc.Root != "" {
			path := r.URL.Path
			if !strings.HasPrefix(loc.Match, "~") {
				path = strings.TrimPrefix(path, strings.TrimSuffix(loc.Match, "/"))
			}
			filePath := filepath.Join(loc.Root, filepath.Clean("/"+path))
			// Security: must stay within loc.Root.
			if !pathsafe.IsWithinBase(loc.Root, filePath) || !pathsafe.IsWithinBaseResolved(loc.Root, filePath) {
				s.renderDomainErrorFor(ctx.Request, ctx.Response, http.StatusForbidden, domain)
				return
			}
			// Same dotfile and listing policy as the domain's own static handler:
			// http.ServeFile serves ".env.production" and renders directory
			// listings that directory_listing: false is meant to prevent (F3601,
			// F3602).
			if hasHiddenSegment(path) || static.IsPHPSourceLeftover(filepath.Base(filePath)) {
				s.renderDomainErrorFor(ctx.Request, ctx.Response, http.StatusForbidden, domain)
				return
			}
			if !domain.DirectoryListing {
				if info, err := os.Stat(filePath); err == nil && info.IsDir() {
					if _, err := os.Stat(filepath.Join(filePath, "index.html")); err != nil {
						s.renderDomainErrorFor(ctx.Request, ctx.Response, http.StatusNotFound, domain)
						return
					}
				}
			}
			// A location root inside the doc root is still under its .htaccess
			// files: <FilesMatch> / Require denies the main static path enforces
			// were skipped here, so a denied dump was served via the location
			// (F3901).
			if domain.Root != "" && pathsafe.IsWithinBase(domain.Root, filePath) {
				isDir := false
				if info, err := os.Stat(filePath); err == nil && info.IsDir() {
					isDir = true
				}
				if deny := s.htaccessAccessReq(domain.Root, filePath, isDir, r); deny.status != 0 {
					s.renderDomainErrorFor(ctx.Request, ctx.Response, deny.status, domain)
					return
				}
			}
			applyDomainResponseHeaders(ctx.Response.Header(), r, domain)
			if !static.ServesMethod(ctx) {
				return
			}
			if s.serveContainedFile(ctx, r, domain, loc.Root, filePath) {
				return
			}
			http.ServeFile(ctx.Response, r, filePath)
			return
		}

		break // first match wins (like Nginx) — if no handler, continue to normal dispatch
	}

	// Per-domain Basic Authentication (unless already evaluated in location matching).
	if !basicAuthChecked {
		if !enforceBasicAuth(ctx.Response, r, domain.Host, domain.BasicAuth) {
			return
		}
	}

	// Per-domain CORS — predicate form (P2/P3). Guard handles preflight
	// inline and returns false when the response was terminated there.
	if guards.cors != nil && !corsDone {
		if !guards.cors(ctx.Response, r) {
			return
		}
	}

	// "LimitRequestBody" in a directory's .htaccess caps request bodies below
	// it, whatever htaccess.mode says (F16901).
	if domain.Root != "" && s.enforceRequestBodyLimit(ctx, domain) {
		return
	}

	// .htaccess import (runtime parse)
	// Skip internal rewrites for paths that should be served directly:
	// - /wp-admin, /wp-includes, /wp-content (WordPress core)
	// - Direct .php file requests (already resolved, no rewrite needed)
	// The rest of .htaccess still applies to them: [F]/[G] rules commonly
	// guard exactly these scripts (xmlrpc.php, wp-includes/*.php), and
	// Redirect, Header and php_value are per-directory, not per-rewrite.
	if domain.Htaccess.Mode == "import" && domain.Root != "" {
		p := r.URL.Path
		skipRewrite := strings.HasPrefix(p, "/wp-admin") ||
			strings.HasPrefix(p, "/wp-includes") ||
			strings.HasPrefix(p, "/wp-content") ||
			hasPHPExt(p)
		if s.applyHtaccess(ctx, domain, !skipRewrite) {
			return
		}
	}

	// Rewrite engine (from YAML config)
	if len(domain.Rewrites) > 0 {
		if s.applyRewrites(ctx, domain) {
			return
		}
	}

	if !s.hotlinkAllowed(ctx, r, domain) {
		return
	}

	// Per-domain header transforms
	applyDomainRequestHeaders(r, domain)
	applyDomainResponseHeaders(ctx.Response.Header(), r, domain)

	// Cache lookup — check global bypass + per-domain bypass rules
	// PHP domains: only cache static assets (images, CSS, JS), never PHP output.
	// PHP responses go through FastCGI and are always dynamic.
	cacheEnabled := s.cache != nil && domain.Cache.Enabled && !cache.ShouldBypass(r)
	if cacheEnabled && domain.Type == "php" {
		// Only cache requests for known static file extensions on PHP domains.
		ext := strings.ToLower(filepath.Ext(r.URL.Path))
		if !phpCacheableStaticExts[ext] {
			cacheEnabled = false
		}
	}
	// TTL of the last matching cache rule, in seconds. Zero means no rule set
	// one and the domain/global value applies. Rules are evaluated in order and
	// the last match wins, matching how Cache-Control is applied just below.
	ruleTTL := 0
	// Cache-Control of the last matching rule; it must win over the handler's.
	ruleCacheControl := ""
	if cacheEnabled {
		// Check per-domain cache bypass rules + set Cache-Control and TTL from rules
		for _, rule := range domain.Cache.Rules {
			if matchPath(r.URL.Path, rule.Match) {
				if rule.Bypass {
					cacheEnabled = false
					break
				}
				if rule.CacheControl != "" {
					stampCacheControl(ctx, rule.CacheControl)
					ruleCacheControl = rule.CacheControl
				}
				if rule.TTL > 0 {
					ruleTTL = rule.TTL
				}
			}
		}
		// Bypass cache for WordPress admin/login paths (always dynamic).
		// Canonical form, so "//wp-admin/x" cannot slip past the prefix (F2531).
		p := pathmatch.Clean(r.URL.Path)
		if strings.HasPrefix(p, "/wp-admin") ||
			strings.HasPrefix(p, "/wp-login") ||
			strings.HasPrefix(p, "/wp-cron") ||
			strings.HasPrefix(p, "/wp-json") ||
			strings.HasPrefix(p, "/xmlrpc") ||
			p == "/wp-comments-post.php" {
			cacheEnabled = false
		}
		// Bypass cache if request has session cookies (WordPress, PHP sessions)
		if cacheEnabled {
			if cookie := r.Header.Get("Cookie"); cookie != "" {
				if strings.Contains(cookie, "wordpress_logged_in") ||
					strings.Contains(cookie, "wp-settings") ||
					strings.Contains(cookie, "PHPSESSID") ||
					strings.Contains(cookie, "comment_author") ||
					strings.Contains(cookie, "woocommerce_cart") ||
					strings.Contains(cookie, "woocommerce_items") {
					cacheEnabled = false
				}
			}
		}
	}
	// Compute cache key once for both lookup and store (avoids recomputing
	// the key + reallocating the vary slice on the Set path).
	var cacheKey string
	if cacheEnabled {
		cacheKey = s.cache.Key(r)
		cached, status := s.cache.GetByKey(cacheKey)
		if cached != nil && (status == cache.StatusHit || status == cache.StatusStale) {
			// The cache is filled by whoever the .htaccess allowed; it must not
			// answer a client the per-directory rules deny (F4801).
			if deny := s.htaccessAccessForURL(ctx, domain); deny.status != 0 {
				s.renderDomainErrorFor(ctx.Request, ctx.Response, deny.status, domain)
				return
			}
			ctx.CacheStatus = status
			s.metrics.RecordCache(status)

			// Serve the stale copy now, refresh behind it. This is the point of
			// the grace window: the visitor waiting on this request should not
			// pay for the origin round trip that makes the entry fresh again.
			if status == cache.StatusStale {
				ttl := cacheTTLFor(ruleTTL, domain.Cache.TTL, s.defaultCacheTTL())
				s.scheduleRevalidate(domain, s.staleJobFor(cacheKey, r, domain, ttl, s.graceTTL()))
			}
			ctx.Response.Header().Set("X-Cache", status)
			ctx.Response.Header().Set("Age", strconv.FormatInt(int64(cached.Age().Seconds()), 10))
			for k, vals := range cached.Headers {
				ck := http.CanonicalHeaderKey(k)
				if ck == "X-Request-Id" { // this request's own ID stays (F5700)
					continue
				}
				ctx.Response.Header()[ck] = append([]string(nil), vals...)
			}
			// An entry stored before the rule existed carries the upstream's
			// Cache-Control; the rule still applies to what the client sees.
			if ruleCacheControl != "" && cached.StatusCode < 400 {
				ctx.Response.Header().Set("Cache-Control", ruleCacheControl)
			}

			// Handle conditional requests against cached ETag
			if etag := ctx.Response.Header().Get("Etag"); etag != "" {
				if match := r.Header.Get("If-None-Match"); match != "" && etagMatches(match, etag) {
					ctx.Response.WriteHeader(http.StatusNotModified)
					return
				}
			}

			body := cached.Body
			// ESI assembly on cache hit: replace ESI tags with cached/fetched fragments
			if cached.ESITemplate && domain.Cache.ESI && s.esiProcessor != nil &&
				r.Header.Get("X-ESI-Subrequest") == "" {
				assembled, err := s.esiProcessor.Process(body, r.Host, r, cacheTagsFor(domain, domain.Host), 0)
				if err == nil {
					body = assembled
				}
				ctx.Response.Header().Set("Content-Length", strconv.Itoa(len(body)))
			}
			ctx.Response.WriteHeader(cached.StatusCode)
			ctx.Response.Write(body)
			return
		}
		ctx.CacheStatus = cache.StatusMiss
		s.metrics.RecordCache(cache.StatusMiss)
	}

	// Wrap the response writer to capture the response for caching.
	var capture *responseCapture
	if cacheEnabled {
		capture = newResponseCapture(ctx.Response.ResponseWriter)
		capture.cacheControl = ruleCacheControl
	}

	// Record handler type for per-handler metrics
	s.metrics.RecordHandlerType(domain.Type)

	// Dispatch to handler. The capture/no-capture branches used to carry
	// duplicate dispatch switches; both now go through dispatchHandler,
	// which is also the single place where O4 per-handler latency timing
	// fires. Refs: refactor.md A22, O4.
	if capture != nil {
		// Temporarily swap the underlying writer so handlers write through the capture.
		origWriter := ctx.Response.ResponseWriter
		ctx.Response.ResponseWriter = capture
		s.dispatchHandler(ctx, domain)
		// Restore the original writer.
		ctx.Response.ResponseWriter = origWriter

		// Store the response in cache if it is cacheable and not too large.
		hdrs := capture.capturedHeaders()
		// The capture records the pre-compression body, but the global compress
		// middleware sits below it and, for bodies >= 1KB, sets Content-Encoding:
		// br|gzip (and drops Content-Length) on the shared header map before we
		// snapshot it. Storing that encoding header with the uncompressed body
		// would make every cache hit serve plaintext mislabeled as br/gzip (the
		// compress writer skips re-compressing when Content-Encoding is already
		// set), so the client fails to decode. Strip both: the cached body is
		// canonical uncompressed bytes, and the compress middleware re-derives
		// encoding/length on each hit.
		hdrs.Del("Content-Encoding")
		hdrs.Del("Content-Length")
		// The request ID was stamped on the shared header map for this request
		// alone; stored, it would be replayed to every later visitor (F5700).
		hdrs.Del("X-Request-Id")
		// A pre-encoded upstream response cannot be safely represented by this
		// cache, whose entries are always uncompressed and are encoded by the
		// outer compression middleware when served. Do not cache it: otherwise a
		// hit brotli-compresses the already-brotli body while advertising one
		// Content-Encoding layer.
		if !capture.overflow && !capture.upstreamEncoded && cache.IsCacheable(r, ctx.Response.StatusCode(), hdrs) {
			ttl := cache.CapTTL(cacheTTLFor(ruleTTL, domain.Cache.TTL, s.defaultCacheTTL()), hdrs)
			capturedBody := capture.body.Bytes()
			isESI := domain.Cache.ESI && s.esiProcessor != nil &&
				strings.Contains(hdrs.Get("Content-Type"), "text/html") &&
				cache.ContainsESI(capturedBody) &&
				r.Header.Get("X-ESI-Subrequest") == ""
			// Generate ETag for non-ESI dynamic responses to support conditional requests.
			if !isESI && hdrs.Get("ETag") == "" && len(capturedBody) > 0 {
				hash := sha256.Sum256(capturedBody)
				var etag [34]byte
				etag[0] = '"'
				hex.Encode(etag[1:33], hash[:16])
				etag[33] = '"'
				hdrs.Set("ETag", string(etag[:]))
			}
			s.cache.SetByKey(cacheKey, &cache.CachedResponse{
				StatusCode:  ctx.Response.StatusCode(),
				Headers:     hdrs,
				Body:        capturedBody,
				Created:     time.Now(),
				TTL:         ttl,
				GraceTTL:    s.graceTTL(),
				Tags:        cacheTagsFor(domain, domain.Host),
				ESITemplate: isESI,
			})
		}
	} else {
		s.dispatchHandler(ctx, domain)
	}

	// Per-domain access log file. Same sensitive-param redaction as the other
	// sinks: this one writes to an operator-configured file on disk, so a raw
	// RequestURI would persist a secret to storage that outlives the process.
	if domain.AccessLog.Path != "" {
		s.domainLogs.Write(
			r.Host, domain.AccessLog,
			r.Method, middleware.SanitizeURI(r),
			r.RemoteAddr, r.UserAgent(),
			ctx.Response.StatusCode(), int(ctx.Response.BytesWritten()),
			time.Since(start),
		)
	}
}

func normalizedRemoteIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	host := strings.TrimSpace(r.RemoteAddr)
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	// A link-local peer arrives as "fe80::1%eth0". net.ParseIP rejects the
	// zone, so no IP condition ever matched the peer and a Deny for its
	// network let it through (F10500).
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	return host
}

// hasHiddenSegment reports whether any path segment is a dotfile, other than
// "." / ".." and the ".well-known" directory the static handler also exempts.
func hasHiddenSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if strings.HasPrefix(seg, ".") && seg != "." && seg != ".." && seg != ".well-known" {
			return true
		}
	}
	return false
}

// hotlinkAllowed applies the domain's hotlink protection, answering 403 itself
// when the request is refused.
func (s *Server) hotlinkAllowed(ctx *router.RequestContext, r *http.Request, domain *config.Domain) bool {
	hp := domain.Security.HotlinkProtection
	if !hp.Enabled {
		return true
	}
	guard := middleware.HotlinkGuard(s.logger, hp.AllowedReferers, hp.Extensions)
	if !guard(ctx.Response, r) {
		s.recordSecurityBlock(ctx, r, "hotlink")
		return false
	}
	return true
}

// dirListingAllowed reports whether a directory-listing request is safe to
// serve: no dotfile path components and the resolved target stays within the
// doc root (both lexically and after symlink resolution).
func dirListingAllowed(root, rawPath, urlPath string) bool {
	for _, component := range strings.Split(urlPath, "/") {
		if strings.HasPrefix(component, ".") && component != "." && component != ".." {
			return false
		}
	}
	return pathsafe.IsWithinBase(root, rawPath) && pathsafe.IsWithinBaseResolved(root, rawPath)
}

func (s *Server) handleFileRequest(ctx *router.RequestContext, domain *config.Domain) {
	// Save original URI before any rewriting (PHP needs this for SCRIPT_NAME)
	if ctx.OriginalURI == "" {
		ctx.OriginalURI = ctx.Request.URL.RequestURI()
	}

	// Resolve first, list second.
	//
	// The listing used to be checked before resolution, so turning
	// directory_listing on replaced the site's homepage with a file listing —
	// GET / found a directory and rendered it, never looking for index.html.
	// Every server since NCSA does the opposite: Apache checks DirectoryIndex
	// before Options +Indexes, nginx checks index before autoindex. An
	// operator enabling listings for a downloads folder should not lose their
	// front page and publish every filename in the doc root.
	if !static.ResolveRequest(ctx, domain) {
		// Nothing to serve. If the path is a directory and listings are on,
		// that is what a listing is for.
		//
		// The guards match the normal static path: no dotfile components, and
		// the target must stay inside the symlink-resolved doc root, or
		// `GET /.config/` and symlinked subdirectories would leak names from
		// outside the root.
		// .htaccess Options ±Indexes — per-directory override (Apache
		// semantics), consulted only when the operator declared one in
		// .htaccess; otherwise the YAML config value stands.
		listing := domain.DirectoryListing
		rawPath := ""
		if domain.Root != "" {
			rawPath = filepath.Join(domain.Root, filepath.Clean("/"+ctx.Request.URL.Path))
			listing = s.htaccessListing(domain.Root, rawPath, listing)
		}
		if listing && domain.Root != "" {
			if dirListingAllowed(domain.Root, rawPath, ctx.Request.URL.Path) {
				if info, err := os.Stat(rawPath); err == nil && info.IsDir() {
					if deny := s.htaccessAccessReq(domain.Root, rawPath, true, ctx.Request); deny.status != 0 {
						s.renderDomainErrorFor(ctx.Request, ctx.Response, deny.status, domain)
						return
					}
					// List through the handle, confirmed to still be inside the
					// root: a directory swapped for a symlink after the check
					// above would otherwise list the directory it points to
					// (F7230).
					dir, err := os.Open(rawPath)
					if err != nil || !pathsafe.OpenedWithin(domain.Root, rawPath, dir) {
						if dir != nil {
							dir.Close()
						}
						s.renderDomainErrorFor(ctx.Request, ctx.Response, http.StatusNotFound, domain)
						return
					}
					defer dir.Close()
					static.ServeOpenDirListing(ctx, dir, ctx.Request.URL.Path)
					return
				}
			}
		}
		s.renderDomainErrorFor(ctx.Request, ctx.Response, http.StatusNotFound, domain)
		return
	}

	resolved := ctx.ResolvedPath

	// Use FileInfo from ResolveRequest if available; otherwise stat.
	info := ctx.FileInfo
	if info == nil {
		var err error
		info, err = os.Stat(resolved)
		if err != nil {
			s.renderDomainErrorFor(ctx.Request, ctx.Response, http.StatusNotFound, domain)
			return
		}
	}

	if info.IsDir() {
		s.renderDomainErrorFor(ctx.Request, ctx.Response, http.StatusForbidden, domain)
		return
	}

	// .htaccess access control — <FilesMatch>/<Files> denies, top-level
	// Require/Order-Allow-Deny, and AuthUserFile — from the docroot and every
	// subdirectory down to the served file, as Apache merges per-directory
	// configuration. Before this, deny blocks were parsed into the RuleSet
	// and silently ignored, top-level "Require all denied" was never
	// enforced, and subdirectory .htaccess files were never read: files an
	// operator explicitly denied (database dumps, backups, uploaded scripts)
	// were served to anyone.
	// An .htaccess that exists but failed to parse hides denies we cannot
	// evaluate; answer 500 like Apache does instead of serving everything.
	// AuthUserFile marks a directory as requiring HTTP Basic auth against an
	// htpasswd file UWAS does not verify; fail closed with 403 and record it
	// so the Security dashboard and the autoblocker see the denial.
	if deny := s.htaccessAccessReq(domain.Root, resolved, false, ctx.Request); deny.status != 0 {
		if deny.reason == "auth" {
			s.logger.Warn("htaccess AuthUserFile present but UWAS does not verify htpasswd; denying request",
				"host", domain.Host, "path", ctx.ResolvedPath)
			s.recordSecurityBlock(ctx, ctx.Request, "auth")
		}
		s.renderDomainErrorFor(ctx.Request, ctx.Response, deny.status, domain)
		return
	} else {
		ctx.NoFollowSymlinks = deny.noFollow
	}

	// A directory asked for without its trailing slash is redirected to the
	// slash form, as Apache (DirectorySlash) and nginx do. Served in place, the
	// index page's relative URLs resolve against the parent directory and
	// break (F10200). After the access checks above so a denied directory
	// does not reveal itself by redirecting.
	if target := dirSlashTarget(ctx, domain.Root, resolved); target != "" {
		http.Redirect(ctx.Response, ctx.Request, target, http.StatusMovedPermanently)
		return
	}

	if domain.Type == "php" && hasPHPExt(resolved) {
		// "php_flag engine off", "RemoveHandler .php" or "SetHandler none" in
		// the script's directory (or an ancestor, deepest wins) is the usual
		// way to keep uploaded scripts from running. Apache would serve the
		// source as text; refusing is the safer reading (F12100).
		if domain.Root != "" && s.phpDisabledForFile(domain, resolved) {
			s.renderDomainErrorFor(ctx.Request, ctx.Response, http.StatusForbidden, domain)
			return
		}
		// ResolveRequest's containment verdict comes from pathsafe's few-second
		// resolution cache, and PHP-FPM follows symlinks when it opens the
		// script: a script swapped for a symlink to a file outside the root
		// would still be executed until the entry expired (F7200). Re-resolve
		// right before the hand-off; the static path does the same after its
		// open (F6900).
		if domain.Root != "" && !pathsafe.IsWithinBaseResolved(domain.Root, resolved) {
			s.renderDomainErrorFor(ctx.Request, ctx.Response, http.StatusNotFound, domain)
			return
		}
		// Resolve FPM address without mutating domain.PHP.FPMAddress (avoids data race).
		// Single map lookup rather than scanning every instance per request.
		fpmAddr := domain.PHP.FPMAddress
		if fpmAddr == "" && s.phpMgr != nil {
			fpmAddr = s.phpMgr.RunningAddrForDomain(domain.Host)
			if fpmAddr == "" {
				fpmAddr = "127.0.0.1:9000"
			}
		}
		// Merge per-request htaccess PHP override into env without mutating domain.
		// ServeWith takes explicit fpmAddr+env so concurrent requests to the same
		// domain never race on a shared *config.Domain pointer.
		if len(ctx.PHPEnvOverride) > 0 || fpmAddr != domain.PHP.FPMAddress {
			merged := make(map[string]string, len(domain.PHP.Env)+len(ctx.PHPEnvOverride))
			for k, v := range domain.PHP.Env {
				merged[k] = v
			}
			for k, v := range ctx.PHPEnvOverride {
				merged[k] = v
			}
			s.php.ServeWith(ctx, domain, fpmAddr, merged)
		} else {
			s.php.Serve(ctx, domain)
		}
		return
	}
	// Image optimization: serve pre-converted WebP/AVIF if available
	if _, ok := s.imageOptChainFor(domain.Host); ok {
		accept := ctx.Request.Header.Get("Accept")
		ext := filepath.Ext(resolved)
		if ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".gif" {
			for _, fmt := range domain.ImageOptimization.Formats {
				if strings.Contains(accept, "image/"+fmt) {
					optPath := resolved + "." + fmt
					// The variant is a different file from the one ResolveRequest
					// contained; a symlinked pic.jpg.webp must not reach outside root.
					if !pathsafe.IsWithinBase(domain.Root, optPath) || !pathsafe.IsWithinBaseResolved(domain.Root, optPath) {
						continue
					}
					// A variant older than the original was made from a
					// previous version of the image; serve the original.
					if fi, err := os.Stat(optPath); err == nil && !fi.IsDir() && !fi.ModTime().Before(info.ModTime()) {
						ctx.ResolvedPath = optPath
						// The static handler derives ETag and Last-Modified from
						// FileInfo; keeping the original's would answer 304 to a
						// client holding an older copy of a regenerated variant.
						ctx.FileInfo = fi
						ctx.Response.Header().Set("Content-Type", "image/"+fmt)
						ctx.Response.Header().Add("Vary", "Accept")
						break
					}
				}
			}
		}
	}

	// Browser caching.
	//
	// Set here rather than inside the static handler because this is the last
	// point where the finally-resolved path is known (index resolution and the
	// image-optimization swap above both change it), and because it lands
	// before the response is captured for the server cache — so a cache HIT
	// replays the same headers a MISS sent.
	//
	// Anything already set wins: locations[].cache_control and
	// cache.rules[].cache_control run earlier and are explicit operator intent.
	resolvedFile := ctx.ResolvedPath
	if resolvedFile == "" {
		resolvedFile = resolved
	}
	if ctx.Response.Header().Get("Cache-Control") == "" {
		if cc := static.BrowserCacheFor(domain.BrowserCache, ctx.Request.URL.Path, resolvedFile); cc != "" {
			ctx.Response.Header().Set("Cache-Control", cc)
		}
	}
	s.static.Serve(ctx)
}

func (s *Server) handleProxy(ctx *router.RequestContext, domain *config.Domain) {
	pool, balancer, cb, mirror, canary := s.proxyRouteFor(domain.Host)
	if pool == nil {
		s.renderDomainErrorFor(ctx.Request, ctx.Response, http.StatusBadGateway, domain)
		return
	}

	if balancer == nil {
		balancer = proxyhandler.NewBalancer("round_robin")
	}

	// Circuit breaker: reject if circuit is open
	if cb != nil && !cb.Allow() {
		s.renderDomainErrorFor(ctx.Request, ctx.Response, http.StatusServiceUnavailable, domain)
		return
	}
	// A request admitted as the half-open probe that leaves without recording
	// an outcome must give the probe slot back (F28200).
	outcomeRecorded := false
	if cb != nil {
		defer func() {
			if !outcomeRecorded {
				cb.Abandon()
			}
		}()
	}

	// Request mirroring: fire-and-forget copy to mirror backend
	if mirror != nil && mirror.ShouldMirror() {
		var bodyBytes []byte
		shouldMirror := true
		maxBytes := int64(mirror.MaxBodyBytes())

		// Mirror request bodies only when size is known and small enough.
		// This avoids unbounded buffering for large uploads.
		if ctx.Request.Body != nil {
			if ctx.Request.ContentLength < 0 || ctx.Request.ContentLength > maxBytes {
				shouldMirror = false
				s.logger.Debug("skipping mirror for large/unknown request body",
					"host", domain.Host,
					"content_length", ctx.Request.ContentLength,
					"limit_bytes", maxBytes,
				)
			} else {
				limited := io.LimitReader(ctx.Request.Body, maxBytes+1)
				buf, err := io.ReadAll(limited)
				if err != nil {
					s.renderDomainErrorFor(ctx.Request, ctx.Response, http.StatusBadRequest, domain)
					return
				}
				if int64(len(buf)) > maxBytes {
					shouldMirror = false
				}
				ctx.Request.Body.Close()
				ctx.Request.Body = io.NopCloser(bytes.NewReader(buf))
				bodyBytes = buf
			}
		}
		if shouldMirror {
			mirror.Send(ctx.Request, bodyBytes)
		}
	}

	// Canary routing: route a percentage of traffic to canary upstreams.
	// Fall back to the primary pool if the canary couldn't serve (no healthy
	// canary backend), otherwise the client would get an empty response.
	served := false
	if canary != nil && canary.IsCanary(ctx.Request, domain.Proxy.Canary) {
		served = canary.Serve(ctx, domain, s.proxy)
	}
	if !served {
		s.proxy.Serve(ctx, domain, pool, balancer)
	}

	// Record circuit breaker result
	if cb != nil {
		outcomeRecorded = true
		if ctx.Response.StatusCode() >= 500 {
			cb.RecordFailure()
		} else {
			cb.RecordSuccess()
		}
	}
}

// dispatchHandler routes the request to the appropriate per-domain
// handler and records per-handler latency (refactor.md O4). The two
// call sites in handleRequest (capture / no-capture branches) and the
// ESI sub-request path all funnel through here so there is exactly one
// place that switches on domain.Type, and exactly one place that
// times handler execution. Refs: refactor.md A22, O4.
func (s *Server) dispatchHandler(ctx *router.RequestContext, domain *config.Domain) {
	start := time.Now()
	switch domain.Type {
	case "redirect":
		s.handleRedirect(ctx, domain)
	case "static", "php":
		s.handleFileRequest(ctx, domain)
	case "proxy":
		s.handleProxy(ctx, domain)
	case "app":
		http.Error(ctx.Response,
			"502 Bad Gateway — type=app is no longer supported. "+
				"Create an app under /api/v1/apps and route domains with type=proxy + apps://<name>.",
			http.StatusBadGateway)
	default:
		s.renderDomainErrorFor(ctx.Request, ctx.Response, http.StatusInternalServerError, domain)
	}
	s.metrics.RecordHandlerLatency(string(domain.Type), ctx.Response.StatusCode(), time.Since(start))
}

func (s *Server) handleRedirect(ctx *router.RequestContext, domain *config.Domain) {
	target := domain.Redirect.Target
	if domain.Redirect.PreservePath {
		target = strings.TrimRight(target, "/") + ctx.Request.URL.RequestURI()
		if dest, err := url.Parse(domain.Redirect.Target); err == nil {
			escapedPath := strings.TrimRight(dest.EscapedPath(), "/") + ctx.Request.URL.EscapedPath()
			dest.Path, _ = url.PathUnescape(escapedPath)
			dest.RawPath = escapedPath
			if query := ctx.Request.URL.RawQuery; query != "" {
				if dest.RawQuery != "" {
					dest.RawQuery += "&"
				}
				dest.RawQuery += query
			}
			dest.ForceQuery = dest.ForceQuery || ctx.Request.URL.ForceQuery
			target = dest.String()
		}
	}
	status := domain.Redirect.Status
	if status == 0 {
		status = http.StatusMovedPermanently
	}
	http.Redirect(ctx.Response, ctx.Request, target, status)
}

// cacheTTLFor resolves how long a cache entry should live, in precedence order:
// the matching cache rule's ttl, then the domain's cache.ttl, then
// global.cache.default_ttl, and finally a one minute floor.
//
// Both the rule ttl and the global default used to be dead configuration: the
// store path hardcoded a 60 second fallback and read neither. Operators could
// set them in the dashboard, see them echoed back by the API, and get no
// change in behaviour.
//
// NOTE: this changes the effective TTL of existing installs. A domain that
// leaves cache.ttl at 0 previously got 60s; it now gets global.cache.default_ttl,
// which defaults to 3600. Operators relying on the old implicit minute should
// set cache.ttl explicitly.
func cacheTTLFor(ruleTTL, domainTTL, globalDefaultTTL int) time.Duration {
	for _, secs := range []int{ruleTTL, domainTTL, globalDefaultTTL} {
		if secs > 0 {
			return time.Duration(secs) * time.Second
		}
	}
	return 60 * time.Second
}

// isDeadline reports whether an error is a context deadline, which for a
// location proxy means request_timeout fired rather than the upstream failing.
func isDeadline(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	// The client's response-header timeout is a net timeout, not a context one.
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// idleResetBody restarts the location proxy's idle timer each time the
// upstream delivers data, so the timeout measures silence, not transfer time.
type idleResetBody struct {
	io.ReadCloser
	reset func()
}

func (b *idleResetBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.reset()
	}
	return n, err
}

func isEventStreamHeader(h http.Header) bool {
	ct := h.Get("Content-Type")
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.EqualFold(strings.TrimSpace(ct), "text/event-stream")
}

// flushingWriter flushes after every successful write.
type flushingWriter struct{ w *router.ResponseWriter }

func (f flushingWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if err == nil {
		f.w.Flush()
	}
	return n, err
}

// etagMatches reports whether an If-None-Match value matches etag. RFC 9110
// §13.1.2 compares weakly: the W/ prefix is ignored, the header may list
// several entity-tags, and "*" matches any current representation.
func etagMatches(ifNoneMatch, etag string) bool {
	etag = strings.TrimPrefix(etag, "W/")
	for _, part := range strings.Split(ifNoneMatch, ",") {
		part = strings.TrimSpace(part)
		if part == "*" || strings.TrimPrefix(part, "W/") == etag {
			return true
		}
	}
	return false
}

// mergeVaryNames appends the Vary field names in prior that h does not already
// list (case-insensitively).
func mergeVaryNames(h http.Header, prior []string) {
	have := make(map[string]bool)
	for _, v := range h.Values("Vary") {
		for _, name := range strings.Split(v, ",") {
			have[strings.ToLower(strings.TrimSpace(name))] = true
		}
	}
	for _, v := range prior {
		for _, name := range strings.Split(v, ",") {
			name = strings.TrimSpace(name)
			if name == "" || have[strings.ToLower(name)] {
				continue
			}
			have[strings.ToLower(name)] = true
			h.Add("Vary", name)
		}
	}
}

// applyDomainRequestHeaders applies the domain's headers.request_add /
// request_remove to the incoming request.
func applyDomainRequestHeaders(r *http.Request, domain *config.Domain) {
	h := domain.Headers
	for k, v := range h.RequestAdd {
		r.Header.Set(k, substituteHeaderVars(v, r))
	}
	for _, k := range h.RequestRemove {
		r.Header.Del(k)
	}
}

// applyDomainResponseHeaders applies the domain's headers.add / response_add /
// remove / response_remove to w.
func applyDomainResponseHeaders(w http.Header, r *http.Request, domain *config.Domain) {
	h := domain.Headers
	for k, v := range h.Add {
		w.Set(k, substituteHeaderVars(v, r))
	}
	for k, v := range h.ResponseAdd {
		w.Set(k, substituteHeaderVars(v, r))
	}
	for _, k := range h.Remove {
		w.Del(k)
	}
	for _, k := range h.ResponseRemove {
		w.Del(k)
	}
}

// serveContainedFile serves a regular file from a location root through the
// handle it opened, after confirming that handle is still inside the root:
// http.ServeFile opens the path by name, so a file swapped for a symlink after
// the containment check was served from outside the root (F7220). It reports
// false, having written nothing, for anything else (directories, index.html
// redirects), which the caller leaves to http.ServeFile.
func (s *Server) serveContainedFile(ctx *router.RequestContext, r *http.Request, domain *config.Domain, root, filePath string) bool {
	info, err := os.Stat(filePath)
	if err != nil || !info.Mode().IsRegular() || strings.HasSuffix(r.URL.Path, "/index.html") {
		return false
	}
	f, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer f.Close()
	if !pathsafe.OpenedWithin(root, filePath, f) {
		s.renderDomainErrorFor(ctx.Request, ctx.Response, http.StatusNotFound, domain)
		return true
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	http.ServeContent(ctx.Response, r, fi.Name(), fi.ModTime(), f)
	return true
}

// hasPHPExt reports whether name ends in ".php", ignoring case. Apache maps
// handlers by extension case-insensitively, and a file named Config.PHP
// served as static text would disclose its source.
func hasPHPExt(name string) bool {
	return len(name) >= 4 && strings.EqualFold(name[len(name)-4:], ".php")
}

// htaccessAccessForURL evaluates the .htaccess access rules for a response that
// is served without reaching the file handler (a cache hit). It resolves the
// request to a file exactly as the file handler does (try_files, index files,
// SPA fallback) so a rule written for the resolved file ("<FilesMatch
// index\.html>") applies here as it does on a cold request (F5100); only an
// unresolvable request falls back to the URL's own directory chain.
func (s *Server) htaccessAccessForURL(ctx *router.RequestContext, domain *config.Domain) htaccessDeny {
	r := ctx.Request
	if domain.Root == "" {
		return htaccessDeny{}
	}
	probe := *ctx
	probe.ResolvedPath, probe.FileInfo = "", nil
	if static.ResolveRequest(&probe, domain) && probe.FileInfo != nil && !probe.FileInfo.IsDir() {
		return s.htaccessAccessReq(domain.Root, probe.ResolvedPath, false, r)
	}
	target := filepath.Join(domain.Root, filepath.Clean("/"+r.URL.Path))
	isDir := strings.HasSuffix(r.URL.Path, "/")
	if !isDir {
		if info, err := os.Stat(target); err == nil && info.IsDir() {
			isDir = true
		}
	}
	return s.htaccessAccessReq(domain.Root, target, isDir, r)
}

// htaccessListing applies the Options +/-Indexes declared in the .htaccess
// files from the doc root down to dir; the deepest declaration wins, as in
// Apache. Only the doc root's file used to be read, so an "Options -Indexes"
// in uploads/.htaccess did not stop a listing of uploads/ (F6102).
func (s *Server) htaccessListing(root, dir string, listing bool) bool {
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return listing
	}
	apply := func(dir string) {
		if entry := s.getHtaccessRuleSet(dir); entry != nil && entry.raw != nil && entry.raw.DirectoryListing != nil {
			listing = *entry.raw.DirectoryListing
		}
	}
	apply(root)
	forEachExistingDir(root, rel, func(cur, _ string) { apply(cur) })
	return listing
}

// stampCacheControl sets an operator-configured Cache-Control (a location or a
// cache rule) on the response. It is stamped before the request is dispatched,
// so an error the dispatch ends in would carry the lifetime meant for the page
// and browsers and shared caches would keep the 404 as long as the page
// (F9010); a value that grants a lifetime is withdrawn when the final status is
// an error. A restrictive value (no-store, no-cache, private) never lengthens
// how long an error is kept, so it stays.
func stampCacheControl(ctx *router.RequestContext, value string) {
	ctx.Response.Header().Set("Cache-Control", value)
	if !grantsCacheLifetime(value) {
		return
	}
	prev := ctx.Response.BeforeHeader
	ctx.Response.BeforeHeader = func(h http.Header, code int) {
		if code >= 400 && h.Get("Cache-Control") == value {
			h.Del("Cache-Control")
		}
		if prev != nil {
			prev(h, code)
		}
	}
}

func grantsCacheLifetime(value string) bool {
	v := strings.ToLower(value)
	for _, restrictive := range []string{"no-store", "no-cache", "private", "max-age=0"} {
		if strings.Contains(v, restrictive) {
			return false
		}
	}
	return true
}

// dirSlashTarget returns the "/dir/" location to redirect a GET or HEAD to when
// the request named a directory without a trailing slash and resolved to one
// of that directory's index files. It returns "" for everything else: other
// methods (a 301 would turn a POST into a GET), requests an .htaccess rewrite
// has already moved (the visitor's own URL must not give way to the rewritten
// one), try_files fallbacks such as an SPA's /index.html, and the root. The
// target is built from the cleaned path, so "//host" can never become a
// scheme-relative Location.
func dirSlashTarget(ctx *router.RequestContext, root, resolved string) string {
	r := ctx.Request
	if root == "" || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		return ""
	}
	p := r.URL.Path
	if p == "" || strings.HasSuffix(p, "/") {
		return ""
	}
	orig := ctx.OriginalURI
	if i := strings.IndexByte(orig, '?'); i >= 0 {
		orig = orig[:i]
	}
	if orig != r.URL.EscapedPath() {
		return ""
	}
	clean := path.Clean("/" + p)
	if clean == "/" || filepath.Dir(resolved) != filepath.Join(root, filepath.FromSlash(clean)) {
		return ""
	}
	return (&url.URL{Path: clean + "/", RawQuery: r.URL.RawQuery}).String()
}

// cleanProxyPassPath resolves the dot segments of an escaped request path
// before it is appended to a location's proxy_pass. The location is matched on
// the canonical path, but the remainder was forwarded as sent, so
// "/api/x/../../../api/y" matched "/api/" and reached an origin that
// normalises it as "/api/y" — outside the base path in proxy_pass
// ("http://up/public"), which is meant to bound what the location exposes
// (F18200). Segments are judged on their decoded form ("%2e%2e" is a dot
// segment); every other escape, "%2F" included, is passed on untouched.
// A segment that decodes to a slash-joined traversal ("..%2f..") is ambiguous
// between origins that decode it and those that do not, so it is refused.
func cleanProxyPassPath(escaped string) (string, bool) {
	if !strings.HasPrefix(escaped, "/") {
		return escaped, true
	}
	segs := strings.Split(escaped[1:], "/")
	out := make([]string, 0, len(segs))
	trailing := false
	for i, seg := range segs {
		dec, err := url.PathUnescape(seg)
		if err != nil {
			return "", false
		}
		if strings.Contains(dec, "/") {
			for _, e := range strings.Split(dec, "/") {
				if e == "." || e == ".." {
					return "", false
				}
			}
		}
		last := i == len(segs)-1
		switch dec {
		case ".", "..":
			if dec == ".." && len(out) > 0 {
				out = out[:len(out)-1]
			}
			trailing = last
			continue
		}
		trailing = false
		out = append(out, seg)
	}
	res := "/" + strings.Join(out, "/")
	if trailing && !strings.HasSuffix(res, "/") {
		res += "/"
	}
	return res, true
}
