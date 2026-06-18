package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/apps"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// =============================================================================
// ringBuffer.PosAndEntries
// =============================================================================

func TestGrpJ_RingBufferPosAndEntries(t *testing.T) {
	rb := newRingBuffer[LogEntry](4)
	pos, entries := rb.PosAndEntries()
	if pos != 0 || len(entries) != 4 {
		t.Fatalf("fresh ring: pos=%d len=%d", pos, len(entries))
	}
	rb.Append(LogEntry{Host: "a.com"})
	rb.Append(LogEntry{Host: "b.com"})
	pos, entries = rb.PosAndEntries()
	if pos != 2 {
		t.Errorf("pos = %d, want 2", pos)
	}
	if entries[0].Host != "a.com" || entries[1].Host != "b.com" {
		t.Errorf("entries = %+v", entries[:2])
	}
}

// =============================================================================
// atomicWriteFile: success + error
// =============================================================================

func TestGrpJ_AtomicWriteFileSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := atomicWriteFile(path, []byte("hello"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "hello" {
		t.Fatalf("read back: %v data=%q", err, data)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Errorf("perm = %v, want 0600", info.Mode().Perm())
	}

	// overwrite existing
	if err := atomicWriteFile(path, []byte("world"), 0600); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	data, _ = os.ReadFile(path)
	if string(data) != "world" {
		t.Errorf("overwrite data = %q", data)
	}
}

func TestGrpJ_AtomicWriteFileBadDir(t *testing.T) {
	// CreateTemp in a nonexistent directory fails.
	err := atomicWriteFile(filepath.Join(t.TempDir(), "nope", "out.txt"), []byte("x"), 0600)
	if err == nil {
		t.Error("expected error writing to nonexistent dir")
	}
}

// =============================================================================
// appDomainHealthURL
// =============================================================================

func TestGrpJ_AppDomainHealthURL(t *testing.T) {
	s := testServer()

	// appsMgr nil -> ("","")
	dom := config.Domain{Host: "x.com"}
	dom.Proxy.Upstreams = []config.Upstream{{Address: "apps://myapp"}}
	if url, msg := s.appDomainHealthURL(dom); url != "" || msg != "" {
		t.Errorf("nil appsMgr = (%q,%q), want empty", url, msg)
	}

	// with appsMgr but no listening app -> "not listening" message
	store := apps.NewStore(filepath.Join(t.TempDir(), "apps.json"))
	s.appsMgr = apps.NewManager(store, logger.New("error", "text"))
	if url, msg := s.appDomainHealthURL(dom); url != "" || msg == "" {
		t.Errorf("non-listening app = (%q,%q), want empty url + msg", url, msg)
	}

	// no apps:// upstream -> ("","")
	dom2 := config.Domain{Host: "y.com"}
	dom2.Proxy.Upstreams = []config.Upstream{{Address: "http://10.0.0.1:80"}}
	if url, msg := s.appDomainHealthURL(dom2); url != "" || msg != "" {
		t.Errorf("non-apps upstream = (%q,%q), want empty", url, msg)
	}

	// empty upstream address skipped, and apps:// with port form
	dom3 := config.Domain{Host: "z.com"}
	dom3.Proxy.Upstreams = []config.Upstream{{Address: ""}, {Address: "apps://myapp:8080/health"}}
	if url, _ := s.appDomainHealthURL(dom3); url != "" {
		t.Errorf("non-listening port form url = %q, want empty", url)
	}
}

// =============================================================================
// handleSSELogs: reseller 403 + cancelled-context admin path
// =============================================================================

func TestGrpJ_SSELogsResellerForbidden(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	s.handleSSELogs(rec, withResellerContext(httptest.NewRequest("GET", "/x", nil)))
	if rec.Code != http.StatusForbidden {
		t.Errorf("reseller = %d, want 403", rec.Code)
	}
}

func TestGrpJ_SSELogsCancelledContext(t *testing.T) {
	s := testServer()
	s.logBuf = newRingBuffer[LogEntry](8)
	s.logBuf.Append(LogEntry{Host: "a.com"})

	// Build an admin-context request, then derive a child request whose context
	// is already cancelled so the streaming loop returns immediately.
	base := withAdminContext(httptest.NewRequest("GET", "/x", nil))
	ctx, cancel := context.WithCancel(base.Context())
	cancel()
	req := base.WithContext(ctx)

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		s.handleSSELogs(rec, req)
		close(done)
	}()
	<-done
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content-type = %q, want text/event-stream", ct)
	}
}
