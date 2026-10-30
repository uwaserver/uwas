package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// F53001: loadDotEnv stopped at the first .env found, so a per-user file
// without UWAS_ADMIN_PIN hid the /etc/uwas/.env that `uwas init` wrote.
func TestLoadDotEnvFilesMergesAllFirstWins(t *testing.T) {
	d := t.TempDir()
	a, b := filepath.Join(d, "a.env"), filepath.Join(d, "b.env")
	os.WriteFile(a, []byte("F53001_SHARED=from-a\nF53001_ONLY_A=1\n"), 0o600)
	os.WriteFile(b, []byte("F53001_SHARED=from-b\nF53001_ONLY_B=2\n"), 0o600)
	for _, k := range []string{"F53001_SHARED", "F53001_ONLY_A", "F53001_ONLY_B"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	t.Cleanup(func() {
		for _, k := range []string{"F53001_SHARED", "F53001_ONLY_A", "F53001_ONLY_B"} {
			os.Unsetenv(k)
		}
	})
	loadDotEnvFiles([]string{a, filepath.Join(d, "missing.env"), b})
	if os.Getenv("F53001_SHARED") != "from-a" {
		t.Errorf("shared = %q, want first file to win", os.Getenv("F53001_SHARED"))
	}
	if os.Getenv("F53001_ONLY_B") != "2" {
		t.Errorf("second file was not loaded: %q", os.Getenv("F53001_ONLY_B"))
	}
}
