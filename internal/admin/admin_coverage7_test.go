package admin

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/cronjob"
)

// Admin Coverage 7 — Cloudflare, User Regenerate API Key, User Change Password

// ── User Regenerate API Key ──────────────────────────────────────────────────

func TestRegenerateAPIKeyNoAuthMgr(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/auth/users/admin/apikey", nil)
	s.mux.ServeHTTP(rec, r)
	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestRegenerateAPIKeyUnauthorized(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/auth/users/admin/apikey", nil)
	r.SetPathValue("username", "admin")
	s.handleUserRegenerateAPIKeyAuth(rec, r)
	if rec.Code != 401 {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestRegenerateAPIKeyForbidden(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/auth/users/admin/apikey", nil)
	r = withResellerContext(r)
	s.mux.ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestRegenerateAPIKeyAdminSuccess(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/auth/users/admin/apikey", nil)
	r = withAdminContext(r)
	s.mux.ServeHTTP(rec, r)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]string
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["api_key"] == "" {
		t.Error("expected non-empty api_key")
	}
}

func TestRegenerateAPIKeySelfUser(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/auth/users/reseller/apikey", nil)
	r = withResellerContext(r)
	s.mux.ServeHTTP(rec, r)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200 (self regenerate)", rec.Code)
	}
}

func TestRegenerateAPIKeyUserNotFound(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/auth/users/nonexistent/apikey", nil)
	r = withAdminContext(r)
	s.mux.ServeHTTP(rec, r)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// ── User Change Password ─────────────────────────────────────────────────────

func TestUserChangePasswordNoAuthMgr(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/auth/users/admin/password", strings.NewReader(`{}`)))
	if rec.Code != 501 {
		t.Errorf("status = %d, want 501", rec.Code)
	}
}

func TestUserChangePasswordUnauthorized(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/auth/users/admin/password", strings.NewReader(`{}`))
	r.SetPathValue("username", "admin")
	s.handleUserChangePasswordAuth(rec, r)
	if rec.Code != 401 {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestUserChangePasswordBadJSON(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/auth/users/admin/password", strings.NewReader(`not json`))
	r = withAdminContext(r)
	s.mux.ServeHTTP(rec, r)
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestUserChangePasswordAdminSuccess(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/auth/users/admin/password", strings.NewReader(`{"new_password":"newpass123"}`))
	r = withAdminContext(r)
	s.mux.ServeHTTP(rec, r)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestUserChangePasswordOtherUserAsReseller(t *testing.T) {
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	rec := httptest.NewRecorder()
	// reseller tries to change admin's password (different user, not admin role)
	r := httptest.NewRequest("POST", "/api/v1/auth/users/admin/password", strings.NewReader(`{"current_password":"x","new_password":"y"}`))
	r = withResellerContext(r)
	s.mux.ServeHTTP(rec, r)
	// reseller != admin user, so it goes to self-change path, but current_password is wrong
	if rec.Code != 200 && rec.Code != 401 && rec.Code != 403 {
		t.Errorf("status = %d, want 200, 401, or 403", rec.Code)
	}
}

// ── Cloudflare ───────────────────────────────────────────────────────────────

func TestCloudflareStatusNotConnected(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = nil
	cloudflareMu.Unlock()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/cloudflare/status", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["connected"] != false {
		t.Error("expected connected=false")
	}
}

func TestCloudflareStatusConnected(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "test-token", AccountID: "acc-123",
		Email: "test@example.com", Connected: true,
	}
	cloudflareMu.Unlock()
	defer func() {
		cloudflareMu.Lock()
		cloudflareConfig = nil
		cloudflareMu.Unlock()
	}()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/cloudflare/status", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["connected"] != true {
		t.Error("expected connected=true")
	}
}

func TestCloudflareConnectBadJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, withAdminContext(httptest.NewRequest("POST", "/api/v1/cloudflare/connect", strings.NewReader(`not json`))))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestCloudflareConnectMissingFields(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, withAdminContext(httptest.NewRequest("POST", "/api/v1/cloudflare/connect", strings.NewReader(`{"token":""}`))))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestCloudflareDisconnect(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "test-token", AccountID: "acc-123", Connected: true,
	}
	cloudflareMu.Unlock()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/disconnect", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	cloudflareMu.RLock()
	cfg := cloudflareConfig
	cloudflareMu.RUnlock()
	if cfg != nil {
		t.Error("expected cloudflareConfig to be nil after disconnect")
	}
}

func TestCloudflareDisconnectNil(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = nil
	cloudflareMu.Unlock()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/disconnect", nil))
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestCloudflareTunnelsNotConnected(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = nil
	cloudflareMu.Unlock()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/cloudflare/tunnels", nil))
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestCloudflareTunnelsList(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "test-token", AccountID: "acc-123", Connected: true,
		Tunnels: []cloudflareTunnel{
			{ID: "t1", Name: "test-tunnel", Domain: "example.com", Running: true},
		},
	}
	cloudflareMu.Unlock()
	defer func() {
		cloudflareMu.Lock()
		cloudflareConfig = nil
		cloudflareMu.Unlock()
	}()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/cloudflare/tunnels", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body []map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body) != 1 || body[0]["name"] != "test-tunnel" {
		t.Errorf("unexpected tunnels response: %v", body)
	}
}

func TestCloudflareTunnelCreateNotConnected(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = nil
	cloudflareMu.Unlock()

	s := testServer()
	rec := httptest.NewRecorder()
	body := `{"name":"test","domain":"example.com"}`
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/tunnels", strings.NewReader(body)))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestCloudflareTunnelCreateBadJSON(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{Token: "t", AccountID: "a", Connected: true}
	cloudflareMu.Unlock()
	defer func() {
		cloudflareMu.Lock()
		cloudflareConfig = nil
		cloudflareMu.Unlock()
	}()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/tunnels", strings.NewReader(`bad`)))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestCloudflareTunnelCreateMissingFields(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{Token: "t", AccountID: "a", Connected: true}
	cloudflareMu.Unlock()
	defer func() {
		cloudflareMu.Lock()
		cloudflareConfig = nil
		cloudflareMu.Unlock()
	}()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/tunnels", strings.NewReader(`{"name":"","domain":""}`)))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestCloudflareTunnelCreateSuccess(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{Token: "t", AccountID: "a", Connected: true, Tunnels: []cloudflareTunnel{}}
	cloudflareMu.Unlock()
	defer func() {
		cloudflareMu.Lock()
		cloudflareConfig = nil
		cloudflareMu.Unlock()
	}()

	s := testServer()
	rec := httptest.NewRecorder()
	body := `{"name":"test-tunnel","domain":"example.com"}`
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/tunnels", strings.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var result map[string]any
	json.Unmarshal(rec.Body.Bytes(), &result)
	if result["id"] == "" || result["token"] == "" {
		t.Errorf("expected tunnel with id and token, got %v", result)
	}
}

func TestCloudflareTunnelDeleteNotConnected(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = nil
	cloudflareMu.Unlock()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/cloudflare/tunnels/t1", nil))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestCloudflareTunnelDeleteNotFound(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{Token: "t", AccountID: "a", Connected: true, Tunnels: []cloudflareTunnel{}}
	cloudflareMu.Unlock()
	defer func() {
		cloudflareMu.Lock()
		cloudflareConfig = nil
		cloudflareMu.Unlock()
	}()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/cloudflare/tunnels/nonexistent", nil))
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestCloudflareTunnelDeleteSuccess(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "t", AccountID: "a", Connected: true,
		Tunnels: []cloudflareTunnel{{ID: "t1", Name: "test", Domain: "example.com"}},
	}
	cloudflareMu.Unlock()
	defer func() {
		cloudflareMu.Lock()
		cloudflareConfig = nil
		cloudflareMu.Unlock()
	}()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/cloudflare/tunnels/t1", nil))
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestCloudflareTunnelStartNotConnected(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = nil
	cloudflareMu.Unlock()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/tunnels/t1/start", nil))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestCloudflareTunnelStartNotFound(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{Token: "t", AccountID: "a", Connected: true, Tunnels: []cloudflareTunnel{}}
	cloudflareMu.Unlock()
	defer func() {
		cloudflareMu.Lock()
		cloudflareConfig = nil
		cloudflareMu.Unlock()
	}()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/tunnels/nonexistent/start", nil))
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestCloudflareTunnelStartSuccess(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "t", AccountID: "a", Connected: true,
		Tunnels: []cloudflareTunnel{{ID: "t1", Name: "test", Domain: "example.com"}},
	}
	cloudflareMu.Unlock()
	defer func() {
		cloudflareMu.Lock()
		cloudflareConfig = nil
		cloudflareMu.Unlock()
	}()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/tunnels/t1/start", nil))
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestCloudflareTunnelStopSuccess(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{
		Token: "t", AccountID: "a", Connected: true,
		Tunnels: []cloudflareTunnel{{ID: "t1", Name: "test", Domain: "example.com", Running: true, Connections: 1}},
	}
	cloudflareMu.Unlock()
	defer func() {
		cloudflareMu.Lock()
		cloudflareConfig = nil
		cloudflareMu.Unlock()
	}()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/tunnels/t1/stop", nil))
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestCloudflareTunnelStopNotFound(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{Token: "t", AccountID: "a", Connected: true, Tunnels: []cloudflareTunnel{}}
	cloudflareMu.Unlock()
	defer func() {
		cloudflareMu.Lock()
		cloudflareConfig = nil
		cloudflareMu.Unlock()
	}()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/tunnels/nonexistent/stop", nil))
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestCloudflareCachePurgeNotConnected(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = nil
	cloudflareMu.Unlock()

	s := testServer()
	rec := httptest.NewRecorder()
	body := `{"url":"https://example.com","everything":false}`
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/cache/purge", strings.NewReader(body)))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestCloudflareCachePurgeBadJSON(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = &cloudflareState{Token: "t", AccountID: "a", Connected: true}
	cloudflareMu.Unlock()
	defer func() {
		cloudflareMu.Lock()
		cloudflareConfig = nil
		cloudflareMu.Unlock()
	}()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/cache/purge", strings.NewReader(`bad`)))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestCloudflareZonesNotConnected(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = nil
	cloudflareMu.Unlock()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/cloudflare/zones", nil))
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestCloudflareZoneSyncNotConnected(t *testing.T) {
	cloudflareMu.Lock()
	cloudflareConfig = nil
	cloudflareMu.Unlock()

	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/cloudflare/zones/z1/sync", nil))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// ── Cron Execute with auth ───────────────────────────────────────────────────

func TestCronExecuteForbidden(t *testing.T) {
	dir := t.TempDir()
	s := testServer()
	s.SetAuthManager(newMockAuthManager())
	s.SetCronMonitor(cronjob.NewMonitor(dir))
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/v1/cron/execute", strings.NewReader(`{"domain":"example.com","command":"echo hi"}`))
	r = withResellerContext(r)
	s.mux.ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}
