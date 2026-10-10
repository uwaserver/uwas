package phpmanager

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ffi.enable is PHP_INI_SYSTEM: a per-domain override is the only way a
// tenant could turn FFI on, and FFI calls libc outside disable_functions and
// open_basedir. It must be rejected like enable_dl, and dropped when seeded
// from config.
func TestDomainOverrideCannotEnableFFI(t *testing.T) {
	m := New(testLogger())
	m.RegisterExistingDomain("tenant.test", "8.4", "127.0.0.1:9001", t.TempDir(),
		map[string]string{"ffi.enable": "true"})

	if err := m.SetDomainConfig("tenant.test", "ffi.enable", "true"); err == nil {
		t.Fatal("SetDomainConfig accepted ffi.enable")
	}

	base := filepath.Join(t.TempDir(), "php.ini")
	if err := os.WriteFile(base, []byte("memory_limit = 128M\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m.domainMu.Lock()
	p, err := m.buildDomainINI("tenant.test", PHPInstall{Version: "8.4", ConfigFile: base}, m.domainMap["tenant.test"].configOverrides)
	m.domainMu.Unlock()
	if err != nil {
		t.Fatalf("buildDomainINI: %v", err)
	}
	defer os.Remove(p)
	b, _ := os.ReadFile(p)
	for _, line := range strings.Split(string(b), "\n") {
		if k, _, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == "ffi.enable" {
			t.Fatalf("config-seeded ffi.enable emitted: %q", line)
		}
	}
}

// AssignDomain must not hand a domain the address of a running shared pool
// started with StartFPM: the domain would be served by that pool, without
// its own open_basedir ini, and its own php-cgi could not bind.
func TestAssignDomainSkipsSharedPoolAddress(t *testing.T) {
	origStat := osStat
	osStat = func(string) (os.FileInfo, error) { return nil, errors.New("no system fpm") }
	defer func() { osStat = origStat }()

	bin := filepath.Join(t.TempDir(), "php-cgi")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexec sleep 300\n"), 0755); err != nil {
		t.Fatal(err)
	}
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.3.1", Binary: bin, SAPI: "cgi-fcgi"},
		{Version: "8.4.1", Binary: bin, SAPI: "cgi-fcgi"},
	}
	if err := m.StartFPM("8.3.1", "127.0.0.1:9001"); err != nil {
		t.Fatalf("StartFPM: %v", err)
	}
	defer m.StopAll()

	d, err := m.AssignDomain("new.test", "8.4.1")
	if err != nil {
		t.Fatalf("AssignDomain: %v", err)
	}
	if d.ListenAddr == "127.0.0.1:9001" {
		t.Fatalf("domain was given the shared pool's address %s", d.ListenAddr)
	}
}
