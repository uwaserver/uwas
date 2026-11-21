//go:build linux

package terminal

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// F67500: with $SHELL unset and no /bin/bash (the alpine-based official
// image), the terminal must fall back to /bin/sh instead of failing every
// session with "fork/exec /bin/bash: no such file or directory".
func TestDefaultShellFallsBackToShWithoutBash(t *testing.T) {
	t.Setenv("SHELL", "")
	old := bashPath
	bashPath = filepath.Join(t.TempDir(), "no-bash")
	t.Cleanup(func() { bashPath = old })

	h := New(nil)
	if h.Shell != "/bin/sh" {
		t.Fatalf("New().Shell = %q without $SHELL and bash, want /bin/sh", h.Shell)
	}
	s := tdDial(t, h)
	defer s.stop()
	if err := s.cli.writeFrame([]byte("echo SHELL_OK_$((40+2))\n")); err != nil {
		t.Fatal(err)
	}
	if out := s.cli.readTextUntil(t, "SHELL_OK_42", 10*time.Second); !strings.Contains(out, "SHELL_OK_42") {
		t.Fatalf("session did not run a command: %q", out)
	}

	t.Setenv("SHELL", "/bin/sh")
	if got := New(nil).Shell; got != "/bin/sh" {
		t.Fatalf("$SHELL not honoured: %q", got)
	}
}
