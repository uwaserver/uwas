package admin

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/cronjob"
)

// F26700: a manual cron run must start in the domain's configured root
// (web_root/<host>/public_html), not in a path guessed from web_root that no
// real layout contains, which left the job in the UWAS process's own cwd.
func TestCronExecuteRunsInConfiguredDomainRoot(t *testing.T) {
	base := t.TempDir()
	webRoot := filepath.Join(base, "www")
	root := filepath.Join(webRoot, "example.com", "public_html")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	s := testServer()
	s.config.Domains = []config.Domain{{Host: "example.com", Root: root, Type: "static"}}
	s.SetCronMonitor(cronjob.NewMonitor(webRoot))

	run := func(domain string) string {
		t.Helper()
		body := `{"domain":"` + domain + `","command":"pwd"}`
		req := httptest.NewRequest("POST", "/api/v1/cron/execute", strings.NewReader(body))
		rec := httptest.NewRecorder()
		s.handleCronExecute(rec, withAdminContext(req))
		if rec.Code != 200 {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}
		var out struct {
			Output string `json:"output"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out.Output)
	}
	same := func(a, b string) bool {
		ai, err1 := os.Stat(a)
		bi, err2 := os.Stat(b)
		return err1 == nil && err2 == nil && os.SameFile(ai, bi)
	}

	for _, host := range []string{"example.com", "Example.COM"} {
		if got := run(host); !same(got, root) {
			t.Errorf("host %q: cron ran in %q, want domain root %q", host, got, root)
		}
	}

	// Control: a host that is not configured keeps the old behaviour (no
	// domain root to enter).
	if got := run("unknown.test"); same(got, root) {
		t.Errorf("unknown host ran in %q", got)
	}
}
