package wordpress

// F51300: the install runs on a goroutine the handler starts and nothing above
// it can recover, so a panic inside it ended the whole server process for every
// tenant (a NUL byte in db_pass did exactly that). The panic must become an
// error result the caller reads through InstallStatus.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	wp "github.com/uwaserver/uwas/internal/wordpress"
)

func TestInstallPanicBecomesErrorResultNotProcessExit(t *testing.T) {
	root := t.TempDir()
	done := make(chan struct{})
	orig := installFn
	installFn = func(req wp.InstallRequest) wp.InstallResult {
		defer close(done)
		panic("escSQL: null byte not allowed in SQL literal")
	}
	t.Cleanup(func() { installFn = orig })

	h := New(fakeDeps{root: root})
	body := `{"domain":"test.com","web_root":"` + strings.ReplaceAll(root, `\`, `\\`) + `"}`
	rec := httptest.NewRecorder()
	h.Install(rec, httptest.NewRequest(http.MethodPost, "/api/v1/wordpress/install", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("install: status %d, body %s", rec.Code, rec.Body.String())
	}
	select {
	case <-done:
	case <-time.After(30 * time.Second): // hang detector only
		t.Fatal("install goroutine never ran")
	}

	// The goroutine stores its result right after installFn returns; poll the
	// state through the handler until the recovered result is published.
	var got wp.InstallResult
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		st := httptest.NewRecorder()
		h.InstallStatus(st, httptest.NewRequest(http.MethodGet, "/api/v1/wordpress/install/status", nil))
		if err := json.Unmarshal(st.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Status != "running" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	t.Logf("EXPECTED: status=error, process alive\nACTUAL: status=%q error=%q", got.Status, got.Error)
	if got.Status != "error" || got.Error == "" {
		t.Fatalf("PROBLEM CONFIRMED: result after a panic = %+v, want an error result", got)
	}
}

func TestInstallResultUnchangedWhenNothingPanics(t *testing.T) {
	root := t.TempDir()
	done := make(chan struct{})
	orig := installFn
	installFn = func(req wp.InstallRequest) wp.InstallResult {
		defer close(done)
		return wp.InstallResult{Status: "done", Domain: req.Domain}
	}
	t.Cleanup(func() { installFn = orig })

	h := New(fakeDeps{root: root})
	body := `{"domain":"test.com","web_root":"` + strings.ReplaceAll(root, `\`, `\\`) + `"}`
	h.Install(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/v1/wordpress/install", strings.NewReader(body)))
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("install goroutine never ran")
	}
	var got wp.InstallResult
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		st := httptest.NewRecorder()
		h.InstallStatus(st, httptest.NewRequest(http.MethodGet, "/api/v1/wordpress/install/status", nil))
		_ = json.Unmarshal(st.Body.Bytes(), &got)
		if got.Status != "running" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if got.Status != "done" {
		t.Fatalf("status = %q, want done", got.Status)
	}
}
