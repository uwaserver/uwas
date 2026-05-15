package respond

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJSON_SetsHeadersAndCode(t *testing.T) {
	w := httptest.NewRecorder()
	JSON(w, http.StatusCreated, map[string]string{"ok": "yes"})

	if w.Code != http.StatusCreated {
		t.Fatalf("status: got %d want %d", w.Code, http.StatusCreated)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: got %q", ct)
	}
	if ns := w.Header().Get("X-Content-Type-Options"); ns != "nosniff" {
		t.Errorf("X-Content-Type-Options: got %q", ns)
	}
	if fo := w.Header().Get("X-Frame-Options"); fo != "DENY" {
		t.Errorf("X-Frame-Options: got %q", fo)
	}
	body := strings.TrimSpace(w.Body.String())
	if body != `{"ok":"yes"}` {
		t.Errorf("body: got %q", body)
	}
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
