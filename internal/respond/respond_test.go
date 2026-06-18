package respond

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

// newBufLogger returns a logger that writes JSON records to buf so tests
// can assert on the emitted 5xx log line deterministically.
func newBufLogger(buf *bytes.Buffer) *logger.Logger {
	return &logger.Logger{
		Logger: slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

// resetLogger clears any package logger so isolated tests don't leak a
// registered logger into one another.
func resetLogger(t *testing.T) {
	t.Helper()
	prev := pkgLogger.Load()
	pkgLogger.Store(nil)
	t.Cleanup(func() { pkgLogger.Store(prev) })
}

func TestError_NoRequestID(t *testing.T) {
	w := httptest.NewRecorder()
	Error(w, http.StatusBadRequest, "bad request")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d", w.Code)
	}
	var got map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["error"] != "bad request" {
		t.Errorf("error field: got %q", got["error"])
	}
	if _, has := got["request_id"]; has {
		t.Errorf("request_id should not be present when header absent")
	}
}

func TestError_IncludesRequestIDWhenSet(t *testing.T) {
	w := httptest.NewRecorder()
	w.Header().Set("X-Request-ID", "abc123")
	Error(w, http.StatusNotFound, "missing")

	var got map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["request_id"] != "abc123" {
		t.Errorf("request_id: got %q", got["request_id"])
	}
}

func TestErrorCause_DoesNotLeakCauseToClient(t *testing.T) {
	w := httptest.NewRecorder()
	ErrorCause(w, http.StatusInternalServerError, "internal", errSentinel("secret detail"))

	body := w.Body.String()
	if strings.Contains(body, "secret detail") {
		t.Fatalf("response body leaked cause: %q", body)
	}
	var got map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["error"] != "internal" {
		t.Errorf("error field: got %q", got["error"])
	}
}

type errSentinel string

func (e errSentinel) Error() string { return string(e) }

func TestError_5xxLogsWithoutCause(t *testing.T) {
	resetLogger(t)
	var buf bytes.Buffer
	SetLogger(newBufLogger(&buf))

	w := httptest.NewRecorder()
	w.Header().Set("X-Request-ID", "req-500")
	Error(w, http.StatusInternalServerError, "boom")

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d", w.Code)
	}
	logged := buf.String()
	if !strings.Contains(logged, "server 5xx") {
		t.Errorf("expected log message, got %q", logged)
	}
	if !strings.Contains(logged, "req-500") {
		t.Errorf("expected request id in log, got %q", logged)
	}
	if !strings.Contains(logged, "boom") {
		t.Errorf("expected message in log, got %q", logged)
	}
	// No cause supplied: the "error" attribute must not be present.
	if strings.Contains(logged, `"error":`) {
		t.Errorf("did not expect error attr in log without cause, got %q", logged)
	}
}

func TestErrorCause_5xxLogsWithCause(t *testing.T) {
	resetLogger(t)
	var buf bytes.Buffer
	SetLogger(newBufLogger(&buf))

	w := httptest.NewRecorder()
	ErrorCause(w, http.StatusBadGateway, "upstream down", errSentinel("dial timeout"))

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status: got %d", w.Code)
	}
	logged := buf.String()
	if !strings.Contains(logged, "server 5xx") {
		t.Errorf("expected log message, got %q", logged)
	}
	if !strings.Contains(logged, "dial timeout") {
		t.Errorf("expected cause in log, got %q", logged)
	}
	// Cause must never reach the client.
	if strings.Contains(w.Body.String(), "dial timeout") {
		t.Errorf("cause leaked to client body: %q", w.Body.String())
	}
}

func TestError_5xxNilLoggerNoPanic(t *testing.T) {
	resetLogger(t) // pkgLogger is now nil
	w := httptest.NewRecorder()
	// Must not panic when no logger is registered for a 5xx.
	Error(w, http.StatusServiceUnavailable, "unavailable")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d", w.Code)
	}
	var got map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["error"] != "unavailable" {
		t.Errorf("error field: got %q", got["error"])
	}
}

func TestError_4xxDoesNotLog(t *testing.T) {
	resetLogger(t)
	var buf bytes.Buffer
	SetLogger(newBufLogger(&buf))

	w := httptest.NewRecorder()
	Error(w, http.StatusBadRequest, "nope")

	if buf.Len() != 0 {
		t.Errorf("4xx should not log, got %q", buf.String())
	}
}

func TestSetLogger_Replaces(t *testing.T) {
	resetLogger(t)
	var first, second bytes.Buffer
	SetLogger(newBufLogger(&first))
	SetLogger(newBufLogger(&second))

	w := httptest.NewRecorder()
	Error(w, http.StatusInternalServerError, "boom")

	if first.Len() != 0 {
		t.Errorf("first logger should have been replaced, got %q", first.String())
	}
	if second.Len() == 0 {
		t.Errorf("second logger should have received the log line")
	}
}

func TestSetBaseHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	Error(w, http.StatusBadRequest, "x")
	h := w.Header()
	checks := map[string]string{
		"Content-Type":              "application/json",
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"Strict-Transport-Security": "max-age=63072000; includeSubDomains; preload",
	}
	for k, want := range checks {
		if got := h.Get(k); got != want {
			t.Errorf("header %s: got %q want %q", k, got, want)
		}
	}
}
