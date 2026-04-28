package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
)

func testAuditServer() *Server {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
		},
	}
	log := logger.New("error", "text")
	m := metrics.New()
	return New(cfg, log, m)
}

func TestRecordAudit(t *testing.T) {
	s := testAuditServer()
	defer s.stopAudit()

	s.RecordAudit("config.reload", "", "127.0.0.1", true)
	s.RecordAudit("domain.create", "domain: example.com", "10.0.0.1", true)
	s.RecordAudit("cache.purge", "all", "10.0.0.2", false)

	// Verify via the endpoint.
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/audit", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var entries []AuditEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}

	// First entry (oldest) should be config.reload.
	if entries[0].Action != "config.reload" {
		t.Errorf("entries[0].Action = %q, want config.reload", entries[0].Action)
	}
	if !entries[0].Success {
		t.Errorf("entries[0].Success = false, want true")
	}

	// Second entry should be domain.create.
	if entries[1].Action != "domain.create" {
		t.Errorf("entries[1].Action = %q, want domain.create", entries[1].Action)
	}
	if entries[1].Detail != "domain: example.com" {
		t.Errorf("entries[1].Detail = %q", entries[1].Detail)
	}

	// Third entry should be cache.purge (failed).
	if entries[2].Action != "cache.purge" {
		t.Errorf("entries[2].Action = %q, want cache.purge", entries[2].Action)
	}
	if entries[2].Success {
		t.Errorf("entries[2].Success = true, want false")
	}
}

func TestAuditRingBufferOverflow(t *testing.T) {
	s := testAuditServer()
	defer s.stopAudit()

	// Fill the buffer beyond capacity.
	for i := 0; i < maxAuditEntries+50; i++ {
		action := "test.action"
		if i < 50 {
			action = "old.action"
		}
		s.RecordAudit(action, "", "127.0.0.1", true)
	}

	// Verify via the endpoint.
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/audit", nil))

	var entries []AuditEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Should have exactly maxAuditEntries entries.
	if len(entries) != maxAuditEntries {
		t.Fatalf("entries = %d, want %d", len(entries), maxAuditEntries)
	}

	// The first 50 "old.action" entries should have been overwritten.
	// All entries should now be "test.action" since the old ones were pushed out.
	for i, e := range entries {
		if e.Action != "test.action" {
			t.Errorf("entries[%d].Action = %q, want test.action", i, e.Action)
		}
	}
}

func TestAuditEmptyReturnsEmptyArray(t *testing.T) {
	s := testAuditServer()
	defer s.stopAudit()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/audit", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	body := strings.TrimSpace(rec.Body.String())
	if body != "[]" {
		t.Errorf("body = %q, want []", body)
	}
}

func TestRateLimitBlocksAfterFailures(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{
				Listen: "127.0.0.1:0",
				APIKey: "secret-key",
			},
		},
	}
	log := logger.New("error", "text")
	m := metrics.New()
	s := New(cfg, log, m)
	defer s.stopAudit()

	handler := s.authMiddleware(s.mux)

	// Make requests with wrong API key (below threshold).
	for i := 0; i < rateLimitMaxFails-1; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/v1/stats", nil)
		req.Header.Set("Authorization", "Bearer wrong-key")
		req.RemoteAddr = "192.168.1.1:12345"
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401", i+1, rec.Code)
		}
	}

	// One more failure should trigger the block (10th failure).
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/stats", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	req.RemoteAddr = "192.168.1.1:12345"
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("10th attempt: status = %d, want 401", rec.Code)
	}

	// Now the IP should be blocked — even with correct key.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/stats", nil)
	req.Header.Set("Authorization", "Bearer secret-key")
	req.RemoteAddr = "192.168.1.1:54321"
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("blocked request: status = %d, want 429", rec.Code)
	}

	// A different IP should NOT be blocked.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/v1/stats", nil)
	req.Header.Set("Authorization", "Bearer secret-key")
	req.RemoteAddr = "10.0.0.1:9999"
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("different IP: status = %d, want 200", rec.Code)
	}
}

func TestRateLimitExpiry(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{
				Listen: "127.0.0.1:0",
				APIKey: "secret-key",
			},
		},
	}
	log := logger.New("error", "text")
	m := metrics.New()
	s := New(cfg, log, m)
	defer s.stopAudit()

	// Simulate failed attempts that trigger a block.
	for i := 0; i < rateLimitMaxFails; i++ {
		s.recordAuthFailure("10.0.0.5", "")
	}

	// Confirm blocked.
	if !s.checkRateLimit("10.0.0.5", "") {
		t.Fatal("expected IP to be blocked")
	}

	// Manually set the blockedAt time to the past so the block expires.
	s.rlMu.Lock()
	entry := s.rateLimit["10.0.0.5"]
	entry.blockedAt = time.Now().Add(-rateLimitBlockTime - time.Second)
	s.rlMu.Unlock()

	// Should no longer be blocked.
	if s.checkRateLimit("10.0.0.5", "") {
		t.Fatal("expected IP to be unblocked after expiry")
	}
}

func TestRateLimitWindowReset(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{
				Listen: "127.0.0.1:0",
				APIKey: "secret-key",
			},
		},
	}
	log := logger.New("error", "text")
	m := metrics.New()
	s := New(cfg, log, m)
	defer s.stopAudit()

	// Record some failures.
	for i := 0; i < rateLimitMaxFails-1; i++ {
		s.recordAuthFailure("10.0.0.6", "")
	}

	// Push the firstFail time back so the window has expired.
	s.rlMu.Lock()
	entry := s.rateLimit["10.0.0.6"]
	entry.firstFail = time.Now().Add(-rateLimitWindow - time.Second)
	s.rlMu.Unlock()

	// Next failure should reset the counter (not trigger a block).
	blocked := s.recordAuthFailure("10.0.0.6", "")
	if blocked {
		t.Fatal("expected window reset, not a block")
	}

	// Verify count was reset.
	s.rlMu.Lock()
	count := s.rateLimit["10.0.0.6"].count
	s.rlMu.Unlock()
	if count != 1 {
		t.Errorf("count = %d, want 1 (reset)", count)
	}
}

func TestCleanupRateLimits(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			Admin: config.AdminConfig{
				Listen: "127.0.0.1:0",
				APIKey: "secret-key",
			},
		},
	}
	log := logger.New("error", "text")
	m := metrics.New()
	s := New(cfg, log, m)
	defer s.stopAudit()

	// Add a stale (non-blocked) entry.
	s.rlMu.Lock()
	s.rateLimit["stale-ip"] = &rateLimitEntry{
		count:     3,
		firstFail: time.Now().Add(-rateLimitWindow - time.Minute),
	}
	// Add a stale blocked entry.
	s.rateLimit["blocked-stale"] = &rateLimitEntry{
		count:     10,
		firstFail: time.Now().Add(-10 * time.Minute),
		blocked:   true,
		blockedAt: time.Now().Add(-rateLimitBlockTime - time.Minute),
	}
	// Add a fresh entry that should survive.
	s.rateLimit["fresh-ip"] = &rateLimitEntry{
		count:     2,
		firstFail: time.Now(),
	}
	// Add stale user entries too.
	s.userRateLimits["stale-user"] = &rateLimitEntry{
		count:     5,
		firstFail: time.Now().Add(-rateLimitWindow - time.Minute),
	}
	s.userRateLimits["blocked-stale-user"] = &rateLimitEntry{
		count:     10,
		firstFail: time.Now().Add(-10 * time.Minute),
		blocked:   true,
		blockedAt: time.Now().Add(-rateLimitBlockTime - time.Minute),
	}
	s.userRateLimits["fresh-user"] = &rateLimitEntry{
		count:     2,
		firstFail: time.Now(),
	}
	s.rlMu.Unlock()

	s.cleanupRateLimits()

	s.rlMu.Lock()
	defer s.rlMu.Unlock()

	if _, ok := s.rateLimit["stale-ip"]; ok {
		t.Error("stale-ip should have been cleaned up")
	}
	if _, ok := s.rateLimit["blocked-stale"]; ok {
		t.Error("blocked-stale should have been cleaned up")
	}
	if _, ok := s.rateLimit["fresh-ip"]; !ok {
		t.Error("fresh-ip should have survived cleanup")
	}

	if _, ok := s.userRateLimits["stale-user"]; ok {
		t.Error("stale-user should have been cleaned up")
	}
	if _, ok := s.userRateLimits["blocked-stale-user"]; ok {
		t.Error("blocked-stale-user should have been cleaned up")
	}
	if _, ok := s.userRateLimits["fresh-user"]; !ok {
		t.Error("fresh-user should have survived cleanup")
	}
}

func TestUserRateLimitBlocksAfterFailures(t *testing.T) {
	s := testAuditServer()
	defer s.stopAudit()

	// Record failures from different IPs but the same username.
	for i := 0; i < rateLimitMaxFails; i++ {
		ip := fmt.Sprintf("10.0.0.%d", i+1)
		blocked := s.recordAuthFailure(ip, "victim")
		if i == rateLimitMaxFails-1 && !blocked {
			t.Fatal("expected username to be blocked after max fails")
		}
	}

	// Username should be blocked.
	if !s.checkRateLimit("", "victim") {
		t.Fatal("expected username 'victim' to be blocked")
	}

	// A different username should NOT be blocked.
	if s.checkRateLimit("", "other") {
		t.Fatal("expected username 'other' to NOT be blocked")
	}

	// The IPs individually should NOT be blocked (only 1 fail each).
	for i := 0; i < rateLimitMaxFails; i++ {
		ip := fmt.Sprintf("10.0.0.%d", i+1)
		if s.checkRateLimit(ip, "") {
			t.Fatalf("expected IP %s to NOT be blocked (only 1 fail)", ip)
		}
	}
}

func TestUserRateLimitExpiry(t *testing.T) {
	s := testAuditServer()
	defer s.stopAudit()

	// Trigger username block.
	for i := 0; i < rateLimitMaxFails; i++ {
		s.recordAuthFailure("10.0.0.1", "testuser")
	}

	if !s.checkRateLimit("", "testuser") {
		t.Fatal("expected username to be blocked")
	}

	// Expire the block.
	s.rlMu.Lock()
	entry := s.userRateLimits["testuser"]
	entry.blockedAt = time.Now().Add(-rateLimitBlockTime - time.Second)
	s.rlMu.Unlock()

	if s.checkRateLimit("", "testuser") {
		t.Fatal("expected username to be unblocked after expiry")
	}
}

func TestRateLimitBlockedByEitherIP(t *testing.T) {
	s := testAuditServer()
	defer s.stopAudit()

	// Block only by IP.
	for i := 0; i < rateLimitMaxFails; i++ {
		s.recordAuthFailure("10.0.0.1", "")
	}

	// checkRateLimit with both should return true (IP blocked).
	if !s.checkRateLimit("10.0.0.1", "someuser") {
		t.Fatal("expected blocked because IP is blocked")
	}

	// Block only by username.
	s2 := testAuditServer()
	defer s2.stopAudit()

	for i := 0; i < rateLimitMaxFails; i++ {
		s2.recordAuthFailure(fmt.Sprintf("192.168.1.%d", i+1), "victim")
	}

	// checkRateLimit with both should return true (username blocked).
	if !s2.checkRateLimit("192.168.1.1", "victim") {
		t.Fatal("expected blocked because username is blocked")
	}
}

func TestAuditRecordedOnReload(t *testing.T) {
	s := testAuditServer()
	defer s.stopAudit()

	s.reloadFn = func() error { return nil }

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/reload", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	// Check audit log.
	rec = httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/audit", nil))

	var entries []AuditEntry
	json.Unmarshal(rec.Body.Bytes(), &entries)
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}
	if entries[0].Action != "config.reload" {
		t.Errorf("action = %q, want config.reload", entries[0].Action)
	}
	if !entries[0].Success {
		t.Error("expected success = true")
	}
	if entries[0].IP != "10.0.0.1" {
		t.Errorf("ip = %q, want 10.0.0.1", entries[0].IP)
	}
}

func TestAuditRecordedOnDomainCRUD(t *testing.T) {
	s := testAuditServer()
	defer s.stopAudit()

	// Create domain
	body := `{"host":"test.com","type":"static"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/domains", strings.NewReader(body))
	req.RemoteAddr = "10.0.0.1:1234"
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201", rec.Code)
	}

	// Update domain
	body = `{"host":"test.com","type":"proxy"}`
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("PUT", "/api/v1/domains/test.com", strings.NewReader(body))
	req.RemoteAddr = "10.0.0.2:1234"
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d, want 200", rec.Code)
	}

	// Delete domain
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("DELETE", "/api/v1/domains/test.com", nil)
	req.RemoteAddr = "10.0.0.3:1234"
	s.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: status = %d, want 200", rec.Code)
	}

	// Check audit log has 3 entries.
	rec = httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/audit", nil))

	var entries []AuditEntry
	json.Unmarshal(rec.Body.Bytes(), &entries)
	if len(entries) != 3 {
		t.Fatalf("audit entries = %d, want 3", len(entries))
	}
	if entries[0].Action != "domain.create" {
		t.Errorf("entries[0].Action = %q, want domain.create", entries[0].Action)
	}
	if entries[1].Action != "domain.update" {
		t.Errorf("entries[1].Action = %q, want domain.update", entries[1].Action)
	}
	if entries[2].Action != "domain.delete" {
		t.Errorf("entries[2].Action = %q, want domain.delete", entries[2].Action)
	}
}

func TestRequestIP(t *testing.T) {
	tests := []struct {
		remoteAddr string
		want       string
	}{
		{"192.168.1.1:12345", "192.168.1.1"},
		{"10.0.0.1:80", "10.0.0.1"},
		{"[::1]:8080", "::1"},
		{"invalid", "invalid"},
	}

	for _, tt := range tests {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = tt.remoteAddr
		got := requestIP(req)
		if got != tt.want {
			t.Errorf("requestIP(%q) = %q, want %q", tt.remoteAddr, got, tt.want)
		}
	}
}
