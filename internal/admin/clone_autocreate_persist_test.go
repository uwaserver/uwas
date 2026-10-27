package admin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/migrate"
)

// runAutoCreate runs autoCreateDomainForClone for a finished clone of
// source.test into clone.test. A hang is reported instead of wedging the suite.
func runAutoCreate(t *testing.T, block bool) (*Server, *migrate.CloneResult, string) {
	t.Helper()
	e := newHostConflictEnv(t, []config.Domain{{Host: "source.test", Type: "static"}})
	if block {
		blockConfigPath(t, e.cfgPath)
	}
	req := &migrate.CloneRequest{SourceDomain: "source.test", TargetDomain: "clone.test", TargetRoot: filepath.Join(t.TempDir(), "clone")}
	result := &migrate.CloneResult{Status: "done", SourceDomain: "source.test", TargetDomain: "clone.test"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.s.autoCreateDomainForClone(req, result)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("EXPECTED: autoCreateDomainForClone returns\nACTUAL: still blocked (persistConfig re-locks configMu held by the caller)")
	}
	return e.s, result, e.cfgPath
}

func hasDomain(s *Server, host string) bool {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	for _, d := range s.config.Domains {
		if d.Host == host {
			return true
		}
	}
	return false
}

// The clone handler registers the new site while holding configMu and then
// persists the configuration. persistConfig takes configMu itself, so with a
// config path set the request deadlocked; and a failed write was ignored, so
// the site vanished on restart while the clone reported "done".
func TestCloneAutoCreatePersists(t *testing.T) {
	s, result, cfgPath := runAutoCreate(t, false)
	if result.Status != "done" || result.Error != "" {
		t.Fatalf("control: status=%q error=%q, want done", result.Status, result.Error)
	}
	if !hasDomain(s, "clone.test") {
		t.Fatal("clone.test missing from memory")
	}
	found := false
	_ = filepath.Walk(filepath.Dir(cfgPath), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			if b, _ := os.ReadFile(p); strings.Contains(string(b), "clone.test") {
				found = true
			}
		}
		return nil
	})
	if !found {
		t.Fatal("clone.test was not written to disk")
	}
}

func TestCloneAutoCreatePersistFailureIsReported(t *testing.T) {
	s, result, _ := runAutoCreate(t, true)
	if result.Status != "error" || result.Error == "" {
		t.Fatalf("EXPECTED: status error with a message when the config cannot be written\nACTUAL: status=%q error=%q", result.Status, result.Error)
	}
	if hasDomain(s, "clone.test") {
		t.Fatal("EXPECTED: domain not kept in memory after a failed write\nACTUAL: memory and disk disagree")
	}
}
