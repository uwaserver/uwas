package admin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// F2080: bulk domain import must report a config that could not be written
// instead of answering 200 for domains that vanish on restart.

const bulkImportBody = `{"domains":[{"host":"bulk-one.example.com","type":"static"},{"host":"bulk-one.example.com"}]}`

func TestBulkDomainImportPersisted(t *testing.T) {
	s := testServer()
	p := filepath.Join(t.TempDir(), "uwas.yaml")
	s.SetConfigPath(p)
	rec := httptest.NewRecorder()
	s.handleBulkDomainImport(rec, totpActivationReq("POST", "/api/v1/domains/bulk-import", bulkImportBody, "admin"))
	if rec.Code != http.StatusOK {
		t.Fatalf("import = %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(p), "domains.d", "bulk-one.example.com.yaml")); err != nil {
		t.Fatalf("imported domain not persisted: %v", err)
	}
}

func TestBulkDomainImportPersistFailureIsReported(t *testing.T) {
	s := testServer()
	p := filepath.Join(t.TempDir(), "uwas.yaml")
	s.SetConfigPath(p)
	blockConfigPath(t, p)
	rec := httptest.NewRecorder()
	s.handleBulkDomainImport(rec, totpActivationReq("POST", "/api/v1/domains/bulk-import", bulkImportBody, "admin"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("import with unwritable config = %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
	// Nothing to add: no persist attempt, so an unwritable path is not an error.
	rec = httptest.NewRecorder()
	s.handleBulkDomainImport(rec, totpActivationReq("POST", "/api/v1/domains/bulk-import", `{"domains":[]}`, "admin"))
	if rec.Code != http.StatusOK {
		t.Fatalf("empty import = %d, want 200", rec.Code)
	}
}
