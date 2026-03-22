package phpmanager

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

func testLogger() *logger.Logger {
	return logger.New("error", "text")
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"PHP 8.4.19 (cgi-fcgi) (built: Jan 15 2025 12:00:00)", "8.4.19"},
		{"PHP 8.3.0 (cli) (built: Nov 21 2023 10:00:00)", "8.3.0"},
		{"PHP 7.4.33 (fpm-fcgi) (built: Sep 29 2022 22:37:55)", "7.4.33"},
		{"not php output", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := parseVersion(tt.input)
		if got != tt.want {
			t.Errorf("parseVersion(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseSAPI(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"PHP 8.4.19 (cgi-fcgi)", "cgi-fcgi"},
		{"PHP 8.3.0 (fpm-fcgi)", "fpm-fcgi"},
		{"PHP 8.2.0 (cli)", "cli"},
		{"PHP 8.1.0 (CGI)", "cgi-fcgi"},
	}
	for _, tt := range tests {
		got := parseSAPI(tt.input)
		if got != tt.want {
			t.Errorf("parseSAPI(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParseConfigPath(t *testing.T) {
	input := `phpinfo()
PHP Version => 8.4.19
Loaded Configuration File => /etc/php/8.4/cgi/php.ini
Scan this dir for additional .ini files => /etc/php/8.4/cgi/conf.d`
	got := parseConfigPath(input)
	if got != "/etc/php/8.4/cgi/php.ini" {
		t.Errorf("parseConfigPath = %q, want /etc/php/8.4/cgi/php.ini", got)
	}

	// Test with (none)
	input2 := "Loaded Configuration File => (none)"
	got2 := parseConfigPath(input2)
	if got2 != "" {
		t.Errorf("parseConfigPath(none) = %q, want empty", got2)
	}
}

func TestParseExtensions(t *testing.T) {
	input := `[PHP Modules]
Core
curl
date
json
mbstring
openssl

[Zend Modules]
Zend OPcache`
	exts := parseExtensions(input)
	if len(exts) != 6 {
		t.Fatalf("extensions count = %d, want 6; got %v", len(exts), exts)
	}
	if exts[0] != "Core" {
		t.Errorf("first ext = %q, want Core", exts[0])
	}
	if exts[5] != "openssl" {
		t.Errorf("last ext = %q, want openssl", exts[5])
	}
}

func TestParseINIConfig(t *testing.T) {
	dir := t.TempDir()
	ini := filepath.Join(dir, "php.ini")
	content := `; PHP Configuration
memory_limit = 256M
max_execution_time = 30
upload_max_filesize = 64M
post_max_size = 64M
display_errors = Off
error_reporting = E_ALL & ~E_DEPRECATED
opcache.enable = 1
date.timezone = UTC
`
	os.WriteFile(ini, []byte(content), 0644)

	cfg, err := parseINIConfig(ini)
	if err != nil {
		t.Fatalf("parseINIConfig: %v", err)
	}

	if cfg.MemoryLimit != "256M" {
		t.Errorf("MemoryLimit = %q, want 256M", cfg.MemoryLimit)
	}
	if cfg.MaxExecutionTime != "30" {
		t.Errorf("MaxExecutionTime = %q, want 30", cfg.MaxExecutionTime)
	}
	if cfg.UploadMaxSize != "64M" {
		t.Errorf("UploadMaxSize = %q, want 64M", cfg.UploadMaxSize)
	}
	if cfg.OPcacheEnabled != "1" {
		t.Errorf("OPcacheEnabled = %q, want 1", cfg.OPcacheEnabled)
	}
	if cfg.Timezone != "UTC" {
		t.Errorf("Timezone = %q, want UTC", cfg.Timezone)
	}
}

func TestUpdateINI(t *testing.T) {
	dir := t.TempDir()
	ini := filepath.Join(dir, "php.ini")
	content := `memory_limit = 128M
;display_errors = On
max_execution_time = 30
`
	os.WriteFile(ini, []byte(content), 0644)

	// Update existing key
	if err := updateINI(ini, "memory_limit", "512M"); err != nil {
		t.Fatalf("updateINI: %v", err)
	}

	data, _ := os.ReadFile(ini)
	if !strings.Contains(string(data), "memory_limit = 512M") {
		t.Errorf("expected memory_limit = 512M in:\n%s", data)
	}

	// Update commented key
	if err := updateINI(ini, "display_errors", "On"); err != nil {
		t.Fatalf("updateINI (commented): %v", err)
	}
	data, _ = os.ReadFile(ini)
	if !strings.Contains(string(data), "display_errors = On") {
		t.Errorf("expected display_errors = On in:\n%s", data)
	}

	// Append new key
	if err := updateINI(ini, "date.timezone", "America/New_York"); err != nil {
		t.Fatalf("updateINI (append): %v", err)
	}
	data, _ = os.ReadFile(ini)
	if !strings.Contains(string(data), "date.timezone = America/New_York") {
		t.Errorf("expected date.timezone = America/New_York in:\n%s", data)
	}
}

func TestManagerNewAndInstallations(t *testing.T) {
	m := New(testLogger())
	if m == nil {
		t.Fatal("New returned nil")
	}
	installs := m.Installations()
	if len(installs) != 0 {
		t.Errorf("expected 0 installations, got %d", len(installs))
	}
}

func TestFindInstall(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
		{Version: "8.3.0", Binary: "/usr/bin/php-cgi8.3"},
	}

	// Exact match
	inst, ok := m.findInstall("8.4.19")
	if !ok || inst.Version != "8.4.19" {
		t.Errorf("findInstall exact: ok=%v, version=%q", ok, inst.Version)
	}

	// Prefix match
	inst, ok = m.findInstall("8.3")
	if !ok || inst.Version != "8.3.0" {
		t.Errorf("findInstall prefix: ok=%v, version=%q", ok, inst.Version)
	}

	// No match
	_, ok = m.findInstall("7.4")
	if ok {
		t.Error("findInstall should not match 7.4")
	}
}

func TestGetConfigNotFound(t *testing.T) {
	m := New(testLogger())
	_, err := m.GetConfig("9.9.9")
	if err == nil {
		t.Error("expected error for non-existent version")
	}
}

func TestGetExtensions(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Extensions: []string{"Core", "curl", "json"}},
	}

	exts, err := m.GetExtensions("8.4.19")
	if err != nil {
		t.Fatalf("GetExtensions: %v", err)
	}
	if len(exts) != 3 {
		t.Errorf("extensions count = %d, want 3", len(exts))
	}
}

func TestGetExtensionsNotFound(t *testing.T) {
	m := New(testLogger())
	_, err := m.GetExtensions("9.9.9")
	if err == nil {
		t.Error("expected error for non-existent version")
	}
}

func TestStatus(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
		{Version: "8.3.0", Binary: "/usr/bin/php-cgi8.3", SAPI: "cgi-fcgi"},
	}

	statuses := m.Status()
	if len(statuses) != 2 {
		t.Fatalf("status count = %d, want 2", len(statuses))
	}
	if statuses[0].Running {
		t.Error("status[0] should not be running")
	}
}

func TestStartFPMNotFound(t *testing.T) {
	m := New(testLogger())
	err := m.StartFPM("9.9.9", "127.0.0.1:9000")
	if err == nil {
		t.Error("expected error for non-existent version")
	}
}

func TestStopFPMNotRunning(t *testing.T) {
	m := New(testLogger())
	err := m.StopFPM("8.4.19")
	if err == nil {
		t.Error("expected error when nothing is running")
	}
}

func TestStartFPMAlreadyRunning(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", Binary: "/usr/bin/php-cgi8.4"},
	}
	// Simulate a running process
	m.processes.Store("8.4.19", &processInfo{
		cmd:        exec.Command("echo"),
		listenAddr: "127.0.0.1:9000",
	})

	err := m.StartFPM("8.4.19", "127.0.0.1:9000")
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("expected already running error, got: %v", err)
	}
}

func TestCandidatePaths(t *testing.T) {
	paths := candidatePaths()
	if len(paths) == 0 {
		t.Error("candidatePaths returned empty list")
	}
}

func TestProbeWithMock(t *testing.T) {
	m := New(testLogger())

	// Override execCommand to return mock data
	callCount := 0
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		callCount++
		var output string
		if len(args) > 0 {
			switch args[0] {
			case "-v":
				output = "PHP 8.4.19 (cgi-fcgi) (built: Jan 15 2025)"
			case "-i":
				output = "Loaded Configuration File => /etc/php/8.4/cgi/php.ini"
			case "-m":
				output = "[PHP Modules]\nCore\ncurl\njson\n\n[Zend Modules]\n"
			}
		}
		// Use echo to produce the output
		return exec.Command("echo", output)
	}

	install, err := m.probe("/usr/bin/php-cgi8.4")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}

	// On Windows, echo adds a trailing newline/space so trim for comparison
	if install.Version != "8.4.19" {
		t.Errorf("version = %q, want 8.4.19", install.Version)
	}
	if install.SAPI != "cgi-fcgi" {
		t.Errorf("sapi = %q, want cgi-fcgi", install.SAPI)
	}
}

func TestGetConfigWithFile(t *testing.T) {
	dir := t.TempDir()
	ini := filepath.Join(dir, "php.ini")
	content := `memory_limit = 256M
max_execution_time = 60
`
	os.WriteFile(ini, []byte(content), 0644)

	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", ConfigFile: ini},
	}

	cfg, err := m.GetConfig("8.4.19")
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if cfg.MemoryLimit != "256M" {
		t.Errorf("MemoryLimit = %q, want 256M", cfg.MemoryLimit)
	}
	if cfg.MaxExecutionTime != "60" {
		t.Errorf("MaxExecutionTime = %q, want 60", cfg.MaxExecutionTime)
	}
}

func TestSetConfig(t *testing.T) {
	dir := t.TempDir()
	ini := filepath.Join(dir, "php.ini")
	os.WriteFile(ini, []byte("memory_limit = 128M\n"), 0644)

	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.4.19", ConfigFile: ini},
	}

	err := m.SetConfig("8.4.19", "memory_limit", "512M")
	if err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	data, _ := os.ReadFile(ini)
	if !strings.Contains(string(data), "memory_limit = 512M") {
		t.Errorf("expected memory_limit = 512M in:\n%s", data)
	}
}

func TestSetConfigNotFound(t *testing.T) {
	m := New(testLogger())
	err := m.SetConfig("9.9.9", "memory_limit", "512M")
	if err == nil {
		t.Error("expected error for non-existent version")
	}
}

func TestStopAll(t *testing.T) {
	m := New(testLogger())
	// Just ensure StopAll doesn't panic with no processes
	m.StopAll()
}
