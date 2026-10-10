package admin

// Regression test for F850/F851: RecordAuditUser and handleConfig read the
// shared config, which server reload overwrites in place under the config
// write lock. The reload-like writer is released by closing a channel and the
// reads happen before the join, so under -race any unlocked read of the config
// is reported. RecordAuditUser must also not take configMu: callers may
// already hold it.

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
)

func newRecordIPTestServer(recordIP bool) *Server {
	cfg := &config.Config{Global: config.GlobalConfig{
		Admin: config.AdminConfig{Listen: "127.0.0.1:0"},
		Audit: config.AuditConfig{RecordIP: recordIP},
	}}
	return New(cfg, logger.New("error", "text"), metrics.New())
}

func TestAuditAndConfigSummaryDoNotRaceReload(t *testing.T) {
	s := newRecordIPTestServer(false)
	start, done := make(chan struct{}), make(chan struct{})
	go func() {
		<-start
		n := *s.config
		n.Global.Audit.RecordIP = true
		s.configMu.Lock()
		*s.config = n
		s.configMu.Unlock()
		close(done)
	}()
	close(start)
	s.RecordAuditUser("domain.add", "x.test", "203.0.113.7", "admin", true)
	s.handleConfig(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/config", nil))
	<-done
}

func TestAuditRecordIPFollowsConfigAndReload(t *testing.T) {
	last := func(s *Server) string {
		snap := s.auditBuf.Snapshot()
		return snap[len(snap)-1].IP
	}
	s := newRecordIPTestServer(true)
	s.RecordAuditUser("a", "d", "198.51.100.1", "", true)
	if ip := last(s); ip != "198.51.100.1" {
		t.Fatalf("record_ip=true: ip=%q", ip)
	}
	s.SetAuditRecordIP(false)
	s.RecordAuditUser("a", "d", "198.51.100.1", "", true)
	if ip := last(s); ip != "" {
		t.Fatalf("after SetAuditRecordIP(false): ip=%q, want redacted", ip)
	}
}

func TestRecordAuditUnderConfigWriteLock(t *testing.T) {
	s := newRecordIPTestServer(false)
	done := make(chan struct{})
	go func() {
		s.configMu.Lock()
		s.RecordAuditUser("a", "d", "1.1.1.1", "", true)
		s.configMu.Unlock()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("RecordAuditUser blocked while the caller held configMu")
	}
}
