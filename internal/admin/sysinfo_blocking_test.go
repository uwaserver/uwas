package admin

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
)

// TestSystemInfoRefreshDoesNotBlockConcurrentRequests stages a slow `apt`
// (unattended-upgrades holding its lock is the production shape of this hang)
// and fires a second /api/v1/system request while the first request is parked
// inside the refresh subprocess. Before the refresh moved outside
// sysInfoCacheMu, the second request blocked on the mutex for the whole
// subprocess duration.
func TestSystemInfoRefreshDoesNotBlockConcurrentRequests(t *testing.T) {
	stubDir := t.TempDir()
	aptScript := "#!/bin/sh\nsleep 12\necho 0\n"
	if err := os.WriteFile(filepath.Join(stubDir, "apt"), []byte(aptScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	s := &Server{
		config:  &config.Config{},
		logger:  logger.New("error", "text"),
		metrics: metrics.New(),
	}
	s.config.Global.WebRoot = t.TempDir()

	var aFinished atomic.Bool
	var wg sync.WaitGroup
	start := make(chan struct{})

	// Request A: cache is cold (first call), so it enters the refresh and
	// parks inside the slow apt subprocess.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer aFinished.Store(true)
		<-start
		s.handleSystem(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/system", nil))
	}()

	close(start)
	<-start
	time.Sleep(1500 * time.Millisecond) // let A park inside its apt subprocess

	// Request B: must serve from the current snapshot without waiting for
	// A's subprocess.
	bStart := time.Now()
	s.handleSystem(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/system", nil))
	bElapsed := time.Since(bStart)

	if bElapsed > 8*time.Second {
		t.Fatalf("concurrent /system request blocked on the refresh for %v (apt stub sleeps 12s)", bElapsed)
	}
	if aFinished.Load() {
		t.Fatal("request B completed only after A's refresh finished")
	}

	wg.Wait()
}
