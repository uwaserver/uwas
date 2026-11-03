package cli

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigCommandAcceptsEqualsFlags(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	path := filepath.Join(dir, "config with space.yaml")
	body := []byte("global:\n  admin:\n    enabled: false\n")
	for _, p := range []string{path, filepath.Join(dir, "uwas.yaml")} {
		if err := os.WriteFile(p, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := &ConfigCommand{}
	for _, args := range [][]string{{"-c", path}, {"--config", path}, {"-c=" + path}, {"--config=" + path}, {"validate", "--config=" + path}, {"test", "-c=" + path}, {}} {
		if err := cmd.Run(args); err != nil {
			t.Fatalf("%q: %v", args, err)
		}
	}
	if err := cmd.Run([]string{"-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatal("help flag", err)
	}
	if err := cmd.Run([]string{"--config=" + filepath.Join(dir, "missing.yaml")}); err == nil || !strings.Contains(err.Error(), "invalid:") {
		t.Fatal("missing explicit path", err)
	}
	if err := cmd.Run([]string{"unknown"}); err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Fatal("unknown subcommand accepted", err)
	}
}
