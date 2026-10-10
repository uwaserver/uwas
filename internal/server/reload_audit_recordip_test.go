package server

// Regression test for F850: reload() must propagate global.audit.record_ip to
// the admin server's audit flag (it no longer reads the shared config).

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func lastAuditIP(t *testing.T, s *Server, action string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	s.admin.HTTPServer().Handler.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/audit?limit=1000", nil))
	var page struct {
		Items []struct {
			Action string `json:"action"`
			IP     string `json:"ip"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode audit: %v (%s)", err, rec.Body.String())
	}
	for i := len(page.Items) - 1; i >= 0; i-- {
		if page.Items[i].Action == action {
			return page.Items[i].IP
		}
	}
	t.Fatalf("no %q audit entry", action)
	return ""
}

func TestReloadPropagatesAuditRecordIP(t *testing.T) {
	s, _, cfgPath, yaml := sharedLockSetup(t, true)
	on := strings.Replace(yaml, "global:\n", "global:\n  audit:\n    record_ip: true\n", 1)
	for i, step := range []struct {
		yaml, want string
	}{{yaml, ""}, {on, "203.0.113.9"}, {yaml, ""}} {
		if err := os.WriteFile(cfgPath, []byte(step.yaml), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := s.reload(); err != nil {
			t.Fatalf("reload %d: %v", i, err)
		}
		s.admin.RecordAudit("reload.recordip", "d", "203.0.113.9", true)
		if ip := lastAuditIP(t, s, "reload.recordip"); ip != step.want {
			t.Errorf("reload %d: ip=%q, want %q", i, ip, step.want)
		}
	}
}
