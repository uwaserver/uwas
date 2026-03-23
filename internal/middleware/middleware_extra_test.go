package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// --- Gzip: binary/non-compressible content ---

func TestGzipSkipBinaryContent(t *testing.T) {
	// Binary/non-compressible content types should not be gzipped.
	body := strings.Repeat("\x00\x01\x02\x03\xFF", 500) // > 1KB binary

	handler := Gzip(1024)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte(body))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") == "gzip" {
		t.Error("should not gzip binary content (application/octet-stream)")
	}
}

func TestGzipSkipImageContent(t *testing.T) {
	body := strings.Repeat("fakepng", 300) // > 1KB

	handler := Gzip(1024)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte(body))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/image.png", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") == "gzip" {
		t.Error("should not gzip image/png content")
	}
}

func TestGzipCompressesJSON(t *testing.T) {
	body := strings.Repeat(`{"key":"value","data":12345}`, 100) // > 1KB

	handler := Gzip(1024)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Error("should gzip application/json content")
	}
}

func TestGzipCompressesXML(t *testing.T) {
	body := strings.Repeat(`<item><name>test</name></item>`, 100)

	handler := Gzip(1024)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(body))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/feed.xml", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Error("should gzip application/xml content")
	}
}

func TestGzipSkipConditionalRequest(t *testing.T) {
	body := strings.Repeat("Hello! ", 300)

	handler := Gzip(1024)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(304)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("If-None-Match", `"abc123"`)
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") == "gzip" {
		t.Error("should not gzip conditional requests with If-None-Match")
	}
	_ = body
}

func TestGzipCompressesSVG(t *testing.T) {
	body := strings.Repeat(`<svg><circle r="50"/></svg>`, 100)

	handler := Gzip(1024)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Write([]byte(body))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/icon.svg", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Error("should gzip image/svg+xml content")
	}
}

// --- RateLimit: Allow/deny cycle ---

func TestRateLimitAllowDenyCycle(t *testing.T) {
	rl := NewRateLimiter(context.Background(), 2, 50*time.Millisecond)

	if !rl.Allow("1.2.3.4") {
		t.Error("first request should be allowed")
	}
	if !rl.Allow("1.2.3.4") {
		t.Error("second request should be allowed")
	}
	if rl.Allow("1.2.3.4") {
		t.Error("third request should be denied (limit=2)")
	}

	// Wait for window to expire
	time.Sleep(60 * time.Millisecond)

	if !rl.Allow("1.2.3.4") {
		t.Error("request after window expiry should be allowed")
	}
}

func TestRateLimitDifferentIPs(t *testing.T) {
	rl := NewRateLimiter(context.Background(), 1, time.Minute)

	if !rl.Allow("1.1.1.1") {
		t.Error("first IP first request should be allowed")
	}
	if rl.Allow("1.1.1.1") {
		t.Error("first IP second request should be denied")
	}
	if !rl.Allow("2.2.2.2") {
		t.Error("second IP should have its own bucket")
	}
}

func TestRateLimitZeroLimit(t *testing.T) {
	handler := RateLimit(context.Background(), 0, time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 {
		t.Error("zero limit should pass through without rate limiting")
	}
}

// --- SecurityGuard: custom blocked paths ---

func TestSecurityGuardCustomPaths(t *testing.T) {
	log := logger.New("error", "text")

	customBlocked := []string{"/admin/secret", "/internal"}
	handler := SecurityGuard(log, customBlocked, false, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	tests := []struct {
		path string
		want int
	}{
		{"/admin/secret/page", 403},
		{"/internal/api", 403},
		{"/normal/page", 200},
		{"/.env", 403},        // default blocked
		{"/.git/HEAD", 403},   // default blocked
	}

	for _, tt := range tests {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", tt.path, nil))
		if rec.Code != tt.want {
			t.Errorf("GET %s: status = %d, want %d", tt.path, rec.Code, tt.want)
		}
	}
}

func TestSecurityGuardWAFEncodedAttacks(t *testing.T) {
	log := logger.New("error", "text")

	handler := SecurityGuard(log, nil, true, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// URL-encoded path traversal
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/page", nil)
	req.URL.RawQuery = "file=%2e%2e%2f%2e%2e%2fetc%2fpasswd"
	handler.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("encoded path traversal: status = %d, want 403", rec.Code)
	}
}

// --- AccessLog: output format ---

func TestAccessLogOutputFormat(t *testing.T) {
	log := logger.New("info", "text")

	handler := AccessLog(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(201)
		w.Write([]byte("created"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/items", nil)
	req.Header.Set("User-Agent", "TestBot/1.0")
	handler.ServeHTTP(rec, req)

	if rec.Code != 201 {
		t.Errorf("status = %d, want 201", rec.Code)
	}
	if rec.Body.String() != "created" {
		t.Errorf("body = %q, want 'created'", rec.Body.String())
	}
}

func TestAccessLogCapturesStatusAndBytes(t *testing.T) {
	log := logger.New("debug", "text")

	handler := AccessLog(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte("not found"))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/missing", nil))

	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// --- Gzip: no write at all ---

func TestGzipNoWriteAtAll(t *testing.T) {
	handler := Gzip(1024)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handler does nothing - no WriteHeader, no Write
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(rec, req)

	// Should not panic and should return 200
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// --- isCompressible direct test ---

func TestIsCompressible(t *testing.T) {
	tests := []struct {
		ct   string
		want bool
	}{
		{"text/html", true},
		{"text/css", true},
		{"text/plain", true},
		{"application/json", true},
		{"application/javascript", true},
		{"application/xml", true},
		{"image/svg+xml", true},
		{"application/octet-stream", false},
		{"image/png", false},
		{"image/jpeg", false},
		{"video/mp4", false},
		{"audio/mpeg", false},
		{"application/zip", false},
		{"", false},
	}

	for _, tt := range tests {
		got := isCompressible(tt.ct)
		if got != tt.want {
			t.Errorf("isCompressible(%q) = %v, want %v", tt.ct, got, tt.want)
		}
	}
}

// --- Gzip: multiple writes ---

func TestGzipMultipleWrites(t *testing.T) {
	handler := Gzip(100)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		// First write: below threshold
		w.Write([]byte(strings.Repeat("a", 50)))
		// Second write: crosses threshold
		w.Write([]byte(strings.Repeat("b", 100)))
		// Third write: after compression started
		w.Write([]byte(strings.Repeat("c", 50)))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Error("should be gzip encoded after crossing threshold")
	}
}

// --- RealIP: extractRealIP, extractIP, parseCIDRs ---

func TestRealIPXRealIPHeader(t *testing.T) {
	var capturedIP string

	handler := RealIP([]string{"192.0.2.0/24"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedIP = r.RemoteAddr
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Real-IP", "198.51.100.99")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if !strings.HasPrefix(capturedIP, "198.51.100.99") {
		t.Errorf("RemoteAddr = %q, want 198.51.100.99", capturedIP)
	}
}

func TestRealIPXFFAllTrusted(t *testing.T) {
	var capturedIP string

	// Trust everything in 10.0.0.0/8
	handler := RealIP([]string{"10.0.0.0/8"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedIP = r.RemoteAddr
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	// All XFF IPs are in the trusted range
	req.Header.Set("X-Forwarded-For", "10.0.0.2, 10.0.0.3")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	// When all IPs are trusted, extractRealIP returns the leftmost
	if !strings.HasPrefix(capturedIP, "10.0.0.2") {
		t.Errorf("RemoteAddr = %q, want 10.0.0.2 (leftmost when all trusted)", capturedIP)
	}
}

func TestParseCIDRsSingleIPs(t *testing.T) {
	// Single IPs should be converted to /32
	nets := parseCIDRs([]string{"192.168.1.1", "10.0.0.5"})
	if len(nets) != 2 {
		t.Fatalf("got %d nets, want 2", len(nets))
	}
}

func TestParseCIDRsIPv6(t *testing.T) {
	// IPv6 single IP should get /128
	nets := parseCIDRs([]string{"::1"})
	if len(nets) != 1 {
		t.Fatalf("got %d nets, want 1", len(nets))
	}
}

func TestParseCIDRsInvalid(t *testing.T) {
	nets := parseCIDRs([]string{"not-an-ip", "999.999.999.999"})
	if len(nets) != 0 {
		t.Errorf("got %d nets, want 0 for invalid IPs", len(nets))
	}
}

func TestExtractIPBareIP(t *testing.T) {
	ip := extractIP("192.168.1.1")
	if ip == nil {
		t.Error("should parse bare IP without port")
	}
}

func TestExtractIPInvalid(t *testing.T) {
	ip := extractIP("not-an-ip")
	if ip != nil {
		t.Error("should return nil for invalid input")
	}
}

func TestExtractRealIPEmptyXFF(t *testing.T) {
	result := extractRealIP("", nil)
	if result != "" {
		t.Errorf("got %q, want empty for empty XFF", result)
	}
}

func TestExtractRealIPInvalidIPInXFF(t *testing.T) {
	// Invalid IP entries should be skipped
	result := extractRealIP("not-an-ip, 203.0.113.50", nil)
	if result != "203.0.113.50" {
		t.Errorf("got %q, want 203.0.113.50 (skip invalid)", result)
	}
}

func TestRealIPDirectIPNil(t *testing.T) {
	var capturedIP string
	handler := RealIP([]string{"10.0.0.0/8"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedIP = r.RemoteAddr
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "not-a-valid-ip"
	req.Header.Set("X-Forwarded-For", "203.0.113.50")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	// Direct IP is invalid, so headers should be ignored
	if capturedIP != "not-a-valid-ip" {
		t.Errorf("RemoteAddr = %q, want not-a-valid-ip (invalid direct IP)", capturedIP)
	}
}

// --- CORS: non-preflight with valid origin ---

func TestCORSNonPreflightWithOrigin(t *testing.T) {
	handler := CORS(CORSConfig{
		AllowedOrigins: []string{"https://example.com"},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/data", nil)
	req.Header.Set("Origin", "https://example.com")
	handler.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://example.com" {
		t.Error("ACAO should be set for valid non-preflight request")
	}
}

// --- RateLimit: Retry-After header ---

func TestRateLimitRetryAfterHeader(t *testing.T) {
	handler := RateLimit(context.Background(), 1, 60*time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	// First request: allowed
	rec1 := httptest.NewRecorder()
	req1 := httptest.NewRequest("GET", "/", nil)
	req1.RemoteAddr = "5.5.5.5:1234"
	handler.ServeHTTP(rec1, req1)
	if rec1.Code != 200 {
		t.Errorf("first: status = %d, want 200", rec1.Code)
	}

	// Second request: denied with Retry-After
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.RemoteAddr = "5.5.5.5:1234"
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != 429 {
		t.Errorf("second: status = %d, want 429", rec2.Code)
	}
	if rec2.Header().Get("Retry-After") != "60" {
		t.Errorf("Retry-After = %q, want 60", rec2.Header().Get("Retry-After"))
	}
}
