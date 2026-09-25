package admin

import (
	"net/http/httptest"
	"sync"
	"testing"
)

// TestRecordLogLazyInitRace is a regression test for the unsynchronized
// lazy init of s.logBuf in RecordLog (internal/admin/api.go).
//
// RecordLog is documented "Safe for concurrent use" and is invoked from the
// per-request dispatch path (internal/server/server_dispatch.go). Before the
// logMu guard, the lazy init wrote s.logBuf with no lock while handleLogs
// and handleSSELogs read the same field without a lock: concurrent RecordLog
// calls raced write-write on the pointer, and raced write-read against the
// log readers. Two first-request writers could each construct a private
// buffer and silently drop entries when only the last assignment survived.
//
// Writers use the real production entry point (RecordLog); readers use the
// real production reader stack (mux -> auth -> handleLogs). Run with
// -race to detect a regression of the guard.
func TestRecordLogLazyInitRace(t *testing.T) {
	s := testServer()

	const n = 8
	start := make(chan struct{})
	var wg sync.WaitGroup

	// Writers: the real production entry point, one per request.
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			<-start
			s.RecordLog(LogEntry{Host: "race.example.com", Method: "GET", Path: "/", Status: 200})
		}()
	}

	// Readers: the real production reader stack for /api/v1/logs.
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			<-start
			rec := httptest.NewRecorder()
			s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/logs", nil))
		}()
	}

	close(start)
	wg.Wait()

	// After wg.Wait() every goroutine has completed (happens-before edge),
	// so reading s.logBuf here is race-free regardless of the fix.
	if s.logBuf == nil {
		t.Fatal("logBuf not initialized after RecordLog")
	}
	if got := len(s.logBuf.Snapshot()); got != n {
		t.Fatalf("expected %d retained entries, got %d (lazy-init race lost entries)", n, got)
	}

	// Control (unaffected path): once the buffer exists, concurrent
	// RecordLog calls must be safe — ringBuffer.Append is internally
	// mutex-guarded and post-init the field is never written again.
	var wg2 sync.WaitGroup
	for i := 0; i < n; i++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			s.RecordLog(LogEntry{Host: "control.example.com", Method: "GET", Path: "/", Status: 200})
		}()
	}
	wg2.Wait()
	if got := len(s.logBuf.Snapshot()); got != 2*n {
		t.Fatalf("expected %d retained entries after control phase, got %d", 2*n, got)
	}
}
