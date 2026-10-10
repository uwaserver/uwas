package phpmanager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func effectiveINI(t *testing.T, content, key string) string {
	t.Helper()
	val := ""
	for _, l := range strings.Split(content, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, ";") {
			continue
		}
		if k, v, ok := strings.Cut(l, "="); ok && strings.TrimSpace(k) == key {
			val = strings.TrimSpace(v) // PHP: last assignment wins
		}
	}
	return val
}

// F1030: updateINI must change the effective (last active) assignment, not an
// earlier commented-out example of the same key.
func TestUpdateINIPrefersActiveLine(t *testing.T) {
	cases := []struct{ name, in string }{
		{"commented-before-active", "[PHP]\n;memory_limit = 64M\nmemory_limit = 128M\n"},
		{"two-active", "[PHP]\nmemory_limit = 64M\nmemory_limit = 128M\n"},
		{"active-before-commented", "[PHP]\nmemory_limit = 128M\n;memory_limit = 64M\n"},
		{"only-commented", "[PHP]\n;memory_limit = 64M\n"},
		{"absent", "[PHP]\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ini := filepath.Join(t.TempDir(), "php.ini")
			if err := os.WriteFile(ini, []byte(c.in), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := updateINI(ini, "memory_limit", "256M"); err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(ini)
			if got := effectiveINI(t, string(b), "memory_limit"); got != "256M" {
				t.Errorf("effective memory_limit = %q, want 256M\n%s", got, b)
			}
		})
	}
}

func fakePHPCGI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "php-cgi8.3")
	script := "#!/bin/sh\ncase \"$1\" in\n-v) echo 'PHP 8.3.1 (cgi-fcgi)';;\n-i) echo 'Loaded Configuration File => " +
		filepath.Join(dir, "php.ini") + "';;\n-m) printf '[PHP Modules]\\ncore\\n\\n[Zend Modules]\\n';;\nesac\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func detectOnly(t *testing.T, m *Manager, bin string) {
	t.Helper()
	old := candidatePathsFunc
	candidatePathsFunc = func() []string { return []string{bin} }
	defer func() { candidatePathsFunc = old }()
	if err := m.Detect(); err != nil {
		t.Fatal(err)
	}
}

// F1031: re-running Detect (after installing another PHP version) must keep
// the versions the operator disabled.
func TestDetectKeepsDisabledVersions(t *testing.T) {
	m := New(testLogger())
	bin := fakePHPCGI(t)
	detectOnly(t, m, bin)
	detectOnly(t, m, bin)
	if m.Installations()[0].Disabled {
		t.Fatal("never-disabled version reported disabled")
	}
	if err := m.DisableVersion("8.3"); err != nil {
		t.Fatal(err)
	}
	detectOnly(t, m, bin)
	if !m.Installations()[0].Disabled {
		t.Fatal("re-detect re-enabled a disabled version")
	}
	m.EnableVersion("8.3")
	detectOnly(t, m, bin)
	if m.Installations()[0].Disabled {
		t.Fatal("explicitly re-enabled version came back disabled")
	}
}
