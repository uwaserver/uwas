package cronjob

import (
	"runtime"
	"strings"
	"testing"
)

// F56002: Execute runs a domain's cron command and returns its output to the
// caller and to alert channels, so it must not hand the command UWAS's own
// secrets (the admin key, DNS/cloud tokens, DB passwords). Deploy builds and
// apps already use childenv.Filtered for the same reason.
func TestExecuteEnvExcludesUWASSecrets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs printenv")
	}
	t.Setenv("UWAS_ADMIN_KEY", "SECRET-ADMIN-KEY")
	t.Setenv("CLOUDFLARE_API_TOKEN", "SECRET-CF")
	m := NewMonitor(t.TempDir())
	rec := m.Execute("", "* * * * *", "printenv")
	if !rec.Success {
		t.Fatalf("printenv failed: %s %s", rec.Error, rec.Output)
	}
	if !strings.Contains(rec.Output, "PATH=") {
		t.Error("PATH missing from the cron environment")
	}
	for _, leak := range []string{"SECRET-ADMIN-KEY", "SECRET-CF"} {
		if strings.Contains(rec.Output, leak) {
			t.Errorf("cron command environment leaked %q", leak)
		}
	}
}
