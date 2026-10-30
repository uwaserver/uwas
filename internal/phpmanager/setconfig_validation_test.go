package phpmanager

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// F58301: SetConfig wrote key + " = " + value into the global php.ini
// unchecked, so a value with a line break appended extra directives (and a
// key with whitespace or a newline produced a line PHP parses differently).
func TestSetConfigRejectsInjectedLines(t *testing.T) {
	for name, tc := range map[string]struct{ key, value string }{
		"newline in value": {"memory_limit", "1M\ndisable_functions ="},
		"CR in value":      {"memory_limit", "1M\rdisable_functions ="},
		"newline in key":   {"memory_limit = 1M\nextension", "x"},
		"space in key":     {"memory limit", "1M"},
		"empty key":        {"", "1M"},
	} {
		t.Run(name, func(t *testing.T) {
			ini := filepath.Join(t.TempDir(), "php.ini")
			const orig = "memory_limit = 128M\n"
			os.WriteFile(ini, []byte(orig), 0644)
			m := New(testLogger())
			m.installations = []PHPInstall{{Version: "8.4.19", ConfigFile: ini}}
			err := m.SetConfig("8.4.19", tc.key, tc.value)
			if !errors.Is(err, ErrInvalidDirective) {
				t.Fatalf("SetConfig(%q,%q) = %v, want ErrInvalidDirective", tc.key, tc.value, err)
			}
			if got, _ := os.ReadFile(ini); string(got) != orig {
				t.Fatalf("php.ini changed by a rejected call:\n%s", got)
			}
		})
	}
}

// Control: ordinary directives, including ones the per-domain blocklist
// refuses, stay settable in the global php.ini by an admin.
func TestSetConfigAcceptsGlobalDirectives(t *testing.T) {
	ini := filepath.Join(t.TempDir(), "php.ini")
	os.WriteFile(ini, []byte("memory_limit = 128M\n"), 0644)
	m := New(testLogger())
	m.installations = []PHPInstall{{Version: "8.4.19", ConfigFile: ini}}
	for _, kv := range [][2]string{{"memory_limit", "512M"}, {"opcache.jit_buffer_size", "64M"}, {"open_basedir", "/var/www"}} {
		if err := m.SetConfig("8.4.19", kv[0], kv[1]); err != nil {
			t.Fatalf("SetConfig(%q): %v", kv[0], err)
		}
	}
	got, _ := os.ReadFile(ini)
	if !strings.Contains(string(got), "memory_limit = 512M") || !strings.Contains(string(got), "open_basedir = /var/www") {
		t.Fatalf("directives missing:\n%s", got)
	}
}

func TestSetConfigUnknownVersionIsTyped(t *testing.T) {
	m := New(testLogger())
	if err := m.SetConfig("9.9.9", "memory_limit", "1M"); !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("got %v, want ErrVersionNotFound", err)
	}
}

func TestSetDomainConfigErrorsAreTyped(t *testing.T) {
	m := New(testLogger())
	m.RegisterExistingDomain("a.com", "8.3", "unix:/run/a.sock", "/var/www/a", nil)
	if err := m.SetDomainConfig("a.com", "open_basedir", "/"); !errors.Is(err, ErrInvalidDirective) {
		t.Fatalf("blocked directive: got %v, want ErrInvalidDirective", err)
	}
	if err := m.SetDomainConfig("a.com", "memory_limit", "1M\nx=y"); !errors.Is(err, ErrInvalidDirective) {
		t.Fatalf("newline value: got %v, want ErrInvalidDirective", err)
	}
	if err := m.SetDomainConfig("nope.com", "memory_limit", "1M"); !errors.Is(err, ErrDomainNotAssigned) {
		t.Fatalf("unassigned: got %v, want ErrDomainNotAssigned", err)
	}
	if err := m.SetDomainConfig("a.com", "memory_limit", "1M"); err != nil {
		t.Fatalf("control: %v", err)
	}
}
