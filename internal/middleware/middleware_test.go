package middleware

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// --- Chain ---

func TestChainOrder(t *testing.T) {
	var order []string

	a := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "A-before")
			next.ServeHTTP(w, r)
			order = append(order, "A-after")
		})
	}
	b := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "B-before")
			next.ServeHTTP(w, r)
			order = append(order, "B-after")
		})
	}

	handler := Chain(a, b)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))

	expected := "A-before,B-before,handler,B-after,A-after"
	got := strings.Join(order, ",")
	if got != expected {
		t.Errorf("order = %q, want %q", got, expected)
	}
}

// --- Recovery ---

func TestRecoveryPanic(t *testing.T) {
	log := logger.New("error", "text")

	handler := Recovery(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != 500 {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestRecoveryNoPanic(t *testing.T) {
	log := logger.New("error", "text")

	handler := Recovery(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// --- Request ID ---

func TestRequestIDGenerated(t *testing.T) {
	handler := RequestID()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	id := rec.Header().Get("X-Request-ID")
	if id == "" {
		t.Error("X-Request-ID should be set")
	}
}

func TestRequestIDPreserved(t *testing.T) {
	handler := RequestID()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Request-ID", "incoming-id-123")
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-Request-ID") != "incoming-id-123" {
		t.Errorf("should preserve incoming ID, got %q", rec.Header().Get("X-Request-ID"))
	}
}

// --- Real IP ---

func TestRealIPFromXFF(t *testing.T) {
	var capturedIP string

	// Trust the default httptest RemoteAddr network (192.0.2.0/24)
	handler := RealIP([]string{"192.0.2.0/24"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedIP = r.RemoteAddr
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.50")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if !strings.HasPrefix(capturedIP, "203.0.113.50") {
		t.Errorf("RemoteAddr = %q, want 203.0.113.50", capturedIP)
	}
}

func TestRealIPFromCF(t *testing.T) {
	var capturedIP string

	// Trust the default httptest RemoteAddr network (192.0.2.0/24)
	handler := RealIP([]string{"192.0.2.0/24"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedIP = r.RemoteAddr
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("CF-Connecting-IP", "198.51.100.25")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if !strings.HasPrefix(capturedIP, "198.51.100.25") {
		t.Errorf("RemoteAddr = %q, want 198.51.100.25", capturedIP)
	}
}

func TestRealIPNoTrustedProxiesPassthrough(t *testing.T) {
	var capturedIP string

	handler := RealIP(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedIP = r.RemoteAddr
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.50")
	req.Header.Set("CF-Connecting-IP", "198.51.100.25")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	// With no trusted proxies, headers should be ignored; RemoteAddr unchanged.
	if !strings.HasPrefix(capturedIP, "192.0.2.1") {
		t.Errorf("RemoteAddr = %q, want 192.0.2.1 (original, headers ignored)", capturedIP)
	}
}

func TestRealIPUntrustedDirectIP(t *testing.T) {
	var capturedIP string

	// Trust 10.0.0.0/8 but direct connection is from 192.0.2.1 (not trusted)
	handler := RealIP([]string{"10.0.0.0/8"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedIP = r.RemoteAddr
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.50")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	// Direct IP is not trusted, so headers should be ignored.
	if !strings.HasPrefix(capturedIP, "192.0.2.1") {
		t.Errorf("RemoteAddr = %q, want 192.0.2.1 (untrusted direct IP, headers ignored)", capturedIP)
	}
}

// --- Rate Limiter ---

func TestRateLimitAllowed(t *testing.T) {
	handler := RateLimit(context.Background(), 10, time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	for i := 0; i < 10; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "1.2.3.4:1234"
		handler.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Errorf("request %d: status = %d, want 200", i, rec.Code)
		}
	}
}

func TestRateLimitExceeded(t *testing.T) {
	handler := RateLimit(context.Background(), 3, time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "1.2.3.4:1234"
		handler.ServeHTTP(rec, req)

		if i < 3 && rec.Code != 200 {
			t.Errorf("request %d: status = %d, want 200", i, rec.Code)
		}
		if i >= 3 && rec.Code != 429 {
			t.Errorf("request %d: status = %d, want 429", i, rec.Code)
		}
	}
}

// --- Gzip ---

func TestGzipCompression(t *testing.T) {
	body := strings.Repeat("Hello, world! ", 200) // > 1KB

	handler := Gzip(1024)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(body))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Error("should be gzip encoded")
	}

	// Verify it's valid gzip
	gr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("invalid gzip: %v", err)
	}
	decoded, _ := io.ReadAll(gr)
	gr.Close()

	if string(decoded) != body {
		t.Errorf("decoded length = %d, want %d", len(decoded), len(body))
	}
}

func TestGzipSkipSmall(t *testing.T) {
	handler := Gzip(1024)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("small"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") == "gzip" {
		t.Error("should not gzip small responses")
	}
}

func TestGzipSkipNoAccept(t *testing.T) {
	handler := Gzip(1024)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(strings.Repeat("x", 2000)))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	// No Accept-Encoding
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") == "gzip" {
		t.Error("should not gzip when client doesn't accept")
	}
}

// --- Security Headers ---

func TestSecurityHeaders(t *testing.T) {
	handler := SecurityHeaders()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	checks := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":       "SAMEORIGIN",
		"Referrer-Policy":       "strict-origin-when-cross-origin",
	}
	for k, want := range checks {
		if got := rec.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

// --- CORS ---

func TestCORSPreflight(t *testing.T) {
	handler := CORS(CORSConfig{
		AllowedOrigins: []string{"https://example.com"},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "/api/data", nil)
	req.Header.Set("Origin", "https://example.com")
	handler.ServeHTTP(rec, req)

	if rec.Code != 204 {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://example.com" {
		t.Error("ACAO header missing")
	}
}

func TestCORSBlockedOrigin(t *testing.T) {
	handler := CORS(CORSConfig{
		AllowedOrigins: []string{"https://allowed.com"},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "https://evil.com")
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("should not set ACAO for blocked origin")
	}
}

// --- Security Guard ---

func TestSecurityGuardBlockedPath(t *testing.T) {
	log := logger.New("error", "text")

	handler := SecurityGuard(log, nil, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	paths := []string{"/.git/config", "/.env", "/wp-config.php"}
	for _, path := range paths {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 403 {
			t.Errorf("GET %s: status = %d, want 403", path, rec.Code)
		}
	}
}

func TestSecurityGuardAllowedPath(t *testing.T) {
	log := logger.New("error", "text")

	handler := SecurityGuard(log, nil, false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/normal/page", nil))
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestSecurityGuardWAF(t *testing.T) {
	log := logger.New("error", "text")

	handler := SecurityGuard(log, nil, true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	attacks := []struct {
		path  string
		query string
	}{
		{"/page", "id=1 UNION SELECT * FROM users"},
		{"/search", "q=<script>alert(1)</script>"},
		{"/file", "path=../../../etc/passwd"},
	}
	for _, attack := range attacks {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", attack.path, nil)
		req.URL.RawQuery = attack.query
		handler.ServeHTTP(rec, req)
		if rec.Code != 403 {
			t.Errorf("GET %s?%s: status = %d, want 403", attack.path, attack.query, rec.Code)
		}
	}
}

// --- Access Log ---

func TestAccessLog(t *testing.T) {
	log := logger.New("info", "text")

	handler := AccessLog(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("hello"))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/test", nil))

	// Just verify it doesn't panic and completes
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// --- CustomHeaders ---

func TestCustomHeaders(t *testing.T) {
	handler := CustomHeaders(
		map[string]string{"X-Custom": "hello", "X-Another": "world"},
		[]string{"X-Remove-Me"},
	)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Remove-Me", "should-be-removed")
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	// Pre-set the header to be removed (CustomHeaders runs before handler)
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if got := rec.Header().Get("X-Custom"); got != "hello" {
		t.Errorf("X-Custom = %q, want hello", got)
	}
	if got := rec.Header().Get("X-Another"); got != "world" {
		t.Errorf("X-Another = %q, want world", got)
	}
}

// --- Gzip: WriteHeader path (explicit status code before write) ---

func TestGzipWriteHeaderExplicit(t *testing.T) {
	body := strings.Repeat("Hello gzip! ", 200)

	handler := Gzip(1024)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(201)
		w.Write([]byte(body))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	handler.ServeHTTP(rec, req)

	if rec.Code != 201 {
		t.Errorf("status = %d, want 201", rec.Code)
	}
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Error("should be gzip encoded")
	}

	gr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("invalid gzip: %v", err)
	}
	decoded, _ := io.ReadAll(gr)
	gr.Close()

	if string(decoded) != body {
		t.Errorf("decoded length = %d, want %d", len(decoded), len(body))
	}
}

// --- CORS: wildcard "*" origin ---

func TestCORSWildcardOrigin(t *testing.T) {
	handler := CORS(CORSConfig{
		AllowedOrigins: []string{"*"},
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "https://anything.example.com")
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://anything.example.com" {
		t.Errorf("ACAO = %q, want the request origin", got)
	}
}

// --- RealIP: with trusted proxy CIDR ---

func TestRealIPTrustedProxyCIDR(t *testing.T) {
	var capturedIP string

	handler := RealIP([]string{"10.0.0.0/8"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedIP = r.RemoteAddr
	}))

	req := httptest.NewRequest("GET", "/", nil)
	// Direct connection is from a trusted proxy
	req.RemoteAddr = "10.0.0.5:1234"
	// XFF: client, trusted proxy
	req.Header.Set("X-Forwarded-For", "203.0.113.50, 10.0.0.1")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if !strings.HasPrefix(capturedIP, "203.0.113.50") {
		t.Errorf("RemoteAddr = %q, want 203.0.113.50 (rightmost untrusted)", capturedIP)
	}
}
