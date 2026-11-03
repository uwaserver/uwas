package admin

import (
	"net/http/httptest"
	"sync"
	"testing"
)

func TestHandleAuditConcurrentStop(t *testing.T) {
	for i := 0; i < 200; i++ {
		s := testAuditServer()
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			req := withAdminContext(httptest.NewRequest("GET", "/api/v1/audit", nil))
			s.handleAudit(httptest.NewRecorder(), req)
		}()
		close(start)
		s.stopAudit()
		wg.Wait()
	}
}
