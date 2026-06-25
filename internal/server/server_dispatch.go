package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"os"
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
	"github.com/uwaserver/uwas/internal/pathsafe"
	"github.com/uwaserver/uwas/internal/router"
)

func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	// Connection limiter: reject with 503 when at capacity.
	if s.connLimiter != nil {
		select {
		case s.connLimiter <- struct{}{}:
			defer func() { <-s.connLimiter }()
		default:
			renderErrorPage(w, http.StatusServiceUnavailable)
			return
		}
	}

	// Health check on main port (no auth, fast path)
	if r.URL.Path == "/.well-known/health" || r.URL.Path == "/healthz" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Write([]byte(`{"status":"ok"}`))
		return
	}

	// Strip internal ESI header from external requests to prevent ESI bypass/leak.
	r.Header.Del("X-ESI-Subrequest")

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
	s.metrics.ActiveConns.Add(1)
	defer func() {
		s.metrics.ActiveConns.Add(-1)
		s.metrics.RequestsTotal.Add(1)
		s.metrics.RecordRequest(ctx.Response.StatusCode())
		s.metrics.RecordLatency(time.Since(start))
		s.metrics.BytesSent.Add(ctx.Response.BytesWritten())
		s.metrics.RecordDomain(r.Host, ctx.Response.StatusCode(), ctx.Response.BytesWritten())

		// Record to admin log ring buffer (skip internal health checks and monitor)
		isMonitor := r.UserAgent() == "UWAS-Monitor/1.0"
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
			blocked, _ := s.bwMgr.Record(r.Host, ctx.Response.BytesWritten())
			_ = blocked // blocking is applied on next request via pre-handler check
		}

		// Record analytics
		if s.analytics != nil {
			s.analytics.RecordFull(r.Host, r.URL.Path, r.RemoteAddr,
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

	if s.rejectNonCloudflareOrigin(ctx.Response, r, domain) {
		return
	}

	// Maintenance mode: serve 503 with Retry-After for all non-allowed IPs
	if domain.Maintenance.Enabled {
		allowed := false
		clientAddr := ctx.RemoteIP
		if clientAddr == "" {
			clientAddr, _, _ = net.SplitHostPort(r.RemoteAddr)
			if clientAddr == "" {
				clientAddr = r.RemoteAddr
			}
		}
		for _, ip := range domain.Maintenance.AllowedIPs {
			if clientAddr == ip {
				allowed = true
				break
			}
		}
		if !allowed {
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
		renderDomainError(ctx.Response, http.StatusServiceUnavailable, domain)
		return
	}

	// Per-domain WAF (Content-Type aware, with bypass path support).
	// Predicate-form guard precompiled at config load (refactor.md P2/P3);
	// no per-request closure allocation, no one-shot handler wrap.
	if guard := s.wafGuardFor(domain.Host); guard != nil {
		if !guard(ctx.Response, r) {
			return
		}
	}

	// Per-domain security headers (CSP, COEP, COOP, CORP, Permissions-Policy, HSTS, etc.)
	if sh := domain.SecurityHeaders; sh.ContentSecurityPolicy != "" {
		ctx.Response.Header().Set("Content-Security-Policy", sh.ContentSecurityPolicy)
	}
	if sh := domain.SecurityHeaders; sh.PermissionsPolicy != "" {
		ctx.Response.Header().Set("Permissions-Policy", sh.PermissionsPolicy)
	}
	if sh := domain.SecurityHeaders; sh.CrossOriginEmbedder != "" {
		ctx.Response.Header().Set("Cross-Origin-Embedder-Policy", sh.CrossOriginEmbedder)
	}
	if sh := domain.SecurityHeaders; sh.CrossOriginOpener != "" {
		ctx.Response.Header().Set("Cross-Origin-Opener-Policy", sh.CrossOriginOpener)
	}
	if sh := domain.SecurityHeaders; sh.CrossOriginResource != "" {
		ctx.Response.Header().Set("Cross-Origin-Resource-Policy", sh.CrossOriginResource)
	}
	if sh := domain.SecurityHeaders; sh.ReferrerPolicy != "" {
		ctx.Response.Header().Set("Referrer-Policy", sh.ReferrerPolicy)
	}
	if sh := domain.SecurityHeaders; sh.StrictTransportSecurity != "" {
		ctx.Response.Header().Set("Strict-Transport-Security", sh.StrictTransportSecurity)
	}
	if sh := domain.SecurityHeaders; sh.XContentTypeOptions != "" {
		ctx.Response.Header().Set("X-Content-Type-Options", sh.XContentTypeOptions)
	}
	if sh := domain.SecurityHeaders; sh.XSSProtection != "" {
		ctx.Response.Header().Set("X-XSS-Protection", sh.XSSProtection)
	}

	basicAuthChecked := false

	// Per-path location overrides (headers, cache-control, proxy, redirect, static root)
	for _, loc := range domain.Locations {
		if !matchLocation(r.URL.Path, loc.Match) {
			continue
		}

		// Apply headers + cache-control
		for k, v := range loc.Headers {
			ctx.Response.Header().Set(k, v)
		}
		if loc.CacheControl != "" {
			ctx.Response.Header().Set("Cache-Control", loc.CacheControl)
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
					ctx.Response.Header().Set("Retry-After", strconv.Itoa(int(window.Seconds())))
					renderDomainError(ctx.Response, http.StatusTooManyRequests, domain)
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

		if loc.Redirect != "" {
			code := loc.RedirectCode
			if code == 0 {
				code = http.StatusMovedPermanently
			}
			http.Redirect(ctx.Response, r, loc.Redirect, code)
			return
		}

		// Location-level proxy_pass (e.g. /api/ → http://127.0.0.1:4000)
		if loc.ProxyPass != "" {
			path := r.URL.Path
			if loc.StripPrefix && !strings.HasPrefix(loc.Match, "~") {
				path = strings.TrimPrefix(path, strings.TrimSuffix(loc.Match, "/"))
				if path == "" {
					path = "/"
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
				s.logger.Warn("location proxy SSRF blocked", "match", loc.Match, "target", targetURL, "error", err)
				renderDomainError(ctx.Response, http.StatusForbidden, domain)
				return
			}
			// Simple single-backend proxy for location blocks
			proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
			if err != nil {
				renderDomainError(ctx.Response, http.StatusBadGateway, domain)
				return
			}
			for k, vv := range r.Header {
				for _, v := range vv {
					proxyReq.Header.Add(k, v)
				}
			}
			proxyReq.Header.Set("X-Forwarded-For", r.RemoteAddr)
			proxyReq.Header.Set("X-Forwarded-Host", r.Host)
			resp, err := locationProxyHTTPClient.Do(proxyReq)
			if err != nil {
				s.logger.Error("location proxy error", "match", loc.Match, "target", loc.ProxyPass, "error", err)
				renderDomainError(ctx.Response, http.StatusBadGateway, domain)
				return
			}
			defer resp.Body.Close()
			for k, vv := range resp.Header {
				for _, v := range vv {
					ctx.Response.Header().Add(k, v)
				}
			}
			ctx.Response.WriteHeader(resp.StatusCode)
			io.Copy(ctx.Response, resp.Body)
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
				renderDomainError(ctx.Response, http.StatusForbidden, domain)
				return
			}
			http.ServeFile(ctx.Response, r, filePath)
			return
		}

		break // first match wins (like Nginx) — if no handler, continue to normal dispatch
	}

	// Per-domain blocked paths
	for _, blocked := range domain.Security.BlockedPaths {
		if strings.Contains(r.URL.Path, blocked) {
			renderDomainError(ctx.Response, http.StatusForbidden, domain)
			return
		}
	}

	// Per-domain IP ACL (whitelist/blacklist) — predicate form (P2/P3).
	if guard := s.ipACLGuardFor(domain.Host); guard != nil {
		if !guard(ctx.Response, r) {
			return
		}
	}

	// Per-domain GeoIP blocking — predicate form (P2/P3).
	if guard := s.geoGuardFor(domain.Host); guard != nil {
		if !guard(ctx.Response, r) {
			return
		}
	}

	// Per-domain rate limiting
	if rl := s.rateLimiterFor(domain.Host); rl != nil {
		ip, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || ip == "" {
			ip = r.RemoteAddr
		}
		if !rl.Allow(ip) {
			ctx.Response.Header().Set("Retry-After", "60")
			ctx.Response.WriteHeader(http.StatusTooManyRequests)
			ctx.Response.Write([]byte("429 Too Many Requests"))
			return
		}
	}

	// Per-domain Basic Authentication (unless already evaluated in location matching).
	if !basicAuthChecked {
		if !enforceBasicAuth(ctx.Response, r, domain.Host, domain.BasicAuth) {
			return
		}
	}

	// Per-domain CORS — predicate form (P2/P3). Guard handles preflight
	// inline and returns false when the response was terminated there.
	if guard := s.corsGuardFor(domain.Host); guard != nil {
		if !guard(ctx.Response, r) {
			return
		}
	}

	// .htaccess import (runtime parse)
	// Skip rewrite for paths that should be served directly:
	// - /wp-admin, /wp-includes, /wp-content (WordPress core)
	// - Direct .php file requests (already resolved, no rewrite needed)
	if domain.Htaccess.Mode == "import" && domain.Root != "" {
		p := r.URL.Path
		skipRewrite := strings.HasPrefix(p, "/wp-admin") ||
			strings.HasPrefix(p, "/wp-includes") ||
			strings.HasPrefix(p, "/wp-content") ||
			strings.HasSuffix(p, ".php")
		if !skipRewrite {
			if s.applyHtaccess(ctx, domain) {
				return
			}
		}
	}

	// Rewrite engine (from YAML config)
	if len(domain.Rewrites) > 0 {
		if s.applyRewrites(ctx, domain) {
			return
		}
	}

	if hp := domain.Security.HotlinkProtection; hp.Enabled {
		guard := middleware.HotlinkGuard(s.logger, hp.AllowedReferers, hp.Extensions)
		if !guard(ctx.Response, r) {
			return
		}
	}

	// Per-domain header transforms
	if h := domain.Headers; len(h.RequestAdd) > 0 || len(h.RequestRemove) > 0 {
		for k, v := range h.RequestAdd {
			r.Header.Set(k, substituteHeaderVars(v, r))
		}
		for _, k := range h.RequestRemove {
			r.Header.Del(k)
		}
	}
	if h := domain.Headers; len(h.Add) > 0 || len(h.Remove) > 0 ||
		len(h.ResponseAdd) > 0 || len(h.ResponseRemove) > 0 {
		w := ctx.Response.Header()
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
	if cacheEnabled {
		// Check per-domain cache bypass rules + set Cache-Control from rules
		for _, rule := range domain.Cache.Rules {
			if matchPath(r.URL.Path, rule.Match) {
				if rule.Bypass {
					cacheEnabled = false
					break
				}
				if rule.CacheControl != "" {
					ctx.Response.Header().Set("Cache-Control", rule.CacheControl)
				}
			}
		}
		// Bypass cache for WordPress admin/login paths (always dynamic)
		p := r.URL.Path
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
	if cacheEnabled {
		cached, status := s.cache.Get(r)
		if cached != nil && (status == cache.StatusHit || status == cache.StatusStale) {
			ctx.CacheStatus = status
			s.metrics.RecordCache(status)
			ctx.Response.Header().Set("X-Cache", status)
			ctx.Response.Header().Set("Age", strconv.FormatInt(int64(cached.Age().Seconds()), 10))
			for k, vals := range cached.Headers {
				for _, v := range vals {
					ctx.Response.Header().Set(k, v)
				}
			}

			// Handle conditional requests against cached ETag
			if etag := ctx.Response.Header().Get("Etag"); etag != "" {
				if match := r.Header.Get("If-None-Match"); match != "" && match == etag {
					ctx.Response.WriteHeader(http.StatusNotModified)
					return
				}
			}

			body := cached.Body
			// ESI assembly on cache hit: replace ESI tags with cached/fetched fragments
			if cached.ESITemplate && domain.Cache.ESI && s.esiProcessor != nil &&
				r.Header.Get("X-ESI-Subrequest") == "" {
				assembled, err := s.esiProcessor.Process(body, r.Host, r, domain.Cache.Tags, 0)
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
		if !capture.overflow && cache.IsCacheable(r, ctx.Response.StatusCode(), hdrs) {
			ttl := time.Duration(domain.Cache.TTL) * time.Second
			if ttl <= 0 {
				ttl = 60 * time.Second
			}
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
			s.cache.Set(r, &cache.CachedResponse{
				StatusCode:  ctx.Response.StatusCode(),
				Headers:     hdrs,
				Body:        capturedBody,
				Created:     time.Now(),
				TTL:         ttl,
				Tags:        domain.Cache.Tags,
				ESITemplate: isESI,
			})
		}
	} else {
		s.dispatchHandler(ctx, domain)
	}

	// Per-domain access log file
	if domain.AccessLog.Path != "" {
		s.domainLogs.Write(
			r.Host, domain.AccessLog.Path, domain.AccessLog.Rotate,
			r.Method, r.URL.RequestURI(),
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
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
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

	// Check if the raw path points to a directory (for directory listing).
	// Apply the same guards the normal static path enforces: reject dotfile
	// path components and require the target stay within the (symlink-resolved)
	// doc root. Without these, `GET /.config/` or a symlinked subdir pointing
	// outside the root would be listed, leaking out-of-root filenames.
	if domain.DirectoryListing && domain.Root != "" {
		rawPath := filepath.Join(domain.Root, filepath.Clean("/"+ctx.Request.URL.Path))
		if dirListingAllowed(domain.Root, rawPath, ctx.Request.URL.Path) {
			if info, err := os.Stat(rawPath); err == nil && info.IsDir() {
				static.ServeDirListing(ctx, rawPath, ctx.Request.URL.Path)
				return
			}
		}
	}

	if !static.ResolveRequest(ctx, domain) {
		renderDomainError(ctx.Response, http.StatusNotFound, domain)
		return
	}

	resolved := ctx.ResolvedPath

	info, err := os.Stat(resolved)
	if err != nil {
		renderDomainError(ctx.Response, http.StatusNotFound, domain)
		return
	}

	if info.IsDir() {
		renderDomainError(ctx.Response, http.StatusForbidden, domain)
		return
	}

	if domain.Type == "php" && strings.HasSuffix(resolved, ".php") {
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
					if _, err := os.Stat(optPath); err == nil {
						ctx.ResolvedPath = optPath
						ctx.Response.Header().Set("Content-Type", "image/"+fmt)
						ctx.Response.Header().Add("Vary", "Accept")
						break
					}
				}
			}
		}
	}

	s.static.Serve(ctx)
}

func (s *Server) handleProxy(ctx *router.RequestContext, domain *config.Domain) {
	pool, balancer, cb, mirror, canary := s.proxyRouteFor(domain.Host)
	if pool == nil {
		renderDomainError(ctx.Response, http.StatusBadGateway, domain)
		return
	}

	if balancer == nil {
		balancer = proxyhandler.NewBalancer("round_robin")
	}

	// Circuit breaker: reject if circuit is open
	if cb != nil && !cb.Allow() {
		renderDomainError(ctx.Response, http.StatusServiceUnavailable, domain)
		return
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
					renderDomainError(ctx.Response, http.StatusBadRequest, domain)
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
		renderDomainError(ctx.Response, http.StatusInternalServerError, domain)
	}
	s.metrics.RecordHandlerLatency(string(domain.Type), ctx.Response.StatusCode(), time.Since(start))
}

func (s *Server) handleRedirect(ctx *router.RequestContext, domain *config.Domain) {
	target := domain.Redirect.Target
	if domain.Redirect.PreservePath {
		target = strings.TrimRight(target, "/") + ctx.Request.URL.RequestURI()
	}
	status := domain.Redirect.Status
	if status == 0 {
		status = http.StatusMovedPermanently
	}
	http.Redirect(ctx.Response, ctx.Request, target, status)
}
