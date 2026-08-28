package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

	h := AccessLog(log, enabled)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	h := AccessLog(logger.New("info", "text"), false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/probe", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d, want 418 — the handler was not reached", rec.Code)
	}
}
