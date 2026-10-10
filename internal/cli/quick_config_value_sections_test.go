package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// The CLI locates the admin API and pid file from the config. It must read
// global.admin.listen (not the first listen: in the file, e.g. mcp.listen) and
// ignore inline comments (F1811).
func TestQuickConfigValueReadsAdminSectionNotFirstMatch(t *testing.T) {
	orig := findConfigFn
	t.Cleanup(func() { findConfigFn = orig })
	use := func(body string) {
		p := filepath.Join(t.TempDir(), "uwas.yaml")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		findConfigFn = func(string) (string, bool) { return p, true }
	}

	cases := []struct {
		name, body, wantURL string
	}{
		{"admin only", "global:\n  admin:\n    listen: \"127.0.0.1:9443\"\n", "http://127.0.0.1:9443"},
		{"mcp before admin", "global:\n  mcp:\n    listen: \"127.0.0.1:9000\"\n  admin:\n    listen: \"0.0.0.0:9555\"\n", "http://127.0.0.1:9555"},
		{"admin without listen, mcp has one", "global:\n  admin:\n    enabled: true\n  mcp:\n    listen: \"127.0.0.1:9000\"\n", "http://127.0.0.1:9443"},
		{"inline comment", "global:\n  admin:\n    listen: \"127.0.0.1:9555\" # api\n", "http://127.0.0.1:9555"},
		{"no listen at all", "global:\n  pid_file: /run/uwas.pid\n", "http://127.0.0.1:9443"},
		{"bare fixture keys", "listen: 127.0.0.1:7777\n", "http://127.0.0.1:7777"},
		{"not yaml falls back to line scan", "global: [\nlisten: 127.0.0.1:7778\n", "http://127.0.0.1:7778"},
	}
	for _, c := range cases {
		use(c.body)
		if got := adminURLFromConfig(); got != c.wantURL {
			t.Errorf("%s: adminURLFromConfig = %q, want %q", c.name, got, c.wantURL)
		}
	}

	use("global:\n  pid_file: /run/uwas.pid # pid\n")
	if got := pidFileFromConfig(); got != "/run/uwas.pid" {
		t.Errorf("pid_file with inline comment = %q", got)
	}
	use("global:\n  admin:\n    listen: \":9443\"\n")
	if got := pidFileFromConfig(); got != "" {
		t.Errorf("missing pid_file = %q, want empty", got)
	}
}
