package admin

// F49600: POST /api/v1/system/update replaces the UWAS binary as root and
// restarts the service, yet left no audit record, unlike service start/stop
// (F31300), package install/remove and doctor fixes.

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/selfupdate"
)

func stubSelfUpdate(t *testing.T, updateErr error) {
	t.Helper()
	origCheck, origUpdate := selfupdate.CheckUpdateFn, selfupdate.UpdateFn
	t.Cleanup(func() { selfupdate.CheckUpdateFn, selfupdate.UpdateFn = origCheck, origUpdate })
	selfupdate.CheckUpdateFn = func(string) (*selfupdate.ReleaseInfo, error) {
		return &selfupdate.ReleaseInfo{
			CurrentVersion: "0.11.38", LatestVersion: "0.11.39", UpdateAvail: true,
			DownloadURL: "https://github.com/uwaserver/uwas/releases/download/v0.11.39/uwas-linux-amd64",
		}, nil
	}
	selfupdate.UpdateFn = func(string) error { return updateErr }
}

func TestSelfUpdateFailureIsAudited(t *testing.T) {
	s := testServer()
	stubSelfUpdate(t, errors.New("checksum mismatch"))

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/system/update", nil))
	t.Logf("status = %d", rec.Code)
	ok := hasAudit(s, "system.update|false")
	t.Logf("EXPECTED: audit %q\nACTUAL: %v", "system.update|false", auditActions(s))
	if !ok {
		t.Fatalf("PROBLEM CONFIRMED: a failed self-update left no audit record")
	}
}

func TestSelfUpdateSuccessIsAudited(t *testing.T) {
	s := testServer()
	stubSelfUpdate(t, nil)
	restarted := make(chan struct{}, 1)
	origRestart := restartSelfFn
	t.Cleanup(func() { restartSelfFn = origRestart })
	restartSelfFn = func() error { restarted <- struct{}{}; return nil }

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/system/update", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	<-restarted // the (stubbed) restart ran, so the audit write is already done
	if !hasAudit(s, "system.update|true") {
		t.Fatalf("a successful self-update left no audit record: %v", auditActions(s))
	}
}

// Control: an up-to-date server changes nothing, so it records nothing.
func TestSelfUpdateUpToDateIsNotAudited(t *testing.T) {
	s := testServer()
	origCheck := selfupdate.CheckUpdateFn
	t.Cleanup(func() { selfupdate.CheckUpdateFn = origCheck })
	selfupdate.CheckUpdateFn = func(string) (*selfupdate.ReleaseInfo, error) {
		return &selfupdate.ReleaseInfo{CurrentVersion: "0.11.39", LatestVersion: "0.11.39"}, nil
	}
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/system/update", nil))
	if got := auditActions(s); len(got) != 0 {
		t.Fatalf("up-to-date update recorded %v", got)
	}
}
