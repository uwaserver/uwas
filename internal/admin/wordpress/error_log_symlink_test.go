package wordpress

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The error-log endpoint reads <root>/wp-content/debug.log as the server
// process, and the docroot is tenant-writable. A planted debug.log symlink, a
// symlinked wp-content, or a FIFO must never be read; only the tail of a
// large log is returned.

func errorLogCall(t *testing.T, root string) (int, string) {
	t.Helper()
	h := New(fakeDeps{root: root})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/wordpress/sites/a.test/error-log", nil)
	req.SetPathValue("domain", "a.test")
	rec := httptest.NewRecorder()
	h.ErrorLog(rec, req)
	var b struct {
		Log string `json:"log"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	return rec.Code, b.Log
}

func TestErrorLogDoesNotFollowDocrootSymlinks(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("OUTSIDE-SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "debug.log"), []byte("OUTSIDE-SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("debug.log symlink", func(t *testing.T) {
		root := t.TempDir()
		os.MkdirAll(filepath.Join(root, "wp-content"), 0o755)
		if err := os.Symlink(secret, filepath.Join(root, "wp-content", "debug.log")); err != nil {
			t.Fatal(err)
		}
		if code, log := errorLogCall(t, root); code == http.StatusOK || strings.Contains(log, "OUTSIDE") {
			t.Fatalf("symlinked debug.log served: code=%d log=%q", code, log)
		}
	})
	t.Run("wp-content symlink", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(root, "wp-content")); err != nil {
			t.Fatal(err)
		}
		if code, log := errorLogCall(t, root); code == http.StatusOK || strings.Contains(log, "OUTSIDE") {
			t.Fatalf("symlinked wp-content served: code=%d log=%q", code, log)
		}
	})
	t.Run("fifo does not block", func(t *testing.T) {
		root := t.TempDir()
		os.MkdirAll(filepath.Join(root, "wp-content"), 0o755)
		fifo := filepath.Join(root, "wp-content", "debug.log")
		if err := syscall.Mkfifo(fifo, 0o644); err != nil {
			t.Skipf("mkfifo: %v", err)
		}
		done := make(chan int, 1)
		go func() { c, _ := errorLogCall(t, root); done <- c }()
		select {
		case code := <-done:
			if code == http.StatusOK {
				t.Fatalf("FIFO debug.log returned 200")
			}
		case <-time.After(10 * time.Second):
			if w, err := os.OpenFile(fifo, os.O_WRONLY, 0); err == nil {
				w.Close()
			}
			t.Fatal("handler blocked on a FIFO debug.log")
		}
	})
	t.Run("tail of large log", func(t *testing.T) {
		root := t.TempDir()
		os.MkdirAll(filepath.Join(root, "wp-content"), 0o755)
		buf := make([]byte, 300*1024+3)
		for i := range buf {
			buf[i] = byte('a' + i%26)
		}
		os.WriteFile(filepath.Join(root, "wp-content", "debug.log"), buf, 0o644)
		code, log := errorLogCall(t, root)
		if code != http.StatusOK || log != string(buf[len(buf)-100*1024:]) {
			t.Fatalf("tail mismatch: code=%d len=%d", code, len(log))
		}
	})
}
