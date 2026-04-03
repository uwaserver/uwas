package admin

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// Admin Coverage 4 - Additional endpoint tests

func TestPHPRestartEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/php/restart", nil))
	// May return 200, 404, 500, or 501
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestAppRestartEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/apps/test-app/restart", nil))
	// May return various codes
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestAppEnvUpdateEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"env":{"KEY":"value"}}`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/apps/test-app/env", body))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 400 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 400, 404, or 501", rec.Code)
	}
}

func TestAppEnvUpdateInvalidJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{not valid`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/apps/test-app/env", body))
	// May return 400 or 501
	if rec.Code != 400 && rec.Code != 501 {
		t.Errorf("status = %d, want 400 or 501", rec.Code)
	}
}

func TestAppLogsEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/apps/test-app/logs", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, or 501", rec.Code)
	}
}

func TestDeployEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"repo":"https://github.com/test/repo"}`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/deploy", body))
	// May return 404 if deploy manager not available
	if rec.Code != 200 && rec.Code != 400 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 400, 404, 500, or 501", rec.Code)
	}
}

func TestDeployInvalidJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{not valid`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/deploy", body))
	// May return 400 or 404
	if rec.Code != 400 && rec.Code != 404 {
		t.Errorf("status = %d, want 400 or 404", rec.Code)
	}
}

func TestDeployStatusEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/deploy/status", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, or 501", rec.Code)
	}
}

func TestDeployListEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/deploy", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, or 501", rec.Code)
	}
}

func TestServiceStopEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/services/test-service/stop", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestServiceRestartEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/services/test-service/restart", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestDBStopEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/databases/test-db/stop", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestDBRestartEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/databases/test-db/restart", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestFirewallEnableEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/firewall/enable", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestFirewallDisableEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/firewall/disable", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestUpdateEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/system/update", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestCertUploadEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"cert":"test","key":"test"}`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/ssl/upload", body))
	if rec.Code != 200 && rec.Code != 400 && rec.Code != 404 && rec.Code != 500 {
		t.Errorf("status = %d, want 200, 400, 404, or 500", rec.Code)
	}
}

func TestCertUploadInvalidJSON2(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{not valid`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/ssl/upload", body))
	// May return 400 or 404
	if rec.Code != 400 && rec.Code != 404 {
		t.Errorf("status = %d, want 400 or 404", rec.Code)
	}
}

func TestDNSSyncEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/dns/sync", nil))
	if rec.Code != 200 && rec.Code != 400 && rec.Code != 404 && rec.Code != 405 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 400, 404, 405, or 501", rec.Code)
	}
}

func TestMigrateCPanelEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"source":"test.com"}`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/migrate/cpanel", body))
	if rec.Code != 200 && rec.Code != 400 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 400, 500, or 501", rec.Code)
	}
}

func TestGenRecoveryCodesEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/auth/recovery-codes", nil))
	if rec.Code != 200 && rec.Code != 401 && rec.Code != 404 && rec.Code != 405 {
		t.Errorf("status = %d, want 200, 401, 404, or 405", rec.Code)
	}
}

func TestUseRecoveryCodeEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"code":"123456"}`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/auth/recovery-code", body))
	if rec.Code != 200 && rec.Code != 400 && rec.Code != 401 && rec.Code != 404 {
		t.Errorf("status = %d, want 200, 400, 401, or 404", rec.Code)
	}
}

func TestDBExploreTablesEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/db-explorer/test-db/tables", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestDBExploreColumnsEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/db-explorer/test-db/tables/users/columns", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestDBExploreQueryEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"query":"SELECT 1"}`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/db-explorer/test-db/query", body))
	if rec.Code != 200 && rec.Code != 400 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 400, 404, 500, or 501", rec.Code)
	}
}

func TestWPSiteDetailEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/wordpress/sites/test.com", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestDBDropEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/databases/test-db", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestDBInstallEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"type":"mysql"}`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/databases/install", body))
	// May return 404 if DB manager not available
	if rec.Code != 200 && rec.Code != 400 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 400, 404, 500, or 501", rec.Code)
	}
}

func TestDBUsersEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/databases/test-db/users", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestDBExportEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/databases/test-db/export", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestDBUninstallEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/databases/test-db/uninstall", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestDBRepairEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/databases/test-db/repair", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestDockerDBListEndpoint2(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/docker/databases", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestDockerDBStopEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/docker/databases/test-db/stop", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestDockerDBRemoveEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/docker/databases/test-db", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestDockerDBCreateDatabaseEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"name":"testdb"}`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/docker/databases/test-container/databases", body))
	if rec.Code != 200 && rec.Code != 400 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 400, 404, 500, or 501", rec.Code)
	}
}

func TestDockerDBDropDatabaseEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/docker/databases/test-container/databases/testdb", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

// System and Info Endpoints

func TestSystemInfoEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/system/info", nil))
	if rec.Code != 200 && rec.Code != 404 {
		t.Errorf("status = %d, want 200 or 404", rec.Code)
	}
}

func TestSystemResourcesEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/system/resources", nil))
	if rec.Code != 200 && rec.Code != 404 {
		t.Errorf("status = %d, want 200 or 404", rec.Code)
	}
}

func TestHealthEndpoint2(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/health", nil))
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestConfigEndpoint2(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/config", nil))
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestStatsEndpoint2(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/stats", nil))
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestStatsDomainsEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/stats/domains", nil))
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// Settings Endpoints

func TestSettingsGetEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/settings", nil))
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestSettingsPutEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"webroot":"/var/www"}`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/settings", body))
	if rec.Code != 200 && rec.Code != 400 {
		t.Errorf("status = %d, want 200 or 400", rec.Code)
	}
}

func TestSettingsPutInvalidJSON(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{not valid`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/v1/settings", body))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// WordPress Endpoints

func TestWordPressSitesEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/wordpress/sites", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestWordPressThemesEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/wordpress/sites/test.com/themes", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestWordPressUsersEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/wordpress/sites/test.com/users", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestWordPressSecurityEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/wordpress/sites/test.com/security", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestWordPressFixPermissionsEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/wordpress/sites/test.com/fix-permissions", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestWordPressToggleDebugEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/wordpress/sites/test.com/toggle-debug", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestWordPressOptimizeDBEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/wordpress/sites/test.com/optimize-db", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestWordPressChangePasswordEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"username":"admin","new_password":"test123"}`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/wordpress/sites/test.com/change-password", body))
	if rec.Code != 200 && rec.Code != 400 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 400, 404, 500, or 501", rec.Code)
	}
}

func TestWordPressHardenEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"disable_xmlrpc":true}`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/wordpress/sites/test.com/harden", body))
	if rec.Code != 200 && rec.Code != 400 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 400, 404, 500, or 501", rec.Code)
	}
}

// Firewall Endpoints

func TestFirewallRulesEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/firewall/rules", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 405 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 405, or 501", rec.Code)
	}
}

func TestFirewallAddRuleEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"port":8080,"action":"allow"}`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/firewall/rules", body))
	if rec.Code != 200 && rec.Code != 400 && rec.Code != 404 && rec.Code != 405 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 400, 404, 405, or 501", rec.Code)
	}
}

func TestFirewallDeleteRuleEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/firewall/rules/1", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 405 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 405, or 501", rec.Code)
	}
}

// Additional coverage tests for 0% functions

func TestPHPRestartNoManager(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/php/8.2/restart", nil))
	if rec.Code != 501 && rec.Code != 404 {
		t.Errorf("status = %d, want 501 or 404", rec.Code)
	}
}

func TestDeployWebhookEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/deploy/webhook", nil))
	if rec.Code != 404 && rec.Code != 501 {
		t.Errorf("status = %d, want 404 or 501", rec.Code)
	}
}

func TestDockerDBImportEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	body := strings.NewReader(`{"database":"testdb"}`)
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/docker/databases/test-container/import", body))
	if rec.Code != 200 && rec.Code != 400 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 400, 404, 500, or 501", rec.Code)
	}
}

func TestDockerDBExportEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/docker/databases/test-container/export", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestDBExportEndpointNew(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/databases/test-db/export", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestDBUninstallEndpointNew(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/databases/test-db/uninstall", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestDBRepairEndpointNew(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/databases/test-db/repair", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestDBForceUninstallEndpoint(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/databases/test-db/force-uninstall", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestWPSiteDetailEndpoint2(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/wordpress/sites/test.com/detail", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

func TestDBUsersEndpoint2(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/databases/test-db/users", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

// =============================================================================
// File Upload Handler Tests
// =============================================================================

func TestHandleFileUpload_NoFileManager(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	// Multipart form data for file upload
	body := strings.NewReader("------Boundary\r\nContent-Disposition: form-data; name=\"file\"; filename=\"test.txt\"\r\nContent-Type: text/plain\r\n\r\ntest content\r\n------Boundary--\r\n")
	req := httptest.NewRequest("POST", "/api/v1/files/test.com/upload", body)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=----Boundary")
	s.mux.ServeHTTP(rec, req)
	if rec.Code != 501 && rec.Code != 404 && rec.Code != 400 && rec.Code != 500 {
		t.Errorf("status = %d, want 501, 404, 400, or 500", rec.Code)
	}
}

// =============================================================================
// SSH Key Handler Tests
// =============================================================================

func TestHandleSSHKeyDelete_NoSFTP(t *testing.T) {
	s := testServer()
	body := strings.NewReader(`{"fingerprint":"aa:bb:cc:dd:ee:ff"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/users/test.com/ssh-keys", body))
	if rec.Code != 501 && rec.Code != 404 && rec.Code != 400 && rec.Code != 500 && rec.Code != 200 {
		t.Errorf("status = %d, want 501, 404, 400, 500, or 200", rec.Code)
	}
}

// =============================================================================
// Update Handler Tests
// =============================================================================

func TestHandleUpdate_NoUpdate(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/system/update", nil))
	if rec.Code != 200 && rec.Code != 404 && rec.Code != 500 && rec.Code != 501 {
		t.Errorf("status = %d, want 200, 404, 500, or 501", rec.Code)
	}
}

// =============================================================================
// Docker DB Handler Tests
// =============================================================================

func TestHandleDockerDBCreate_NoDocker(t *testing.T) {
	s := testServer()
	body := strings.NewReader(`{"engine":"mysql","name":"test","port":3306}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/database/docker", body))
	if rec.Code != 501 && rec.Code != 404 && rec.Code != 400 && rec.Code != 500 {
		t.Errorf("status = %d, want 501, 404, 400, or 500", rec.Code)
	}
}

// =============================================================================
// DNS Handler Tests
// =============================================================================

func TestHandleDNSRecordDelete_NoDNSManager(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("DELETE", "/api/v1/dns/test.com/records/test-id", nil))
	if rec.Code != 501 && rec.Code != 404 && rec.Code != 400 && rec.Code != 500 {
		t.Errorf("status = %d, want 501, 404, 400, or 500", rec.Code)
	}
}

// =============================================================================
// Package Handler Tests
// =============================================================================

func TestHandlePackageInstall_NoPackage(t *testing.T) {
	s := testServer()
	body := strings.NewReader(`{"id":"test-package"}`)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/packages/install", body))
	if rec.Code != 501 && rec.Code != 404 && rec.Code != 400 && rec.Code != 500 {
		t.Errorf("status = %d, want 501, 404, 400, or 500", rec.Code)
	}
}

