package middleware

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// mustIP parses an IP for tests, failing if invalid.
func mustIP(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatalf("invalid IP %q", s)
	}
	return ip
}

// runGuard invokes a predicate guard against a request with the given RemoteAddr
// and returns whether the request was allowed to proceed.
func runGuard(t *testing.T, guard func(http.ResponseWriter, *http.Request) bool, remoteAddr string) bool {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = remoteAddr
	return guard(httptest.NewRecorder(), r)
}

// writeGeoDB writes a JSON geo DB file and returns its path.
func writeGeoDB(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "geo.json")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write geo db: %v", err)
	}
	return p
}

// --- accesslog.go: sanitizeURI / isSensitiveQueryParam / redactReferer ---

func TestSanitizeURINoQuery(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://x.test/path", nil)
	if got := sanitizeURI(r); got != "/path" {
		t.Fatalf("expected /path, got %q", got)
	}
}

func TestSanitizeURINonSensitiveQuery(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://x.test/path?page=2&sort=asc", nil)
	got := sanitizeURI(r)
	// No sensitive params → returns RequestURI unchanged.
	if !strings.Contains(got, "page=2") || !strings.Contains(got, "sort=asc") {
		t.Fatalf("expected unmodified query, got %q", got)
	}
	if strings.Contains(got, "REDACTED") {
		t.Fatalf("unexpected redaction in %q", got)
	}
}

func TestSanitizeURISensitiveQueryRedacted(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://x.test/p?token=abc&page=3", nil)
	got := sanitizeURI(r)
	if !strings.Contains(got, "token=%5BREDACTED%5D") && !strings.Contains(got, "token=[REDACTED]") {
		t.Fatalf("token not redacted: %q", got)
	}
	if !strings.Contains(got, "page=3") {
		t.Fatalf("non-sensitive param dropped: %q", got)
	}
}

func TestIsSensitiveQueryParam(t *testing.T) {
	cases := map[string]bool{
		"token":        true,
		"API_KEY":      true,
		"access_token": true,
		"my_password":  true, // substring match
		"page":         false,
		"sort":         false,
	}
	for name, want := range cases {
		if got := isSensitiveQueryParam(name); got != want {
			t.Errorf("isSensitiveQueryParam(%q)=%v, want %v", name, got, want)
		}
	}
}

func TestRedactRefererNoQuery(t *testing.T) {
	in := "https://ref.test/path"
	if got := redactReferer(in); got != in {
		t.Fatalf("expected unchanged %q, got %q", in, got)
	}
}

func TestRedactRefererNonSensitive(t *testing.T) {
	in := "https://ref.test/path?page=1"
	if got := redactReferer(in); got != in {
		t.Fatalf("expected unchanged %q, got %q", in, got)
	}
}

func TestRedactRefererSensitive(t *testing.T) {
	in := "https://ref.test/path?token=secret&page=1"
	got := redactReferer(in)
	if strings.Contains(got, "secret") {
		t.Fatalf("secret leaked: %q", got)
	}
	if !strings.Contains(got, "REDACTED") {
		t.Fatalf("expected redaction: %q", got)
	}
	if !strings.Contains(got, "page=1") {
		t.Fatalf("non-sensitive dropped: %q", got)
	}
}

func TestRedactRefererMalformedQuery(t *testing.T) {
	// A query that url.ParseQuery rejects (invalid percent-encoding).
	in := "https://ref.test/path?%zz=token"
	got := redactReferer(in)
	if got != "https://ref.test/path?[REDACTED]" {
		t.Fatalf("expected blanket redaction, got %q", got)
	}
}

func TestAccessLogRedactsReferer(t *testing.T) {
	log := logger.New("info", "text")
	handler := AccessLog(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	r := httptest.NewRequest(http.MethodGet, "http://x.test/p?token=abc", nil)
	r.Header.Set("Referer", "https://ref.test/x?secret=zzz")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
}

// --- ratelimit.go: Stop / SetTrustedProxies / isTrustedProxy / clientIP XFF ---

func TestRateLimiterStopNil(t *testing.T) {
	var rl *RateLimiter
	rl.Stop() // must not panic
}

func TestRateLimiterStopIdempotent(t *testing.T) {
	rl := NewRateLimiter(context.Background(), 5, time.Second)
	rl.Stop()
	rl.Stop() // safe to call twice
}

func TestSetTrustedProxiesParsesValidSkipsInvalid(t *testing.T) {
	rl := NewRateLimiter(context.Background(), 5, time.Second)
	defer rl.Stop()
	rl.SetTrustedProxies([]string{"10.0.0.0/8", "not-a-cidr", "192.168.1.0/24"})
	if len(rl.trustedProxies) != 2 {
		t.Fatalf("expected 2 trusted nets, got %d", len(rl.trustedProxies))
	}
}

func TestIsTrustedProxy(t *testing.T) {
	rl := NewRateLimiter(context.Background(), 5, time.Second)
	defer rl.Stop()

	// No trusted proxies → always false.
	if rl.isTrustedProxy(mustIP(t, "10.1.2.3")) {
		t.Fatal("expected false with no trusted proxies")
	}

	rl.SetTrustedProxies([]string{"10.0.0.0/8"})
	if !rl.isTrustedProxy(mustIP(t, "10.1.2.3")) {
		t.Fatal("10.1.2.3 should be trusted")
	}
	if rl.isTrustedProxy(mustIP(t, "8.8.8.8")) {
		t.Fatal("8.8.8.8 should not be trusted")
	}
}

func TestClientIPNoProxyUsesRemoteAddr(t *testing.T) {
	rl := NewRateLimiter(context.Background(), 5, time.Second)
	defer rl.Stop()
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = "203.0.113.9:1234"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := clientIP(rl, r); got != "203.0.113.9" {
		t.Fatalf("expected RemoteAddr ip, got %q", got)
	}
}

func TestClientIPRemoteAddrNoPort(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = "203.0.113.9" // no port
	if got := clientIP(nil, r); got != "203.0.113.9" {
		t.Fatalf("expected bare addr, got %q", got)
	}
}

func TestClientIPTrustedProxyRightmostUntrustedXFF(t *testing.T) {
	rl := NewRateLimiter(context.Background(), 5, time.Second)
	defer rl.Stop()
	rl.SetTrustedProxies([]string{"10.0.0.0/8"})

	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = "10.0.0.5:9999" // connection from trusted proxy
	// Client-controlled fake leftmost + real client + trusted hop.
	r.Header.Set("X-Forwarded-For", "9.9.9.9, 203.0.113.7, 10.0.0.6")
	if got := clientIP(rl, r); got != "203.0.113.7" {
		t.Fatalf("expected rightmost untrusted 203.0.113.7, got %q", got)
	}
}

func TestClientIPTrustedProxyFallsBackToXRealIP(t *testing.T) {
	rl := NewRateLimiter(context.Background(), 5, time.Second)
	defer rl.Stop()
	rl.SetTrustedProxies([]string{"10.0.0.0/8"})

	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = "10.0.0.5:9999"
	// No XFF, but X-Real-IP present.
	r.Header.Set("X-Real-IP", " 198.51.100.2 ")
	if got := clientIP(rl, r); got != "198.51.100.2" {
		t.Fatalf("expected X-Real-IP, got %q", got)
	}
}

func TestClientIPTrustedProxyAllTrustedXFFFallsThroughToRemote(t *testing.T) {
	rl := NewRateLimiter(context.Background(), 5, time.Second)
	defer rl.Stop()
	rl.SetTrustedProxies([]string{"10.0.0.0/8"})

	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = "10.0.0.5:9999"
	// All XFF entries trusted → extractRealIP returns leftmost (10.0.0.1).
	r.Header.Set("X-Forwarded-For", "10.0.0.1, 10.0.0.2")
	if got := clientIP(rl, r); got != "10.0.0.1" {
		t.Fatalf("expected leftmost trusted 10.0.0.1, got %q", got)
	}
}

func TestClientIPUntrustedProxyIgnoresHeaders(t *testing.T) {
	rl := NewRateLimiter(context.Background(), 5, time.Second)
	defer rl.Stop()
	rl.SetTrustedProxies([]string{"10.0.0.0/8"})

	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = "8.8.8.8:1111" // not a trusted proxy
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.Header.Set("X-Real-IP", "5.6.7.8")
	if got := clientIP(rl, r); got != "8.8.8.8" {
		t.Fatalf("expected RemoteAddr 8.8.8.8, got %q", got)
	}
}

// --- realip.go: DirectIP / RealIP branches ---

func TestDirectIPNilRequest(t *testing.T) {
	if got := DirectIP(nil); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestDirectIPNoValueInContext(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	if got := DirectIP(r); got != "" {
		t.Fatalf("expected empty for plain request, got %q", got)
	}
}

func TestRealIPSetsDirectIPContext(t *testing.T) {
	var captured string
	h := RealIP(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = DirectIP(r)
	}))
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = "203.0.113.50:4000"
	h.ServeHTTP(httptest.NewRecorder(), r)
	if captured != "203.0.113.50" {
		t.Fatalf("DirectIP=%q, want 203.0.113.50", captured)
	}
}

func TestRealIPTrustedProxyCFConnectingIP(t *testing.T) {
	var seen string
	h := RealIP([]string{"10.0.0.0/8"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.RemoteAddr
	}))
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = "10.0.0.1:5000"
	r.Header.Set("CF-Connecting-IP", "1.2.3.4")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if seen != "1.2.3.4:0" {
		t.Fatalf("expected CF IP, got %q", seen)
	}
}

func TestRealIPTrustedProxyIPv6Headers(t *testing.T) {
	tests := []struct {
		name   string
		header string
		value  string
	}{
		{name: "cloudflare", header: "CF-Connecting-IP", value: "2001:db8::10"},
		{name: "x-real-ip", header: "X-Real-IP", value: "2001:db8::11"},
		{name: "forwarded-for", header: "X-Forwarded-For", value: "2001:db8::12"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var seen string
			h := RealIP([]string{"10.0.0.0/8"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = r.RemoteAddr
			}))
			r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
			r.RemoteAddr = "10.0.0.1:5000"
			r.Header.Set(tt.header, tt.value)
			h.ServeHTTP(httptest.NewRecorder(), r)
			if want := net.JoinHostPort(tt.value, "0"); seen != want {
				t.Fatalf("RemoteAddr=%q, want %q", seen, want)
			}
		})
	}
}

func TestRealIPTrustedProxyXRealIP(t *testing.T) {
	var seen string
	h := RealIP([]string{"10.0.0.0/8"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.RemoteAddr
	}))
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = "10.0.0.1:5000"
	r.Header.Set("X-Real-IP", "5.6.7.8")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if seen != "5.6.7.8:0" {
		t.Fatalf("expected X-Real-IP, got %q", seen)
	}
}

func TestRealIPTrustedProxyXFF(t *testing.T) {
	var seen string
	h := RealIP([]string{"10.0.0.0/8"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.RemoteAddr
	}))
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = "10.0.0.1:5000"
	r.Header.Set("X-Forwarded-For", "9.9.9.9, 203.0.113.7, 10.0.0.2")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if seen != "203.0.113.7:0" {
		t.Fatalf("expected rightmost untrusted, got %q", seen)
	}
}

func TestRealIPTrustedProxyNoHeaders(t *testing.T) {
	var seen string
	h := RealIP([]string{"10.0.0.0/8"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.RemoteAddr
	}))
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = "10.0.0.1:5000"
	h.ServeHTTP(httptest.NewRecorder(), r)
	if seen != "10.0.0.1:5000" {
		t.Fatalf("expected unchanged RemoteAddr, got %q", seen)
	}
}

func TestRealIPUntrustedDirectNoRewrite(t *testing.T) {
	var seen string
	h := RealIP([]string{"10.0.0.0/8"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.RemoteAddr
	}))
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = "8.8.8.8:5000"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if seen != "8.8.8.8:5000" {
		t.Fatalf("expected unchanged for untrusted, got %q", seen)
	}
}

func TestRealIPTrustedProxyXFFAllTrustedReturnsLeftmost(t *testing.T) {
	var seen string
	h := RealIP([]string{"10.0.0.0/8"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.RemoteAddr
	}))
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = "10.0.0.1:5000"
	r.Header.Set("X-Forwarded-For", "10.0.0.2, 10.0.0.3")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if seen != "10.0.0.2:0" {
		t.Fatalf("expected leftmost trusted, got %q", seen)
	}
}

func TestExtractRealIPEmptyAndInvalid(t *testing.T) {
	trusted := parseCIDRs([]string{"10.0.0.0/8"})
	if got := extractRealIP("", trusted); got != "" {
		t.Fatalf("expected empty for empty xff, got %q", got)
	}
	// Invalid IP entries are skipped, fall to leftmost.
	if got := extractRealIP("not-an-ip, 10.0.0.5", trusted); got != "not-an-ip" {
		t.Fatalf("got %q", got)
	}
}

// --- ipacl.go: IPACLGuard ---

func TestIPACLGuardNilWhenEmpty(t *testing.T) {
	if IPACLGuard(IPACLConfig{}) != nil {
		t.Fatal("expected nil guard for empty config")
	}
}

func TestIPACLGuardWhitelistAllowsAndDenies(t *testing.T) {
	guard := IPACLGuard(IPACLConfig{Whitelist: []string{"10.0.0.0/8"}})
	if guard == nil {
		t.Fatal("expected non-nil guard")
	}
	// Allowed.
	if !runGuard(t, guard, "10.1.1.1:80") {
		t.Fatal("10.1.1.1 should be allowed")
	}
	// Denied.
	if runGuard(t, guard, "8.8.8.8:80") {
		t.Fatal("8.8.8.8 should be denied")
	}
}

func TestIPACLGuardWhitelistPlusBlacklist(t *testing.T) {
	guard := IPACLGuard(IPACLConfig{
		Whitelist: []string{"10.0.0.0/8"},
		Blacklist: []string{"10.9.0.0/16"},
	})
	if !runGuard(t, guard, "10.1.1.1:80") {
		t.Fatal("10.1.1.1 allowed")
	}
	if runGuard(t, guard, "10.9.1.1:80") {
		t.Fatal("10.9.1.1 should be blacklisted")
	}
}

func TestIPACLGuardBlacklistOnly(t *testing.T) {
	guard := IPACLGuard(IPACLConfig{Blacklist: []string{"6.6.6.0/24"}})
	if runGuard(t, guard, "6.6.6.6:80") {
		t.Fatal("6.6.6.6 should be denied")
	}
	if !runGuard(t, guard, "1.1.1.1:80") {
		t.Fatal("1.1.1.1 should be allowed")
	}
}

func TestIPACLGuardNilIP(t *testing.T) {
	guard := IPACLGuard(IPACLConfig{Blacklist: []string{"6.6.6.0/24"}})
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = "garbage" // unparseable
	rec := httptest.NewRecorder()
	if guard(rec, r) {
		t.Fatal("expected deny on unparseable IP")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

// --- geoip.go: GeoIPGuard ---

func TestGeoIPGuardNilWhenEmpty(t *testing.T) {
	if GeoIPGuard(GeoIPConfig{}) != nil {
		t.Fatal("expected nil guard for empty config")
	}
}

func TestGeoIPGuardPrivateIPAllowed(t *testing.T) {
	guard := GeoIPGuard(GeoIPConfig{BlockedCountries: []string{"CN"}})
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = "192.168.1.5:80"
	if !guard(httptest.NewRecorder(), r) {
		t.Fatal("private IP should be allowed")
	}
}

func TestGeoIPGuardEmptyIPAllowed(t *testing.T) {
	guard := GeoIPGuard(GeoIPConfig{BlockedCountries: []string{"CN"}})
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = "" // SplitHostPort yields empty host
	if !guard(httptest.NewRecorder(), r) {
		t.Fatal("empty IP should be allowed")
	}
}

func TestGeoIPGuardBlockedCountryWithDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := writeGeoDB(t, dir, `{"203.0.113.0/24":"CN"}`)
	guard := GeoIPGuard(GeoIPConfig{BlockedCountries: []string{"cn"}, DBPath: dbPath})
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = "203.0.113.5:80"
	rec := httptest.NewRecorder()
	if guard(rec, r) {
		t.Fatal("CN should be blocked")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestGeoIPGuardBlockedAllowsOther(t *testing.T) {
	dir := t.TempDir()
	dbPath := writeGeoDB(t, dir, `{"203.0.113.0/24":"US"}`)
	guard := GeoIPGuard(GeoIPConfig{BlockedCountries: []string{"CN"}, DBPath: dbPath})
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = "203.0.113.5:80"
	if !guard(httptest.NewRecorder(), r) {
		t.Fatal("US should be allowed")
	}
}

func TestGeoIPGuardWhitelistMode(t *testing.T) {
	dir := t.TempDir()
	dbPath := writeGeoDB(t, dir, `{"203.0.113.0/24":"DE","198.51.100.0/24":"FR"}`)
	guard := GeoIPGuard(GeoIPConfig{AllowedCountries: []string{"DE"}, DBPath: dbPath})

	// DE allowed.
	r1 := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r1.RemoteAddr = "203.0.113.5:80"
	if !guard(httptest.NewRecorder(), r1) {
		t.Fatal("DE should be allowed in whitelist mode")
	}

	// FR denied.
	r2 := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r2.RemoteAddr = "198.51.100.5:80"
	rec := httptest.NewRecorder()
	if guard(rec, r2) {
		t.Fatal("FR should be denied in whitelist mode")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestGeoIPGuardUnknownCountryAllowed(t *testing.T) {
	// IP not in DB → lookupCountry returns "" → allowed (default-allow).
	dir := t.TempDir()
	dbPath := writeGeoDB(t, dir, `{"203.0.113.0/24":"CN"}`)
	guard := GeoIPGuard(GeoIPConfig{BlockedCountries: []string{"CN"}, DBPath: dbPath})
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = "8.8.8.8:80" // not in DB; enqueues async lookup, returns ""
	if !guard(httptest.NewRecorder(), r) {
		t.Fatal("unknown country should be allowed on first request")
	}
}

func TestGeoIPGuardBadDBPathIgnored(t *testing.T) {
	guard := GeoIPGuard(GeoIPConfig{BlockedCountries: []string{"CN"}, DBPath: "/nonexistent/geo.json"})
	if guard == nil {
		t.Fatal("guard should still be created with bad DB path")
	}
}

// --- compress.go: selectEncoding / WriteHeader / Close branches ---

func TestSelectEncodingQualityValues(t *testing.T) {
	if selectEncoding("gzip;q=1.0") != encodingGzip {
		t.Error("gzip with quality value")
	}
	if selectEncoding("br;q=0.9, deflate;q=0.1") != encodingBrotli {
		t.Error("br with quality value")
	}
	if selectEncoding("deflate;q=1.0, identity;q=0") != encodingNone {
		t.Error("no br/gzip with quality values → none")
	}
}

func TestCompressGzipNegotiation(t *testing.T) {
	body := strings.Repeat("hello world ", 500)
	h := Compress(64)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(body))
	}))
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected gzip, got %q", rec.Header().Get("Content-Encoding"))
	}
	if rec.Body.Len() >= len(body) {
		t.Fatal("expected compressed (smaller) body")
	}
}

func TestCompressWriteHeader1xxFlushesImmediately(t *testing.T) {
	h := Compress(8)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusEarlyHints) // 103 < 200
		w.Write([]byte("body data here"))
	}))
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Header().Get("Content-Encoding") == "gzip" {
		t.Fatal("1xx should not be compressed")
	}
}

func TestCompressWriteHeaderDoubleCallIgnored(t *testing.T) {
	h := Compress(8)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.WriteHeader(http.StatusInternalServerError) // ignored
		w.Write([]byte(strings.Repeat("x", 100)))
	}))
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestCompressEmptyResponseNoBody(t *testing.T) {
	h := Compress(8)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// no write, no WriteHeader at all → Close should default to 200
	}))
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 default, got %d", rec.Code)
	}
}

func TestCompressHeaderOnlyNoWrite(t *testing.T) {
	h := Compress(8)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted) // 202, body-bearing status, but no body written
	}))
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}
}

// --- geoip.go: GeoIP middleware (non-guard) ---

func TestGeoIPMiddlewareBlocksWithDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := writeGeoDB(t, dir, `{"203.0.113.0/24":"CN"}`)
	called := false
	h := GeoIP(GeoIPConfig{BlockedCountries: []string{"CN"}, DBPath: dbPath})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = "203.0.113.5:80"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if called || rec.Code != http.StatusForbidden {
		t.Fatalf("expected block, called=%v code=%d", called, rec.Code)
	}
}

func TestGeoIPMiddlewareAllowsOtherWithDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := writeGeoDB(t, dir, `{"203.0.113.0/24":"US"}`)
	called := false
	h := GeoIP(GeoIPConfig{BlockedCountries: []string{"CN"}, DBPath: dbPath})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = "203.0.113.5:80"
	h.ServeHTTP(httptest.NewRecorder(), r)
	if !called {
		t.Fatal("US should pass through")
	}
}

func TestGeoIPMiddlewareWhitelistDeniesWithDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := writeGeoDB(t, dir, `{"198.51.100.0/24":"FR"}`)
	called := false
	h := GeoIP(GeoIPConfig{AllowedCountries: []string{"DE"}, DBPath: dbPath})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = "198.51.100.5:80"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if called || rec.Code != http.StatusForbidden {
		t.Fatalf("FR should be denied, called=%v code=%d", called, rec.Code)
	}
}

func TestGeoIPMiddlewareWhitelistAllowsWithDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := writeGeoDB(t, dir, `{"203.0.113.0/24":"DE"}`)
	called := false
	h := GeoIP(GeoIPConfig{AllowedCountries: []string{"DE"}, DBPath: dbPath})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = "203.0.113.5:80"
	h.ServeHTTP(httptest.NewRecorder(), r)
	if !called {
		t.Fatal("DE should be allowed")
	}
}

func TestGeoIPMiddlewarePrivateIPPassthrough(t *testing.T) {
	called := false
	h := GeoIP(GeoIPConfig{BlockedCountries: []string{"CN"}})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = "10.1.1.1:80"
	h.ServeHTTP(httptest.NewRecorder(), r)
	if !called {
		t.Fatal("private IP should pass through")
	}
}

func TestGeoIPMiddlewareUnknownCountryPassthrough(t *testing.T) {
	dir := t.TempDir()
	dbPath := writeGeoDB(t, dir, `{"203.0.113.0/24":"CN"}`)
	called := false
	h := GeoIP(GeoIPConfig{BlockedCountries: []string{"CN"}, DBPath: dbPath})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = "8.8.8.8:80" // not in DB
	h.ServeHTTP(httptest.NewRecorder(), r)
	if !called {
		t.Fatal("unknown country allowed on first request")
	}
}

// --- geoip.go: helper internals ---

func TestIsPrivateIPInvalid(t *testing.T) {
	if isPrivateIP("not-an-ip") {
		t.Fatal("invalid IP should not be private")
	}
	if !isPrivateIP("127.0.0.1") {
		t.Fatal("loopback is private")
	}
	if !isPrivateIP("::1") {
		t.Fatal("ipv6 loopback is private")
	}
	if isPrivateIP("8.8.8.8") {
		t.Fatal("public IP not private")
	}
}

func TestGeoExtractIP(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://x.test/", nil)
	r.RemoteAddr = "203.0.113.9:1234"
	if got := geoExtractIP(r); got != "203.0.113.9" {
		t.Fatalf("got %q", got)
	}
}

func TestLookupExternalInvalidIP(t *testing.T) {
	if got := lookupExternal("not-an-ip"); got != "" {
		t.Fatalf("expected empty for invalid IP, got %q", got)
	}
}

func TestGeoCacheNegativeAndPositiveTTL(t *testing.T) {
	c := &geoCache{entries: make(map[string]geoCacheEntry), inflight: make(map[string]struct{})}
	c.set("1.1.1.1", "US")
	c.set("2.2.2.2", "") // negative
	if v, ok := c.get("1.1.1.1"); !ok || v != "US" {
		t.Fatalf("positive entry: %q %v", v, ok)
	}
	if v, ok := c.get("2.2.2.2"); !ok || v != "" {
		t.Fatalf("negative entry should be cached: %q %v", v, ok)
	}
	if _, ok := c.get("3.3.3.3"); ok {
		t.Fatal("missing entry should report miss")
	}
}

func TestGeoCacheTryClaimInflight(t *testing.T) {
	c := &geoCache{entries: make(map[string]geoCacheEntry), inflight: make(map[string]struct{})}
	if !c.tryClaimInflight("1.1.1.1") {
		t.Fatal("first claim should succeed")
	}
	if c.tryClaimInflight("1.1.1.1") {
		t.Fatal("second claim should fail (already inflight)")
	}
	c.releaseInflight("1.1.1.1")
	if !c.tryClaimInflight("1.1.1.1") {
		t.Fatal("claim should succeed after release")
	}
}

func TestEnqueueGeoLookupSkipsWhenInflight(t *testing.T) {
	c := &geoCache{entries: make(map[string]geoCacheEntry), inflight: make(map[string]struct{})}
	c.tryClaimInflight("198.51.100.99") // pretend already inflight
	// Should return immediately without panicking or enqueuing.
	enqueueGeoLookup("198.51.100.99", c)
}

// --- security.go: DomainWAFGuard branches ---

func TestDomainWAFGuardURLBlockedExpectHeader(t *testing.T) {
	log := logger.New("error", "text")
	stats := NewSecurityStats()
	guard := DomainWAFGuard(log, nil, stats)
	r := httptest.NewRequest(http.MethodGet, "http://x.test/?q=<script>alert(1)</script>", nil)
	r.Header.Set("Expect", "100-continue")
	rec := httptest.NewRecorder()
	if guard(rec, r) {
		t.Fatal("malicious URL should be blocked")
	}
	if rec.Code != http.StatusExpectationFailed {
		t.Fatalf("expected 417 with Expect header, got %d", rec.Code)
	}
}

func TestDomainWAFGuardBodyBlockedRestoresStream(t *testing.T) {
	log := logger.New("error", "text")
	stats := NewSecurityStats()
	guard := DomainWAFGuard(log, nil, stats)
	r := httptest.NewRequest(http.MethodPost, "http://x.test/submit",
		strings.NewReader("name=1' UNION SELECT password FROM users--"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	if guard(rec, r) {
		t.Fatal("malicious body should be blocked")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestDomainWAFGuardSafeBodyPassesAndPreserved(t *testing.T) {
	log := logger.New("error", "text")
	guard := DomainWAFGuard(log, nil, nil)
	r := httptest.NewRequest(http.MethodPost, "http://x.test/submit",
		strings.NewReader("name=alice&age=30"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if !guard(httptest.NewRecorder(), r) {
		t.Fatal("safe body should pass")
	}
	// Body must still be readable downstream (MultiReader restore).
	rest, _ := io.ReadAll(r.Body)
	if !strings.Contains(string(rest), "name=alice") {
		t.Fatalf("body not preserved: %q", string(rest))
	}
}

func TestDomainWAFGuardBypassPath(t *testing.T) {
	log := logger.New("error", "text")
	guard := DomainWAFGuard(log, []string{"/health"}, nil)
	r := httptest.NewRequest(http.MethodGet, "http://x.test/health?q=<script>", nil)
	if !guard(httptest.NewRecorder(), r) {
		t.Fatal("bypass path should always proceed")
	}
}

// --- hotlink.go: additional branches ---

func TestHotlinkGuardDefaultExtensions(t *testing.T) {
	log := logger.New("error", "text")
	guard := HotlinkGuard(log, []string{"trusted.com"}, nil) // nil → defaults
	r := httptest.NewRequest(http.MethodGet, "http://site.test/pic.png", nil)
	r.Header.Set("Referer", "http://evil.com/page")
	rec := httptest.NewRecorder()
	if guard(rec, r) {
		t.Fatal("foreign referer to default-protected ext should be blocked")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestHotlinkGuardSameHostReferer(t *testing.T) {
	log := logger.New("error", "text")
	guard := HotlinkGuard(log, nil, []string{".png"})
	r := httptest.NewRequest(http.MethodGet, "http://site.test/pic.png", nil)
	r.Host = "site.test"
	r.Header.Set("Referer", "http://site.test/gallery")
	if !guard(httptest.NewRecorder(), r) {
		t.Fatal("same-host referer should be allowed")
	}
}

func TestHotlinkGuardAllowedRefererMatch(t *testing.T) {
	log := logger.New("error", "text")
	guard := HotlinkGuard(log, []string{"partner.com"}, []string{".png"})
	r := httptest.NewRequest(http.MethodGet, "http://site.test/pic.png", nil)
	r.Host = "site.test"
	r.Header.Set("Referer", "http://partner.com/embed")
	if !guard(httptest.NewRecorder(), r) {
		t.Fatal("allowed referer should pass")
	}
}

func TestHotlinkGuardUnprotectedExtension(t *testing.T) {
	log := logger.New("error", "text")
	guard := HotlinkGuard(log, nil, []string{".png"})
	r := httptest.NewRequest(http.MethodGet, "http://site.test/index.html", nil)
	r.Header.Set("Referer", "http://evil.com/")
	if !guard(httptest.NewRecorder(), r) {
		t.Fatal("non-protected extension should pass regardless of referer")
	}
}

// --- geoip.go: set eviction + lookupCountry DB parse-error + enqueue full ---

func TestGeoCacheSetEviction(t *testing.T) {
	c := &geoCache{entries: make(map[string]geoCacheEntry), inflight: make(map[string]struct{})}
	for i := 0; i < 10005; i++ {
		c.set(string(rune(i)), "US")
	}
	c.mu.RLock()
	n := len(c.entries)
	c.mu.RUnlock()
	if n > 10000 {
		t.Fatalf("cache exceeded cap: %d entries", n)
	}
}

func TestLookupCountryDBParseErrorContinues(t *testing.T) {
	// DB contains one invalid CIDR (skipped) and one valid match.
	db := map[string]string{
		"not-a-cidr":     "XX",
		"203.0.113.0/24": "JP",
	}
	c := &geoCache{entries: make(map[string]geoCacheEntry), inflight: make(map[string]struct{})}
	got := lookupCountry("203.0.113.5", db, c)
	if got != "JP" {
		t.Fatalf("expected JP after skipping invalid CIDR, got %q", got)
	}
}

func TestLookupCountryCacheHit(t *testing.T) {
	c := &geoCache{entries: make(map[string]geoCacheEntry), inflight: make(map[string]struct{})}
	c.set("9.9.9.9", "CA")
	if got := lookupCountry("9.9.9.9", nil, c); got != "CA" {
		t.Fatalf("expected cached CA, got %q", got)
	}
}
