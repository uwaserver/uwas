package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// newDispatchTestServer builds a minimal Server from the given domains and
// cancels its background context on cleanup so no goroutines leak.
func newDispatchTestServer(t *testing.T, domains []config.Domain) *Server {
	t.Helper()
	cfg := &config.Config{
		Global: config.GlobalConfig{
			WorkerCount: "1",
			LogLevel:    "error",
			LogFormat:   "text",
		},
		Domains: domains,
	}
	s := New(cfg, logger.New("error", "text"))
	t.Cleanup(func() { s.cancel() })
	return s
}

// --- normalizedRemoteIP nil + no-port branches ---

func TestNormalizedRemoteIPBranches(t *testing.T) {
	if got := normalizedRemoteIP(nil); got != "" {
		t.Errorf("nil request: got %q want empty", got)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.9" // no port → SplitHostPort fails, TrimSpace path
	if got := normalizedRemoteIP(r); got != "203.0.113.9" {
		t.Errorf("no-port: got %q want 203.0.113.9", got)
	}
	r.RemoteAddr = "203.0.113.9:1234"
	if got := normalizedRemoteIP(r); got != "203.0.113.9" {
		t.Errorf("with-port: got %q want 203.0.113.9", got)
	}
}

// --- headerVarRemoteAddr no-port branch ---

func TestHeaderVarRemoteAddrNoPort(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "198.51.100.7" // no port → fallback branch
	if got := headerVarRemoteAddr(r); got != "198.51.100.7" {
		t.Errorf("got %q want 198.51.100.7", got)
	}
}

// --- directPeerIP no-port fallback ---

func TestDirectPeerIPNoPort(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.0.2.55" // no port; no DirectIP set → TrimSpace fallback
	if got := directPeerIP(r); got != "192.0.2.55" {
		t.Errorf("got %q want 192.0.2.55", got)
	}
}

// --- dispatchHandler: type=app returns 502, unknown type → 500 ---

func TestDispatchHandlerAppType(t *testing.T) {
	s := newDispatchTestServer(t, []config.Domain{
		{Host: "app.test", Type: "app", SSL: config.SSLConfig{Mode: "off"}},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "app.test"
	s.handleRequest(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("app type: status = %d want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "no longer supported") {
		t.Errorf("app type body = %q", rec.Body.String())
	}
}

func TestDispatchHandlerUnknownType(t *testing.T) {
	s := newDispatchTestServer(t, []config.Domain{
		{Host: "weird.test", Type: "wat", SSL: config.SSLConfig{Mode: "off"}},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "weird.test"
	s.handleRequest(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("unknown type: status = %d want 500", rec.Code)
	}
}

// --- vhost not found → 404 ---

func TestHandleRequestVHostNotFound(t *testing.T) {
	s := newDispatchTestServer(t, []config.Domain{
		{Host: "known.test", Type: "static", Root: t.TempDir(), SSL: config.SSLConfig{Mode: "off"}},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "absent.test"
	s.handleRequest(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d want 404", rec.Code)
	}
}

// --- maintenance mode: blocked (no port) + allowed IP bypass ---

func TestHandleRequestMaintenanceBlocked(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("home"), 0644)
	s := newDispatchTestServer(t, []config.Domain{
		{
			Host: "maint.test",
			Type: "static",
			Root: dir,
			SSL:  config.SSLConfig{Mode: "off"},
			Maintenance: config.MaintenanceConfig{
				Enabled:    true,
				Message:    "<h1>back soon</h1>",
				RetryAfter: 120,
				AllowedIPs: []string{"10.9.9.9"},
			},
		},
	})

	// Blocked client (RemoteAddr without port exercises the SplitHostPort-fail branch).
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "maint.test"
	req.RemoteAddr = "203.0.113.1"
	s.handleRequest(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("blocked: status = %d want 503", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "120" {
		t.Errorf("Retry-After = %q want 120", rec.Header().Get("Retry-After"))
	}
	if !strings.Contains(rec.Body.String(), "back soon") {
		t.Errorf("body = %q", rec.Body.String())
	}

	// Allowed IP bypasses maintenance and serves the file.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/index.html", nil)
	req2.Host = "maint.test"
	req2.RemoteAddr = "10.9.9.9:5050"
	s.handleRequest(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("allowed: status = %d want 200", rec2.Code)
	}
}

func TestHandleRequestMaintenanceDefaultMessage(t *testing.T) {
	s := newDispatchTestServer(t, []config.Domain{
		{
			Host:        "maint2.test",
			Type:        "static",
			Root:        t.TempDir(),
			SSL:         config.SSLConfig{Mode: "off"},
			Maintenance: config.MaintenanceConfig{Enabled: true}, // no message, no retry-after
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "maint2.test"
	req.RemoteAddr = "203.0.113.2:80"
	s.handleRequest(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Under Maintenance") {
		t.Errorf("default message missing: %q", rec.Body.String())
	}
}

// --- per-domain security headers all set ---

func TestHandleRequestSecurityHeadersCov2(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("x"), 0644)
	s := newDispatchTestServer(t, []config.Domain{
		{
			Host: "sec.test",
			Type: "static",
			Root: dir,
			SSL:  config.SSLConfig{Mode: "off"},
			SecurityHeaders: config.SecurityHeadersConfig{
				ContentSecurityPolicy:   "default-src 'self'",
				PermissionsPolicy:       "geolocation=()",
				CrossOriginEmbedder:     "require-corp",
				CrossOriginOpener:       "same-origin",
				CrossOriginResource:     "same-origin",
				ReferrerPolicy:          "no-referrer",
				StrictTransportSecurity: "max-age=31536000",
				XContentTypeOptions:     "nosniff",
				XSSProtection:           "1; mode=block",
			},
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/index.html", nil)
	req.Host = "sec.test"
	s.handleRequest(rec, req)
	h := rec.Header()
	checks := map[string]string{
		"Content-Security-Policy":      "default-src 'self'",
		"Permissions-Policy":           "geolocation=()",
		"Cross-Origin-Embedder-Policy": "require-corp",
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Referrer-Policy":              "no-referrer",
		"Strict-Transport-Security":    "max-age=31536000",
		"X-Content-Type-Options":       "nosniff",
		"X-Xss-Protection":             "1; mode=block",
	}
	for k, want := range checks {
		if got := h.Get(k); got != want {
			t.Errorf("header %s = %q want %q", k, got, want)
		}
	}
}

// --- location: headers + cache-control + redirect ---

func TestHandleRequestLocationRedirectCov2(t *testing.T) {
	s := newDispatchTestServer(t, []config.Domain{
		{
			Host: "loc.test",
			Type: "static",
			Root: t.TempDir(),
			SSL:  config.SSLConfig{Mode: "off"},
			Locations: []config.LocationConfig{
				{
					Match:        "/old",
					Redirect:     "https://example.com/new",
					RedirectCode: 302,
					Headers:      map[string]string{"X-Loc": "hit"},
					CacheControl: "no-store",
				},
			},
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/old", nil)
	req.Host = "loc.test"
	s.handleRequest(rec, req)
	if rec.Code != 302 {
		t.Fatalf("status = %d want 302", rec.Code)
	}
	if rec.Header().Get("Location") != "https://example.com/new" {
		t.Errorf("Location = %q", rec.Header().Get("Location"))
	}
	if rec.Header().Get("X-Loc") != "hit" {
		t.Errorf("X-Loc header not applied")
	}
}

// --- location: static root serving + traversal forbidden ---

func TestHandleRequestLocationRoot(t *testing.T) {
	locRoot := t.TempDir()
	os.WriteFile(filepath.Join(locRoot, "doc.txt"), []byte("docbody"), 0644)
	s := newDispatchTestServer(t, []config.Domain{
		{
			Host: "locroot.test",
			Type: "static",
			Root: t.TempDir(),
			SSL:  config.SSLConfig{Mode: "off"},
			Locations: []config.LocationConfig{
				{Match: "/docs/", Root: locRoot},
			},
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/docs/doc.txt", nil)
	req.Host = "locroot.test"
	s.handleRequest(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "docbody") {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// --- location: per-path rate limit exceeded ---

func TestHandleRequestLocationRateLimit(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("x"), 0644)
	s := newDispatchTestServer(t, []config.Domain{
		{
			Host: "lrl.test",
			Type: "static",
			Root: dir,
			SSL:  config.SSLConfig{Mode: "off"},
			Locations: []config.LocationConfig{
				{
					Match:     "/api/",
					RateLimit: &config.RateLimitConfig{Requests: 1, Window: config.Duration{Duration: time.Minute}},
				},
			},
		},
	})
	// First request within limit (path has no handler in the location → falls through to 404 static).
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/x", nil)
		req.Host = "lrl.test"
		req.RemoteAddr = "203.0.113.50:1000"
		s.handleRequest(rec, req)
		if i == 1 && rec.Code != http.StatusTooManyRequests {
			t.Fatalf("second request status = %d want 429", rec.Code)
		}
	}
}

// --- location: proxy_pass with SSRF block (private upstream blocked by default) ---

func TestHandleRequestLocationProxySSRFBlocked(t *testing.T) {
	s := newDispatchTestServer(t, []config.Domain{
		{
			Host: "lproxy.test",
			Type: "static",
			Root: t.TempDir(),
			SSL:  config.SSLConfig{Mode: "off"},
			Locations: []config.LocationConfig{
				{Match: "/api/", ProxyPass: "http://169.254.169.254", StripPrefix: true},
			},
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/users", nil)
	req.Host = "lproxy.test"
	s.handleRequest(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d want 403 (SSRF block)", rec.Code)
	}
}

// --- location: proxy_pass succeeds against a real backend ---

func TestHandleRequestLocationProxySuccess(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Backend", "yes")
		w.WriteHeader(201)
		w.Write([]byte("proxied:" + r.URL.Path))
	}))
	defer backend.Close()

	s := newDispatchTestServer(t, []config.Domain{
		{
			Host:  "lproxy2.test",
			Type:  "static",
			Root:  t.TempDir(),
			SSL:   config.SSLConfig{Mode: "off"},
			Proxy: config.ProxyConfig{AllowPrivateUpstreams: true},
			Locations: []config.LocationConfig{
				{Match: "/api/", ProxyPass: backend.URL, StripPrefix: true},
			},
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/thing?q=1", nil)
	req.Host = "lproxy2.test"
	s.handleRequest(rec, req)
	if rec.Code != 201 {
		t.Fatalf("status = %d want 201", rec.Code)
	}
	if rec.Header().Get("X-Backend") != "yes" {
		t.Errorf("backend header missing")
	}
	if !strings.Contains(rec.Body.String(), "proxied:/thing") {
		t.Errorf("body = %q (strip prefix expected)", rec.Body.String())
	}
}

// --- blocked path → 403 ---

func TestHandleRequestBlockedPathCov2(t *testing.T) {
	s := newDispatchTestServer(t, []config.Domain{
		{
			Host:     "blk.test",
			Type:     "static",
			Root:     t.TempDir(),
			SSL:      config.SSLConfig{Mode: "off"},
			Security: config.SecurityConfig{BlockedPaths: []string{"/secret"}},
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/secret/file", nil)
	req.Host = "blk.test"
	s.handleRequest(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d want 403", rec.Code)
	}
}

// --- per-domain IP ACL blacklist guard ---

func TestHandleRequestIPACLBlacklist(t *testing.T) {
	s := newDispatchTestServer(t, []config.Domain{
		{
			Host:     "acl.test",
			Type:     "static",
			Root:     t.TempDir(),
			SSL:      config.SSLConfig{Mode: "off"},
			Security: config.SecurityConfig{IPBlacklist: []string{"203.0.113.66"}},
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "acl.test"
	req.RemoteAddr = "203.0.113.66:1234"
	s.handleRequest(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("blacklisted IP should not get 200")
	}
}

// --- per-domain rate limit guard exceeded ---

func TestHandleRequestDomainRateLimit(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("x"), 0644)
	s := newDispatchTestServer(t, []config.Domain{
		{
			Host: "drl.test",
			Type: "static",
			Root: dir,
			SSL:  config.SSLConfig{Mode: "off"},
			Security: config.SecurityConfig{
				RateLimit: config.RateLimitConfig{Requests: 1, Window: config.Duration{Duration: time.Minute}},
			},
		},
	})
	var last int
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/index.html", nil)
		req.Host = "drl.test"
		req.RemoteAddr = "203.0.113.77:2000"
		s.handleRequest(rec, req)
		last = rec.Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("final status = %d want 429", last)
	}
}

// --- per-domain CORS preflight ---

func TestHandleRequestCORSPreflightCov2(t *testing.T) {
	s := newDispatchTestServer(t, []config.Domain{
		{
			Host: "cors.test",
			Type: "static",
			Root: t.TempDir(),
			SSL:  config.SSLConfig{Mode: "off"},
			CORS: config.CORSConfig{
				Enabled:        true,
				AllowedOrigins: []string{"https://app.example"},
				AllowedMethods: []string{"GET", "POST"},
			},
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "/", nil)
	req.Host = "cors.test"
	req.Header.Set("Origin", "https://app.example")
	req.Header.Set("Access-Control-Request-Method", "POST")
	s.handleRequest(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Errorf("CORS allow-origin header missing")
	}
}

// --- hotlink protection guard ---

func TestHandleRequestHotlinkBlock(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "pic.jpg"), []byte("img"), 0644)
	s := newDispatchTestServer(t, []config.Domain{
		{
			Host: "hot.test",
			Type: "static",
			Root: dir,
			SSL:  config.SSLConfig{Mode: "off"},
			Security: config.SecurityConfig{
				HotlinkProtection: config.HotlinkConfig{
					Enabled:         true,
					AllowedReferers: []string{"hot.test"},
					Extensions:      []string{"jpg"},
				},
			},
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/pic.jpg", nil)
	req.Host = "hot.test"
	req.Header.Set("Referer", "http://evil.example/page")
	s.handleRequest(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("hotlinked request should be blocked, got 200")
	}
}

// --- per-domain header transforms (request + response add/remove) ---

func TestHandleRequestHeaderTransformsCov2(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("x"), 0644)
	s := newDispatchTestServer(t, []config.Domain{
		{
			Host: "hdr.test",
			Type: "static",
			Root: dir,
			SSL:  config.SSLConfig{Mode: "off"},
			Headers: config.HeadersConfig{
				RequestAdd:     map[string]string{"X-Req": "$host"},
				RequestRemove:  []string{"X-Drop-Req"},
				Add:            map[string]string{"X-Resp": "$remote_addr"},
				ResponseAdd:    map[string]string{"X-Resp2": "v2"},
				Remove:         []string{"X-Drop-Resp"},
				ResponseRemove: []string{"X-Drop-Resp2"},
			},
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/index.html", nil)
	req.Host = "hdr.test"
	req.Header.Set("X-Drop-Req", "should-go")
	s.handleRequest(rec, req)
	if rec.Header().Get("X-Resp") == "" {
		t.Errorf("response header X-Resp not added")
	}
	if rec.Header().Get("X-Resp2") != "v2" {
		t.Errorf("response header X-Resp2 not added")
	}
}

// --- redirect domain with preserve path ---

func TestHandleRequestRedirectPreservePath(t *testing.T) {
	s := newDispatchTestServer(t, []config.Domain{
		{
			Host: "rd.test",
			Type: "redirect",
			SSL:  config.SSLConfig{Mode: "off"},
			Redirect: config.RedirectConfig{
				Target:       "https://target.example",
				Status:       301,
				PreservePath: true,
			},
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/deep/path?x=1", nil)
	req.Host = "rd.test"
	s.handleRequest(rec, req)
	if rec.Code != 301 {
		t.Fatalf("status = %d want 301", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "/deep/path") {
		t.Errorf("Location = %q, expected preserved path", loc)
	}
}
