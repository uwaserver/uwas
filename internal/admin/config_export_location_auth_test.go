package admin

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// Config export redacts per-location basic-auth users. LocationConfig.BasicAuth
// is a pointer shared with the live config, so redacting it in place wiped the
// live users (enforceBasicAuth then let every request through), and locations
// without basic_auth made the export dereference nil.
func TestConfigExportKeepsLiveLocationBasicAuth(t *testing.T) {
	s := testServer()
	s.config.Domains[0].Locations = []config.LocationConfig{
		{Match: "/static", CacheControl: "public"},
		{Match: "/private", BasicAuth: &config.BasicAuthConfig{Enabled: true, Users: map[string]string{"bob": "s3cret-loc"}}},
	}

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/config/export", nil))
		if rec.Code != 200 {
			t.Fatalf("export #%d status = %d, want 200", i+1, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "s3cret-loc") {
			t.Fatalf("export #%d leaked a location basic-auth password", i+1)
		}
		if !strings.Contains(rec.Body.String(), "/private") {
			t.Fatalf("export #%d dropped the location", i+1)
		}
	}

	ba := s.config.Domains[0].Locations[1].BasicAuth
	if ba == nil || len(ba.Users) != 1 || ba.Users["bob"] != "s3cret-loc" {
		t.Fatalf("live location basic auth changed by export: %+v", ba)
	}
	if s.config.Domains[0].Locations[0].BasicAuth != nil {
		t.Fatal("export gave a location without basic_auth a basic_auth block")
	}
}
