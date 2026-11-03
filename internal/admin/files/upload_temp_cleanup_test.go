package files

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/apps"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/cronjob"
)

type uploadCleanupDeps struct{ root string }

func (d uploadCleanupDeps) RequireAdmin(http.ResponseWriter, *http.Request) bool { return true }
func (d uploadCleanupDeps) RequirePin(http.ResponseWriter, *http.Request) bool   { return true }
func (d uploadCleanupDeps) RequireDomainAccess(http.ResponseWriter, *http.Request, string, string) bool {
	return true
}
func (d uploadCleanupDeps) CanAccessDomain(*http.Request, string) bool      { return true }
func (d uploadCleanupDeps) CronMonitor() *cronjob.Monitor                   { return nil }
func (d uploadCleanupDeps) RecordAudit(*http.Request, string, string, bool) {}
func (d uploadCleanupDeps) ParsePagination(*http.Request) (int, int)        { return 100, 0 }
func (d uploadCleanupDeps) LogInfo(string, ...any)                          {}
func (d uploadCleanupDeps) Domains() []config.Domain {
	return []config.Domain{{Host: "example.com", Root: d.root}}
}
func (d uploadCleanupDeps) WebRoot() string            { return filepath.Dir(d.root) }
func (d uploadCleanupDeps) AppsManager() *apps.Manager { return nil }
func (d uploadCleanupDeps) AuthEnabled() bool          { return false }

func TestUploadRemovesMultipartTemporaryFiles(t *testing.T) {
	tmp := t.TempDir()
	uploadRoot := filepath.Join(tmp, "site")
	if err := os.MkdirAll(uploadRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tmp)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("files", "large.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(bytes.Repeat([]byte("x"), uploadMemoryBytes+1)); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodPost, "/upload", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.SetPathValue("domain", "example.com")
	w := httptest.NewRecorder()
	New(uploadCleanupDeps{root: uploadRoot}).Upload(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("upload status = %d, body = %s", w.Code, w.Body.String())
	}

	matches, err := filepath.Glob(filepath.Join(tmp, "multipart-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("multipart temporary files remain after request: %v", matches)
	}
}
