package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureDefaultConfigReportsEnvWriteFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	envPath := filepath.Join(home, ".uwas", ".env")
	if err := os.MkdirAll(envPath, 0o755); err != nil {
		t.Fatal(err)
	}

	cfgPath, err := ensureDefaultConfig("18080", "19443", "127.0.0.1", filepath.Join(home, "www"), "")
	if err == nil || !strings.Contains(err.Error(), ".env") || cfgPath != "" {
		t.Fatalf("cfg=%q err=%v, want contextual .env error", cfgPath, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".uwas", "uwas.yaml")); !os.IsNotExist(err) {
		t.Fatalf("partial config prevents retry: %v", err)
	}
	if err := os.Remove(envPath); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureDefaultConfig("18080", "19443", "127.0.0.1", filepath.Join(home, "www"), ""); err != nil {
		t.Fatalf("retry after fixing destination: %v", err)
	}
}
