package wordpress

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	wp "github.com/uwaserver/uwas/internal/wordpress"
)

func TestInstallRejectsMissingDocumentRoot(t *testing.T) {
	orig := installFn
	called := make(chan wp.InstallRequest, 1)
	installFn = func(req wp.InstallRequest) wp.InstallResult {
		called <- req
		return wp.InstallResult{Status: "failed", Domain: req.Domain}
	}
	t.Cleanup(func() { installFn = orig })

	rec := httptest.NewRecorder()
	New(fakeDeps{}).Install(rec, httptest.NewRequest(http.MethodPost, "/api/v1/wordpress/install", strings.NewReader(`{"domain":"missing-root.test"}`)))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("PROBLEM CONFIRMED: empty document root status = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
	select {
	case req := <-called:
		t.Fatalf("installer was started with empty document root: %+v", req)
	default:
	}
}

func TestInstallWithConfiguredDocumentRootStillStarts(t *testing.T) {
	root := t.TempDir()
	orig := installFn
	called := make(chan wp.InstallRequest, 1)
	installFn = func(req wp.InstallRequest) wp.InstallResult {
		called <- req
		return wp.InstallResult{Status: "failed", Domain: req.Domain}
	}
	t.Cleanup(func() { installFn = orig })

	rec := httptest.NewRecorder()
	New(fakeDeps{root: root}).Install(rec, httptest.NewRequest(http.MethodPost, "/api/v1/wordpress/install", strings.NewReader(`{"domain":"configured.test"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("configured document root status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if req := <-called; req.WebRoot != root {
		t.Fatalf("installer root = %q, want %q", req.WebRoot, root)
	}
}
