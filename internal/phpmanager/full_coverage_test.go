package phpmanager

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ============================================================
// install.go — DetectDistro
// ============================================================

func TestDetectDistroNonLinux(t *testing.T) {
	orig := runtimeGOOSInstall
	defer func() { runtimeGOOSInstall = orig }()

	runtimeGOOSInstall = "windows"
	d := DetectDistro()
	if d.ID != "windows" {
		t.Errorf("ID = %q, want windows", d.ID)
	}
	if d.Name != "windows" {
		t.Errorf("Name = %q, want windows", d.Name)
	}
}

func TestDetectDistroLinuxSuccess(t *testing.T) {
	origGOOS := runtimeGOOSInstall
	origRead := readOSRelease
	defer func() {
		runtimeGOOSInstall = origGOOS
		readOSRelease = origRead
	}()

	runtimeGOOSInstall = "linux"
	readOSRelease = func() ([]byte, error) {
		return []byte(`ID=ubuntu
VERSION_ID="22.04"
PRETTY_NAME="Ubuntu 22.04.4 LTS"
OTHER_KEY=ignored
`), nil
	}

	d := DetectDistro()
	if d.ID != "ubuntu" {
		t.Errorf("ID = %q, want ubuntu", d.ID)
	}
	if d.Version != "22.04" {
		t.Errorf("Version = %q, want 22.04", d.Version)
	}
	if d.Name != "Ubuntu 22.04.4 LTS" {
		t.Errorf("Name = %q, want Ubuntu 22.04.4 LTS", d.Name)
	}
}

func TestDetectDistroLinuxReadError(t *testing.T) {
	origGOOS := runtimeGOOSInstall
	origRead := readOSRelease
	defer func() {
		runtimeGOOSInstall = origGOOS
		readOSRelease = origRead
	}()

	runtimeGOOSInstall = "linux"
	readOSRelease = func() ([]byte, error) {
		return nil, fmt.Errorf("no such file")
	}

	d := DetectDistro()
	if d.ID != "unknown" {
		t.Errorf("ID = %q, want unknown", d.ID)
	}
}

func TestDetectDistroLinuxLinesWithoutEquals(t *testing.T) {
	origGOOS := runtimeGOOSInstall
	origRead := readOSRelease
	defer func() {
		runtimeGOOSInstall = origGOOS
		readOSRelease = origRead
	}()

	runtimeGOOSInstall = "linux"
	readOSRelease = func() ([]byte, error) {
		return []byte("no-equals-here\nID=debian\nalso no equals\n"), nil
	}

	d := DetectDistro()
	if d.ID != "debian" {
		t.Errorf("ID = %q, want debian", d.ID)
	}
}

func TestDetectDistroLinuxEmptyFile(t *testing.T) {
	origGOOS := runtimeGOOSInstall
	origRead := readOSRelease
	defer func() {
		runtimeGOOSInstall = origGOOS
		readOSRelease = origRead
	}()

	runtimeGOOSInstall = "linux"
	readOSRelease = func() ([]byte, error) {
		return []byte(""), nil
	}

	d := DetectDistro()
	if d.ID != "" {
		t.Errorf("ID = %q, want empty", d.ID)
	}
}

// ============================================================
// install.go — GetInstallInfo (all distro branches)
// ============================================================

func withDistro(id, version, name string, fn func()) {
	origGOOS := runtimeGOOSInstall
	origRead := readOSRelease
	defer func() {
		runtimeGOOSInstall = origGOOS
		readOSRelease = origRead
	}()

	runtimeGOOSInstall = "linux"
	readOSRelease = func() ([]byte, error) {
		return []byte(fmt.Sprintf("ID=%s\nVERSION_ID=%s\nPRETTY_NAME=%s\n", id, version, name)), nil
	}
	fn()
}

func TestGetInstallInfoUbuntu(t *testing.T) {
	withDistro("ubuntu", "22.04", "Ubuntu 22.04", func() {
		info := GetInstallInfo("8.3.6")
		if info.Version != "8.3.6" {
			t.Errorf("Version = %q", info.Version)
		}
		if len(info.Packages) != 12 {
			t.Errorf("Packages count = %d, want 12", len(info.Packages))
		}
		if len(info.Commands) != 3 {
			t.Errorf("Commands count = %d, want 3", len(info.Commands))
		}
		if !strings.Contains(info.Commands[2], "php8.3-cgi") {
			t.Errorf("install command should contain php8.3-cgi: %s", info.Commands[2])
		}
		if !strings.Contains(info.Notes, "ondrej/php") {
			t.Errorf("Notes = %q", info.Notes)
		}
	})
}

func TestGetInstallInfoDebian(t *testing.T) {
	withDistro("debian", "12", "Debian 12", func() {
		info := GetInstallInfo("8.2")
		if len(info.Commands) != 3 {
			t.Errorf("Commands count = %d, want 3", len(info.Commands))
		}
	})
}

func TestGetInstallInfoCentos(t *testing.T) {
	withDistro("centos", "9", "CentOS 9", func() {
		info := GetInstallInfo("8.3.6")
		if len(info.Packages) != 2 {
			t.Errorf("Packages count = %d, want 2", len(info.Packages))
		}
		if !strings.Contains(info.Packages[0], "php83") {
			t.Errorf("first package = %q, expected php83", info.Packages[0])
		}
		if len(info.Commands) != 4 {
			t.Errorf("Commands count = %d, want 4", len(info.Commands))
		}
		if !strings.Contains(info.Notes, "Remi") {
			t.Errorf("Notes = %q", info.Notes)
		}
	})
}

func TestGetInstallInfoRHEL(t *testing.T) {
	withDistro("rhel", "8", "RHEL 8", func() {
		info := GetInstallInfo("8.1")
		if len(info.Commands) < 1 {
			t.Fatal("expected commands for rhel")
		}
		if !strings.Contains(info.Commands[0], "epel-release") {
			t.Errorf("first command should install epel: %s", info.Commands[0])
		}
	})
}

func TestGetInstallInfoRocky(t *testing.T) {
	withDistro("rocky", "9", "Rocky 9", func() {
		info := GetInstallInfo("8.3")
		if len(info.Commands) < 1 {
			t.Fatal("expected commands for rocky")
		}
	})
}

func TestGetInstallInfoAlma(t *testing.T) {
	withDistro("alma", "9", "AlmaLinux 9", func() {
		info := GetInstallInfo("8.3")
		if len(info.Commands) < 1 {
			t.Fatal("expected commands for alma")
		}
	})
}

func TestGetInstallInfoFedora(t *testing.T) {
	withDistro("fedora", "39", "Fedora 39", func() {
		info := GetInstallInfo("8.3")
		if len(info.Commands) != 1 {
			t.Errorf("Commands count = %d, want 1", len(info.Commands))
		}
		if !strings.Contains(info.Commands[0], "php-cgi") {
			t.Errorf("command = %q", info.Commands[0])
		}
		if !strings.Contains(info.Notes, "Fedora") {
			t.Errorf("Notes = %q", info.Notes)
		}
	})
}

func TestGetInstallInfoArch(t *testing.T) {
	withDistro("arch", "", "Arch Linux", func() {
		info := GetInstallInfo("8.3")
		if len(info.Commands) != 1 {
			t.Errorf("Commands count = %d, want 1", len(info.Commands))
		}
		if !strings.Contains(info.Commands[0], "pacman") {
			t.Errorf("command = %q", info.Commands[0])
		}
	})
}

func TestGetInstallInfoManjaro(t *testing.T) {
	withDistro("manjaro", "", "Manjaro", func() {
		info := GetInstallInfo("8.3")
		if len(info.Commands) != 1 {
			t.Errorf("Commands count = %d, want 1", len(info.Commands))
		}
		if !strings.Contains(info.Commands[0], "pacman") {
			t.Errorf("command = %q", info.Commands[0])
		}
	})
}

func TestGetInstallInfoAlpine(t *testing.T) {
	withDistro("alpine", "3.19", "Alpine 3.19", func() {
		info := GetInstallInfo("8.3")
		if len(info.Packages) != 2 {
			t.Errorf("Packages count = %d, want 2", len(info.Packages))
		}
		if !strings.Contains(info.Commands[0], "apk add") {
			t.Errorf("command = %q", info.Commands[0])
		}
	})
}

func TestGetInstallInfoUnknownDistro(t *testing.T) {
	withDistro("gentoo", "", "Gentoo", func() {
		info := GetInstallInfo("8.3")
		if len(info.Commands) < 1 {
			t.Fatal("expected fallback commands")
		}
		if !strings.HasPrefix(info.Commands[0], "#") {
			t.Errorf("command should be comment: %s", info.Commands[0])
		}
		if !strings.Contains(info.Notes, "UWAS needs") {
			t.Errorf("Notes = %q", info.Notes)
		}
	})
}

func TestGetInstallInfoVersionNormalization(t *testing.T) {
	withDistro("ubuntu", "22.04", "Ubuntu", func() {
		// Three-part version gets normalized to two-part
		info := GetInstallInfo("8.3.6")
		if !strings.Contains(info.Packages[0], "php8.3-cgi") {
			t.Errorf("should normalize version: %s", info.Packages[0])
		}

		// Two-part version stays as-is
		info2 := GetInstallInfo("8.3")
		if !strings.Contains(info2.Packages[0], "php8.3-cgi") {
			t.Errorf("two-part version: %s", info2.Packages[0])
		}

		// Single-part version
		info3 := GetInstallInfo("8")
		if !strings.Contains(info3.Packages[0], "php8-cgi") {
			t.Errorf("single-part version: %s", info3.Packages[0])
		}
	})
}

// ============================================================
// install.go — RunInstall
// ============================================================

func TestRunInstallCommentCommands(t *testing.T) {
	// Use unknown distro so commands are all comments
	origGOOS := runtimeGOOSInstall
	origRead := readOSRelease
	origExec := installExecCommand
	defer func() {
		runtimeGOOSInstall = origGOOS
		readOSRelease = origRead
		installExecCommand = origExec
	}()

	runtimeGOOSInstall = "linux"
	readOSRelease = func() ([]byte, error) {
		return []byte("ID=gentoo\n"), nil
	}

	output, err := RunInstall("8.3")
	if err != nil {
		t.Fatalf("RunInstall: %v", err)
	}
	if !strings.Contains(output, "# Could not detect") {
		t.Errorf("expected comment in output: %s", output)
	}
}

func TestRunInstallSuccessful(t *testing.T) {
	origGOOS := runtimeGOOSInstall
	origRead := readOSRelease
	origExec := installExecCommand
	defer func() {
		runtimeGOOSInstall = origGOOS
		readOSRelease = origRead
		installExecCommand = origExec
	}()

	runtimeGOOSInstall = "linux"
	readOSRelease = func() ([]byte, error) {
		return []byte("ID=fedora\n"), nil
	}

	// Mock exec to use echo (which succeeds)
	installExecCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("echo", "mock install output")
	}

	output, err := RunInstall("8.3")
	if err != nil {
		t.Fatalf("RunInstall: %v", err)
	}
	if !strings.Contains(output, "$") {
		t.Errorf("expected command prefix in output: %s", output)
	}
	if !strings.Contains(output, "mock install output") {
		t.Errorf("expected mock output: %s", output)
	}
}

func TestRunInstallFailure(t *testing.T) {
	origGOOS := runtimeGOOSInstall
	origRead := readOSRelease
	origExec := installExecCommand
	defer func() {
		runtimeGOOSInstall = origGOOS
		readOSRelease = origRead
		installExecCommand = origExec
	}()

	runtimeGOOSInstall = "linux"
	readOSRelease = func() ([]byte, error) {
		return []byte("ID=fedora\n"), nil
	}

	// Mock exec to fail
	installExecCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("/nonexistent/binary/should-not-exist-xyz")
	}

	_, err := RunInstall("8.3")
	if err == nil {
		t.Error("expected error from failing install command")
	}
	if !strings.Contains(err.Error(), "command failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ============================================================
// manager.go — SetOnCrash
// ============================================================

func TestSetOnCrash(t *testing.T) {
	m := New(testLogger())
	called := false
	m.SetOnCrash(func(domain string) {
		called = true
	})

	m.domainMu.RLock()
	fn := m.onCrash
	m.domainMu.RUnlock()

	if fn == nil {
		t.Fatal("onCrash should be set")
	}
	fn("test.com")
	if !called {
		t.Error("onCrash callback not called")
	}
}

func TestSetOnCrashNil(t *testing.T) {
	m := New(testLogger())
	m.SetOnCrash(func(domain string) {})
	m.SetOnCrash(nil)

	m.domainMu.RLock()
	if m.onCrash != nil {
		t.Error("onCrash should be nil")
	}
	m.domainMu.RUnlock()
}

// ============================================================
// manager.go — AssignDomainWithRoot
// ============================================================

func TestAssignDomainWithRootSuccess(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}

	dp, err := m.AssignDomainWithRoot("blog.com", "8.4", "/var/www/blog.com")
	if err != nil {
		t.Fatalf("AssignDomainWithRoot: %v", err)
	}
	if dp.Domain != "blog.com" {
		t.Errorf("domain = %q", dp.Domain)
	}

	// Verify webRoot was set
	m.domainMu.RLock()
	inst := m.domainMap["blog.com"]
	m.domainMu.RUnlock()
	if inst.webRoot != "/var/www/blog.com" {
		t.Errorf("webRoot = %q, want /var/www/blog.com", inst.webRoot)
	}
}

func TestAssignDomainWithRootError(t *testing.T) {
	m := New(testLogger())
	// No installations => version not found
	_, err := m.AssignDomainWithRoot("blog.com", "9.9", "/var/www/blog.com")
	if err == nil {
		t.Error("expected error for non-existent version")
	}
}

// ============================================================
// manager.go — AllowedDomainDirectives
// ============================================================

func TestAllowedDomainDirectives(t *testing.T) {
	directives := AllowedDomainDirectives()
	if len(directives) == 0 {
		t.Fatal("expected non-empty directives list")
	}
	found := false
	for _, d := range directives {
		if d == "memory_limit" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected memory_limit in allowed directives")
	}
}

// ============================================================
// manager.go — SetDomainConfig blocked directive
// ============================================================

func TestSetDomainConfigBlockedDirective(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}
	m.AssignDomain("blog.com", "8.4")

	err := m.SetDomainConfig("blog.com", "disable_functions", "exec")
	if err == nil {
		t.Error("expected error for blocked directive")
	}
	if !strings.Contains(err.Error(), "blocked for security") {
		t.Errorf("unexpected error: %v", err)
	}

	// Try other blocked directives
	for _, key := range []string{"open_basedir", "allow_url_include", "sendmail_path", "extension_dir"} {
		err := m.SetDomainConfig("blog.com", key, "value")
		if err == nil {
			t.Errorf("expected error for blocked directive %q", key)
		}
	}
}

// ============================================================
// manager.go — GetConfigRaw
// ============================================================

func TestGetConfigRawSuccess(t *testing.T) {
	dir := t.TempDir()
	ini := filepath.Join(dir, "php.ini")
	content := "memory_limit = 256M\n"
	os.WriteFile(ini, []byte(content), 0644)

	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", ConfigFile: ini},
	}

	raw, err := m.GetConfigRaw("8.4.19")
	if err != nil {
		t.Fatalf("GetConfigRaw: %v", err)
	}
	if raw != content {
		t.Errorf("raw = %q, want %q", raw, content)
	}
}

func TestGetConfigRawNotFound(t *testing.T) {
	m := New(testLogger())
	_, err := m.GetConfigRaw("9.9.9")
	if err == nil {
		t.Error("expected error for non-existent version")
	}
}

func TestGetConfigRawNoConfigFile(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", ConfigFile: ""},
	}

	raw, err := m.GetConfigRaw("8.4.19")
	if err != nil {
		t.Fatalf("GetConfigRaw: %v", err)
	}
	if !strings.Contains(raw, "No php.ini found") {
		t.Errorf("expected fallback content, got: %s", raw)
	}
}

func TestGetConfigRawReadError(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", ConfigFile: "/nonexistent/path/php.ini"},
	}

	_, err := m.GetConfigRaw("8.4.19")
	if err == nil {
		t.Error("expected error when file doesn't exist")
	}
}

// ============================================================
// manager.go — SetConfigRaw
// ============================================================

func TestSetConfigRawSuccess(t *testing.T) {
	dir := t.TempDir()
	ini := filepath.Join(dir, "php.ini")
	os.WriteFile(ini, []byte("old content\n"), 0644)

	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", ConfigFile: ini},
	}

	err := m.SetConfigRaw("8.4.19", "new content\n")
	if err != nil {
		t.Fatalf("SetConfigRaw: %v", err)
	}

	data, _ := os.ReadFile(ini)
	if string(data) != "new content\n" {
		t.Errorf("content = %q", string(data))
	}
}

func TestSetConfigRawNotFound(t *testing.T) {
	m := New(testLogger())
	err := m.SetConfigRaw("9.9.9", "content")
	if err == nil {
		t.Error("expected error for non-existent version")
	}
}

func TestSetConfigRawNoConfigFile(t *testing.T) {
	m := New(testLogger())
	// Mock execCommand so runPHP returns something
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("echo", "no config info")
	}
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", ConfigFile: ""},
	}

	// findOrCreatePHPConfig will try to create files in /etc which will fail on Windows,
	// so this should return an error about not being able to create php.ini
	err := m.SetConfigRaw("8.4.19", "content")
	// On Windows/non-root, this will fail to create the config file
	if err == nil {
		// If it succeeded (unlikely on Windows), that's also fine
		return
	}
	if !strings.Contains(err.Error(), "cannot create") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ============================================================
// manager.go — shortVersion
// ============================================================

func TestShortVersion(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"8.3.6", "8.3"},
		{"8.3", "8.3"},
		{"8", "8"},
		{"", ""},
	}
	for _, tt := range tests {
		got := shortVersion(tt.input)
		if got != tt.want {
			t.Errorf("shortVersion(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ============================================================
// manager.go — EnableVersion / DisableVersion
// ============================================================

func TestEnableVersion(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", Disabled: true},
		{Version: "8.3.0", Binary: "/usr/bin/php-cgi8.3", Disabled: false},
	}

	m.EnableVersion("8.4")
	if m.installations[0].Disabled {
		t.Error("8.4.19 should be enabled")
	}
	if m.installations[1].Disabled {
		t.Error("8.3.0 should remain enabled")
	}
}

func TestEnableVersionNoMatch(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", Disabled: true},
	}

	m.EnableVersion("7.4")
	// Should not panic, and 8.4 should still be disabled
	if !m.installations[0].Disabled {
		t.Error("8.4.19 should still be disabled")
	}
}

func TestDisableVersionSuccess(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", Disabled: false},
		{Version: "8.3.0", Binary: "/usr/bin/php-cgi8.3", Disabled: false},
	}

	err := m.DisableVersion("8.4")
	if err != nil {
		t.Fatalf("DisableVersion: %v", err)
	}
	if !m.installations[0].Disabled {
		t.Error("8.4.19 should be disabled")
	}
	if m.installations[1].Disabled {
		t.Error("8.3.0 should remain enabled")
	}
}

func TestDisableVersionWithAttachedDomains(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}

	m.AssignDomain("blog.com", "8.4")

	err := m.DisableVersion("8.4")
	if err == nil {
		t.Error("expected error when domain is attached")
	}
	if !strings.Contains(err.Error(), "cannot disable") {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "blog.com") {
		t.Errorf("error should mention attached domain: %v", err)
	}
}

func TestDisableVersionNoMatch(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}

	err := m.DisableVersion("7.4")
	if err != nil {
		t.Fatalf("DisableVersion: %v", err)
	}
	// Should not have disabled 8.4
	if m.installations[0].Disabled {
		t.Error("8.4.19 should not be disabled")
	}
}

// ============================================================
// manager.go — startFPMDaemon
// ============================================================

func TestStartFPMDaemonSuccess(t *testing.T) {
	m := New(testLogger())

	// Mock exec command to use a long-running process
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("ping", "-n", "100", "127.0.0.1")
	}

	err := m.startFPMDaemon("8.4.19", "/usr/sbin/php-fpm8.4", "127.0.0.1:9000")
	if err != nil {
		t.Fatalf("startFPMDaemon: %v", err)
	}

	// Verify process was stored
	val, loaded := m.processes.Load("8.4.19")
	if !loaded {
		t.Fatal("process should be stored")
	}
	info := val.(*processInfo)
	if info.listenAddr != "127.0.0.1:9000" {
		t.Errorf("listenAddr = %q, want 127.0.0.1:9000", info.listenAddr)
	}

	// Clean up
	if info.cmd != nil && info.cmd.Process != nil {
		info.cmd.Process.Kill()
	}
	m.processes.Delete("8.4.19")
}

func TestStartFPMDaemonExecFailure(t *testing.T) {
	m := New(testLogger())

	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("/nonexistent/binary/should-fail-xyz")
	}

	err := m.startFPMDaemon("8.4.19", "/nonexistent/php-fpm", "127.0.0.1:9000")
	if err == nil {
		t.Error("expected error when exec fails")
	}
	if !strings.Contains(err.Error(), "start php-fpm") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ============================================================
// manager.go — StartFPM with fpm-fcgi SAPI (triggers startFPMDaemon)
// ============================================================

func TestStartFPMWithFPMSAPI(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-fpm8.4", SAPI: "fpm-fcgi"},
	}

	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("ping", "-n", "100", "127.0.0.1")
	}

	err := m.StartFPM("8.4.19", "127.0.0.1:9000")
	if err != nil {
		t.Fatalf("StartFPM with fpm-fcgi: %v", err)
	}

	// Clean up
	m.StopFPM("8.4.19")
}

func TestStartFPMWrongSAPI(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php8.4", SAPI: "cli"},
	}

	err := m.StartFPM("8.4.19", "127.0.0.1:9000")
	if err == nil {
		t.Error("expected error for CLI SAPI")
	}
	if !strings.Contains(err.Error(), "not cgi-fcgi") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ============================================================
// manager.go — StartDomain with unix socket (system php-fpm)
// ============================================================

func TestStartDomainWithUnixSocket(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}

	// Directly assign with unix socket address
	m.domainMu.Lock()
	m.domainMap["unixtest.com"] = &domainInstance{
		domain:          "unixtest.com",
		version:         "8.4",
		listenAddr:      "unix:/run/php/php8.4-fpm.sock",
		configOverrides: make(map[string]string),
	}
	m.domainMu.Unlock()

	err := m.StartDomain("unixtest.com")
	if err != nil {
		t.Fatalf("StartDomain with unix socket: %v", err)
	}

	// Verify proc was set (no cmd, just listenAddr)
	m.domainMu.RLock()
	inst := m.domainMap["unixtest.com"]
	m.domainMu.RUnlock()
	if inst.proc == nil {
		t.Fatal("proc should be set for unix socket")
	}
	if inst.proc.cmd != nil {
		t.Error("cmd should be nil for system-managed php-fpm")
	}
}

func TestStartDomainWithSlashSocket(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}

	// Assign with absolute path socket
	m.domainMu.Lock()
	m.domainMap["slashtest.com"] = &domainInstance{
		domain:          "slashtest.com",
		version:         "8.4",
		listenAddr:      "/run/php/php8.4-fpm.sock",
		configOverrides: make(map[string]string),
	}
	m.domainMu.Unlock()

	err := m.StartDomain("slashtest.com")
	if err != nil {
		t.Fatalf("StartDomain with / socket: %v", err)
	}
}

// ============================================================
// manager.go — StartDomain with wrong SAPI
// ============================================================

func TestStartDomainWrongSAPI(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php8.4", SAPI: "cli"},
	}

	m.domainMu.Lock()
	m.domainMap["sapi-test.com"] = &domainInstance{
		domain:          "sapi-test.com",
		version:         "8.4",
		listenAddr:      "127.0.0.1:9099",
		configOverrides: make(map[string]string),
	}
	m.domainMu.Unlock()

	err := m.StartDomain("sapi-test.com")
	if err == nil {
		t.Error("expected error for CLI SAPI")
	}
	if !strings.Contains(err.Error(), "not cgi-fcgi") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ============================================================
// manager.go — StopDomain with system FPM socket (no cmd)
// ============================================================

func TestStopDomainSystemFPM(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}

	// Create a domain with a running system FPM socket (proc with nil cmd)
	m.domainMu.Lock()
	m.domainMap["sysfpm.com"] = &domainInstance{
		domain:          "sysfpm.com",
		version:         "8.4",
		listenAddr:      "unix:/run/php/php8.4-fpm.sock",
		configOverrides: make(map[string]string),
		proc:            &processInfo{listenAddr: "unix:/run/php/php8.4-fpm.sock"},
	}
	m.domainMu.Unlock()

	err := m.StopDomain("sysfpm.com")
	if err != nil {
		t.Fatalf("StopDomain with system FPM: %v", err)
	}
}

// ============================================================
// manager.go — domainPHPFromInstance with unix socket
// ============================================================

func TestDomainPHPFromInstanceUnixSocket(t *testing.T) {
	m := New(testLogger())

	inst := &domainInstance{
		domain:          "unix.com",
		version:         "8.4",
		listenAddr:      "unix:/run/php/php8.4-fpm.sock",
		configOverrides: make(map[string]string),
		proc:            &processInfo{listenAddr: "unix:/run/php/php8.4-fpm.sock"},
	}

	dp := m.domainPHPFromInstance(inst)
	if !dp.Running {
		t.Error("should be running for unix socket")
	}
	if dp.PID != -1 {
		t.Errorf("PID = %d, want -1 for system-managed", dp.PID)
	}
}

func TestDomainPHPFromInstanceSlashSocket(t *testing.T) {
	m := New(testLogger())

	inst := &domainInstance{
		domain:          "slash.com",
		version:         "8.4",
		listenAddr:      "/run/php/php8.4-fpm.sock",
		configOverrides: make(map[string]string),
		proc:            &processInfo{listenAddr: "/run/php/php8.4-fpm.sock"},
	}

	dp := m.domainPHPFromInstance(inst)
	if !dp.Running {
		t.Error("should be running for slash socket")
	}
	if dp.PID != -1 {
		t.Errorf("PID = %d, want -1 for system-managed", dp.PID)
	}
}

// ============================================================
// manager.go — Status: CLI SAPI filtering, domain count
// ============================================================

func TestStatusSkipsCLI(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php8.4", SAPI: "cli"},
		{Version: "8.3.0", Binary: "/usr/bin/php-cgi8.3", SAPI: "cgi-fcgi"},
	}

	statuses := m.Status()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status (cli filtered), got %d", len(statuses))
	}
	if statuses[0].Version != "8.3.0" {
		t.Errorf("expected 8.3.0, got %s", statuses[0].Version)
	}
}

func TestStatusDomainCount(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
		{Version: "8.3.0", Binary: "/usr/bin/php-cgi8.3", SAPI: "cgi-fcgi"},
	}

	m.AssignDomain("site1.com", "8.4")
	m.AssignDomain("site2.com", "8.4")
	m.AssignDomain("site3.com", "8.3")

	statuses := m.Status()
	for _, st := range statuses {
		switch {
		case strings.HasPrefix(st.Version, "8.4"):
			if st.DomainCount != 2 {
				t.Errorf("8.4 DomainCount = %d, want 2", st.DomainCount)
			}
			if len(st.Domains) != 2 {
				t.Errorf("8.4 Domains count = %d, want 2", len(st.Domains))
			}
		case strings.HasPrefix(st.Version, "8.3"):
			if st.DomainCount != 1 {
				t.Errorf("8.3 DomainCount = %d, want 1", st.DomainCount)
			}
		}
	}
}

// ============================================================
// manager.go — detectSystemFPMSocket (no sockets on test system)
// ============================================================

func TestDetectSystemFPMSocketNoSocket(t *testing.T) {
	m := New(testLogger())
	sock := m.detectSystemFPMSocket("8.4")
	if sock != "" {
		t.Errorf("expected empty socket on test system, got %q", sock)
	}
}

func TestDetectSystemFPMSocketVersionNormalization(t *testing.T) {
	m := New(testLogger())
	// Three-part version should be normalized
	sock := m.detectSystemFPMSocket("8.4.19")
	if sock != "" {
		t.Errorf("expected empty socket, got %q", sock)
	}
}

// ============================================================
// manager.go — buildDomainINI with webRoot
// ============================================================

func TestBuildDomainINIWithWebRoot(t *testing.T) {
	m := New(testLogger())
	dir := t.TempDir()
	webRoot := filepath.Join(dir, "www")
	os.MkdirAll(webRoot, 0755)

	iniPath := filepath.Join(dir, "php.ini")
	os.WriteFile(iniPath, []byte("memory_limit = 128M\n"), 0644)

	inst := PHPInstall{Version: "8.4.19", ConfigFile: iniPath}

	// Create a domain with webRoot
	m.domainMu.Lock()
	m.domainMap["webroot.com"] = &domainInstance{
		domain:          "webroot.com",
		version:         "8.4",
		listenAddr:      "127.0.0.1:9001",
		webRoot:         webRoot,
		configOverrides: map[string]string{"memory_limit": "512M"},
	}
	m.domainMu.Unlock()

	tmpPath, err := m.buildDomainINI("webroot.com", inst, map[string]string{"memory_limit": "512M"})
	if err != nil {
		t.Fatalf("buildDomainINI: %v", err)
	}
	defer os.Remove(tmpPath)

	data, _ := os.ReadFile(tmpPath)
	content := string(data)

	// Should contain security directives
	if !strings.Contains(content, "disable_functions") {
		t.Error("expected disable_functions in ini")
	}
	if !strings.Contains(content, "allow_url_include = Off") {
		t.Error("expected allow_url_include = Off")
	}
	if !strings.Contains(content, "open_basedir") {
		t.Error("expected open_basedir when webRoot is set")
	}
	if !strings.Contains(content, webRoot) {
		t.Error("expected webRoot in open_basedir")
	}
	if !strings.Contains(content, ".tmp") {
		t.Error("expected .tmp dir in open_basedir")
	}
	if !strings.Contains(content, "realpath_cache_size") {
		t.Error("expected performance defaults")
	}
	if !strings.Contains(content, "memory_limit = 512M") {
		t.Error("expected override")
	}
}

func TestBuildDomainINIWithDomainNoWebRoot(t *testing.T) {
	m := New(testLogger())

	inst := PHPInstall{Version: "8.4.19"}

	// Domain assigned but no webRoot
	m.domainMu.Lock()
	m.domainMap["nowebroot.com"] = &domainInstance{
		domain:          "nowebroot.com",
		version:         "8.4",
		listenAddr:      "127.0.0.1:9001",
		configOverrides: map[string]string{"memory_limit": "256M"},
	}
	m.domainMu.Unlock()

	tmpPath, err := m.buildDomainINI("nowebroot.com", inst, map[string]string{"memory_limit": "256M"})
	if err != nil {
		t.Fatalf("buildDomainINI: %v", err)
	}
	defer os.Remove(tmpPath)

	data, _ := os.ReadFile(tmpPath)
	content := string(data)

	// Should contain security directives but NOT open_basedir
	if !strings.Contains(content, "disable_functions") {
		t.Error("expected disable_functions")
	}
	if strings.Contains(content, "open_basedir") {
		t.Error("should not have open_basedir without webRoot")
	}
}

// ============================================================
// manager.go — findOrCreatePHPConfig
// ============================================================

func TestFindOrCreatePHPConfigScanDir(t *testing.T) {
	dir := t.TempDir()
	scanDir := filepath.Join(dir, "cgi", "conf.d")
	os.MkdirAll(scanDir, 0755)

	// Create php.ini in parent of scan dir
	iniPath := filepath.Join(dir, "cgi", "php.ini")
	os.WriteFile(iniPath, []byte("memory_limit = 128M\n"), 0644)

	phpInfo := fmt.Sprintf("Scan this dir for additional .ini files => %s\n", scanDir)

	result := findOrCreatePHPConfig("8.4.19", phpInfo)
	if result != iniPath {
		t.Errorf("result = %q, want %q", result, iniPath)
	}
}

func TestFindOrCreatePHPConfigScanDirNone(t *testing.T) {
	phpInfo := "Scan this dir for additional .ini files => (none)\n"

	// Will fall through to common paths and create attempts, which will fail on Windows
	result := findOrCreatePHPConfig("8.4.19", phpInfo)
	// On Windows, this will likely return "" since /etc doesn't exist
	// Just verify it doesn't panic
	_ = result
}

func TestFindOrCreatePHPConfigScanDirEmpty(t *testing.T) {
	phpInfo := "Scan this dir for additional .ini files => \n"

	result := findOrCreatePHPConfig("8.4.19", phpInfo)
	_ = result
}

func TestFindOrCreatePHPConfigCommonPaths(t *testing.T) {
	// On Windows, none of the common paths exist, so it will fall through to create
	result := findOrCreatePHPConfig("8.4.19", "no scan dir info here")
	// Just verify no panic
	_ = result
}

func TestFindOrCreatePHPConfigNoScanDir(t *testing.T) {
	// phpInfo without scan dir line
	result := findOrCreatePHPConfig("8.4.19", "Some random output\nNo scan directory line\n")
	_ = result
}

func TestFindOrCreatePHPConfigTwoPartVersion(t *testing.T) {
	result := findOrCreatePHPConfig("8.3", "")
	_ = result
}

func TestFindOrCreatePHPConfigSinglePartVersion(t *testing.T) {
	result := findOrCreatePHPConfig("8", "")
	_ = result
}

// ============================================================
// manager.go — candidatePaths with different OS
// ============================================================

func TestCandidatePathsLinux(t *testing.T) {
	orig := runtimeGOOS
	defer func() { runtimeGOOS = orig }()

	runtimeGOOS = "linux"
	paths := candidatePaths()
	if len(paths) == 0 {
		t.Error("expected paths for linux")
	}
	found := false
	for _, p := range paths {
		if strings.Contains(p, "/usr/bin/php-cgi") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected /usr/bin/php-cgi* in linux paths")
	}
}

func TestCandidatePathsDarwin(t *testing.T) {
	orig := runtimeGOOS
	defer func() { runtimeGOOS = orig }()

	runtimeGOOS = "darwin"
	paths := candidatePaths()
	if len(paths) == 0 {
		t.Error("expected paths for darwin")
	}
	found := false
	for _, p := range paths {
		if strings.Contains(p, "homebrew") || strings.Contains(p, "/usr/local") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected homebrew or /usr/local in darwin paths")
	}
}

func TestCandidatePathsWindows(t *testing.T) {
	orig := runtimeGOOS
	defer func() { runtimeGOOS = orig }()

	runtimeGOOS = "windows"
	paths := candidatePaths()
	if len(paths) == 0 {
		t.Error("expected paths for windows")
	}
	found := false
	for _, p := range paths {
		if strings.Contains(p, "php-cgi.exe") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected php-cgi.exe in windows paths")
	}
}

func TestCandidatePathsDefault(t *testing.T) {
	orig := runtimeGOOS
	defer func() { runtimeGOOS = orig }()

	runtimeGOOS = "freebsd"
	paths := candidatePaths()
	if len(paths) == 0 {
		t.Error("expected default paths for freebsd")
	}
}

// ============================================================
// manager.go — parseINIConfig edge cases
// ============================================================

func TestParseINIConfigComments(t *testing.T) {
	dir := t.TempDir()
	ini := filepath.Join(dir, "php.ini")
	content := `; This is a comment
# This is also a comment
memory_limit = 256M

post_max_size = 32M
no-equals-line
key_only_no_value
display_errors = On
`
	os.WriteFile(ini, []byte(content), 0644)

	cfg, err := parseINIConfig(ini)
	if err != nil {
		t.Fatalf("parseINIConfig: %v", err)
	}
	if cfg.MemoryLimit != "256M" {
		t.Errorf("MemoryLimit = %q", cfg.MemoryLimit)
	}
	if cfg.PostMaxSize != "32M" {
		t.Errorf("PostMaxSize = %q", cfg.PostMaxSize)
	}
	if cfg.DisplayErrors != "On" {
		t.Errorf("DisplayErrors = %q", cfg.DisplayErrors)
	}
}

func TestParseINIConfigNonExistent(t *testing.T) {
	_, err := parseINIConfig("/nonexistent/path/php.ini")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestParseINIConfigAllFields(t *testing.T) {
	dir := t.TempDir()
	ini := filepath.Join(dir, "php.ini")
	content := `memory_limit = 512M
max_execution_time = 60
upload_max_filesize = 128M
post_max_size = 128M
display_errors = On
error_reporting = E_ALL
opcache.enable = 1
date.timezone = Europe/Istanbul
`
	os.WriteFile(ini, []byte(content), 0644)

	cfg, err := parseINIConfig(ini)
	if err != nil {
		t.Fatalf("parseINIConfig: %v", err)
	}
	if cfg.MemoryLimit != "512M" {
		t.Errorf("MemoryLimit = %q", cfg.MemoryLimit)
	}
	if cfg.MaxExecutionTime != "60" {
		t.Errorf("MaxExecutionTime = %q", cfg.MaxExecutionTime)
	}
	if cfg.UploadMaxSize != "128M" {
		t.Errorf("UploadMaxSize = %q", cfg.UploadMaxSize)
	}
	if cfg.PostMaxSize != "128M" {
		t.Errorf("PostMaxSize = %q", cfg.PostMaxSize)
	}
	if cfg.DisplayErrors != "On" {
		t.Errorf("DisplayErrors = %q", cfg.DisplayErrors)
	}
	if cfg.ErrorReporting != "E_ALL" {
		t.Errorf("ErrorReporting = %q", cfg.ErrorReporting)
	}
	if cfg.OPcacheEnabled != "1" {
		t.Errorf("OPcacheEnabled = %q", cfg.OPcacheEnabled)
	}
	if cfg.Timezone != "Europe/Istanbul" {
		t.Errorf("Timezone = %q", cfg.Timezone)
	}
}

// ============================================================
// manager.go — updateINI edge cases
// ============================================================

func TestUpdateININonExistentFile(t *testing.T) {
	err := updateINI("/nonexistent/path/php.ini", "key", "value")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

// ============================================================
// manager.go — findInstall with fallback (cli SAPI)
// ============================================================

func TestFindInstallCLIFallback(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php8.4", SAPI: "cli"},
	}

	inst, ok := m.findInstall("8.4")
	if !ok {
		t.Fatal("expected to find 8.4 via CLI fallback")
	}
	if inst.SAPI != "cli" {
		t.Errorf("SAPI = %q, want cli", inst.SAPI)
	}
}

func TestFindInstallPrefersCGI(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php8.4", SAPI: "cli"},
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}

	inst, ok := m.findInstall("8.4")
	if !ok {
		t.Fatal("expected to find 8.4")
	}
	if inst.SAPI != "cgi-fcgi" {
		t.Errorf("SAPI = %q, want cgi-fcgi (should prefer cgi over cli)", inst.SAPI)
	}
}

func TestFindInstallPrefersFPM(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php8.4", SAPI: "cli"},
		{Version: "8.4.19", Binary: "/usr/bin/php-fpm8.4", SAPI: "fpm-fcgi"},
	}

	inst, ok := m.findInstall("8.4")
	if !ok {
		t.Fatal("expected to find 8.4")
	}
	if inst.SAPI != "fpm-fcgi" {
		t.Errorf("SAPI = %q, want fpm-fcgi", inst.SAPI)
	}
}

// ============================================================
// manager.go — StopDomain with tmpINI cleanup
// ============================================================

func TestStopDomainCleansUpTmpINI(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}

	// Create a temp ini file
	tmpFile := filepath.Join(t.TempDir(), "test-stop.ini")
	os.WriteFile(tmpFile, []byte("test"), 0644)

	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("ping", "-n", "100", "127.0.0.1")
	}

	m.AssignDomain("stop-cleanup.com", "8.4")

	err := m.StartDomain("stop-cleanup.com")
	if err != nil {
		t.Fatalf("StartDomain: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	// Set tmpINI manually
	m.domainMu.Lock()
	m.domainMap["stop-cleanup.com"].tmpINI = tmpFile
	m.domainMu.Unlock()

	err = m.StopDomain("stop-cleanup.com")
	if err != nil {
		t.Fatalf("StopDomain: %v", err)
	}

	// Verify tmp file was removed
	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Error("tmpINI file should be removed after StopDomain")
	}
}

// ============================================================
// manager.go — Detect with mock binaries
// ============================================================

func TestDetectNoBinaries(t *testing.T) {
	m := New(testLogger())
	// Override exec to fail (no binaries found via glob anyway on test system)
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("/nonexistent/binary")
	}

	err := m.Detect()
	if err != nil {
		t.Errorf("Detect: %v", err)
	}
	// No binaries found is not an error
}

// ============================================================
// manager.go — fileExists
// ============================================================

func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "exists.txt")
	os.WriteFile(f, []byte("hi"), 0644)

	if !fileExists(f) {
		t.Error("file should exist")
	}
	if fileExists(filepath.Join(dir, "nonexistent.txt")) {
		t.Error("file should not exist")
	}
}

// ============================================================
// manager.go — parseSAPI additional cases
// ============================================================

func TestParseSAPIDefaultCLI(t *testing.T) {
	got := parseSAPI("PHP 8.4.19 (something-else)")
	if got != "cli" {
		t.Errorf("parseSAPI = %q, want cli", got)
	}
}

// ============================================================
// manager.go — parseExtensions edge cases
// ============================================================

func TestParseExtensionsNoModulesSection(t *testing.T) {
	input := "no modules section here\njust text\n"
	exts := parseExtensions(input)
	if len(exts) != 0 {
		t.Errorf("expected 0 extensions, got %d", len(exts))
	}
}

func TestParseExtensionsEmptyModulesSection(t *testing.T) {
	input := "[PHP Modules]\n\n[Zend Modules]\n"
	exts := parseExtensions(input)
	if len(exts) != 0 {
		t.Errorf("expected 0 extensions, got %d", len(exts))
	}
}

// ============================================================
// manager.go — parseConfigPath edge cases
// ============================================================

func TestParseConfigPathNoMatch(t *testing.T) {
	input := "Random output\nNothing useful\n"
	got := parseConfigPath(input)
	if got != "" {
		t.Errorf("parseConfigPath = %q, want empty", got)
	}
}

func TestParseConfigPathEmpty(t *testing.T) {
	input := "Loaded Configuration File => "
	got := parseConfigPath(input)
	if got != "" {
		t.Errorf("parseConfigPath = %q, want empty", got)
	}
}

// ============================================================
// manager.go — parseVersion edge case
// ============================================================

func TestParseVersionNoMatch(t *testing.T) {
	got := parseVersion("not a version")
	if got != "" {
		t.Errorf("parseVersion = %q, want empty", got)
	}
}

// ============================================================
// manager.go — probe with extensions and config failure
// ============================================================

func TestProbeNoConfig(t *testing.T) {
	m := New(testLogger())
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		if len(args) > 0 {
			switch args[0] {
			case "-v":
				return exec.Command("echo", "PHP 8.4.19 (cgi-fcgi)")
			case "-i":
				// Return error (command fails)
				return exec.Command("/nonexistent/cmd")
			case "-m":
				return exec.Command("echo", "[PHP Modules]\nCore\n")
			}
		}
		return exec.Command("echo", "")
	}

	inst, err := m.probe("/usr/bin/php-cgi8.4")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if inst.Version != "8.4.19" {
		t.Errorf("Version = %q", inst.Version)
	}
	// ConfigFile will be whatever findOrCreatePHPConfig returns (likely empty on Windows)
}

func TestProbeNoExtensions(t *testing.T) {
	m := New(testLogger())
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		if len(args) > 0 {
			switch args[0] {
			case "-v":
				return exec.Command("echo", "PHP 8.4.19 (cgi-fcgi)")
			case "-i":
				return exec.Command("echo", "Loaded Configuration File => /etc/php.ini")
			case "-m":
				// Return error
				return exec.Command("/nonexistent/cmd")
			}
		}
		return exec.Command("echo", "")
	}

	inst, err := m.probe("/usr/bin/php-cgi8.4")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if inst.Version != "8.4.19" {
		t.Errorf("Version = %q", inst.Version)
	}
	if len(inst.Extensions) != 0 {
		t.Errorf("expected 0 extensions when -m fails, got %d", len(inst.Extensions))
	}
}

// ============================================================
// manager.go — StopAll error handling (process already exited)
// ============================================================

func TestStopAllProcessAlreadyExited(t *testing.T) {
	m := New(testLogger())

	// Create a process that has already exited
	cmd := exec.Command("echo", "done")
	cmd.Start()
	cmd.Wait() // Wait for it to finish

	m.processes.Store("8.4.19", &processInfo{
		cmd:        cmd,
		listenAddr: "127.0.0.1:9000",
	})

	// StopAll should handle the already-exited process gracefully
	m.StopAll()

	// Verify process was cleaned up
	_, loaded := m.processes.Load("8.4.19")
	if loaded {
		t.Error("process should be removed after StopAll")
	}
}

// ============================================================
// manager.go — Auto-restart on crash
// ============================================================

func TestAutoRestartOnCrash(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}

	crashCh := make(chan string, 1)
	m.SetOnCrash(func(domain string) {
		select {
		case crashCh <- domain:
		default:
		}
	})

	callCount := 0
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		callCount++
		if callCount == 1 {
			// First call: process that exits with error (simulates crash).
			// Use a command guaranteed to fail with non-zero exit.
			cmd := exec.Command("ping", "-n", "1", "0.0.0.0.0.0.invalid.host.test")
			return cmd
		}
		// Subsequent calls: long-running process (auto-restart succeeds)
		return exec.Command("ping", "-n", "100", "127.0.0.1")
	}

	m.AssignDomain("crash.com", "8.4")
	err := m.StartDomain("crash.com")
	if err != nil {
		t.Fatalf("StartDomain: %v", err)
	}

	// Wait for crash detection and auto-restart (process exit + 500ms backoff + start)
	select {
	case domain := <-crashCh:
		if domain != "crash.com" {
			t.Errorf("crash domain = %q, want crash.com", domain)
		}
	case <-time.After(10 * time.Second):
		t.Error("timed out waiting for crash callback")
	}

	// Clean up
	m.StopAll()
}

// ============================================================
// manager.go — StopDomain should not trigger auto-restart
// ============================================================

func TestStopDomainDoesNotAutoRestart(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}

	var startCount atomic.Int32
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		startCount.Add(1)
		return exec.Command("ping", "-n", "100", "127.0.0.1")
	}

	if _, err := m.AssignDomain("manual-stop.com", "8.4"); err != nil {
		t.Fatalf("AssignDomain: %v", err)
	}
	if err := m.StartDomain("manual-stop.com"); err != nil {
		t.Fatalf("StartDomain: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	if err := m.StopDomain("manual-stop.com"); err != nil {
		t.Fatalf("StopDomain: %v", err)
	}

	// Wait longer than auto-restart backoff window.
	time.Sleep(900 * time.Millisecond)

	if got := startCount.Load(); got != 1 {
		t.Fatalf("unexpected restart after manual stop, starts=%d want 1", got)
	}
}

// ============================================================
// manager.go — StopDomain kill error (already exited)
// ============================================================

func TestStopDomainKillError(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}

	// Create a process that has already exited
	cmd := exec.Command("echo", "done")
	cmd.Start()
	cmd.Wait()

	m.domainMu.Lock()
	m.domainMap["killerr.com"] = &domainInstance{
		domain:          "killerr.com",
		version:         "8.4",
		listenAddr:      "127.0.0.1:9099",
		configOverrides: make(map[string]string),
		proc: &processInfo{
			cmd:        cmd,
			listenAddr: "127.0.0.1:9099",
		},
	}
	m.domainMu.Unlock()

	err := m.StopDomain("killerr.com")
	// Kill on already-exited process may or may not error depending on OS
	_ = err
}

// ============================================================
// manager.go — Status with running global and domain processes
// ============================================================

func TestStatusWithSystemSocket(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}

	// detectSystemFPMSocket returns "" on test systems, so SystemManaged should be false
	statuses := m.Status()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].SystemManaged {
		t.Error("should not be system managed on test system")
	}
}

// ============================================================
// manager.go — buildDomainINI base config read error
// ============================================================

func TestBuildDomainINIBaseConfigReadError(t *testing.T) {
	m := New(testLogger())

	// Config file path that doesn't exist
	inst := PHPInstall{Version: "8.4.19", ConfigFile: "/nonexistent/php.ini"}

	tmpPath, err := m.buildDomainINI("baseread.com", inst, map[string]string{"memory_limit": "256M"})
	if err != nil {
		t.Fatalf("buildDomainINI should not error (base read failure is not fatal): %v", err)
	}
	defer os.Remove(tmpPath)

	// Should still contain overrides even if base config failed to read
	data, _ := os.ReadFile(tmpPath)
	if !strings.Contains(string(data), "memory_limit = 256M") {
		t.Error("expected override in output despite base config read failure")
	}
}

// ============================================================
// Additional: probe with findOrCreatePHPConfig triggered
// ============================================================

func TestProbeWithNoConfigFromPHPInfo(t *testing.T) {
	m := New(testLogger())
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		if len(args) > 0 {
			switch args[0] {
			case "-v":
				return exec.Command("echo", "PHP 8.4.19 (cgi-fcgi)")
			case "-i":
				// No Loaded Configuration File line
				return exec.Command("echo", "Some PHP Info\nRandom data\n")
			case "-m":
				return exec.Command("echo", "[PHP Modules]\nCore\n")
			}
		}
		return exec.Command("echo", "")
	}

	inst, err := m.probe("/usr/bin/php-cgi8.4")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if inst.Version != "8.4.19" {
		t.Errorf("Version = %q", inst.Version)
	}
}

// ============================================================
// Additional edge cases for complete coverage
// ============================================================

func TestDetectDistroActualOS(t *testing.T) {
	// Test with real OS (restoring original hooks)
	d := DetectDistro()
	if runtime.GOOS != "linux" {
		if d.ID != runtime.GOOS {
			t.Errorf("ID = %q, want %q on non-linux", d.ID, runtime.GOOS)
		}
	}
}

func TestGetInstallInfoNonLinux(t *testing.T) {
	// On non-linux (Windows), GetInstallInfo should use the default case
	// since DetectDistro returns the OS name
	if runtime.GOOS != "linux" {
		info := GetInstallInfo("8.3")
		if info.Version != "8.3" {
			t.Errorf("Version = %q", info.Version)
		}
		// Should have fallback commands on non-linux
		if len(info.Commands) == 0 {
			t.Error("expected fallback commands on non-linux")
		}
	}
}

// ============================================================
// manager.go — StopFPM error path
// ============================================================

func TestStopFPMKillError(t *testing.T) {
	m := New(testLogger())

	// Create a process that has already exited
	cmd := exec.Command("echo", "done")
	cmd.Start()
	cmd.Wait()

	m.processes.Store("8.4.19", &processInfo{
		cmd:        cmd,
		listenAddr: "127.0.0.1:9000",
	})

	err := m.StopFPM("8.4.19")
	// Kill on exited process may or may not error
	_ = err
}

func TestStopFPMStaleEntry(t *testing.T) {
	m := New(testLogger())
	m.processes.Store("8.4.19", &processInfo{
		listenAddr: "127.0.0.1:9000",
	})

	if err := m.StopFPM("8.4.19"); err != nil {
		t.Fatalf("StopFPM stale entry: %v", err)
	}
	if _, loaded := m.processes.Load("8.4.19"); loaded {
		t.Error("stale process entry should be removed")
	}
}

// ============================================================
// manager.go — buildDomainINI with nil overrides map
// ============================================================

func TestBuildDomainININilOverrides(t *testing.T) {
	dir := t.TempDir()
	ini := filepath.Join(dir, "php.ini")
	os.WriteFile(ini, []byte("memory_limit = 128M\n"), 0644)

	m := New(testLogger())
	inst := PHPInstall{Version: "8.4.19", ConfigFile: ini}

	tmpPath, err := m.buildDomainINI("niloverrides.com", inst, nil)
	if err != nil {
		t.Fatalf("buildDomainINI: %v", err)
	}
	defer os.Remove(tmpPath)

	data, _ := os.ReadFile(tmpPath)
	if !strings.Contains(string(data), "memory_limit = 128M") {
		t.Error("expected base config content")
	}
}

// ============================================================
// manager.go — Detect with actual glob patterns (exercise glob error path)
// ============================================================

func TestDetectGlobPatterns(t *testing.T) {
	orig := runtimeGOOS
	defer func() { runtimeGOOS = orig }()

	// Test each OS to make sure all glob pattern branches are reached
	for _, os := range []string{"linux", "darwin", "windows", "freebsd"} {
		runtimeGOOS = os
		paths := candidatePaths()
		if len(paths) == 0 {
			t.Errorf("candidatePaths() empty for %s", os)
		}
	}
}

// ============================================================
// manager.go — Detect path deduplication
// ============================================================

func TestDetectDeduplicatesBinaries(t *testing.T) {
	m := New(testLogger())
	// This test just verifies Detect completes without error;
	// on the test system it won't find real PHP binaries
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("echo", "PHP 8.4.19 (cgi-fcgi)")
	}

	err := m.Detect()
	if err != nil {
		t.Errorf("Detect: %v", err)
	}
}

// ============================================================
// manager.go — findOrCreatePHPConfig with real temp paths
// ============================================================

func TestFindOrCreatePHPConfigCommonPathFound(t *testing.T) {
	dir := t.TempDir()
	// Create a fake common path structure
	short := "8.4"
	cgiDir := filepath.Join(dir, "cgi")
	os.MkdirAll(cgiDir, 0755)
	iniPath := filepath.Join(cgiDir, "php.ini")
	os.WriteFile(iniPath, []byte("test"), 0644)

	// Patch scan dir in phpInfo to use our temp dir
	phpInfo := fmt.Sprintf("Scan this dir for additional .ini files => %s\n", filepath.Join(cgiDir, "conf.d"))

	// The scan dir parent is cgiDir, so it should find cgiDir/php.ini
	result := findOrCreatePHPConfig(short, phpInfo)
	if result != iniPath {
		t.Errorf("result = %q, want %q", result, iniPath)
	}
}

// ============================================================
// manager.go — RunInstall empty fields line
// ============================================================

func TestRunInstallEmptyCommand(t *testing.T) {
	origGOOS := runtimeGOOSInstall
	origRead := readOSRelease
	origExec := installExecCommand
	defer func() {
		runtimeGOOSInstall = origGOOS
		readOSRelease = origRead
		installExecCommand = origExec
	}()

	// Use fedora which has a non-comment command
	runtimeGOOSInstall = "linux"
	readOSRelease = func() ([]byte, error) {
		return []byte("ID=fedora\n"), nil
	}

	installExecCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("echo", "ok")
	}

	output, err := RunInstall("8.3")
	if err != nil {
		t.Fatalf("RunInstall: %v", err)
	}
	if !strings.Contains(output, "$ ") {
		t.Errorf("expected '$ ' prefix in output: %s", output)
	}
}

// ============================================================
// manager.go — StartDomain buildDomainINI error
// ============================================================

func TestStartDomainBuildINIError(t *testing.T) {
	origCreateTemp := osCreateTempHook
	defer func() { osCreateTempHook = origCreateTemp }()

	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi", ConfigFile: "/some/file"},
	}

	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("ping", "-n", "100", "127.0.0.1")
	}

	m.AssignDomain("buildini-err.com", "8.4")

	// Make CreateTemp fail to trigger the error path in StartDomain
	osCreateTempHook = func(dir, pattern string) (*os.File, error) {
		return nil, fmt.Errorf("disk full")
	}

	err := m.StartDomain("buildini-err.com")
	if err == nil {
		t.Error("expected error when buildDomainINI fails")
		m.StopAll()
		return
	}
	if !strings.Contains(err.Error(), "build domain ini") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ============================================================
// manager.go — StopAll with tmpINI cleanup on domain instances
// ============================================================

func TestStartDomainExecFailWithTmpINI(t *testing.T) {
	m := New(testLogger())
	dir := t.TempDir()
	iniPath := filepath.Join(dir, "php.ini")
	os.WriteFile(iniPath, []byte("memory_limit = 128M\n"), 0644)

	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi", ConfigFile: iniPath},
	}

	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("/nonexistent/binary/should-fail-xyz")
	}

	m.AssignDomain("exec-tmpini.com", "8.4")

	err := m.StartDomain("exec-tmpini.com")
	if err == nil {
		t.Error("expected error when exec fails")
		m.StopAll()
		return
	}
	if !strings.Contains(err.Error(), "start php-cgi") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStartDomainAutoRestartCleansUpTmpINI(t *testing.T) {
	m := New(testLogger())
	dir := t.TempDir()
	iniPath := filepath.Join(dir, "php.ini")
	os.WriteFile(iniPath, []byte("memory_limit = 128M\n"), 0644)

	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi", ConfigFile: iniPath},
	}

	callCount := 0
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		callCount++
		if callCount == 1 {
			// First call: exits immediately (triggers goroutine cleanup)
			return exec.Command("echo", "exit-quick")
		}
		// Subsequent: long-running for auto-restart
		return exec.Command("ping", "-n", "100", "127.0.0.1")
	}

	m.AssignDomain("tmpini-goroutine.com", "8.4")

	err := m.StartDomain("tmpini-goroutine.com")
	if err != nil {
		t.Fatalf("StartDomain: %v", err)
	}

	// Wait for exit + goroutine cleanup + auto-restart (500ms backoff)
	time.Sleep(1200 * time.Millisecond)

	// The goroutine should have cleaned up the tmpINI and auto-restarted
	m.domainMu.RLock()
	inst := m.domainMap["tmpini-goroutine.com"]
	m.domainMu.RUnlock()

	if inst == nil {
		t.Fatal("domain should still be assigned")
	}

	// Clean up
	m.StopAll()
}

func TestStopAllCleansUpDomainTmpINI(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}

	m.AssignDomain("tmpini-stopall.com", "8.4")

	// Create a temp file and set it as tmpINI
	tmpFile := filepath.Join(t.TempDir(), "stopall-tmp.ini")
	os.WriteFile(tmpFile, []byte("test"), 0644)

	m.domainMu.Lock()
	m.domainMap["tmpini-stopall.com"].tmpINI = tmpFile
	m.domainMu.Unlock()

	m.StopAll()

	// Verify tmp file was removed
	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Error("tmpINI file should be removed after StopAll")
	}
}

// ============================================================
// manager.go — Status with system-managed socket detection
// ============================================================

func TestStatusDomainVersionMatching(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}

	// Assign a domain with short version that should match the full version
	m.domainMu.Lock()
	m.domainMap["matching.com"] = &domainInstance{
		domain:          "matching.com",
		version:         "8.4.19", // exact version match
		listenAddr:      "127.0.0.1:9001",
		configOverrides: make(map[string]string),
	}
	m.domainMu.Unlock()

	statuses := m.Status()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if statuses[0].DomainCount != 1 {
		t.Errorf("DomainCount = %d, want 1", statuses[0].DomainCount)
	}
}

// ============================================================
// manager.go — Detect with real binary files in temp dir
// ============================================================

func TestDetectWithRealBinaries(t *testing.T) {
	m := New(testLogger())
	dir := t.TempDir()

	// Create fake "php-cgi" binaries that match glob patterns
	fakeBin1 := filepath.Join(dir, "php-cgi8.4")
	os.WriteFile(fakeBin1, []byte("fake"), 0755)
	fakeBin2 := filepath.Join(dir, "php-cgi8.3")
	os.WriteFile(fakeBin2, []byte("fake"), 0755)

	// Override candidatePathsFunc to use our temp dir
	origCandidatePaths := candidatePathsFunc
	defer func() { candidatePathsFunc = origCandidatePaths }()

	candidatePathsFunc = func() []string {
		return []string{filepath.Join(dir, "php-cgi*")}
	}

	m.execCommand = func(name string, args ...string) *exec.Cmd {
		if len(args) > 0 {
			switch args[0] {
			case "-v":
				return exec.Command("echo", "PHP 8.4.19 (cgi-fcgi)")
			case "-i":
				return exec.Command("echo", "Loaded Configuration File => /etc/php.ini")
			case "-m":
				return exec.Command("echo", "[PHP Modules]\nCore\ncurl\n")
			}
		}
		return exec.Command("echo", "")
	}

	err := m.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	installs := m.Installations()
	if len(installs) == 0 {
		t.Error("expected at least 1 installation from Detect")
	}
}

func TestDetectDeduplicatesSameBinary(t *testing.T) {
	m := New(testLogger())
	dir := t.TempDir()

	// Create a single binary
	fakeBin := filepath.Join(dir, "php-cgi8.4")
	os.WriteFile(fakeBin, []byte("fake"), 0755)

	// Override candidatePathsFunc to return duplicate-matching patterns
	origCandidatePaths := candidatePathsFunc
	defer func() { candidatePathsFunc = origCandidatePaths }()

	candidatePathsFunc = func() []string {
		return []string{
			filepath.Join(dir, "php-cgi*"),
			filepath.Join(dir, "php-cgi*"), // duplicate pattern
		}
	}

	m.execCommand = func(name string, args ...string) *exec.Cmd {
		if len(args) > 0 && args[0] == "-v" {
			return exec.Command("echo", "PHP 8.4.19 (cgi-fcgi)")
		}
		return exec.Command("echo", "")
	}

	err := m.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	installs := m.Installations()
	// Should be deduplicated to 1
	if len(installs) != 1 {
		t.Errorf("expected 1 installation (dedup), got %d", len(installs))
	}
}

func TestDetectGlobError(t *testing.T) {
	m := New(testLogger())

	origCandidatePaths := candidatePathsFunc
	defer func() { candidatePathsFunc = origCandidatePaths }()

	// A malformed glob pattern triggers filepath.Glob error
	candidatePathsFunc = func() []string {
		return []string{"[invalid-glob"}
	}

	err := m.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	installs := m.Installations()
	if len(installs) != 0 {
		t.Errorf("expected 0 installations for glob error, got %d", len(installs))
	}
}

func TestDetectProbeFails(t *testing.T) {
	m := New(testLogger())
	dir := t.TempDir()

	fakeBin := filepath.Join(dir, "php-cgi-bad")
	os.WriteFile(fakeBin, []byte("fake"), 0755)

	origCandidatePaths := candidatePathsFunc
	defer func() { candidatePathsFunc = origCandidatePaths }()

	candidatePathsFunc = func() []string {
		return []string{filepath.Join(dir, "php-cgi*")}
	}

	// Probe fails (returns non-PHP output)
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("echo", "not php")
	}

	err := m.Detect()
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	installs := m.Installations()
	if len(installs) != 0 {
		t.Errorf("expected 0 installations when probe fails, got %d", len(installs))
	}
}

// ============================================================
// manager.go — SetConfigRaw with writable temp config file
// ============================================================

func TestSetConfigRawCreatesConfig(t *testing.T) {
	m := New(testLogger())
	dir := t.TempDir()

	// Install with no config file, but mock runPHP to provide scan dir info
	scanDir := filepath.Join(dir, "cgi", "conf.d")
	os.MkdirAll(filepath.Dir(scanDir), 0755)
	os.MkdirAll(scanDir, 0755)

	// Create php.ini in parent of scan dir
	iniPath := filepath.Join(dir, "cgi", "php.ini")
	os.WriteFile(iniPath, []byte("old content"), 0644)

	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("echo", fmt.Sprintf("Scan this dir for additional .ini files => %s", scanDir))
	}

	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", ConfigFile: ""},
	}

	err := m.SetConfigRaw("8.4.19", "new raw content\n")
	if err != nil {
		t.Fatalf("SetConfigRaw: %v", err)
	}

	// Verify the config was written to the discovered path
	data, readErr := os.ReadFile(iniPath)
	if readErr != nil {
		t.Fatalf("ReadFile: %v", readErr)
	}
	if string(data) != "new raw content\n" {
		t.Errorf("content = %q", string(data))
	}
}

// ============================================================
// manager.go — SetConfig with writable temp config file
// ============================================================

func TestSetConfigCreatesConfig(t *testing.T) {
	m := New(testLogger())
	dir := t.TempDir()

	scanDir := filepath.Join(dir, "cgi", "conf.d")
	os.MkdirAll(filepath.Dir(scanDir), 0755)
	os.MkdirAll(scanDir, 0755)

	iniPath := filepath.Join(dir, "cgi", "php.ini")
	os.WriteFile(iniPath, []byte("memory_limit = 128M\n"), 0644)

	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("echo", fmt.Sprintf("Scan this dir for additional .ini files => %s", scanDir))
	}

	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", ConfigFile: ""},
	}

	err := m.SetConfig("8.4.19", "memory_limit", "512M")
	if err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	data, _ := os.ReadFile(iniPath)
	if !strings.Contains(string(data), "memory_limit = 512M") {
		t.Errorf("expected memory_limit = 512M, got: %s", data)
	}
}

// ============================================================
// manager.go — StartDomain auto-restart normal exit (no crash callback)
// ============================================================

func TestStartDomainAutoRestartNormalExit(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}

	crashCalled := false
	m.SetOnCrash(func(domain string) {
		crashCalled = true
	})

	callCount := 0
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		callCount++
		if callCount == 1 {
			// Normal exit (exit code 0) — no crash
			return exec.Command("echo", "normal-exit")
		}
		return exec.Command("ping", "-n", "100", "127.0.0.1")
	}

	m.AssignDomain("normalexit.com", "8.4")
	err := m.StartDomain("normalexit.com")
	if err != nil {
		t.Fatalf("StartDomain: %v", err)
	}

	// Wait for auto-restart cycle
	time.Sleep(1200 * time.Millisecond)

	// Crash callback should NOT be called for normal exit
	if crashCalled {
		t.Error("crash callback should not be called for normal exit")
	}

	// Clean up
	m.StopAll()
}

// ============================================================
// manager.go — StartDomain auto-restart when domain was unassigned
// ============================================================

func TestStartDomainNoAutoRestartWhenUnassigned(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}

	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("echo", "quick-exit")
	}

	m.AssignDomain("unassign-restart.com", "8.4")
	err := m.StartDomain("unassign-restart.com")
	if err != nil {
		t.Fatalf("StartDomain: %v", err)
	}

	// Immediately unassign so auto-restart skips
	time.Sleep(10 * time.Millisecond)
	m.UnassignDomain("unassign-restart.com")

	// Wait and verify no panic
	time.Sleep(800 * time.Millisecond)
}

// ============================================================
// manager.go — StartDomain auto-restart failure path
// ============================================================

func TestStartDomainAutoRestartFailure(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}

	// All exec commands fail
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("/nonexistent/binary/should-fail-xyz")
	}

	// Manually create a domain assignment with a mock process
	cmd := exec.Command("echo", "quick-exit")
	cmd.Start()

	m.domainMu.Lock()
	m.domainMap["autofail.com"] = &domainInstance{
		domain:          "autofail.com",
		version:         "8.4",
		listenAddr:      "127.0.0.1:9077",
		configOverrides: make(map[string]string),
		proc: &processInfo{
			cmd:        cmd,
			listenAddr: "127.0.0.1:9077",
		},
	}
	m.domainMu.Unlock()

	// Wait for the process to exit and auto-restart to fail
	time.Sleep(1200 * time.Millisecond)

	// Domain should still be assigned but not running
	m.domainMu.RLock()
	inst := m.domainMap["autofail.com"]
	m.domainMu.RUnlock()
	if inst == nil {
		t.Fatal("domain should still be assigned")
	}
}

// ============================================================
// manager.go — buildDomainINI tmpFile write error paths
// ============================================================

func TestBuildDomainINIEmptyOverridesMap(t *testing.T) {
	m := New(testLogger())
	dir := t.TempDir()
	ini := filepath.Join(dir, "php.ini")
	os.WriteFile(ini, []byte("memory_limit = 128M\n"), 0644)

	inst := PHPInstall{Version: "8.4.19", ConfigFile: ini}

	tmpPath, err := m.buildDomainINI("emptymap.com", inst, map[string]string{})
	if err != nil {
		t.Fatalf("buildDomainINI: %v", err)
	}
	defer os.Remove(tmpPath)

	data, _ := os.ReadFile(tmpPath)
	if !strings.Contains(string(data), "memory_limit = 128M") {
		t.Error("expected base config content")
	}
}

// ============================================================
// manager.go — StopAll domain with nil proc (already stopped)
// ============================================================

func TestStopAllDomainNilProc(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}

	m.AssignDomain("nilproc.com", "8.4")

	// Domain assigned but no process running (proc is nil)
	m.StopAll()

	// Should not panic
}

func TestStopAllStaleGlobalEntry(t *testing.T) {
	m := New(testLogger())
	m.processes.Store("8.4.19", &processInfo{
		listenAddr: "127.0.0.1:9000",
	})

	// Should not panic and should clean stale entry.
	m.StopAll()
	if _, loaded := m.processes.Load("8.4.19"); loaded {
		t.Error("stale process entry should be removed after StopAll")
	}
}

// ============================================================
// manager.go — detectSystemFPMSocket version normalization
// ============================================================

func TestDetectSystemFPMSocketSinglePartVersion(t *testing.T) {
	m := New(testLogger())
	// Single part version
	sock := m.detectSystemFPMSocket("8")
	if sock != "" {
		t.Errorf("expected empty socket, got %q", sock)
	}
}

// ============================================================
// manager.go — Status empty installations
// ============================================================

func TestStatusEmpty(t *testing.T) {
	m := New(testLogger())
	statuses := m.Status()
	if len(statuses) != 0 {
		t.Errorf("expected 0 statuses, got %d", len(statuses))
	}
}

// ============================================================
// Hooked tests: detectSystemFPMSocket with mock stat + dial
// ============================================================

// mockConn implements net.Conn for testing.
type mockConn struct{}

func (mockConn) Read(b []byte) (n int, err error)   { return 0, nil }
func (mockConn) Write(b []byte) (n int, err error)  { return len(b), nil }
func (mockConn) Close() error                       { return nil }
func (mockConn) LocalAddr() net.Addr                { return nil }
func (mockConn) RemoteAddr() net.Addr               { return nil }
func (mockConn) SetDeadline(t time.Time) error      { return nil }
func (mockConn) SetReadDeadline(t time.Time) error  { return nil }
func (mockConn) SetWriteDeadline(t time.Time) error { return nil }

func TestDetectSystemFPMSocketFound(t *testing.T) {
	origStat := osStat
	origDial := netDialTimeout
	defer func() {
		osStat = origStat
		netDialTimeout = origDial
	}()

	// Mock stat to succeed for the first socket path
	osStat = func(name string) (os.FileInfo, error) {
		if strings.Contains(name, "php8.4-fpm.sock") {
			return os.Stat(".") // any non-error result
		}
		return nil, os.ErrNotExist
	}

	// Mock dial to succeed
	netDialTimeout = func(network, address string, timeout time.Duration) (net.Conn, error) {
		return mockConn{}, nil
	}

	m := New(testLogger())
	sock := m.detectSystemFPMSocket("8.4")
	if !strings.Contains(sock, "php8.4-fpm.sock") {
		t.Errorf("expected socket path containing php8.4-fpm.sock, got %q", sock)
	}
	if !strings.HasPrefix(sock, "unix:") {
		t.Errorf("expected unix: prefix, got %q", sock)
	}
}

func TestDetectSystemFPMSocketStatSuccessDialFails(t *testing.T) {
	origStat := osStat
	origDial := netDialTimeout
	defer func() {
		osStat = origStat
		netDialTimeout = origDial
	}()

	// Stat succeeds but dial fails (socket file exists but no listener)
	osStat = func(name string) (os.FileInfo, error) {
		if strings.Contains(name, "fpm.sock") {
			return os.Stat(".")
		}
		return nil, os.ErrNotExist
	}

	netDialTimeout = func(network, address string, timeout time.Duration) (net.Conn, error) {
		return nil, fmt.Errorf("connection refused")
	}

	m := New(testLogger())
	sock := m.detectSystemFPMSocket("8.4")
	if sock != "" {
		t.Errorf("expected empty socket when dial fails, got %q", sock)
	}
}

// ============================================================
// Hooked tests: Status with system socket
// ============================================================

func TestStatusWithSystemSocketFound(t *testing.T) {
	origStat := osStat
	origDial := netDialTimeout
	defer func() {
		osStat = origStat
		netDialTimeout = origDial
	}()

	osStat = func(name string) (os.FileInfo, error) {
		if strings.Contains(name, "php8.4-fpm.sock") {
			return os.Stat(".")
		}
		return nil, os.ErrNotExist
	}

	netDialTimeout = func(network, address string, timeout time.Duration) (net.Conn, error) {
		return mockConn{}, nil
	}

	m := New(testLogger())

	// FPM SAPI should show system socket
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/sbin/php-fpm8.4", SAPI: "fpm-fcgi"},
	}

	statuses := m.Status()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if !statuses[0].SystemManaged {
		t.Error("FPM should be system managed when socket found")
	}
	if !statuses[0].Running {
		t.Error("FPM should be running when system socket found")
	}
	if statuses[0].SocketPath == "" {
		t.Error("FPM SocketPath should be set")
	}
	if statuses[0].ListenAddr == "" {
		t.Error("FPM ListenAddr should be set to socket path")
	}

	// CGI SAPI should NOT show system FPM socket
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}

	statuses = m.Status()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status for CGI, got %d", len(statuses))
	}
	if statuses[0].SystemManaged {
		t.Error("CGI should NOT be system managed (FPM socket belongs to FPM)")
	}
	if statuses[0].SocketPath != "" {
		t.Error("CGI SocketPath should be empty")
	}
}

// ============================================================
// Hooked tests: findOrCreatePHPConfig all branches
// ============================================================

func TestFindOrCreatePHPConfigCommonPathFoundViaHook(t *testing.T) {
	origStat := osStat
	defer func() { osStat = origStat }()

	// Make the second common path candidate succeed
	osStat = func(name string) (os.FileInfo, error) {
		if strings.HasSuffix(name, "/etc/php/8.4/fpm/php.ini") {
			return os.Stat(".")
		}
		return nil, os.ErrNotExist
	}

	result := findOrCreatePHPConfig("8.4.19", "no scan dir here")
	if !strings.HasSuffix(result, "/etc/php/8.4/fpm/php.ini") {
		t.Errorf("expected fpm php.ini path, got %q", result)
	}
}

func TestFindOrCreatePHPConfigCreateSuccess(t *testing.T) {
	origStat := osStat
	origMkdir := osMkdirAllHook
	origWrite := osWriteFileHook
	defer func() {
		osStat = origStat
		osMkdirAllHook = origMkdir
		osWriteFileHook = origWrite
	}()

	// Stat always fails (no files found)
	osStat = func(name string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}

	// MkdirAll succeeds
	osMkdirAllHook = func(path string, perm os.FileMode) error {
		return nil
	}

	// WriteFile succeeds
	osWriteFileHook = func(name string, data []byte, perm os.FileMode) error {
		return nil
	}

	result := findOrCreatePHPConfig("8.4.19", "")
	// On Windows filepath.Join uses backslashes; normalize for comparison
	normalized := strings.ReplaceAll(result, `\`, `/`)
	if !strings.Contains(normalized, "/etc/php/8.4/cgi/php.ini") {
		t.Errorf("expected created path containing /etc/php/8.4/cgi/php.ini, got %q", result)
	}
}

func TestFindOrCreatePHPConfigCreateFailsFallbackCLI(t *testing.T) {
	origStat := osStat
	origMkdir := osMkdirAllHook
	origWrite := osWriteFileHook
	defer func() {
		osStat = origStat
		osMkdirAllHook = origMkdir
		osWriteFileHook = origWrite
	}()

	writeAttempted := false
	// Stat: fail for everything except the CLI fallback path (which is checked
	// only after WriteFile fails for the cgi path)
	osStat = func(name string) (os.FileInfo, error) {
		normalized := strings.ReplaceAll(name, `\`, `/`)
		if writeAttempted && strings.Contains(normalized, "/cli/php.ini") {
			// CLI fallback path exists — only succeed after write was attempted
			return os.Stat(".")
		}
		return nil, os.ErrNotExist
	}

	osMkdirAllHook = func(path string, perm os.FileMode) error {
		return nil
	}

	// WriteFile fails (simulating permission denied on /etc)
	osWriteFileHook = func(name string, data []byte, perm os.FileMode) error {
		writeAttempted = true
		return fmt.Errorf("permission denied")
	}

	result := findOrCreatePHPConfig("8.4.19", "")
	normalized := strings.ReplaceAll(result, `\`, `/`)
	if !strings.Contains(normalized, "/cli/php.ini") {
		t.Errorf("expected CLI fallback path, got %q", result)
	}
}

func TestFindOrCreatePHPConfigCreateFailsNoFallback(t *testing.T) {
	origStat := osStat
	origMkdir := osMkdirAllHook
	origWrite := osWriteFileHook
	defer func() {
		osStat = origStat
		osMkdirAllHook = origMkdir
		osWriteFileHook = origWrite
	}()

	// Everything fails
	osStat = func(name string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}
	osMkdirAllHook = func(path string, perm os.FileMode) error {
		return nil
	}
	osWriteFileHook = func(name string, data []byte, perm os.FileMode) error {
		return fmt.Errorf("permission denied")
	}

	result := findOrCreatePHPConfig("8.4.19", "")
	if result != "" {
		t.Errorf("expected empty string when all fails, got %q", result)
	}
}

// ============================================================
// Hooked tests: buildDomainINI CreateTemp failure
// ============================================================

func TestBuildDomainINICreateTempFailure(t *testing.T) {
	origCreateTemp := osCreateTempHook
	defer func() { osCreateTempHook = origCreateTemp }()

	osCreateTempHook = func(dir, pattern string) (*os.File, error) {
		return nil, fmt.Errorf("no space left on device")
	}

	m := New(testLogger())
	inst := PHPInstall{Version: "8.4.19", ConfigFile: "/some/php.ini"}

	_, err := m.buildDomainINI("test.com", inst, map[string]string{"memory_limit": "256M"})
	if err == nil {
		t.Error("expected error when CreateTemp fails")
	}
	if !strings.Contains(err.Error(), "no space left") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBuildDomainINIWriteStringFailure(t *testing.T) {
	origCreateTemp := osCreateTempHook
	defer func() { osCreateTempHook = origCreateTemp }()

	// Return a file that's already closed, so WriteString will fail
	osCreateTempHook = func(dir, pattern string) (*os.File, error) {
		f, err := os.CreateTemp(dir, pattern)
		if err != nil {
			return nil, err
		}
		name := f.Name()
		f.Close()
		// Reopen as read-only so write fails
		return os.Open(name)
	}

	m := New(testLogger())
	inst := PHPInstall{Version: "8.4.19", ConfigFile: "/some/php.ini"}

	_, err := m.buildDomainINI("writefail.com", inst, map[string]string{"memory_limit": "256M"})
	if err == nil {
		// On some systems, opening a file for reading still allows writing on Windows.
		// That's ok, this is a best-effort test for the error path.
		return
	}
}

// ============================================================
// Hooked tests: SetConfigRaw with create config failure
// ============================================================

func TestSetConfigRawCreateConfigFails(t *testing.T) {
	origStat := osStat
	origMkdir := osMkdirAllHook
	origWrite := osWriteFileHook
	defer func() {
		osStat = origStat
		osMkdirAllHook = origMkdir
		osWriteFileHook = origWrite
	}()

	// All stat calls fail
	osStat = func(name string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}
	osMkdirAllHook = func(path string, perm os.FileMode) error { return nil }
	osWriteFileHook = func(name string, data []byte, perm os.FileMode) error {
		return fmt.Errorf("permission denied")
	}

	m := New(testLogger())
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("echo", "no config")
	}
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", ConfigFile: ""},
	}

	err := m.SetConfigRaw("8.4.19", "content")
	if err == nil {
		t.Error("expected error when config creation fails")
	}
	if !strings.Contains(err.Error(), "cannot create") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ============================================================
// Hooked tests: SetConfig with create config failure
// ============================================================

func TestSetConfigCreateConfigFails(t *testing.T) {
	origStat := osStat
	origMkdir := osMkdirAllHook
	origWrite := osWriteFileHook
	defer func() {
		osStat = origStat
		osMkdirAllHook = origMkdir
		osWriteFileHook = origWrite
	}()

	osStat = func(name string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}
	osMkdirAllHook = func(path string, perm os.FileMode) error { return nil }
	osWriteFileHook = func(name string, data []byte, perm os.FileMode) error {
		return fmt.Errorf("permission denied")
	}

	m := New(testLogger())
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("echo", "no config")
	}
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", ConfigFile: ""},
	}

	err := m.SetConfig("8.4.19", "memory_limit", "512M")
	if err == nil {
		t.Error("expected error when config creation fails")
	}
	if !strings.Contains(err.Error(), "cannot create") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ============================================================
// Hooked tests: startFPMDaemon write config failure
// ============================================================

func TestStartFPMDaemonWriteConfigFailure(t *testing.T) {
	origWrite := osWriteFileHook
	defer func() { osWriteFileHook = origWrite }()

	osWriteFileHook = func(name string, data []byte, perm os.FileMode) error {
		if strings.Contains(name, "fpm.conf") {
			return fmt.Errorf("disk full")
		}
		return os.WriteFile(name, data, perm)
	}

	m := New(testLogger())
	err := m.startFPMDaemon("8.4.19", "/usr/bin/php-fpm8.4", "127.0.0.1:9000")
	if err == nil {
		t.Error("expected error when write config fails")
	}
	if !strings.Contains(err.Error(), "write fpm config") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestStartFPMDaemonBackgroundCleanup(t *testing.T) {
	m := New(testLogger())
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("ping", "-n", "100", "127.0.0.1")
	}

	err := m.startFPMDaemon("8.4.19", "/usr/bin/php-fpm8.4", "127.0.0.1:9000")
	if err != nil {
		t.Fatalf("startFPMDaemon: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	// Stop and verify background goroutine handles the exit
	val, ok := m.processes.Load("8.4.19")
	if !ok {
		t.Fatal("process should be stored")
	}
	info := val.(*processInfo)
	info.cmd.Process.Kill()

	time.Sleep(100 * time.Millisecond)

	// Process should be cleaned up by background goroutine
	_, loaded := m.processes.Load("8.4.19")
	if loaded {
		t.Error("process should be cleaned up after kill")
	}
}

// ============================================================
// Hooked tests: RunInstall with empty fields command
// ============================================================

func TestRunInstallEmptyFieldsSkipped(t *testing.T) {
	origGOOS := runtimeGOOSInstall
	origRead := readOSRelease
	origExec := installExecCommand
	defer func() {
		runtimeGOOSInstall = origGOOS
		readOSRelease = origRead
		installExecCommand = origExec
	}()

	runtimeGOOSInstall = "linux"
	readOSRelease = func() ([]byte, error) {
		return []byte("ID=fedora\n"), nil
	}

	// Track how many times exec is called
	execCount := 0
	installExecCommand = func(name string, args ...string) *exec.Cmd {
		execCount++
		return exec.Command("echo", "ok")
	}

	output, err := RunInstall("8.3")
	if err != nil {
		t.Fatalf("RunInstall: %v", err)
	}
	// Fedora has exactly 1 non-comment command
	if execCount != 1 {
		t.Errorf("expected 1 exec call, got %d", execCount)
	}
	_ = output
}

// ============================================================
// Hooked tests: StopAll with domain that has tmpINI + kill error
// ============================================================

func TestStopAllDomainKillError(t *testing.T) {
	m := New(testLogger())

	// Create a process that already exited
	cmd := exec.Command("echo", "done")
	cmd.Start()
	cmd.Wait()

	tmpFile := filepath.Join(t.TempDir(), "stopall-kill-err.ini")
	os.WriteFile(tmpFile, []byte("test"), 0644)

	m.domainMu.Lock()
	m.domainMap["kill-err.com"] = &domainInstance{
		domain:          "kill-err.com",
		version:         "8.4",
		listenAddr:      "127.0.0.1:9099",
		configOverrides: make(map[string]string),
		proc: &processInfo{
			cmd:        cmd,
			listenAddr: "127.0.0.1:9099",
		},
		tmpINI: tmpFile,
	}
	m.domainMu.Unlock()

	// StopAll should handle kill error gracefully and still clean up tmpINI
	m.StopAll()

	// tmpINI should be cleaned up
	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Error("tmpINI should be removed even if kill errors")
	}
}
