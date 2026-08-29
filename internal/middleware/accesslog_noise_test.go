package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

// The access-log middleware wrote one Info line per request unconditionally.
// The only way to stop it was to lower global.log_level, which also silenced
// startup, reload, certificate and process events — and silenced failed
// requests along with successful ones, hiding the part worth keeping.

// captureAt runs one request through the middleware with the logger at the
// given threshold and returns what reached stdout. logger.New captures the
// writer at construction, so it is built after the swap; tests in this
// package do not run in parallel, so the swap is safe.
func captureAt(t *testing.T, level string, enabled bool, status int) string {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w

	log := logger.New(level, "text")
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	h := AccessLog(log, flag(enabled))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	}))
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.RemoteAddr = "203.0.113.1:5000"
	h.ServeHTTP(httptest.NewRecorder(), req)

	os.Stdout = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()
	return out
}

// flag adapts a fixed bool to the func() bool the middleware now takes.
func flag(v bool) func() bool { return func() bool { return v } }

// At info nothing changes: every request is still logged.
func TestAccessLogInfoLogsEveryStatus(t *testing.T) {
	for _, status := range []int{200, 301, 404, 500} {
		if out := captureAt(t, "info", true, status); !strings.Contains(out, "/probe") {
			t.Errorf("status %d was not logged at info:\n%s", status, out)
		}
	}
}

// At warn the successful and client-error requests go quiet — that is the
// point — but a 5xx must survive. Lowering the level to reduce noise must not
// stop the server telling you it is breaking.
func TestAccessLogWarnKeepsServerErrors(t *testing.T) {
	if out := captureAt(t, "warn", true, 500); !strings.Contains(out, "/probe") {
		t.Errorf("a 500 was silenced at warn — the failures are what warn is for:\n%s", out)
	}
	if out := captureAt(t, "warn", true, 200); strings.Contains(out, "/probe") {
		t.Errorf("a 200 was logged at warn:\n%s", out)
	}
}

// 4xx stays Info on purpose: scanner 404s are constant on a public site and
// would put the noise straight back into warn. Blocked requests are reported
// at Warn by the security and WAF guards instead.
func TestAccessLogClientErrorsStayInfo(t *testing.T) {
	if out := captureAt(t, "warn", true, 404); strings.Contains(out, "/probe") {
		t.Errorf("a 404 reached warn — scanner traffic would flood it:\n%s", out)
	}
	if out := captureAt(t, "info", true, 404); !strings.Contains(out, "/probe") {
		t.Errorf("a 404 was not logged at info:\n%s", out)
	}
}

// enabled=false drops the line at every status, for a deployment that already
// writes per-domain access_log files and does not want the data twice.
func TestAccessLogDisabledLogsNothing(t *testing.T) {
	for _, status := range []int{200, 404, 500} {
		if out := captureAt(t, "info", false, status); strings.Contains(out, "/probe") {
			t.Errorf("status %d logged with the access log disabled:\n%s", status, out)
		}
	}
}

// Disabling it must not break the chain.
func TestAccessLogDisabledStillServes(t *testing.T) {
	h := AccessLog(logger.New("info", "text"), flag(false))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/probe", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d, want 418 — the handler was not reached", rec.Code)
	}
}

// The middleware chain is built once at startup and never rebuilt, so the
// flag has to be read per request. Capturing it at construction time is the
// bug this guards: the panel toggle would persist to disk, survive a reload
// and still change nothing until the process restarted.
func TestAccessLogReadsFlagPerRequest(t *testing.T) {
	var live atomic.Bool
	live.Store(true)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	h := AccessLog(logger.New("info", "text"), live.Load)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	serve := func(path string) {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}

	serve("/while-on")
	live.Store(false) // what reload does
	serve("/while-off")
	live.Store(true)
	serve("/back-on")

	os.Stdout = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()

	if !strings.Contains(out, "/while-on") {
		t.Errorf("the request before the flip was not logged:\n%s", out)
	}
	if strings.Contains(out, "/while-off") {
		t.Errorf("the flag flipped to false but the line was still written — it was captured at build time:\n%s", out)
	}
	if !strings.Contains(out, "/back-on") {
		t.Errorf("turning it back on did not restore the line:\n%s", out)
	}
}

// A nil func means on: that is what an absent access_log block has always
// meant, and callers that do not care must not silently lose their log.
func TestAccessLogNilEnabledLogs(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	h := AccessLog(logger.New("info", "text"), nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/probe", nil))

	os.Stdout = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()

	if !strings.Contains(out, "/probe") {
		t.Errorf("nil enabled silenced the log:\n%s", out)
	}
}
