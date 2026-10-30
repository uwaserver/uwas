package admin

// F50600: the dashboard lists (domains, audit log, backups, apps, ...) used to
// call the paginated endpoints without a limit. The server answers with the
// FIRST 50 entries, oldest first, so an audit log with more than 50 entries
// never showed the newest ones and a server with more than 50 domains listed
// only 50. This pins the server side of that contract: the default page is the
// oldest 50, `total` reports the full count, and limit=500 returns the whole
// ring buffer (maxAuditEntries). The dashboard fetchers rely on both.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

type auditPage struct {
	Items  []AuditEntry `json:"items"`
	Total  int          `json:"total"`
	Limit  int          `json:"limit"`
	Offset int          `json:"offset"`
}

func getAuditPage(t *testing.T, s *Server, query string) auditPage {
	t.Helper()
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/audit"+query, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/audit%s = %d", query, rec.Code)
	}
	var p auditPage
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return p
}

func TestAuditPaginationDefaultIsOldestFiftyAndLimitReturnsAll(t *testing.T) {
	s := testAuditServer()
	defer s.stopAudit()
	const n = 120
	for i := 0; i < n; i++ {
		s.RecordAudit("config.reload", fmt.Sprintf("entry %03d", i), "127.0.0.1", true)
	}

	def := getAuditPage(t, s, "")
	t.Logf("EXPECTED: default page = oldest 50 of %d\nACTUAL: items=%d total=%d first=%q last=%q",
		n, len(def.Items), def.Total, def.Items[0].Detail, def.Items[len(def.Items)-1].Detail)
	if len(def.Items) != 50 || def.Total != n {
		t.Fatalf("default page: items=%d total=%d, want 50 and %d", len(def.Items), def.Total, n)
	}
	if def.Items[0].Detail != "entry 000" || def.Items[49].Detail != "entry 049" {
		t.Errorf("default page is not the oldest 50: %q .. %q", def.Items[0].Detail, def.Items[49].Detail)
	}

	all := getAuditPage(t, s, "?limit=500")
	if len(all.Items) != n || all.Items[n-1].Detail != "entry 119" {
		t.Errorf("limit=500: items=%d last=%q, want %d and entry 119", len(all.Items), all.Items[len(all.Items)-1].Detail, n)
	}

	tail := getAuditPage(t, s, "?limit=500&offset=50")
	if len(tail.Items) != n-50 || tail.Items[0].Detail != "entry 050" {
		t.Errorf("offset=50: items=%d first=%q, want %d and entry 050", len(tail.Items), tail.Items[0].Detail, n-50)
	}
}
