package backup

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDownloadBodyDrained verifies that S3Provider.Download drains resp.Body
// before closing on non-2xx responses, so HTTP/1.1 keep-alive connections are
// returned to the transport pool instead of leaking.
func TestDownloadBodyDrained(t *testing.T) {
	var connCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connCount++
		w.WriteHeader(http.StatusNotFound)
		// Non-empty body — must be drained before Close() for keep-alive reuse.
		w.Write([]byte(`<?xml version="1.0"?><Error><Code>NoSuchKey</Code></Error>`))
	}))
	defer srv.Close()

	p := NewS3Provider(srv.URL, "test-bucket", "ak", "sk", "us-east-1")

	for i := 0; i < 5; i++ {
		_, err := p.Download(nil, "nonexistent.txt")
		if err == nil {
			t.Fatalf("expected error for nonexistent key, got nil")
		}
	}

	t.Logf("INFO: %d requests → %d server connections", 5, connCount)
	if connCount > 2 {
		t.Errorf("FAIL: server saw %d connections for 5 requests — resp.Body was not drained before Close(), breaking HTTP/1.1 keep-alive reuse", connCount)
	}
}

// TestUploadBodyDrained verifies that S3Provider.Upload drains resp.Body
// before closing on non-2xx responses.
func TestUploadBodyDrained(t *testing.T) {
	var connCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connCount++
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal"}`))
	}))
	defer srv.Close()

	p := NewS3Provider(srv.URL, "test-bucket", "ak", "sk", "us-east-1")

	for i := 0; i < 5; i++ {
		err := p.Upload(nil, "file.txt", strings.NewReader("data"))
		if err == nil {
			t.Fatalf("expected error for failed upload, got nil")
		}
	}

	t.Logf("INFO: %d requests → %d server connections", 5, connCount)
	if connCount > 2 {
		t.Errorf("FAIL: server saw %d connections for 5 requests — resp.Body was not drained before Close(), breaking HTTP/1.1 keep-alive reuse", connCount)
	}
}
