package admin

import (
	cryptorand "crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type failingEntropyReader struct{}

func (failingEntropyReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}

func TestSoftwareInstallRejectsEntropyFailure(t *testing.T) {
	originalReader := cryptorand.Reader
	cryptorand.Reader = failingEntropyReader{}
	t.Cleanup(func() { cryptorand.Reader = originalReader })

	originalRoot := softwareLibraryRoot
	softwareLibraryRoot = t.TempDir()
	t.Cleanup(func() { softwareLibraryRoot = originalRoot })
	calls := stubSoftwareCompose(t, "container-id\n", 0)

	s := testServer()
	body := strings.NewReader(`{"template_id":"postgres","name":"database"}`)
	rec := httptest.NewRecorder()
	s.handleSoftwareInstall(rec, withAdminContext(httptest.NewRequest(http.MethodPost, "/api/v1/software/install", body)))

	if rec.Code != http.StatusInternalServerError {
		compose, _ := os.ReadFile(filepath.Join(softwareLibraryRoot, "database", "docker-compose.yml"))
		t.Fatalf("status = %d, want 500; compose=%q calls=%v", rec.Code, compose, *calls)
	}
	if _, err := os.Stat(filepath.Join(softwareLibraryRoot, "database")); !os.IsNotExist(err) {
		t.Fatalf("installation directory exists after entropy failure: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("compose called after entropy failure: %v", *calls)
	}
}
