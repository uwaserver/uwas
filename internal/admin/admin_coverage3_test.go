package admin

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
)

func TestIsExpensiveGET(t *testing.T) {
	cases := map[string]bool{
		"/api/v1/database/foo/export":            true,
		"/api/v1/config/export":                  true,
		"/api/v1/backups/abc/download":           true,
		"/api/v1/domains":                        false,
		"/api/v1/health":                         false,
		"/api/v1/database/foo/exporter":          false, // suffix match, "exporter" must not match
	}
	for path, want := range cases {
		if got := isExpensiveGET(path); got != want {
			t.Errorf("isExpensiveGET(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestIsLoopbackListenAddr(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:9443":    true,
		"localhost:9443":    true,
		"[::1]:9443":        true,
		"0.0.0.0:9443":      false,
		":9443":             false,
		"192.168.1.10:9443": false,
		"example.com:443":   false,
		"not a valid addr":  false,
	}
	for addr, want := range cases {
		if got := isLoopbackListenAddr(addr); got != want {
			t.Errorf("isLoopbackListenAddr(%q) = %v, want %v", addr, got, want)
		}
	}
}

// TestSensitiveSettingsEndpointsRequireAdmin locks in the requireAdmin guards
// on the notify/branding endpoints — without them a low-priv user could
// redirect alerts to an attacker-controlled webhook or inject branding HTML.
func TestSensitiveSettingsEndpointsRequireAdmin(t *testing.T) {
	cases := []struct {
		name, path, body string
	}{
		{"notify_prefs", "/api/v1/settings/notifications", `{"alerting":{},"webhooks":[]}`},
		{"branding", "/api/v1/settings/branding", `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := testServer()
			req := httptest.NewRequest("PUT", tc.path, strings.NewReader(tc.body))
			req = req.WithContext(auth.WithUser(req.Context(), &auth.User{
				ID: "u1", Username: "regular", Role: auth.RoleUser, Enabled: true,
			}))
			rec := httptest.NewRecorder()
			// Dispatch via the inner mux directly to skip testMux's auto-admin shim.
			s.mux.(*testMux).mux.ServeHTTP(rec, req)
			if rec.Code != 403 {
				t.Errorf("non-admin write to %s returned %d, want 403", tc.path, rec.Code)
			}
		})
	}
}

// TestGetDomainNotFound2 tests getting a non-existent domain.
func TestGetDomainNotFound2(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/domains/nonexistent.com", nil))

	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// TestPHPConfigGet2 tests getting PHP config.
func TestPHPConfigGet2(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/php/config", nil))

	// May return 200, 404, or 503 depending on PHP manager
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 503 {
		t.Errorf("status = %d, want 200, 404, or 503", rec.Code)
	}
}

// TestPHPExtensions2 tests getting PHP extensions.
func TestPHPExtensions2(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/php/extensions", nil))

	// May return 200, 404, or 503 depending on PHP manager
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 503 {
		t.Errorf("status = %d, want 200, 404, or 503", rec.Code)
	}
}

// TestTaskListEmpty2 tests getting empty task list.
func TestTaskListEmpty2(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/tasks", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}

	var resp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	tasks := resp.Items
	if tasks == nil {
		t.Error("expected empty array, got nil")
	}
}

// TestSettingsPutInvalidJSON2 tests settings with invalid JSON.
func TestSettingsPutInvalidJSON2(t *testing.T) {
	s := testServer()

	body := strings.NewReader(`{not valid`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/v1/settings", body)
	s.mux.ServeHTTP(rec, withAdminContext(req))

	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// TestAnalyticsEndpoint2 tests the analytics endpoint.
func TestAnalyticsEndpoint2(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/analytics", nil))

	// May return 200 or 404 depending on analytics configuration
	if rec.Code != 200 && rec.Code != 404 {
		t.Errorf("status = %d, want 200 or 404", rec.Code)
	}
}

// TestCacheStatsEndpoint2 tests the cache stats endpoint.
func TestCacheStatsEndpoint2(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/cache/stats", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestBackupListEndpoint2 tests the backup list endpoint.
func TestBackupListEndpoint2(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/backups", nil))

	// May return 200, 404, or 501 depending on backup configuration
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, or 501", rec.Code)
	}
}

// TestCronJobsListEndpoint2 tests the cron jobs list endpoint.
func TestCronJobsListEndpoint2(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/cronjobs", nil))

	// May return 200 or 404 depending on configuration
	if rec.Code != 200 && rec.Code != 404 {
		t.Errorf("status = %d, want 200 or 404", rec.Code)
	}
}

// TestFirewallRulesEndpoint2 tests the firewall rules endpoint.
func TestFirewallRulesEndpoint2(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/firewall/rules", nil))

	// May return 200, 404, or 405 depending on firewall configuration
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 405 {
		t.Errorf("status = %d, want 200, 404, or 405", rec.Code)
	}
}

// TestSSLCertsEndpoint2 tests the SSL certificates endpoint.
func TestSSLCertsEndpoint2(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/ssl/certs", nil))

	// May return 200 or 404 depending on TLS configuration
	if rec.Code != 200 && rec.Code != 404 {
		t.Errorf("status = %d, want 200 or 404", rec.Code)
	}
}

// TestWebhooksListEndpoint2 tests the webhooks list endpoint.
func TestWebhooksListEndpoint2(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/webhooks", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestUsersListEndpoint2 tests the users list endpoint.
func TestUsersListEndpoint2(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/users", nil))

	// May return 200 or 403 depending on auth
	if rec.Code != 200 && rec.Code != 403 {
		t.Errorf("status = %d, want 200 or 403", rec.Code)
	}
}

// TestSystemInfoEndpoint2 tests the system info endpoint.
func TestSystemInfoEndpoint2(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/system/info", nil))

	// May return 200 or 404 depending on configuration
	if rec.Code != 200 && rec.Code != 404 {
		t.Errorf("status = %d, want 200 or 404", rec.Code)
	}
}

// TestSystemResourcesEndpoint2 tests the system resources endpoint.
func TestSystemResourcesEndpoint2(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/system/resources", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestPHPDomainConfigGet2 tests getting PHP domain config.
func TestPHPDomainConfigGet2(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/php/domains/example.com/config", nil))

	// May return 200, 404, or 501
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, or 501", rec.Code)
	}
}

// TestAppListEndpoint2 tests the apps list endpoint.
func TestAppListEndpoint2(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/apps", nil))

	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestDatabaseListEndpoint2 tests the database list endpoint.
func TestDatabaseListEndpoint2(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/databases", nil))

	// May return 200 or 404 depending on database configuration
	if rec.Code != 200 && rec.Code != 404 {
		t.Errorf("status = %d, want 200 or 404", rec.Code)
	}
}

// TestSFTPUsersListEndpoint2 tests the SFTP users list endpoint.
func TestSFTPUsersListEndpoint2(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/sftp/users", nil))

	// May return 200 or 404 depending on SFTP configuration
	if rec.Code != 200 && rec.Code != 404 {
		t.Errorf("status = %d, want 200 or 404", rec.Code)
	}
}

// TestWordPressListEndpoint2 tests the WordPress list endpoint.
func TestWordPressListEndpoint2(t *testing.T) {
	s := testServer()

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/wordpress", nil))

	// May return 200 or 404 depending on WordPress configuration
	if rec.Code != 200 && rec.Code != 404 {
		t.Errorf("status = %d, want 200 or 404", rec.Code)
	}
}

// =============================================================================
// App Handler Tests (handlers_app.go)
// =============================================================================

func TestHandleAppRestart_NoAppManager(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/apps/test.com/restart", nil))
	if rec.Code != 501 && rec.Code != 404 && rec.Code != 500 {
		t.Errorf("status = %d, want 501, 404, or 500", rec.Code)
	}
}

func TestHandleAppEnvUpdate_InvalidJSON(t *testing.T) {
	s := testServer()
	body := strings.NewReader(`{invalid json}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/apps/test.com/env", body))
	if rec.Code != 400 && rec.Code != 501 && rec.Code != 500 {
		t.Errorf("status = %d, want 400, 501, or 500", rec.Code)
	}
}

func TestHandleAppEnvUpdate_NoAppManager(t *testing.T) {
	s := testServer()
	body := strings.NewReader(`{"env":{"KEY":"value"}}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/apps/test.com/env", body))
	if rec.Code != 501 && rec.Code != 404 && rec.Code != 500 {
		t.Errorf("status = %d, want 501, 404, or 500", rec.Code)
	}
}

func TestHandleAppLogs_NoAppManager(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/apps/test.com/logs", nil))
	if rec.Code != 501 && rec.Code != 404 && rec.Code != 500 {
		t.Errorf("status = %d, want 501, 404, or 500", rec.Code)
	}
}

func TestHandleDeployWebhook_InvalidJSON(t *testing.T) {
	s := testServer()
	body := strings.NewReader(`{invalid json}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/apps/test.com/webhook", body))
	if rec.Code != 400 && rec.Code != 501 && rec.Code != 500 {
		t.Errorf("status = %d, want 400, 501, or 500", rec.Code)
	}
}

// =============================================================================
// WordPress Handler Tests (handlers_hosting.go)
// =============================================================================

func TestHandleWPSiteDetail_NoWordPress(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/wordpress/sites/test.com/detail", nil))
	if rec.Code != 404 && rec.Code != 500 {
		t.Errorf("status = %d, want 404 or 500", rec.Code)
	}
}

func TestHandleWPChangePassword_InvalidJSON(t *testing.T) {
	s := testServer()
	body := strings.NewReader(`{invalid json}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/wordpress/sites/test.com/change-password", body))
	if rec.Code != 400 && rec.Code != 500 && rec.Code != 404 {
		t.Errorf("status = %d, want 400, 500, or 404", rec.Code)
	}
}

func TestHandleWPChangePassword_MissingFields(t *testing.T) {
	s := testServer()
	body := strings.NewReader(`{"username":""}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/wordpress/sites/test.com/change-password", body))
	if rec.Code != 400 && rec.Code != 500 && rec.Code != 404 {
		t.Errorf("status = %d, want 400, 500, or 404", rec.Code)
	}
}

func TestHandleWPHarden_InvalidJSON(t *testing.T) {
	s := testServer()
	body := strings.NewReader(`{invalid json}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/wordpress/sites/test.com/harden", body))
	if rec.Code != 400 && rec.Code != 500 && rec.Code != 404 {
		t.Errorf("status = %d, want 400, 500, or 404", rec.Code)
	}
}

func TestHandleWPOptimizeDB_NoWordPress(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/wordpress/sites/test.com/optimize-db", nil))
	if rec.Code != 404 && rec.Code != 500 {
		t.Errorf("status = %d, want 404 or 500", rec.Code)
	}
}

// =============================================================================
// SSH Key Handler Tests (handlers_hosting.go)
// =============================================================================

func TestHandleSSHKeyAdd_InvalidJSON(t *testing.T) {
	s := testServer()
	body := strings.NewReader(`{invalid json}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/users/test.com/ssh-keys", body))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleSSHKeyAdd_NoSFTP(t *testing.T) {
	s := testServer()
	body := strings.NewReader(`{"public_key":"ssh-rsa AAAAB3NzaC1 test"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/users/test.com/ssh-keys", body))
	if rec.Code != 501 && rec.Code != 404 && rec.Code != 500 {
		t.Errorf("status = %d, want 501, 404, or 500", rec.Code)
	}
}

func TestHandleSSHKeyDelete_InvalidJSON(t *testing.T) {
	s := testServer()
	body := strings.NewReader(`{invalid json}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/users/test.com/ssh-keys", body))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// =============================================================================
// Database Handler Tests (handlers_hosting.go)
// =============================================================================

func TestHandleDBList_NoDBManager(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/database/list", nil))
	if rec.Code != 501 && rec.Code != 404 && rec.Code != 500 && rec.Code != 200 {
		t.Errorf("status = %d, want 501, 404, 500, or 200", rec.Code)
	}
}

func TestHandleDBExport_NoDBManager3(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/database/testdb/export", nil))
	if rec.Code != 501 && rec.Code != 404 && rec.Code != 500 {
		t.Errorf("status = %d, want 501, 404, or 500", rec.Code)
	}
}

// =============================================================================
// Notification Handler Tests (handlers_hosting.go)
// =============================================================================

func TestHandleNotifyTest_NoNotifier(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/notify/test", nil))
	if rec.Code != 501 && rec.Code != 404 && rec.Code != 500 && rec.Code != 200 && rec.Code != 400 {
		t.Errorf("status = %d, want 501, 404, 500, 200, or 400", rec.Code)
	}
}

// =============================================================================
// DNS Handler Tests (handlers_hosting.go)
// =============================================================================

func TestHandleDNSRecords_NoDNSManager(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/dns/test.com/records", nil))
	if rec.Code != 501 && rec.Code != 404 && rec.Code != 500 && rec.Code != 400 {
		t.Errorf("status = %d, want 501, 404, 500, or 400", rec.Code)
	}
}

func TestHandleDNSRecordCreate_InvalidJSON3(t *testing.T) {
	s := testServer()
	body := strings.NewReader(`{invalid json}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/dns/test.com/records", body))
	if rec.Code != 400 && rec.Code != 501 && rec.Code != 500 {
		t.Errorf("status = %d, want 400, 501, or 500", rec.Code)
	}
}

func TestHandleDNSRecordUpdate_InvalidJSON3(t *testing.T) {
	s := testServer()
	body := strings.NewReader(`{invalid json}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/dns/test.com/records/test-id", body))
	if rec.Code != 400 && rec.Code != 501 && rec.Code != 500 {
		t.Errorf("status = %d, want 400, 501, or 500", rec.Code)
	}
}

func TestHandleDNSSync_NoDNSManager(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/dns/test.com/sync", nil))
	if rec.Code != 501 && rec.Code != 404 && rec.Code != 500 && rec.Code != 400 {
		t.Errorf("status = %d, want 501, 404, 500, or 400", rec.Code)
	}
}
