package middleware

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

type failingRecoveryHijacker struct{ *httptest.ResponseRecorder }

func (w *failingRecoveryHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("hijack rejected before commit")
}

type successfulRecoveryHijacker struct{ *httptest.ResponseRecorder }

func (w *successfulRecoveryHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	a, b := net.Pipe()
	_ = b.Close()
	return a, bufio.NewReadWriter(bufio.NewReader(a), bufio.NewWriter(a)), nil
}

func TestRecoveryFailedHijackDoesNotCommitResponse(t *testing.T) {
	log := logger.New("error", "text")
	t.Cleanup(func() { log.Flush(0) })
	base := &failingRecoveryHijacker{ResponseRecorder: httptest.NewRecorder()}
	h := Recovery(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, _, err := w.(http.Hijacker).Hijack(); err == nil {
			t.Fatal("control: failing hijacker returned nil error")
		}
		panic("panic after failed hijack")
	}))
	defer func() {
		if got := recover(); got != nil {
			t.Fatalf("failed hijack incorrectly marked the response committed; recovery re-panicked: %v", got)
		}
		if base.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500 after a pre-commit hijack failure", base.Code)
		}
	}()
	h.ServeHTTP(base, httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestRecoverySuccessfulHijackCommitsResponse(t *testing.T) {
	base := &successfulRecoveryHijacker{ResponseRecorder: httptest.NewRecorder()}
	w := &startedWriter{ResponseWriter: base}
	conn, _, err := w.Hijack()
	if err != nil {
		t.Fatalf("control: successful hijack: %v", err)
	}
	_ = conn.Close()
	if !w.started {
		t.Fatal("successful hijack must mark the response committed")
	}
}
