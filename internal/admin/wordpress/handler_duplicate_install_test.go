package wordpress

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	wp "github.com/uwaserver/uwas/internal/wordpress"
)

// The handler answers 409 while an install is in flight. Observing that by
// racing a real wp.Install is only reliable when the install is slow: it
// writes the finished result over h.result, so an install that fails fast —
// no network, no PHP, an unwritable root — clears the "running" state before
// the duplicate request arrives, and the second call gets 200.
//
// installFn is held open here instead, so the in-flight window is the test's
// to control.

type fakeDeps struct{ root string }

func (d fakeDeps) RequireDomainAccess(http.ResponseWriter, *http.Request, string, string) bool {
	return true
}
func (d fakeDeps) CanAccessDomain(*http.Request, string) bool { return true }
func (d fakeDeps) AuthorizedDomainRoot(_ http.ResponseWriter, _ *http.Request, _, _ string) (string, bool) {
	return d.root, true
}
func (d fakeDeps) DomainRoot(string) string                        { return d.root }
func (d fakeDeps) GlobalWebRoot() string                           { return d.root }
func (d fakeDeps) Domains() []wp.DomainInfo                        { return nil }
func (d fakeDeps) LogInfo(string, ...any)                          {}
func (d fakeDeps) RecordAudit(*http.Request, string, string, bool) {}

func TestInstallRejectsDuplicateWhileRunning(t *testing.T) {
	root := t.TempDir()

	// Hold the install open for the duration of the test.
	release := make(chan struct{})
	finished := make(chan struct{})
	orig := installFn
	installFn = func(req wp.InstallRequest) wp.InstallResult {
		<-release
		close(finished)
		return wp.InstallResult{Status: "failed", Domain: req.Domain}
	}
	t.Cleanup(func() {
		close(release)
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("kurulum goroutine'i bitmedi")
		}
		installFn = orig
	})

	h := New(fakeDeps{root: root})
	body := func() *strings.Reader {
		return strings.NewReader(`{"domain":"test.com","web_root":"` + strings.ReplaceAll(root, `\`, `\\`) + `"}`)
	}

	first := httptest.NewRecorder()
	h.Install(first, httptest.NewRequest(http.MethodPost, "/api/v1/wordpress/install", body()))
	if first.Code != http.StatusOK {
		t.Fatalf("ilk kurulum: durum %d, body %s", first.Code, first.Body.String())
	}

	second := httptest.NewRecorder()
	h.Install(second, httptest.NewRequest(http.MethodPost, "/api/v1/wordpress/install", body()))
	if second.Code != http.StatusConflict {
		t.Errorf("yinelenen kurulum: durum %d, want 409, body %s", second.Code, second.Body.String())
	}
	if body := second.Body.String(); !strings.Contains(body, "already in progress") {
		t.Errorf("body = %s", body)
	}
}

// Once the install finishes, a new one must be accepted.
func TestInstallAcceptedAfterPreviousFinishes(t *testing.T) {
	root := t.TempDir()

	// Every invocation signals here. Waiting on it keeps the goroutine from
	// outliving the test — t.TempDir()'s cleanup runs while it is still
	// writing otherwise, and fails with "directory not empty".
	called := make(chan struct{}, 4)
	orig := installFn
	installFn = func(req wp.InstallRequest) wp.InstallResult {
		defer func() { called <- struct{}{} }()
		return wp.InstallResult{Status: "failed", Domain: req.Domain}
	}
	t.Cleanup(func() { installFn = orig })

	awaitCall := func(ne string) {
		t.Helper()
		select {
		case <-called:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s kurulum goroutine'i bitmedi", ne)
		}
	}

	h := New(fakeDeps{root: root})
	body := func() *strings.Reader {
		return strings.NewReader(`{"domain":"test.com","web_root":"` + strings.ReplaceAll(root, `\`, `\\`) + `"}`)
	}

	first := httptest.NewRecorder()
	h.Install(first, httptest.NewRequest(http.MethodPost, "/api/v1/wordpress/install", body()))
	if first.Code != http.StatusOK {
		t.Fatalf("ilk kurulum: durum %d", first.Code)
	}
	awaitCall("ilk")
	// The signal fires as installFn returns; the handler's goroutine writes
	// h.result after that, so waiting on the channel alone leaves a window
	// where the next request still sees "running".
	waitUntilIdle(t, h)

	second := httptest.NewRecorder()
	h.Install(second, httptest.NewRequest(http.MethodPost, "/api/v1/wordpress/install", body()))
	if second.Code != http.StatusOK {
		t.Errorf("biten kurulumdan sonra durum %d, want 200, body %s", second.Code, second.Body.String())
	}
	awaitCall("ikinci")
}

// waitUntilIdle waits until the handler no longer reports an install in
// flight.
func waitUntilIdle(t *testing.T, h *Handler) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h.mu.Lock()
		calisiyor := h.result != nil && h.result.Status == "running"
		h.mu.Unlock()
		if !calisiyor {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("kurulum durumu \"running\" and never cleared")
}
