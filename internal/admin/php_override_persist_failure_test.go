package admin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/auth"
)

// F27903: a per-domain php.ini override was kept in memory and the API answered
// "updated" even when the domain file could not be rewritten, so the override
// vanished on the next restart. Every other config-writing handler answers 500.
func TestPHPDomainConfigPutReportsPersistFailure(t *testing.T) {
	put := func(unwritable bool) (int, string) {
		srv, dir := newPHPOverrideServer(t)
		if unwritable {
			p := filepath.Join(dir, "domains.d", "example.com.yaml")
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(p, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		r := httptest.NewRequest(http.MethodPut, "/api/v1/php/domains/example.com/config", strings.NewReader(`{"key":"memory_limit","value":"512M"}`))
		r = r.WithContext(auth.WithUser(r.Context(), &auth.User{ID: "u", Username: "u", Role: auth.RoleAdmin, Enabled: true}))
		rec := httptest.NewRecorder()
		srv.mux.ServeHTTP(rec, r)
		return rec.Code, rec.Body.String()
	}
	if code, body := put(false); code != http.StatusOK {
		t.Fatalf("control = %d %s, want 200", code, body)
	}
	if code, body := put(true); code != http.StatusInternalServerError {
		t.Fatalf("EXPECTED: 500 when the domain file cannot be written\nACTUAL: %d %s", code, body)
	}
}
