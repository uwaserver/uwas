package settings

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// F64200/F64201: SettingsPut and BrandingPut changed the live config, then
// answered 500 on a persist failure without undoing it, so a rejected change
// (a new admin api_key included) stayed active until restart.
func TestSettingsAndBrandingRolledBackWhenPersistFails(t *testing.T) {
	t.Run("settings", func(t *testing.T) {
		d := &failPersistDeps{}
		d.cfg.Global.LogLevel = "info"
		d.cfg.Global.Admin.APIKey = "old-key-0123456789abcdef"
		rec := httptest.NewRecorder()
		New(d).SettingsPut(rec, httptest.NewRequest(http.MethodPut, "/",
			strings.NewReader(`{"global.log_level":"debug","global.admin.api_key":"new-key-0123456789abcdef"}`)))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		if g := d.cfg.Global; g.LogLevel != "info" || g.Admin.APIKey != "old-key-0123456789abcdef" {
			t.Fatalf("settings live after failed persist: log_level=%q api_key=%q", g.LogLevel, g.Admin.APIKey)
		}
	})
	t.Run("branding", func(t *testing.T) {
		d := &failPersistDeps{}
		d.cfg.Global.Admin.Branding.Name = "Old Panel"
		rec := httptest.NewRecorder()
		New(d).BrandingPut(rec, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"name":"New Panel"}`)))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		if got := d.cfg.Global.Admin.Branding.Name; got != "Old Panel" {
			t.Fatalf("branding live after failed persist: %q", got)
		}
	})
	t.Run("control: successful persist applies", func(t *testing.T) {
		d := &stubDeps{}
		rec := httptest.NewRecorder()
		New(d).SettingsPut(rec, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"global.log_level":"debug"}`)))
		if rec.Code != http.StatusOK || d.cfg.Global.LogLevel != "debug" {
			t.Fatalf("control broken: %d %q", rec.Code, d.cfg.Global.LogLevel)
		}
	})
}
