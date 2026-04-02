package admin

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

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

	var tasks []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &tasks); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if tasks == nil {
		t.Error("expected empty array, got nil")
	}
}

// TestSettingsPutInvalidJSON2 tests settings with invalid JSON.
func TestSettingsPutInvalidJSON2(t *testing.T) {
	s := testServer()

	body := strings.NewReader(`{not valid`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/settings", body))

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
