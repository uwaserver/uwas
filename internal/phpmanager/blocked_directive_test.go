package phpmanager

// Regression guard: the per-domain php.ini security blocklist must hold no
// matter how a domain's overrides reached the map.
//
// blockedPHPDirectives was enforced only at the admin API boundary
// (SetDomainConfig). RegisterExistingDomain seeds configOverrides straight from
// d.PHP.ConfigOverrides in uwas.yaml with no such check, and buildDomainINI —
// the single funnel every override passes through on its way into the ini —
// re-validated only syntax and control characters. Because the enforced sandbox
// is written BEFORE the override block and PHP ini is last-value-wins, an
// operator-supplied "open_basedir = /" or "disable_functions =" silently
// defeated UWAS's chroot and function blocklist.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// countDirective returns how many times key is assigned as a directive,
// ignoring comments and blank lines.
func countDirective(ini, key string) int {
	n := 0
	for _, line := range strings.Split(ini, "\n") {
		l := strings.TrimSpace(line)
		if l == "" || strings.HasPrefix(l, ";") {
			continue
		}
		if strings.HasPrefix(l, key+" ") || strings.HasPrefix(l, key+"=") {
			n++
		}
	}
	return n
}

// renderINIFor renders a per-domain ini the way production does:
// RegisterExistingDomain seeds domainMap from config-supplied overrides
// (internal/server/server.go), then buildDomainINI renders the file
// (StartDomain).
func renderINIFor(t *testing.T, domain, webRoot string, overrides map[string]string) string {
	t.Helper()
	m := New(testLogger())

	iniPath := filepath.Join(t.TempDir(), "php.ini")
	if err := os.WriteFile(iniPath, []byte("memory_limit = 128M\n"), 0644); err != nil {
		t.Fatalf("write base ini: %v", err)
	}
	inst := PHPInstall{Version: "8.4.19", ConfigFile: iniPath}

	m.RegisterExistingDomain(domain, "8.4", "127.0.0.1:9001", webRoot, overrides)

	m.domainMu.Lock()
	di := m.domainMap[domain]
	m.domainMu.Unlock()
	if di == nil {
		t.Fatalf("RegisterExistingDomain did not register %q", domain)
	}

	p, err := m.buildDomainINI(domain, inst, di.configOverrides)
	if err != nil {
		t.Fatalf("buildDomainINI: %v", err)
	}
	t.Cleanup(func() { os.Remove(p) })

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read generated ini: %v", err)
	}
	return string(data)
}

// A disabled disable_functions is the canonical sandbox-clearing override: it
// re-enables exec/system/shell_exec for the domain.
func TestBlockedDirectiveFromConfigOverridesDoesNotClearSandbox(t *testing.T) {
	ini := renderINIFor(t, "shop.example.com", t.TempDir(), map[string]string{
		"disable_functions": "",
	})

	if n := countDirective(ini, "disable_functions"); n != 1 {
		t.Errorf("disable_functions appears %d times; want exactly 1 (UWAS's enforced "+
			"value only). A second assignment is emitted after the enforced line and "+
			"PHP's last-value-wins makes the operator's override effective, clearing "+
			"the sandbox.", n)
	}
}

// open_basedir = / removes the per-domain filesystem chroot entirely.
func TestBlockedDirectiveFromConfigOverridesDoesNotEscapeChroot(t *testing.T) {
	ini := renderINIFor(t, "shop.example.com", t.TempDir(), map[string]string{
		"open_basedir": "/",
	})

	if n := countDirective(ini, "open_basedir"); n != 1 {
		t.Errorf("open_basedir appears %d times; want exactly 1 (UWAS's chroot only). "+
			"A second assignment overrides the chroot and grants filesystem access "+
			"outside the web root.", n)
	}
	// The enforced chroot is itself an absolute path, so compare the whole
	// trimmed line rather than a substring.
	for _, line := range strings.Split(ini, "\n") {
		if strings.TrimSpace(line) == "open_basedir = /" {
			t.Errorf("per-domain ini contains an operator-supplied open_basedir = /:\n%s", ini)
		}
	}
}

// Every blocked directive, not just the two above, must be dropped when it
// arrives via config overrides.
func TestAllBlockedDirectivesDroppedFromConfigOverrides(t *testing.T) {
	for key := range blockedPHPDirectives {
		ini := renderINIFor(t, "shop.example.com", t.TempDir(), map[string]string{
			key: "OVERRIDE_VALUE",
		})
		if strings.Contains(ini, key+" = OVERRIDE_VALUE") {
			t.Errorf("blocked directive %q reached the per-domain ini:\n%s", key, ini)
		}
	}
}

// A non-blocked override must still be applied — a fix that dropped all
// overrides would pass the tests above but break the feature.
func TestNonBlockedOverrideStillApplied(t *testing.T) {
	ini := renderINIFor(t, "shop.example.com", t.TempDir(), map[string]string{
		"memory_limit": "512M",
	})

	if !strings.Contains(ini, "memory_limit = 512M") {
		t.Errorf("non-blocked override memory_limit = 512M was dropped:\n%s", ini)
	}
	if n := countDirective(ini, "memory_limit"); n != 2 {
		t.Errorf("memory_limit appears %d times; want 2 (base ini + override)", n)
	}
}

// The enforced sandbox must be present and singular when no blocked override
// is supplied.
func TestEnforcedSandboxIntact(t *testing.T) {
	webRoot := t.TempDir()
	ini := renderINIFor(t, "shop.example.com", webRoot, map[string]string{
		"memory_limit": "512M",
	})

	for _, want := range []string{
		"disable_functions = exec,passthru,shell_exec,system,popen,pcntl_exec",
		"allow_url_include = Off",
		"expose_php = Off",
		"display_errors = Off",
	} {
		if !strings.Contains(ini, want) {
			t.Errorf("enforced directive %q missing:\n%s", want, ini)
		}
	}
	if n := countDirective(ini, "open_basedir"); n != 1 {
		t.Errorf("open_basedir appears %d times; want 1", n)
	}
	if !strings.Contains(ini, webRoot) {
		t.Errorf("enforced open_basedir does not contain the domain web root %q:\n%s", webRoot, ini)
	}
}

// The admin API path already blocked these; the fix must not regress it.
func TestSetDomainConfigStillBlocksDirective(t *testing.T) {
	m := New(testLogger())
	m.domainMu.Lock()
	m.domainMap["shop.example.com"] = &domainInstance{
		domain:          "shop.example.com",
		version:         "8.4",
		listenAddr:      "127.0.0.1:9001",
		webRoot:         t.TempDir(),
		configOverrides: map[string]string{},
	}
	m.domainMu.Unlock()

	for _, key := range []string{"disable_functions", "open_basedir", "sendmail_path"} {
		if err := m.SetDomainConfig("shop.example.com", key, ""); err == nil {
			t.Errorf("SetDomainConfig(%q) succeeded; want an error — blocked for security", key)
		}
	}
}
