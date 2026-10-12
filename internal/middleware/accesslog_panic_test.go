package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// accessLinesForPanic runs one request through Recovery+AccessLog (as the
// server chains them) and returns the access-log lines written to stdout.
func accessLinesForPanic(t *testing.T, inner http.HandlerFunc) []string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	log := logger.New("info", "text")
	done := make(chan string, 1)
	go func() { b, _ := io.ReadAll(r); done <- string(b) }()

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.RemoteAddr = "203.0.113.1:5000"
	func() {
		// Recovery aborts a response that had already started by re-panicking
		// with http.ErrAbortHandler, which net/http swallows (F8800).
		defer func() {
			if p := recover(); p != nil && p != http.ErrAbortHandler {
				panic(p)
			}
		}()
		Chain(Recovery(log), AccessLog(log, nil))(inner).ServeHTTP(httptest.NewRecorder(), req)
	}()
	log.Flush(time.Second) // logger writes asynchronously

	os.Stdout = orig
	_ = w.Close()
	out := <-done
	_ = r.Close()

	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "msg=request") && strings.Contains(l, "/probe") {
			lines = append(lines, l)
		}
	}
	return lines
}

// F8810: a request whose handler panics is answered 500 by Recovery, so the
// access log must carry that failure too.
func TestAccessLogRecordsPanickedRequestAs500(t *testing.T) {
	lines := accessLinesForPanic(t, func(w http.ResponseWriter, r *http.Request) { panic("boom") })
	if len(lines) != 1 || !strings.Contains(lines[0], "status=500") || !strings.Contains(lines[0], "level=ERROR") {
		t.Fatalf("access lines = %q, want exactly one ERROR line with status=500", lines)
	}
}

// A panic after the body started keeps the status the client already saw, and
// still produces exactly one line.
func TestAccessLogPanicAfterBodyKeepsStatus(t *testing.T) {
	lines := accessLinesForPanic(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("partial"))
		panic("boom after start")
	})
	if len(lines) != 1 || !strings.Contains(lines[0], "status=200") {
		t.Fatalf("access lines = %q, want one line with status=200", lines)
	}
}

// A normal request is still logged once.
func TestAccessLogNormalRequestLoggedOnce(t *testing.T) {
	lines := accessLinesForPanic(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	if len(lines) != 1 || !strings.Contains(lines[0], "status=418") {
		t.Fatalf("access lines = %q", lines)
	}
}
