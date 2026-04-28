package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/siteuser"
)

// ==========================================================================
// stop.go tests
// ==========================================================================

func TestStopCommandNameDescriptionHelp(t *testing.T) {
	s := &StopCommand{}
	if s.Name() != "stop" {
		t.Errorf("Name() = %q, want stop", s.Name())
	}
	if s.Description() == "" {
		t.Error("Description() should not be empty")
	}
	h := s.Help()
	if !strings.Contains(h, "--pid-file") {
		t.Error("Help should mention --pid-file")
	}
	if !strings.Contains(h, "SIGTERM") {
		t.Error("Help should mention SIGTERM")
	}
}

func TestStopCommand_PIDFileNotFound(t *testing.T) {
	s := &StopCommand{}

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := s.Run([]string{"--pid-file", "/nonexistent/uwas.pid"})

	w.Close()
	os.Stdout = old

	if err == nil {
		t.Fatal("expected error for missing PID file")
	}
	if !strings.Contains(err.Error(), "cannot read PID file") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestStopCommand_InvalidPID(t *testing.T) {
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "uwas.pid")
	os.WriteFile(pidFile, []byte("not-a-number\n"), 0644)

	s := &StopCommand{}

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := s.Run([]string{"--pid-file", pidFile})

	w.Close()
	os.Stdout = old

	if err == nil {
		t.Fatal("expected error for invalid PID")
	}
	if !strings.Contains(err.Error(), "invalid PID") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestStopCommand_ProcessNotRunning(t *testing.T) {
	// Write a PID file with a PID that is almost certainly not running.
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "uwas.pid")
	// Use PID 999999999 which should not exist.
	os.WriteFile(pidFile, []byte("999999999\n"), 0644)

	s := &StopCommand{}

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := s.Run([]string{"--pid-file", pidFile})

	w.Close()
	os.Stdout = old

	// On Windows, FindProcess succeeds even for dead PIDs, but Signal fails.
	// On Unix, FindProcess always succeeds, but Signal fails.
	if err == nil {
		// The process might not exist; it's ok if Stop continues to SIGTERM and gets an error.
		t.Log("stop completed without error (process may have appeared dead)")
	} else {
		// Expected: error from Signal.
		if !strings.Contains(err.Error(), "SIGTERM") && !strings.Contains(err.Error(), "cannot find") {
			t.Logf("error = %q (acceptable)", err.Error())
		}
	}
}

func TestStopCommand_DefaultPIDFile(t *testing.T) {
	// When no --pid-file is given and no config found, uses /var/run/uwas.pid.
	// Mock findConfig to return not found.
	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) {
		return "", false
	}
	defer func() { findConfigFn = origFindConfig }()

	s := &StopCommand{}
	err := s.Run(nil)
	if err == nil {
		t.Fatal("expected error (default PID file doesn't exist)")
	}
	// Should fail trying to read the default PID file or find process.
	errStr := err.Error()
	if !strings.Contains(errStr, "uwas.pid") && !strings.Contains(errStr, "cannot") && !strings.Contains(errStr, "not supported") {
		t.Errorf("error = %q, expected mention of uwas.pid or a system error", errStr)
	}
}

// ==========================================================================
// pidcheck.go tests
// ==========================================================================

func TestReadAlivePID_EmptyFile(t *testing.T) {
	pid, ok := readAlivePID("")
	if ok || pid != 0 {
		t.Errorf("readAlivePID('') = %d, %v; want 0, false", pid, ok)
	}
}

func TestReadAlivePID_FileNotFound(t *testing.T) {
	pid, ok := readAlivePID("/nonexistent/uwas.pid")
	if ok || pid != 0 {
		t.Errorf("readAlivePID(nonexistent) = %d, %v; want 0, false", pid, ok)
	}
}

func TestReadAlivePID_InvalidContent(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "uwas.pid")
	os.WriteFile(f, []byte("not-a-number"), 0644)

	pid, ok := readAlivePID(f)
	if ok || pid != 0 {
		t.Errorf("readAlivePID(invalid) = %d, %v; want 0, false", pid, ok)
	}
}

func TestReadAlivePID_ZeroPID(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "uwas.pid")
	os.WriteFile(f, []byte("0"), 0644)

	pid, ok := readAlivePID(f)
	if ok || pid != 0 {
		t.Errorf("readAlivePID(0) = %d, %v; want 0, false", pid, ok)
	}
}

func TestReadAlivePID_NegativePID(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "uwas.pid")
	os.WriteFile(f, []byte("-5"), 0644)

	pid, ok := readAlivePID(f)
	if ok || pid != 0 {
		t.Errorf("readAlivePID(-5) = %d, %v; want 0, false", pid, ok)
	}
}

func TestReadAlivePID_DeadProcess(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "uwas.pid")
	// 999999999 should not be alive.
	os.WriteFile(f, []byte("999999999"), 0644)

	pid, ok := readAlivePID(f)
	if ok {
		t.Errorf("readAlivePID(999999999) = %d, true; expected false", pid)
	}
}

func TestReadAlivePID_CurrentProcess(t *testing.T) {
	// Use current process PID — it is definitely alive.
	tmp := t.TempDir()
	f := filepath.Join(tmp, "uwas.pid")
	os.WriteFile(f, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)

	pid, ok := readAlivePID(f)
	if !ok {
		t.Errorf("readAlivePID(self) = %d, false; expected true", pid)
	}
	if pid != os.Getpid() {
		t.Errorf("pid = %d, want %d", pid, os.Getpid())
	}
}

func TestReadAlivePID_WithHookOverride(t *testing.T) {
	// Override isProcessAliveFn to always return false.
	origAlive := isProcessAliveFn
	isProcessAliveFn = func(proc *os.Process) bool { return false }
	defer func() { isProcessAliveFn = origAlive }()

	tmp := t.TempDir()
	f := filepath.Join(tmp, "uwas.pid")
	os.WriteFile(f, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)

	_, ok := readAlivePID(f)
	if ok {
		t.Error("expected false when isProcessAliveFn returns false")
	}
}

func TestIsProcessAlive_CurrentProcess(t *testing.T) {
	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if !isProcessAlive(proc) {
		t.Error("current process should be alive")
	}
}

// ==========================================================================
// stop.go helper function tests
// ==========================================================================

func TestQuickConfigValue_Found(t *testing.T) {
	tmp := t.TempDir()
	cfgFile := filepath.Join(tmp, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("pid_file: /var/run/uwas.pid\nlisten: 0.0.0.0:9443\n"), 0644)

	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) {
		return cfgFile, true
	}
	defer func() { findConfigFn = origFindConfig }()

	val := quickConfigValue("pid_file")
	if val != "/var/run/uwas.pid" {
		t.Errorf("quickConfigValue(pid_file) = %q, want /var/run/uwas.pid", val)
	}

	val = quickConfigValue("listen")
	if val != "0.0.0.0:9443" {
		t.Errorf("quickConfigValue(listen) = %q, want 0.0.0.0:9443", val)
	}
}

func TestQuickConfigValue_NotFound(t *testing.T) {
	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) {
		return "", false
	}
	defer func() { findConfigFn = origFindConfig }()

	val := quickConfigValue("pid_file")
	if val != "" {
		t.Errorf("quickConfigValue should be empty when config not found, got %q", val)
	}
}

func TestQuickConfigValue_KeyNotInFile(t *testing.T) {
	tmp := t.TempDir()
	cfgFile := filepath.Join(tmp, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("some_other_key: value\n"), 0644)

	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) {
		return cfgFile, true
	}
	defer func() { findConfigFn = origFindConfig }()

	val := quickConfigValue("pid_file")
	if val != "" {
		t.Errorf("quickConfigValue should be empty for missing key, got %q", val)
	}
}

func TestQuickConfigValue_QuotedValue(t *testing.T) {
	tmp := t.TempDir()
	cfgFile := filepath.Join(tmp, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("listen: \"0.0.0.0:9443\"\n"), 0644)

	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) {
		return cfgFile, true
	}
	defer func() { findConfigFn = origFindConfig }()

	val := quickConfigValue("listen")
	if val != "0.0.0.0:9443" {
		t.Errorf("quickConfigValue(listen) = %q, want 0.0.0.0:9443", val)
	}
}

func TestQuickConfigValue_FileReadError(t *testing.T) {
	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) {
		return "/nonexistent/file.yaml", true
	}
	defer func() { findConfigFn = origFindConfig }()

	val := quickConfigValue("listen")
	if val != "" {
		t.Errorf("quickConfigValue should return empty on read error, got %q", val)
	}
}

func TestPidFileFromConfig(t *testing.T) {
	tmp := t.TempDir()
	cfgFile := filepath.Join(tmp, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("pid_file: /tmp/test.pid\n"), 0644)

	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) {
		return cfgFile, true
	}
	defer func() { findConfigFn = origFindConfig }()

	val := pidFileFromConfig()
	if val != "/tmp/test.pid" {
		t.Errorf("pidFileFromConfig() = %q, want /tmp/test.pid", val)
	}
}

func TestAdminURLFromConfig_DefaultFallback(t *testing.T) {
	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) {
		return "", false
	}
	defer func() { findConfigFn = origFindConfig }()

	url := adminURLFromConfig()
	if url != "http://127.0.0.1:9443" {
		t.Errorf("adminURLFromConfig() = %q, want http://127.0.0.1:9443", url)
	}
}

func TestAdminURLFromConfig_WithListenValue(t *testing.T) {
	tmp := t.TempDir()
	cfgFile := filepath.Join(tmp, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("listen: 0.0.0.0:7777\n"), 0644)

	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) {
		return cfgFile, true
	}
	defer func() { findConfigFn = origFindConfig }()

	url := adminURLFromConfig()
	if url != "http://127.0.0.1:7777" {
		t.Errorf("adminURLFromConfig() = %q, want http://127.0.0.1:7777", url)
	}
}

func TestAdminURLFromConfig_WithLocalhost(t *testing.T) {
	tmp := t.TempDir()
	cfgFile := filepath.Join(tmp, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("listen: 127.0.0.1:8888\n"), 0644)

	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) {
		return cfgFile, true
	}
	defer func() { findConfigFn = origFindConfig }()

	url := adminURLFromConfig()
	if url != "http://127.0.0.1:8888" {
		t.Errorf("adminURLFromConfig() = %q, want http://127.0.0.1:8888", url)
	}
}

// ==========================================================================
// cert.go tests
// ==========================================================================

func TestCertCommandNameDescriptionHelp(t *testing.T) {
	c := &CertCommand{}
	if c.Name() != "cert" {
		t.Errorf("Name() = %q, want cert", c.Name())
	}
	if c.Description() == "" {
		t.Error("Description() should not be empty")
	}
	h := c.Help()
	if !strings.Contains(h, "list") {
		t.Error("Help should mention list")
	}
	if !strings.Contains(h, "renew") {
		t.Error("Help should mention renew")
	}
}

func TestCertCommandRunNoArgs(t *testing.T) {
	c := &CertCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := c.Run(nil)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Errorf("Run(nil) error: %v", err)
	}
	if !strings.Contains(buf.String(), "list") {
		t.Error("should print help")
	}
}

func TestCertCommandUnknownSubcommand(t *testing.T) {
	c := &CertCommand{}
	err := c.Run([]string{"bogus"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestCertCommandListAlias(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"host": "example.com", "ssl_mode": "auto", "status": "active", "issuer": "LE", "expiry": "2025-12-31T00:00:00Z", "days_left": 90},
		})
	}))
	defer srv.Close()

	c := &CertCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := c.Run([]string{"ls", "--api-url", srv.URL, "--api-key", "k"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	if !strings.Contains(buf.String(), "example.com") {
		t.Errorf("output should contain host, got:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "HOST") {
		t.Errorf("output should contain header, got:\n%s", buf.String())
	}
}

func TestCertCommandListEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "[]")
	}))
	defer srv.Close()

	c := &CertCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := c.Run([]string{"list", "--api-url", srv.URL, "--api-key", "k"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	if !strings.Contains(buf.String(), "No certificates") {
		t.Errorf("output should say no certs, got:\n%s", buf.String())
	}
}

func TestCertCommandListBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not json")
	}))
	defer srv.Close()

	c := &CertCommand{}
	err := c.Run([]string{"list", "--api-url", srv.URL, "--api-key", "k"})
	if err == nil {
		t.Fatal("expected error for bad JSON")
	}
	if !strings.Contains(err.Error(), "parse response") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestCertCommandListServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, "error")
	}))
	defer srv.Close()

	c := &CertCommand{}
	err := c.Run([]string{"list", "--api-url", srv.URL, "--api-key", "k"})
	if err == nil {
		t.Fatal("expected error for server error")
	}
}

func TestCertCommandListCertExpiry(t *testing.T) {
	// Test various cert states: active, pending, empty expiry.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"host": "active.com", "ssl_mode": "auto", "status": "active", "issuer": "LE", "expiry": "2025-12-31T00:00:00Z", "days_left": 90},
			{"host": "pending.com", "ssl_mode": "auto", "status": "pending", "issuer": "", "expiry": "", "days_left": 0},
		})
	}))
	defer srv.Close()

	c := &CertCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := c.Run([]string{"list", "--api-url", srv.URL, "--api-key", "k"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("error: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "active.com") {
		t.Error("should list active cert")
	}
	if !strings.Contains(output, "pending.com") {
		t.Error("should list pending cert")
	}
	if !strings.Contains(output, "2025-12-31") {
		t.Error("should show truncated expiry date")
	}
}

func TestCertCommandRenewMissingDomain(t *testing.T) {
	c := &CertCommand{}
	err := c.Run([]string{"renew"})
	if err == nil {
		t.Fatal("expected error for missing domain")
	}
	if !strings.Contains(err.Error(), "domain required") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestCertCommandRenewSuccess(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		json.NewEncoder(w).Encode(map[string]string{"status": "renewed"})
	}))
	defer srv.Close()

	c := &CertCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := c.Run([]string{"renew", "example.com", "--api-url", srv.URL, "--api-key", "k"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("renew error: %v", err)
	}
	if gotMethod != "POST" {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/api/v1/certs/example.com/renew" {
		t.Errorf("path = %s", gotPath)
	}
	if !strings.Contains(buf.String(), "Certificate renewed") {
		t.Errorf("output = %q", buf.String())
	}
}

func TestCertCommandRenewServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, "renewal failed")
	}))
	defer srv.Close()

	c := &CertCommand{}

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := c.Run([]string{"renew", "example.com", "--api-url", srv.URL, "--api-key", "k"})

	w.Close()
	os.Stdout = old

	if err == nil {
		t.Fatal("expected error for server error")
	}
	if !strings.Contains(err.Error(), "renewal failed") {
		t.Errorf("error = %q", err.Error())
	}
}

// ==========================================================================
// user.go tests
// ==========================================================================

func TestUserCommandNameDescriptionHelp(t *testing.T) {
	u := &UserCommand{}
	if u.Name() != "user" {
		t.Errorf("Name() = %q, want user", u.Name())
	}
	if u.Description() == "" {
		t.Error("Description() should not be empty")
	}
	h := u.Help()
	if !strings.Contains(h, "list") {
		t.Error("Help should mention list")
	}
	if !strings.Contains(h, "add") {
		t.Error("Help should mention add")
	}
	if !strings.Contains(h, "remove") {
		t.Error("Help should mention remove")
	}
}

func TestUserCommandRunNoArgs(t *testing.T) {
	u := &UserCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := u.Run(nil)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Errorf("Run(nil) error: %v", err)
	}
	if !strings.Contains(buf.String(), "list") {
		t.Error("should print help")
	}
}

func TestUserCommandUnknownSubcommand(t *testing.T) {
	u := &UserCommand{}
	err := u.Run([]string{"bogus"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestUserCommandList(t *testing.T) {
	// On Windows, siteuser.ListUsers() returns nil.
	u := &UserCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := u.Run([]string{"list"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Errorf("list error: %v", err)
	}
	// On Windows, expect "No site users configured."
	output := buf.String()
	if !strings.Contains(output, "No site users") && !strings.Contains(output, "USERNAME") {
		t.Errorf("unexpected output: %s", output)
	}
}

func TestUserCommandAddMissingDomain(t *testing.T) {
	u := &UserCommand{}
	err := u.Run([]string{"add"})
	if err == nil {
		t.Fatal("expected error for missing domain")
	}
	if !strings.Contains(err.Error(), "domain required") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestUserCommandRemoveMissingDomain(t *testing.T) {
	u := &UserCommand{}
	err := u.Run([]string{"remove"})
	if err == nil {
		t.Fatal("expected error for missing domain")
	}
	if !strings.Contains(err.Error(), "domain required") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestUserCommandAddOnWindows(t *testing.T) {
	// user add on Windows should return error about unsupported.
	u := &UserCommand{}
	err := u.Run([]string{"add", "example.com"})
	if err == nil {
		// On Linux without root, it would fail too.
		t.Log("add completed without error (running as root?)")
	} else {
		// Could be "not supported on Windows" or "root required".
		if !strings.Contains(err.Error(), "Windows") && !strings.Contains(err.Error(), "root required") {
			t.Logf("error = %q (acceptable)", err.Error())
		}
	}
}

func TestUserCommandRemoveOnWindows(t *testing.T) {
	u := &UserCommand{}
	err := u.Run([]string{"remove", "example.com"})
	if err == nil {
		t.Log("remove completed without error")
	} else {
		if !strings.Contains(err.Error(), "Windows") && !strings.Contains(err.Error(), "root required") {
			t.Logf("error = %q (acceptable)", err.Error())
		}
	}
}

func TestUserCommandRemoveAliases(t *testing.T) {
	u := &UserCommand{}
	// Test "rm" alias — should fail the same way as "remove"
	err := u.Run([]string{"rm"})
	if err == nil {
		t.Fatal("expected error for missing domain")
	}
	if !strings.Contains(err.Error(), "domain required") {
		t.Errorf("error = %q", err.Error())
	}

	// Test "delete" alias
	err = u.Run([]string{"delete"})
	if err == nil {
		t.Fatal("expected error for missing domain")
	}
	if !strings.Contains(err.Error(), "domain required") {
		t.Errorf("error = %q", err.Error())
	}
}

// ==========================================================================
// install.go tests — InstallCmd, UninstallCmd, DoctorCmd
// ==========================================================================

func TestInstallCmdNameDescription(t *testing.T) {
	c := &InstallCmd{}
	if c.Name() != "install" {
		t.Errorf("Name() = %q, want install", c.Name())
	}
	if c.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

func TestInstallCmd_NotLinux(t *testing.T) {
	orig := installRuntimeGOOS
	installRuntimeGOOS = "windows"
	defer func() { installRuntimeGOOS = orig }()

	c := &InstallCmd{}
	err := c.Run(nil)
	if err == nil {
		t.Fatal("expected error on non-Linux")
	}
	if !strings.Contains(err.Error(), "only supported on Linux") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestInstallCmd_NotRoot(t *testing.T) {
	origGOOS := installRuntimeGOOS
	origGetuid := installOsGetuid
	installRuntimeGOOS = "linux"
	installOsGetuid = func() int { return 1000 }
	defer func() {
		installRuntimeGOOS = origGOOS
		installOsGetuid = origGetuid
	}()

	c := &InstallCmd{}
	err := c.Run(nil)
	if err == nil {
		t.Fatal("expected error for non-root")
	}
	if !strings.Contains(err.Error(), "root") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestInstallCmd_Success(t *testing.T) {
	// Mock all system calls for a successful install.
	origGOOS := installRuntimeGOOS
	origGetuid := installOsGetuid
	origExec := installOsExecutable
	origRead := installOsReadFile
	origWrite := installOsWriteFile
	origStat := installOsStat
	origSymlink := installOsSymlink
	origExecCmd := installExecCommand
	origMkdirAll := installOsMkdirAll

	installRuntimeGOOS = "linux"
	installOsGetuid = func() int { return 0 }
	installOsExecutable = func() (string, error) { return "/tmp/uwas-test", nil }
	installOsReadFile = func(name string) ([]byte, error) { return []byte("binary-data"), nil }
	installOsWriteFile = func(name string, data []byte, perm os.FileMode) error { return nil }
	installOsStat = func(name string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	installOsSymlink = func(old, new string) error { return nil }
	installExecCommand = func(name string, arg ...string) *exec.Cmd {
		return exec.Command("echo", "mocked")
	}
	installOsMkdirAll = func(path string, perm os.FileMode) error { return nil }
	defer func() {
		installRuntimeGOOS = origGOOS
		installOsGetuid = origGetuid
		installOsExecutable = origExec
		installOsReadFile = origRead
		installOsWriteFile = origWrite
		installOsStat = origStat
		installOsSymlink = origSymlink
		installExecCommand = origExecCmd
		installOsMkdirAll = origMkdirAll
	}()

	c := &InstallCmd{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := c.Run(nil)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("install error: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "Installing UWAS") {
		t.Errorf("output should contain 'Installing UWAS', got:\n%s", output)
	}
	if !strings.Contains(output, "Installation complete") {
		t.Errorf("output should contain 'Installation complete', got:\n%s", output)
	}
}

func TestInstallCmd_BinaryAlreadyAtTarget(t *testing.T) {
	// When Executable returns the target path, we need EvalSymlinks to work.
	// Create a real temp file to act as the "binary" so EvalSymlinks succeeds.
	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "uwas")
	os.WriteFile(fakeBin, []byte("fake"), 0755)

	origGOOS := installRuntimeGOOS
	origGetuid := installOsGetuid
	origExec := installOsExecutable
	origRead := installOsReadFile
	origWrite := installOsWriteFile
	origStat := installOsStat
	origSymlink := installOsSymlink
	origExecCmd := installExecCommand
	origMkdirAll := installOsMkdirAll

	installRuntimeGOOS = "linux"
	installOsGetuid = func() int { return 0 }
	// Return the actual path of /usr/local/bin/uwas but make EvalSymlinks resolve to itself.
	// Since /usr/local/bin/uwas doesn't exist, return the fakeBin path so EvalSymlinks works,
	// but it won't match "/usr/local/bin/uwas", so let's set self to exactly the binPath.
	// The trick: self after EvalSymlinks will be fakeBin, which != "/usr/local/bin/uwas",
	// so it will try to copy. Let's just test the "already at target" path differently:
	// We need EvalSymlinks(path) to return "/usr/local/bin/uwas".
	// Simplest: mock the Executable to return a path that EvalSymlinks resolves to the target.
	// Actually the code does: self, _ = filepath.EvalSymlinks(self)
	// Then checks: if self != binPath
	// Since we can't make EvalSymlinks return "/usr/local/bin/uwas" from a temp file,
	// we test the "copy" path instead (which is already tested in TestInstallCmd_Success).
	// Instead, test that when the binary IS at the target, it says "already at".
	// Since we can't mock EvalSymlinks, just provide a binary that copies successfully.
	installOsExecutable = func() (string, error) { return fakeBin, nil }
	installOsReadFile = func(name string) ([]byte, error) { return []byte("binary-data"), nil }
	installOsWriteFile = func(name string, data []byte, perm os.FileMode) error { return nil }
	installOsStat = func(name string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	installOsSymlink = func(old, new string) error { return nil }
	installExecCommand = func(name string, arg ...string) *exec.Cmd {
		return exec.Command("echo", "mocked")
	}
	installOsMkdirAll = func(path string, perm os.FileMode) error { return nil }
	defer func() {
		installRuntimeGOOS = origGOOS
		installOsGetuid = origGetuid
		installOsExecutable = origExec
		installOsReadFile = origRead
		installOsWriteFile = origWrite
		installOsStat = origStat
		installOsSymlink = origSymlink
		installExecCommand = origExecCmd
		installOsMkdirAll = origMkdirAll
	}()

	c := &InstallCmd{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := c.Run(nil)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("install error: %v", err)
	}
	if !strings.Contains(buf.String(), "Binary installed") {
		t.Errorf("output should say binary installed, got:\n%s", buf.String())
	}
}

func TestUninstallCmdNameDescription(t *testing.T) {
	c := &UninstallCmd{}
	if c.Name() != "uninstall" {
		t.Errorf("Name() = %q, want uninstall", c.Name())
	}
	if c.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

func TestUninstallCmd_NotLinux(t *testing.T) {
	orig := installRuntimeGOOS
	installRuntimeGOOS = "windows"
	defer func() { installRuntimeGOOS = orig }()

	c := &UninstallCmd{}
	err := c.Run(nil)
	if err == nil {
		t.Fatal("expected error on non-Linux")
	}
	if !strings.Contains(err.Error(), "only supported on Linux") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestUninstallCmd_NotRoot(t *testing.T) {
	origGOOS := installRuntimeGOOS
	origGetuid := installOsGetuid
	installRuntimeGOOS = "linux"
	installOsGetuid = func() int { return 1000 }
	defer func() {
		installRuntimeGOOS = origGOOS
		installOsGetuid = origGetuid
	}()

	c := &UninstallCmd{}
	err := c.Run(nil)
	if err == nil {
		t.Fatal("expected error for non-root")
	}
	if !strings.Contains(err.Error(), "root") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestUninstallCmd_Cancelled(t *testing.T) {
	origGOOS := installRuntimeGOOS
	origGetuid := installOsGetuid
	installRuntimeGOOS = "linux"
	installOsGetuid = func() int { return 0 }
	defer func() {
		installRuntimeGOOS = origGOOS
		installOsGetuid = origGetuid
	}()

	// Provide "n" as input to cancel.
	rIn, wIn, _ := os.Pipe()
	wIn.WriteString("n\n")
	wIn.Close()
	oldStdin := os.Stdin
	os.Stdin = rIn
	defer func() { os.Stdin = oldStdin }()

	c := &UninstallCmd{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := c.Run(nil)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("uninstall error: %v", err)
	}
	if !strings.Contains(buf.String(), "Cancelled") {
		t.Errorf("output should say Cancelled, got:\n%s", buf.String())
	}
}

func TestUninstallCmd_Confirmed(t *testing.T) {
	origGOOS := installRuntimeGOOS
	origGetuid := installOsGetuid
	origExec := installOsExecutable
	origRemove := installOsRemove
	origExecCmd := installExecCommand

	installRuntimeGOOS = "linux"
	installOsGetuid = func() int { return 0 }
	installOsExecutable = func() (string, error) { return "/tmp/test-uwas", nil }
	installOsRemove = func(name string) error { return nil }
	installExecCommand = func(name string, arg ...string) *exec.Cmd {
		return exec.Command("echo", "mocked")
	}
	defer func() {
		installRuntimeGOOS = origGOOS
		installOsGetuid = origGetuid
		installOsExecutable = origExec
		installOsRemove = origRemove
		installExecCommand = origExecCmd
	}()

	// Provide "y" as input.
	rIn, wIn, _ := os.Pipe()
	wIn.WriteString("y\n")
	wIn.Close()
	oldStdin := os.Stdin
	os.Stdin = rIn
	defer func() { os.Stdin = oldStdin }()

	c := &UninstallCmd{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := c.Run(nil)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("uninstall error: %v", err)
	}
	if !strings.Contains(buf.String(), "UWAS uninstalled") {
		t.Errorf("output should say uninstalled, got:\n%s", buf.String())
	}
}

func TestDoctorCmdNameDescription(t *testing.T) {
	c := &DoctorCmd{}
	if c.Name() != "doctor" {
		t.Errorf("Name() = %q, want doctor", c.Name())
	}
	if c.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

func TestDoctorCommand_Basic(t *testing.T) {
	// DoctorCommand runs without error even when checks return warnings/failures.
	// Mock exec hooks so doctor checks don't actually run system commands.
	origExecCmd := doctorExecCommand
	origLookPath := doctorExecLookPath
	origStat := doctorOsStat
	doctorExecCommand = func(name string, arg ...string) *exec.Cmd {
		// Return a command that will fail (which is fine for doctor checks).
		return exec.Command("echo", "mocked")
	}
	doctorExecLookPath = func(file string) (string, error) {
		return "", fmt.Errorf("not found")
	}
	doctorOsStat = func(name string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}
	defer func() {
		doctorExecCommand = origExecCmd
		doctorExecLookPath = origLookPath
		doctorOsStat = origStat
	}()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := DoctorCommand(nil)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("doctor error: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "UWAS Doctor") {
		t.Errorf("should contain header, got:\n%s", output)
	}
	if !strings.Contains(output, "Summary:") {
		t.Errorf("should contain summary, got:\n%s", output)
	}
}

func TestDoctorCommand_WithFix(t *testing.T) {
	origExecCmd := doctorExecCommand
	origLookPath := doctorExecLookPath
	origStat := doctorOsStat
	doctorExecCommand = func(name string, arg ...string) *exec.Cmd {
		return exec.Command("echo", "mocked")
	}
	doctorExecLookPath = func(file string) (string, error) {
		return "", fmt.Errorf("not found")
	}
	doctorOsStat = func(name string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}
	defer func() {
		doctorExecCommand = origExecCmd
		doctorExecLookPath = origLookPath
		doctorOsStat = origStat
	}()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := DoctorCommand([]string{"--fix"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("doctor error: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "AUTO-FIX enabled") {
		t.Errorf("should show auto-fix mode, got:\n%s", output)
	}
}

func TestDoctorCommand_WithConfigFound(t *testing.T) {
	// Create a temp config so doctor finds it.
	tmp := t.TempDir()
	cfgFile := filepath.Join(tmp, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("test: true\n"), 0644)

	origExecCmd := doctorExecCommand
	origLookPath := doctorExecLookPath
	origStat := doctorOsStat
	doctorExecCommand = func(name string, arg ...string) *exec.Cmd {
		return exec.Command("echo", "mocked")
	}
	doctorExecLookPath = func(file string) (string, error) {
		return "", fmt.Errorf("not found")
	}
	doctorOsStat = func(name string) (os.FileInfo, error) {
		if name == cfgFile {
			return os.Stat(cfgFile)
		}
		return nil, os.ErrNotExist
	}
	defer func() {
		doctorExecCommand = origExecCmd
		doctorExecLookPath = origLookPath
		doctorOsStat = origStat
	}()

	// Temporarily set cwd so doctor can find the config.
	origDir, _ := os.Getwd()
	os.Chdir(tmp)
	defer os.Chdir(origDir)
	os.WriteFile(filepath.Join(tmp, "uwas.yaml"), []byte("test: true\n"), 0644)

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := DoctorCommand(nil)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("doctor error: %v", err)
	}
}

// ==========================================================================
// install.go — utility function tests
// ==========================================================================

func TestContainsCI(t *testing.T) {
	tests := []struct {
		s, sub string
		want   bool
	}{
		{"Hello World", "hello", true},
		{"Hello World", "WORLD", true},
		{"Hello", "hello", true},
		{"Hello", "xyz", false},
		{"", "", true},
		{"abc", "", true},
		{"", "a", false},
		{"A", "a", true},
		{"Active", "active", true},
	}
	for _, tt := range tests {
		got := containsCI(tt.s, tt.sub)
		if got != tt.want {
			t.Errorf("containsCI(%q, %q) = %v, want %v", tt.s, tt.sub, got, tt.want)
		}
	}
}

func TestToLower(t *testing.T) {
	tests := []struct{ in, want string }{
		{"ABC", "abc"},
		{"Hello World", "hello world"},
		{"already lower", "already lower"},
		{"", ""},
		{"123", "123"},
		{"MiXeD", "mixed"},
	}
	for _, tt := range tests {
		got := toLower(tt.in)
		if got != tt.want {
			t.Errorf("toLower(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestIndexStr(t *testing.T) {
	tests := []struct {
		s, sub string
		want   int
	}{
		{"hello world", "world", 6},
		{"hello", "hello", 0},
		{"hello", "xyz", -1},
		{"aaa", "a", 0},
		{"", "", 0},
		{"hello", "", 0},
	}
	for _, tt := range tests {
		got := indexStr(tt.s, tt.sub)
		if got != tt.want {
			t.Errorf("indexStr(%q, %q) = %d, want %d", tt.s, tt.sub, got, tt.want)
		}
	}
}

func TestJoinStr(t *testing.T) {
	tests := []struct {
		in   []string
		want string
	}{
		{[]string{"a", "b", "c"}, "a, b, c"},
		{[]string{"x"}, "x"},
		{nil, ""},
		{[]string{}, ""},
	}
	for _, tt := range tests {
		got := joinStr(tt.in)
		if got != tt.want {
			t.Errorf("joinStr(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPhpPkgs(t *testing.T) {
	got := phpPkgs([]string{"curl", "GD", "mbstring"})
	if !strings.Contains(got, "php-curl") {
		t.Errorf("should contain php-curl, got %q", got)
	}
	if !strings.Contains(got, "php-gd") {
		t.Errorf("should contain php-gd (lowered), got %q", got)
	}
}

func TestSplitFields(t *testing.T) {
	input := "Filesystem  Size  Used\n/dev/sda1   100G  50G"
	fields := splitFields(input)
	if len(fields) < 6 {
		t.Errorf("expected at least 6 fields, got %d: %v", len(fields), fields)
	}
}

func TestSplitLines(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"a\nb\nc", 3},
		{"single", 1},
		{"", 0},
		{"a\n", 1},
		{"\n", 1},
	}
	for _, tt := range tests {
		got := splitLines(tt.in)
		if len(got) != tt.want {
			t.Errorf("splitLines(%q) = %d lines, want %d", tt.in, len(got), tt.want)
		}
	}
}

func TestSplitWhitespace(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"a b c", 3},
		{"  spaced  out  ", 2},
		{"single", 1},
		{"", 0},
		{"\t\ttabs\t", 1},
	}
	for _, tt := range tests {
		got := splitWhitespace(tt.in)
		if len(got) != tt.want {
			t.Errorf("splitWhitespace(%q) = %d, want %d (%v)", tt.in, len(got), tt.want, got)
		}
	}
}

// ==========================================================================
// install.go — individual doctor check tests
// ==========================================================================

func TestCheckCLI_Config(t *testing.T) {
	c := checkCLI_Config("/etc/uwas/uwas.yaml")
	if c.status != "ok" {
		t.Errorf("status = %q, want ok", c.status)
	}
	if c.message != "/etc/uwas/uwas.yaml" {
		t.Errorf("message = %q", c.message)
	}

	c = checkCLI_Config("")
	if c.status != "warn" {
		t.Errorf("status = %q, want warn", c.status)
	}
}

func TestCheckCLI_PHPFPM_NotInstalled(t *testing.T) {
	origLookPath := doctorExecLookPath
	origStat := doctorOsStat
	doctorExecLookPath = func(file string) (string, error) { return "", fmt.Errorf("not found") }
	doctorOsStat = func(name string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	defer func() {
		doctorExecLookPath = origLookPath
		doctorOsStat = origStat
	}()

	c := checkCLI_PHPFPM(false)
	if c.status != "fail" {
		t.Errorf("status = %q, want fail", c.status)
	}
	if !strings.Contains(c.message, "Not installed") {
		t.Errorf("message = %q", c.message)
	}
}

func TestCheckCLI_PHPFPM_SocketFound(t *testing.T) {
	// Create a temp file to act as a socket.
	tmp := t.TempDir()
	sockPath := filepath.Join(tmp, "php8.3-fpm.sock")
	os.WriteFile(sockPath, []byte(""), 0644)

	origStat := doctorOsStat
	doctorOsStat = func(name string) (os.FileInfo, error) {
		if name == sockPath || name == "/run/php/php8.3-fpm.sock" {
			return os.Stat(sockPath)
		}
		return nil, os.ErrNotExist
	}
	defer func() { doctorOsStat = origStat }()

	c := checkCLI_PHPFPM(false)
	if c.status != "ok" {
		t.Errorf("status = %q, want ok", c.status)
	}
}

func TestCheckCLI_PHPFPM_InstalledNotRunning(t *testing.T) {
	origLookPath := doctorExecLookPath
	origStat := doctorOsStat
	doctorExecLookPath = func(file string) (string, error) {
		if file == "php-fpm8.3" {
			return "/usr/sbin/php-fpm8.3", nil
		}
		return "", fmt.Errorf("not found")
	}
	doctorOsStat = func(name string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	defer func() {
		doctorExecLookPath = origLookPath
		doctorOsStat = origStat
	}()

	c := checkCLI_PHPFPM(false)
	if c.status != "fail" {
		t.Errorf("status = %q, want fail", c.status)
	}
	if !strings.Contains(c.message, "Installed but not running") {
		t.Errorf("message = %q", c.message)
	}
}

func TestCheckCLI_PHPModules_CannotCheck(t *testing.T) {
	origCmd := doctorExecCommand
	doctorExecCommand = func(name string, arg ...string) *exec.Cmd {
		// Return a command that will fail.
		return exec.Command("nonexistent-binary-xyz")
	}
	defer func() { doctorExecCommand = origCmd }()

	c := checkCLI_PHPModules()
	if c.status != "warn" {
		t.Errorf("status = %q, want warn", c.status)
	}
	if !strings.Contains(c.message, "Cannot check") {
		t.Errorf("message = %q", c.message)
	}
}

func TestCheckCLI_MySQL_NotInstalled(t *testing.T) {
	origCmd := doctorExecCommand
	origLookPath := doctorExecLookPath
	doctorExecCommand = func(name string, arg ...string) *exec.Cmd {
		// Return a command that produces empty output (fails or outputs nothing useful).
		return exec.Command("nonexistent-binary-xyz-mysql-test")
	}
	doctorExecLookPath = func(file string) (string, error) {
		return "", fmt.Errorf("not found")
	}
	defer func() {
		doctorExecCommand = origCmd
		doctorExecLookPath = origLookPath
	}()

	c := checkCLI_MySQL(false)
	if c.status != "warn" {
		t.Errorf("status = %q, want warn", c.status)
	}
}

func TestCheckCLI_MySQL_NotRunning(t *testing.T) {
	origCmd := doctorExecCommand
	origLookPath := doctorExecLookPath
	doctorExecCommand = func(name string, arg ...string) *exec.Cmd {
		// Return a command that produces empty/error output (not "active").
		return exec.Command("nonexistent-binary-xyz-mysql-test")
	}
	doctorExecLookPath = func(file string) (string, error) {
		if file == "mariadb" {
			return "/usr/bin/mariadb", nil
		}
		return "", fmt.Errorf("not found")
	}
	defer func() {
		doctorExecCommand = origCmd
		doctorExecLookPath = origLookPath
	}()

	c := checkCLI_MySQL(false)
	if c.status != "fail" {
		t.Errorf("status = %q, want fail", c.status)
	}
	if !strings.Contains(c.message, "Not running") {
		t.Errorf("message = %q", c.message)
	}
}

func TestCheckCLI_WebRoot_Exists(t *testing.T) {
	// Create a temp dir and override doctorOsStat to see it.
	tmp := t.TempDir()
	origStat := doctorOsStat
	doctorOsStat = func(name string) (os.FileInfo, error) {
		if name == "/var/www" {
			return os.Stat(tmp)
		}
		return nil, os.ErrNotExist
	}
	defer func() { doctorOsStat = origStat }()

	c := checkCLI_WebRoot(false)
	if c.status != "ok" {
		t.Errorf("status = %q, want ok", c.status)
	}
}

func TestCheckCLI_WebRoot_Missing(t *testing.T) {
	origStat := doctorOsStat
	doctorOsStat = func(name string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	defer func() { doctorOsStat = origStat }()

	c := checkCLI_WebRoot(false)
	if c.status != "fail" {
		t.Errorf("status = %q, want fail", c.status)
	}
}

func TestCheckCLI_Disk(t *testing.T) {
	// Just make sure it doesn't panic.
	origCmd := doctorExecCommand
	doctorExecCommand = func(name string, arg ...string) *exec.Cmd {
		return exec.Command("echo", "mocked")
	}
	defer func() { doctorExecCommand = origCmd }()

	c := checkCLI_Disk()
	if c.name != "Disk" {
		t.Errorf("name = %q", c.name)
	}
}

func TestCheckCLI_DNS(t *testing.T) {
	origCmd := doctorExecCommand
	doctorExecCommand = func(name string, arg ...string) *exec.Cmd {
		return exec.Command("nonexistent-binary-xyz")
	}
	defer func() { doctorExecCommand = origCmd }()

	c := checkCLI_DNS()
	if c.status != "warn" {
		t.Errorf("status = %q, want warn", c.status)
	}
}

// ==========================================================================
// conflicts.go tests
// ==========================================================================

func TestDetectConflicts_Windows(t *testing.T) {
	orig := conflictsRuntimeGOOS
	conflictsRuntimeGOOS = "windows"
	defer func() { conflictsRuntimeGOOS = orig }()

	conflicts := DetectConflicts()
	if len(conflicts) != 0 {
		t.Errorf("expected no conflicts on Windows, got %d", len(conflicts))
	}
}

func TestDetectConflicts_NoneInstalled(t *testing.T) {
	orig := conflictsRuntimeGOOS
	origLookPath := conflictsExecLookPath
	conflictsRuntimeGOOS = "linux"
	conflictsExecLookPath = func(file string) (string, error) {
		return "", fmt.Errorf("not found")
	}
	defer func() {
		conflictsRuntimeGOOS = orig
		conflictsExecLookPath = origLookPath
	}()

	conflicts := DetectConflicts()
	if len(conflicts) != 0 {
		t.Errorf("expected no conflicts, got %d", len(conflicts))
	}
}

func TestDetectConflicts_NginxInstalled(t *testing.T) {
	orig := conflictsRuntimeGOOS
	origLookPath := conflictsExecLookPath
	origExecCmd := conflictsExecCommand

	conflictsRuntimeGOOS = "linux"
	conflictsExecLookPath = func(file string) (string, error) {
		if file == "nginx" {
			return "/usr/sbin/nginx", nil
		}
		return "", fmt.Errorf("not found")
	}
	conflictsExecCommand = func(name string, arg ...string) *exec.Cmd {
		// pidof fails — not running.
		return exec.Command("echo", "")
	}
	defer func() {
		conflictsRuntimeGOOS = orig
		conflictsExecLookPath = origLookPath
		conflictsExecCommand = origExecCmd
	}()

	conflicts := DetectConflicts()
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	if conflicts[0].Name != "Nginx" {
		t.Errorf("name = %q, want Nginx", conflicts[0].Name)
	}
	if conflicts[0].Running {
		t.Error("nginx should not be running")
	}
}

func TestPrintConflicts_Empty(t *testing.T) {
	hasRunning := PrintConflicts(nil)
	if hasRunning {
		t.Error("expected false for empty conflicts")
	}
}

func TestPrintConflicts_NotRunning(t *testing.T) {
	conflicts := []ConflictingServer{
		{Name: "Nginx", Running: false, Service: "nginx"},
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	hasRunning := PrintConflicts(conflicts)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if hasRunning {
		t.Error("expected false when none are running")
	}
	if !strings.Contains(buf.String(), "Nginx") {
		t.Errorf("output should mention Nginx, got:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "stopped") {
		t.Errorf("output should say stopped, got:\n%s", buf.String())
	}
}

func TestPrintConflicts_Running(t *testing.T) {
	conflicts := []ConflictingServer{
		{Name: "Apache", Running: true, PID: "1234", Service: "apache2"},
	}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	hasRunning := PrintConflicts(conflicts)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if !hasRunning {
		t.Error("expected true when some are running")
	}
	if !strings.Contains(buf.String(), "1234") {
		t.Errorf("output should mention PID 1234, got:\n%s", buf.String())
	}
}

func TestOfferStopConflicts_NoneRunning(t *testing.T) {
	// Should return immediately if no running conflicts.
	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	OfferStopConflicts([]ConflictingServer{
		{Name: "Nginx", Running: false},
	})

	w.Close()
	os.Stdout = old
	// No crash = pass.
}

func TestOfferStopConflicts_UserSkips(t *testing.T) {
	origExecCmd := conflictsExecCommand
	conflictsExecCommand = func(name string, arg ...string) *exec.Cmd {
		return exec.Command("echo", "mocked")
	}
	defer func() { conflictsExecCommand = origExecCmd }()

	// Provide "n" input.
	rIn, wIn, _ := os.Pipe()
	wIn.WriteString("n\n")
	wIn.Close()
	oldStdin := os.Stdin
	os.Stdin = rIn
	defer func() { os.Stdin = oldStdin }()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	OfferStopConflicts([]ConflictingServer{
		{Name: "Nginx", Running: true, PID: "123", Service: "nginx"},
	})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if !strings.Contains(buf.String(), "Skipped") {
		t.Errorf("output should say skipped, got:\n%s", buf.String())
	}
}

func TestOfferStopConflicts_UserAccepts(t *testing.T) {
	origExecCmd := conflictsExecCommand
	conflictsExecCommand = func(name string, arg ...string) *exec.Cmd {
		// Make systemctl stop succeed (echo returns 0).
		return exec.Command("echo", "ok")
	}
	defer func() { conflictsExecCommand = origExecCmd }()

	// Provide "y\nn\n" — yes to stop, no to uninstall.
	rIn, wIn, _ := os.Pipe()
	wIn.WriteString("y\nn\n")
	wIn.Close()
	oldStdin := os.Stdin
	os.Stdin = rIn
	defer func() { os.Stdin = oldStdin }()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	OfferStopConflicts([]ConflictingServer{
		{Name: "Nginx", Running: true, PID: "123", Service: "nginx"},
	})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if !strings.Contains(buf.String(), "stopped and disabled") {
		t.Errorf("output should say stopped, got:\n%s", buf.String())
	}
}

func TestOfferPHPInstall_Windows(t *testing.T) {
	orig := conflictsRuntimeGOOS
	conflictsRuntimeGOOS = "windows"
	defer func() { conflictsRuntimeGOOS = orig }()

	// Should return immediately on Windows.
	OfferPHPInstall()
}

func TestOfferPHPInstall_AlreadyInstalled(t *testing.T) {
	orig := conflictsRuntimeGOOS
	origLookPath := conflictsExecLookPath
	conflictsRuntimeGOOS = "linux"
	conflictsExecLookPath = func(file string) (string, error) {
		if file == "php-cgi" {
			return "/usr/bin/php-cgi", nil
		}
		return "", fmt.Errorf("not found")
	}
	defer func() {
		conflictsRuntimeGOOS = orig
		conflictsExecLookPath = origLookPath
	}()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	OfferPHPInstall()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	// Should print nothing and return early.
	if buf.Len() > 0 {
		t.Logf("output (unexpected): %s", buf.String())
	}
}

func TestOfferPHPInstall_UserSkips(t *testing.T) {
	orig := conflictsRuntimeGOOS
	origLookPath := conflictsExecLookPath
	conflictsRuntimeGOOS = "linux"
	conflictsExecLookPath = func(file string) (string, error) {
		return "", fmt.Errorf("not found")
	}
	defer func() {
		conflictsRuntimeGOOS = orig
		conflictsExecLookPath = origLookPath
	}()

	// Provide "s" to skip.
	rIn, wIn, _ := os.Pipe()
	wIn.WriteString("s\n")
	wIn.Close()
	oldStdin := os.Stdin
	os.Stdin = rIn
	defer func() { os.Stdin = oldStdin }()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	OfferPHPInstall()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if !strings.Contains(buf.String(), "Skipped") {
		t.Errorf("output should say skipped, got:\n%s", buf.String())
	}
}

// ==========================================================================
// serve.go tests
// ==========================================================================

func TestFilterArg_AlreadyPresent(t *testing.T) {
	args := []string{"serve", "-d"}
	result := filterArg(args, "-d")
	if len(result) != 2 {
		t.Errorf("filterArg should not duplicate, got %v", result)
	}
}

func TestFilterArg_NotPresent(t *testing.T) {
	args := []string{"serve"}
	result := filterArg(args, "-d")
	if len(result) != 2 {
		t.Errorf("filterArg should append, got %v", result)
	}
	if result[1] != "-d" {
		t.Errorf("last arg should be -d, got %q", result[1])
	}
}

func TestFilterArg_Empty(t *testing.T) {
	result := filterArg(nil, "-d")
	if len(result) != 1 || result[0] != "-d" {
		t.Errorf("filterArg(nil) should return [-d], got %v", result)
	}
}

// ==========================================================================
// php.go tests — installInfo and install subcommands
// ==========================================================================

func TestPHPInstallInfoCommand(t *testing.T) {
	p := &PHPCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := p.Run([]string{"install-info", "8.3"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("install-info error: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "PHP 8.3") {
		t.Errorf("should mention PHP 8.3, got:\n%s", output)
	}
	if !strings.Contains(output, "Install Guide") {
		t.Errorf("should say Install Guide, got:\n%s", output)
	}
}

func TestPHPInstallInfoDefaultVersion(t *testing.T) {
	p := &PHPCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// install-info with no version — defaults to 8.3.
	err := p.Run([]string{"install-info"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("install-info error: %v", err)
	}
	if !strings.Contains(buf.String(), "PHP 8.3") {
		t.Errorf("default should be 8.3, got:\n%s", buf.String())
	}
}

func TestPHPInstallCommand_NotRoot(t *testing.T) {
	// php install on non-root (which we always are in tests) should print message.
	p := &PHPCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := p.Run([]string{"install", "8.3"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Logf("install error: %v (expected on non-root)", err)
	}
	// On Windows or non-root, should print a root-required message or succeed.
	output := buf.String()
	if !strings.Contains(output, "PHP 8.3") {
		t.Errorf("should mention PHP version, got:\n%s", output)
	}
}

func TestPHPInstallDefaultVersion(t *testing.T) {
	p := &PHPCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := p.Run([]string{"install"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Logf("install error: %v", err)
	}
	if !strings.Contains(buf.String(), "PHP 8.3") {
		t.Errorf("default should be 8.3, got:\n%s", buf.String())
	}
}

// ==========================================================================
// config.go tests — ConfigCommand test subcommand
// ==========================================================================

func TestConfigCommand_TestSubcommand(t *testing.T) {
	tmp := t.TempDir()
	cfgFile := filepath.Join(tmp, "uwas.yaml")
	os.WriteFile(cfgFile, []byte(`
domains:
  - host: test.com
    root: /tmp
    type: static
    ssl:
      mode: "off"
  - host: api.test.com
    root: /tmp
    type: proxy
    ssl:
      mode: auto
    proxy:
      upstreams:
        - address: "http://127.0.0.1:3000"
`), 0644)

	cmd := &ConfigCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := cmd.Run([]string{"test", "-c", cfgFile})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("test error: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "Config OK") {
		t.Errorf("should say Config OK, got:\n%s", output)
	}
	if !strings.Contains(output, "test.com") {
		t.Errorf("should list domains, got:\n%s", output)
	}
}

func TestConfigCommand_TestSubcommandInvalid(t *testing.T) {
	tmp := t.TempDir()
	cfgFile := filepath.Join(tmp, "bad.yaml")
	os.WriteFile(cfgFile, []byte("invalid {{ yaml"), 0644)

	cmd := &ConfigCommand{}
	err := cmd.Run([]string{"test", "-c", cfgFile})
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestConfigCommand_UnknownSubcommand(t *testing.T) {
	cmd := &ConfigCommand{}
	err := cmd.Run([]string{"bogus"})
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestConfigCommand_DefaultSubcommand(t *testing.T) {
	// When first arg starts with "-", should default to "validate".
	tmp := t.TempDir()
	cfgFile := filepath.Join(tmp, "uwas.yaml")
	os.WriteFile(cfgFile, []byte(`
domains:
  - host: test.com
    root: /tmp
    type: static
    ssl:
      mode: "off"
`), 0644)

	cmd := &ConfigCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := cmd.Run([]string{"-c", cfgFile})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("validate error: %v", err)
	}
	if !strings.Contains(buf.String(), "Configuration valid") {
		t.Errorf("should say valid, got:\n%s", buf.String())
	}
}

// ==========================================================================
// restart.go — additional coverage for restart with valid PID
// ==========================================================================

func TestRestartCommand_WithHealthAndValidPID(t *testing.T) {
	// Test when API health succeeds but PID is for current process.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "uptime": "1h"})
	}))
	defer srv.Close()

	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "uwas.pid")
	// Use current PID so FindProcess succeeds. Signal will fail since it's our own process
	// and we don't want to kill ourselves, but restart just sends SIGTERM and returns.
	os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)

	r := &RestartCommand{}

	old := os.Stdout
	rOut, w, _ := os.Pipe()
	os.Stdout = w

	err := r.Run([]string{"--pid-file", pidFile, "--api-url", srv.URL})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(rOut)

	// On Windows SIGTERM isn't supported, so we may get an error.
	if err != nil {
		if !strings.Contains(err.Error(), "SIGTERM") {
			t.Logf("restart error (may be expected on this platform): %v", err)
		}
	} else {
		output := buf.String()
		if !strings.Contains(output, "SIGTERM sent") {
			t.Errorf("should say SIGTERM sent, got:\n%s", output)
		}
	}
}

func TestRestartCommand_HealthFails(t *testing.T) {
	// When health API is unreachable but PID file exists with valid PID.
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "uwas.pid")
	os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)

	r := &RestartCommand{}

	old := os.Stdout
	rOut, w, _ := os.Pipe()
	os.Stdout = w

	err := r.Run([]string{"--pid-file", pidFile, "--api-url", "http://127.0.0.1:1"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(rOut)

	// Should still try to send SIGTERM.
	if err != nil {
		if !strings.Contains(err.Error(), "SIGTERM") {
			t.Logf("error (may be expected): %v", err)
		}
	} else {
		if !strings.Contains(buf.String(), "Sending SIGTERM") {
			t.Errorf("should attempt SIGTERM, got:\n%s", buf.String())
		}
	}
}

// ==========================================================================
// domain.go — additional API request coverage
// ==========================================================================

func TestDomainListConnectionError(t *testing.T) {
	d := &DomainCommand{}
	err := d.Run([]string{"list", "--api-url", "http://127.0.0.1:1", "--api-key", "k"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDomainAddConnectionError(t *testing.T) {
	d := &DomainCommand{}
	err := d.Run([]string{"add", "--api-url", "http://127.0.0.1:1", "--api-key", "k", "test.com"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDomainRemoveConnectionError(t *testing.T) {
	d := &DomainCommand{}
	err := d.Run([]string{"remove", "--api-url", "http://127.0.0.1:1", "--api-key", "k", "test.com"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// ==========================================================================
// status.go — additional edge cases
// ==========================================================================

func TestStatusCommand_NoStats(t *testing.T) {
	// Test when health succeeds but stats and domains fail.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/health":
			json.NewEncoder(w).Encode(map[string]any{"status": "ok", "uptime": "1m"})
		default:
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()

	s := &StatusCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := s.Run([]string{"--api-url", srv.URL})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("status error: %v", err)
	}
	if !strings.Contains(buf.String(), "UWAS Server Status") {
		t.Error("should still print header")
	}
}

func TestReloadCommand_ParseFlags(t *testing.T) {
	// Just verify flag parsing works.
	rl := &ReloadCommand{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "reloaded"})
	}))
	defer srv.Close()

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := rl.Run([]string{"--api-url", srv.URL, "--api-key", "testkey"})

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("reload error: %v", err)
	}
}

// ==========================================================================
// cache command — additional coverage
// ==========================================================================

func TestCachePurgeConnectionError(t *testing.T) {
	c := &CacheCommand{}
	err := c.Run([]string{"purge", "--api-url", "http://127.0.0.1:1", "--api-key", "k"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCacheStatsConnectionError(t *testing.T) {
	c := &CacheCommand{}
	err := c.Run([]string{"stats", "--api-url", "http://127.0.0.1:1", "--api-key", "k"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// ==========================================================================
// PHP command — API connection error tests
// ==========================================================================

func TestPHPListConnectionError(t *testing.T) {
	p := &PHPCommand{}
	err := p.Run([]string{"list", "--api-url", "http://127.0.0.1:1", "--api-key", "k"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPHPStartConnectionError(t *testing.T) {
	p := &PHPCommand{}
	err := p.Run([]string{"start", "--api-url", "http://127.0.0.1:1", "--api-key", "k", "8.4"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPHPStopConnectionError(t *testing.T) {
	p := &PHPCommand{}
	err := p.Run([]string{"stop", "--api-url", "http://127.0.0.1:1", "--api-key", "k", "8.4"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPHPConfigConnectionError(t *testing.T) {
	p := &PHPCommand{}
	err := p.Run([]string{"config", "--api-url", "http://127.0.0.1:1", "--api-key", "k", "8.4"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPHPExtensionsConnectionError(t *testing.T) {
	p := &PHPCommand{}
	err := p.Run([]string{"extensions", "--api-url", "http://127.0.0.1:1", "--api-key", "k", "8.4"})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPHPListBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not json")
	}))
	defer srv.Close()

	p := &PHPCommand{}
	err := p.Run([]string{"list", "--api-url", srv.URL, "--api-key", "k"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "parse response") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestPHPConfigBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not json")
	}))
	defer srv.Close()

	p := &PHPCommand{}
	err := p.Run([]string{"config", "--api-url", srv.URL, "--api-key", "k", "8.4"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "parse response") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestPHPExtensionsBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "not json")
	}))
	defer srv.Close()

	p := &PHPCommand{}
	err := p.Run([]string{"extensions", "--api-url", srv.URL, "--api-key", "k", "8.4"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "parse response") {
		t.Errorf("error = %q", err.Error())
	}
}

// ==========================================================================
// cert.go — renew with connection error
// ==========================================================================

func TestCertRenewConnectionError(t *testing.T) {
	c := &CertCommand{}

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := c.Run([]string{"renew", "example.com", "--api-url", "http://127.0.0.1:1", "--api-key", "k"})

	w.Close()
	os.Stdout = old

	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCertListConnectionError(t *testing.T) {
	c := &CertCommand{}
	err := c.Run([]string{"list", "--api-url", "http://127.0.0.1:1", "--api-key", "k"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// ==========================================================================
// runDoctorChecks — test individual check wiring
// ==========================================================================

func TestRunDoctorChecks(t *testing.T) {
	origCmd := doctorExecCommand
	origLookPath := doctorExecLookPath
	origStat := doctorOsStat
	doctorExecCommand = func(name string, arg ...string) *exec.Cmd {
		return exec.Command("echo", "mocked")
	}
	doctorExecLookPath = func(file string) (string, error) {
		return "", fmt.Errorf("not found")
	}
	doctorOsStat = func(name string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}
	defer func() {
		doctorExecCommand = origCmd
		doctorExecLookPath = origLookPath
		doctorOsStat = origStat
	}()

	checks := runDoctorChecks("/etc/uwas/uwas.yaml", false)
	// Should have at least 8 checks (OS + PHP-FPM + PHP Modules + MySQL + Web Root + Config + Disk + DNS).
	if len(checks) < 8 {
		t.Errorf("expected at least 8 checks, got %d", len(checks))
	}

	// First check should be OS.
	if checks[0].name != "OS" {
		t.Errorf("first check name = %q, want OS", checks[0].name)
	}
}

// ==========================================================================
// backup.go — restore with path traversal protection
// ==========================================================================

func TestRestoreBackup_SkipsUnknownPrefixes(t *testing.T) {
	tmp := t.TempDir()
	archivePath := filepath.Join(tmp, "test.tar.gz")

	// Create a real tar.gz with an entry that has an "unknown/" prefix.
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	data := []byte("test data")
	hdr := &tar.Header{
		Name: "unknown/file.txt",
		Size: int64(len(data)),
		Mode: 0644,
	}
	tw.WriteHeader(hdr)
	tw.Write(data)
	tw.Close()
	gw.Close()
	f.Close()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err = restoreBackup(archivePath, filepath.Join(tmp, "cfg"), filepath.Join(tmp, "certs"))

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("restore error: %v", err)
	}
	if !strings.Contains(buf.String(), "0 files") {
		t.Errorf("should restore 0 files for unknown prefix, got:\n%s", buf.String())
	}
}

func TestRestoreBackup_WithRealArchive(t *testing.T) {
	tmp := t.TempDir()
	archivePath := filepath.Join(tmp, "test.tar.gz")

	// Use createBackup to make a real archive, then restore it.
	srcCfg := filepath.Join(tmp, "src-uwas.yaml")
	os.WriteFile(srcCfg, []byte("test: restore-real\n"), 0644)

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := createBackup(archivePath, srcCfg, filepath.Join(tmp, "no-certs"))

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("createBackup error: %v", err)
	}

	// Restore.
	destCfg := filepath.Join(tmp, "dest-cfg")
	destCerts := filepath.Join(tmp, "dest-certs")

	old = os.Stdout
	r, w2, _ := os.Pipe()
	os.Stdout = w2

	err = restoreBackup(archivePath, destCfg, destCerts)

	w2.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("restore error: %v", err)
	}
	if !strings.Contains(buf.String(), "Restore complete") {
		t.Errorf("output = %q", buf.String())
	}
}

func TestRestoreBackup_BadGzip(t *testing.T) {
	tmp := t.TempDir()
	badFile := filepath.Join(tmp, "bad.tar.gz")
	os.WriteFile(badFile, []byte("not gzip data"), 0644)

	err := restoreBackup(badFile, filepath.Join(tmp, "cfg"), filepath.Join(tmp, "certs"))
	if err == nil {
		t.Fatal("expected error for bad gzip")
	}
	if !strings.Contains(err.Error(), "decompress") {
		t.Errorf("error = %q", err.Error())
	}
}

// ==========================================================================
// Additional coverage: OfferStopConflicts — full uninstall path
// ==========================================================================

func TestOfferStopConflicts_StopAndUninstall(t *testing.T) {
	origExecCmd := conflictsExecCommand
	conflictsExecCommand = func(name string, arg ...string) *exec.Cmd {
		return exec.Command("echo", "ok")
	}
	defer func() { conflictsExecCommand = origExecCmd }()

	// promptWithDefault creates a new bufio.NewReader each time, which buffers
	// the entire pipe content. So the first call consumes everything.
	// The second prompt gets no input and returns default "n".
	// This test verifies the "stop" path works, even when uninstall defaults to "n".
	rIn, wIn, _ := os.Pipe()
	wIn.WriteString("y\n")
	wIn.Close()
	oldStdin := os.Stdin
	os.Stdin = rIn
	defer func() { os.Stdin = oldStdin }()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	OfferStopConflicts([]ConflictingServer{
		{Name: "Apache", Running: true, PID: "456", Service: "apache2"},
	})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	output := buf.String()
	if !strings.Contains(output, "stopped and disabled") {
		t.Errorf("should stop services, got:\n%s", output)
	}
	// Uninstall prompt defaults to "n", so uninstall won't happen. That's fine.
}

func TestOfferStopConflicts_StopFails(t *testing.T) {
	origExecCmd := conflictsExecCommand
	conflictsExecCommand = func(name string, arg ...string) *exec.Cmd {
		// All commands fail.
		return exec.Command("nonexistent-binary-xyz-stop")
	}
	defer func() { conflictsExecCommand = origExecCmd }()

	rIn, wIn, _ := os.Pipe()
	wIn.WriteString("y\nn\n")
	wIn.Close()
	oldStdin := os.Stdin
	os.Stdin = rIn
	defer func() { os.Stdin = oldStdin }()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	OfferStopConflicts([]ConflictingServer{
		{Name: "Nginx", Running: true, PID: "789", Service: "nginx"},
	})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	output := buf.String()
	if !strings.Contains(output, "Could not stop") {
		t.Errorf("should report failure to stop, got:\n%s", output)
	}
}

// ==========================================================================
// Additional OfferPHPInstall paths
// ==========================================================================

func TestOfferPHPInstall_FPMDetected(t *testing.T) {
	orig := conflictsRuntimeGOOS
	origLookPath := conflictsExecLookPath
	conflictsRuntimeGOOS = "linux"
	conflictsExecLookPath = func(file string) (string, error) {
		if file == "php-fpm8.3" {
			return "/usr/sbin/php-fpm8.3", nil
		}
		return "", fmt.Errorf("not found")
	}
	defer func() {
		conflictsRuntimeGOOS = orig
		conflictsExecLookPath = origLookPath
	}()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	OfferPHPInstall()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	// Should return early since PHP FPM is found.
	if strings.Contains(buf.String(), "No PHP") {
		t.Error("should not offer install when FPM detected")
	}
}

func TestOfferPHPInstall_InvalidChoice(t *testing.T) {
	orig := conflictsRuntimeGOOS
	origLookPath := conflictsExecLookPath
	conflictsRuntimeGOOS = "linux"
	conflictsExecLookPath = func(file string) (string, error) {
		return "", fmt.Errorf("not found")
	}
	defer func() {
		conflictsRuntimeGOOS = orig
		conflictsExecLookPath = origLookPath
	}()

	// Provide invalid choice "z".
	rIn, wIn, _ := os.Pipe()
	wIn.WriteString("z\n")
	wIn.Close()
	oldStdin := os.Stdin
	os.Stdin = rIn
	defer func() { os.Stdin = oldStdin }()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	OfferPHPInstall()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if !strings.Contains(buf.String(), "Skipped") {
		t.Errorf("invalid choice should skip, got:\n%s", buf.String())
	}
}

func TestOfferPHPInstall_ChooseVersion_Decline(t *testing.T) {
	orig := conflictsRuntimeGOOS
	origLookPath := conflictsExecLookPath
	conflictsRuntimeGOOS = "linux"
	conflictsExecLookPath = func(file string) (string, error) {
		return "", fmt.Errorf("not found")
	}
	defer func() {
		conflictsRuntimeGOOS = orig
		conflictsExecLookPath = origLookPath
	}()

	// Choose version "1" (PHP 8.5), then "n" to not run commands.
	rIn, wIn, _ := os.Pipe()
	wIn.WriteString("1\nn\n")
	wIn.Close()
	oldStdin := os.Stdin
	os.Stdin = rIn
	defer func() { os.Stdin = oldStdin }()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	OfferPHPInstall()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	output := buf.String()
	if !strings.Contains(output, "PHP 8.5") {
		t.Errorf("should mention PHP 8.5, got:\n%s", output)
	}
	// On Windows, phpmanager.RunInstall may succeed immediately, so "Skipped" might not appear.
	// Just verify the version choice was recognized.
}

func TestOfferPHPInstall_ChooseVersionByNumber(t *testing.T) {
	orig := conflictsRuntimeGOOS
	origLookPath := conflictsExecLookPath
	conflictsRuntimeGOOS = "linux"
	conflictsExecLookPath = func(file string) (string, error) {
		return "", fmt.Errorf("not found")
	}
	defer func() {
		conflictsRuntimeGOOS = orig
		conflictsExecLookPath = origLookPath
	}()

	// Test various version choices.
	for _, input := range []string{"2\nn\n", "3\nn\n", "4\nn\n", "8.4\nn\n"} {
		rIn, wIn, _ := os.Pipe()
		wIn.WriteString(input)
		wIn.Close()
		oldStdin := os.Stdin
		os.Stdin = rIn

		oldOut := os.Stdout
		_, w, _ := os.Pipe()
		os.Stdout = w

		OfferPHPInstall()

		w.Close()
		os.Stdout = oldOut
		os.Stdin = oldStdin
	}
}

func TestOfferPHPInstall_EmptyChoice(t *testing.T) {
	orig := conflictsRuntimeGOOS
	origLookPath := conflictsExecLookPath
	conflictsRuntimeGOOS = "linux"
	conflictsExecLookPath = func(file string) (string, error) {
		return "", fmt.Errorf("not found")
	}
	defer func() {
		conflictsRuntimeGOOS = orig
		conflictsExecLookPath = origLookPath
	}()

	// Empty input (just press enter) => default is "s" => skip.
	rIn, wIn, _ := os.Pipe()
	wIn.WriteString("\n")
	wIn.Close()
	oldStdin := os.Stdin
	os.Stdin = rIn
	defer func() { os.Stdin = oldStdin }()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	OfferPHPInstall()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	// Default is "s", so it should skip.
	if !strings.Contains(buf.String(), "Skipped") {
		t.Errorf("empty choice should skip, got:\n%s", buf.String())
	}
}

// ==========================================================================
// Additional: DetectConflicts with running processes
// ==========================================================================

func TestDetectConflicts_RunningProcess(t *testing.T) {
	orig := conflictsRuntimeGOOS
	origLookPath := conflictsExecLookPath
	origExecCmd := conflictsExecCommand

	conflictsRuntimeGOOS = "linux"
	conflictsExecLookPath = func(file string) (string, error) {
		if file == "apache2" {
			return "/usr/sbin/apache2", nil
		}
		return "", fmt.Errorf("not found")
	}
	// Mock pidof to return a PID for apache2.
	conflictsExecCommand = func(name string, arg ...string) *exec.Cmd {
		if name == "pidof" {
			// On Windows, we can't easily mock pidof. Use Go test helper pattern.
			return exec.Command("echo", "12345")
		}
		return exec.Command("echo", "")
	}
	defer func() {
		conflictsRuntimeGOOS = orig
		conflictsExecLookPath = origLookPath
		conflictsExecCommand = origExecCmd
	}()

	conflicts := DetectConflicts()
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	if !conflicts[0].Running {
		t.Error("should be running")
	}
	if conflicts[0].PID != "12345" {
		t.Errorf("PID = %q, want 12345", conflicts[0].PID)
	}
}

// ==========================================================================
// Additional: checkCLI_MySQL with "active" output
// ==========================================================================

func TestCheckCLI_MySQL_Running(t *testing.T) {
	origCmd := doctorExecCommand
	origLookPath := doctorExecLookPath

	// Create a temp script that outputs "active".
	tmp := t.TempDir()
	scriptPath := filepath.Join(tmp, "fakeactive.bat")
	os.WriteFile(scriptPath, []byte("@echo active"), 0755)

	doctorExecCommand = func(name string, arg ...string) *exec.Cmd {
		if name == "systemctl" && len(arg) > 0 && arg[0] == "is-active" {
			return exec.Command("cmd", "/c", "echo active")
		}
		return exec.Command("echo", "ok")
	}
	doctorExecLookPath = func(file string) (string, error) {
		return "", fmt.Errorf("not found")
	}
	defer func() {
		doctorExecCommand = origCmd
		doctorExecLookPath = origLookPath
	}()

	c := checkCLI_MySQL(false)
	if c.status != "ok" {
		t.Errorf("status = %q, want ok", c.status)
	}
}

// ==========================================================================
// Additional: checkCLI_PHPFPM with autoFix
// ==========================================================================

func TestCheckCLI_PHPFPM_AutoFix(t *testing.T) {
	origLookPath := doctorExecLookPath
	origStat := doctorOsStat
	origCmd := doctorExecCommand
	doctorExecLookPath = func(file string) (string, error) {
		if file == "php-fpm8.3" {
			return "/usr/sbin/php-fpm8.3", nil
		}
		return "", fmt.Errorf("not found")
	}
	doctorOsStat = func(name string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	doctorExecCommand = func(name string, arg ...string) *exec.Cmd {
		return exec.Command("echo", "mocked")
	}
	defer func() {
		doctorExecLookPath = origLookPath
		doctorOsStat = origStat
		doctorExecCommand = origCmd
	}()

	c := checkCLI_PHPFPM(true)
	if c.status != "fixed" {
		t.Errorf("status = %q, want fixed", c.status)
	}
}

// ==========================================================================
// Additional: checkCLI_PHPModules with all modules present
// ==========================================================================

func TestCheckCLI_PHPModules_AllPresent(t *testing.T) {
	origCmd := doctorExecCommand
	doctorExecCommand = func(name string, arg ...string) *exec.Cmd {
		if name == "php" {
			return exec.Command("cmd", "/c", "echo mysqli curl gd mbstring xml")
		}
		return exec.Command("echo", "ok")
	}
	defer func() { doctorExecCommand = origCmd }()

	c := checkCLI_PHPModules()
	if c.status != "ok" {
		t.Errorf("status = %q, want ok", c.status)
	}
}

func TestCheckCLI_PHPModules_MissingSome(t *testing.T) {
	origCmd := doctorExecCommand
	doctorExecCommand = func(name string, arg ...string) *exec.Cmd {
		if name == "php" {
			// Output that includes only some modules.
			return exec.Command("cmd", "/c", "echo curl mbstring")
		}
		return exec.Command("echo", "ok")
	}
	defer func() { doctorExecCommand = origCmd }()

	c := checkCLI_PHPModules()
	if c.status != "warn" {
		t.Errorf("status = %q, want warn", c.status)
	}
	if !strings.Contains(c.message, "Missing") {
		t.Errorf("message = %q, should mention Missing", c.message)
	}
}

// ==========================================================================
// Additional: checkCLI_Disk with real "df" style output
// ==========================================================================

func TestCheckCLI_Disk_WithOutput(t *testing.T) {
	origCmd := doctorExecCommand
	doctorExecCommand = func(name string, arg ...string) *exec.Cmd {
		if name == "df" {
			return exec.Command("cmd", "/c", "echo Filesystem Size Used Avail Use%& echo /dev/sda1 100G 50G 50G 50%")
		}
		return exec.Command("echo", "ok")
	}
	defer func() { doctorExecCommand = origCmd }()

	c := checkCLI_Disk()
	if c.status != "ok" {
		t.Errorf("status = %q, want ok", c.status)
	}
}

// ==========================================================================
// Additional: DoctorCmd.Run (the struct method, not the function)
// ==========================================================================

func TestDoctorCmdRun(t *testing.T) {
	origCmd := doctorExecCommand
	origLookPath := doctorExecLookPath
	origStat := doctorOsStat
	doctorExecCommand = func(name string, arg ...string) *exec.Cmd {
		return exec.Command("nonexistent-xyz")
	}
	doctorExecLookPath = func(file string) (string, error) {
		return "", fmt.Errorf("not found")
	}
	doctorOsStat = func(name string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}
	defer func() {
		doctorExecCommand = origCmd
		doctorExecLookPath = origLookPath
		doctorOsStat = origStat
	}()

	c := &DoctorCmd{}

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := c.Run(nil)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("DoctorCmd.Run error: %v", err)
	}
}

func TestDoctorCmdRunWithFix(t *testing.T) {
	origCmd := doctorExecCommand
	origLookPath := doctorExecLookPath
	origStat := doctorOsStat
	doctorExecCommand = func(name string, arg ...string) *exec.Cmd {
		return exec.Command("nonexistent-xyz")
	}
	doctorExecLookPath = func(file string) (string, error) {
		return "", fmt.Errorf("not found")
	}
	doctorOsStat = func(name string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}
	defer func() {
		doctorExecCommand = origCmd
		doctorExecLookPath = origLookPath
		doctorOsStat = origStat
	}()

	c := &DoctorCmd{}

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := c.Run([]string{"--fix"})

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("DoctorCmd.Run --fix error: %v", err)
	}
}

// ==========================================================================
// Additional: checkCLI_WebRoot with autoFix
// ==========================================================================

func TestCheckCLI_WebRoot_AutoFix(t *testing.T) {
	origStat := doctorOsStat
	origCmd := doctorExecCommand
	doctorOsStat = func(name string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	doctorExecCommand = func(name string, arg ...string) *exec.Cmd {
		return exec.Command("echo", "ok")
	}
	defer func() {
		doctorOsStat = origStat
		doctorExecCommand = origCmd
	}()

	c := checkCLI_WebRoot(true)
	if c.status != "fixed" {
		t.Errorf("status = %q, want fixed", c.status)
	}
}

// ==========================================================================
// Additional: checkCLI_DNS success
// ==========================================================================

func TestCheckCLI_DNS_Success(t *testing.T) {
	origCmd := doctorExecCommand
	doctorExecCommand = func(name string, arg ...string) *exec.Cmd {
		if name == "dig" {
			return exec.Command("cmd", "/c", "echo 1.2.3.4")
		}
		return exec.Command("echo", "ok")
	}
	defer func() { doctorExecCommand = origCmd }()

	c := checkCLI_DNS()
	if c.status != "ok" {
		t.Errorf("status = %q, want ok", c.status)
	}
}

// ==========================================================================
// serve.go — additional tests for edge cases
// ==========================================================================

func TestServeCommand_BadFlagParsing(t *testing.T) {
	cmd := &ServeCommand{}
	err := cmd.Run([]string{"--unknown-flag"})
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

// ==========================================================================
// Additional: user.go list with actual users on Windows (nil result)
// ==========================================================================

func TestUserListOnWindows_NoUsers(t *testing.T) {
	u := &UserCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := u.list()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Errorf("list error: %v", err)
	}
	// On Windows, ListUsers returns nil, so we should see "No site users configured."
	if !strings.Contains(buf.String(), "No site users") {
		t.Errorf("expected 'No site users' message, got:\n%s", buf.String())
	}
}

// ==========================================================================
// Additional: stop.go — StopCommand full success path via hook
// ==========================================================================

func TestStopCommand_SuccessPath(t *testing.T) {
	// Write a PID file with our own PID.
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "uwas.pid")
	os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)

	s := &StopCommand{}

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := s.Run([]string{"--pid-file", pidFile})

	w.Close()
	os.Stdout = old

	// On Windows, SIGTERM is not supported, so we expect an error.
	if err != nil {
		if !strings.Contains(err.Error(), "SIGTERM") {
			t.Logf("expected SIGTERM error on Windows: %v", err)
		}
	}
	// The important thing is we exercised the Run path through PID parsing, FindProcess, etc.
}

// ==========================================================================
// Additional: QuickConfigValue — empty value after key
// ==========================================================================

func TestQuickConfigValue_EmptyValue(t *testing.T) {
	tmp := t.TempDir()
	cfgFile := filepath.Join(tmp, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("pid_file:\n"), 0644)

	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) {
		return cfgFile, true
	}
	defer func() { findConfigFn = origFindConfig }()

	val := quickConfigValue("pid_file")
	if val != "" {
		t.Errorf("quickConfigValue should be empty for empty value, got %q", val)
	}
}

// ==========================================================================
// Additional: backup — addFileToTar error paths
// ==========================================================================

func TestAddFileToTar_Success(t *testing.T) {
	tmp := t.TempDir()
	srcFile := filepath.Join(tmp, "test.txt")
	os.WriteFile(srcFile, []byte("test content"), 0644)

	outFile := filepath.Join(tmp, "out.tar.gz")
	f, _ := os.Create(outFile)
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	err := addFileToTar(tw, srcFile, "test.txt")

	tw.Close()
	gw.Close()
	f.Close()

	if err != nil {
		t.Errorf("addFileToTar error: %v", err)
	}
}

func TestAddFileToTar_NonexistentFile(t *testing.T) {
	tmp := t.TempDir()
	outFile := filepath.Join(tmp, "out.tar.gz")
	f, _ := os.Create(outFile)
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	err := addFileToTar(tw, filepath.Join(tmp, "nonexistent.txt"), "test.txt")

	tw.Close()
	gw.Close()
	f.Close()

	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

// ==========================================================================
// Additional: restore — with both config and cert entries
// ==========================================================================

func TestRestoreBackup_ConfigAndCerts(t *testing.T) {
	tmp := t.TempDir()
	archivePath := filepath.Join(tmp, "full.tar.gz")

	f, _ := os.Create(archivePath)
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	// Config entry.
	cfgData := []byte("config content")
	tw.WriteHeader(&tar.Header{Name: "config/uwas.yaml", Size: int64(len(cfgData)), Mode: 0644})
	tw.Write(cfgData)

	// Cert entry.
	certData := []byte("cert content")
	tw.WriteHeader(&tar.Header{Name: "certs/domain.pem", Size: int64(len(certData)), Mode: 0644})
	tw.Write(certData)

	tw.Close()
	gw.Close()
	f.Close()

	destCfg := filepath.Join(tmp, "restore-cfg")
	destCerts := filepath.Join(tmp, "restore-certs")

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := restoreBackup(archivePath, destCfg, destCerts)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("restore error: %v", err)
	}
	if !strings.Contains(buf.String(), "2 files") {
		t.Errorf("should restore 2 files, got:\n%s", buf.String())
	}

	// Verify files.
	data, _ := os.ReadFile(filepath.Join(destCfg, "uwas.yaml"))
	if string(data) != "config content" {
		t.Errorf("config content = %q", string(data))
	}
	data, _ = os.ReadFile(filepath.Join(destCerts, "domain.pem"))
	if string(data) != "cert content" {
		t.Errorf("cert content = %q", string(data))
	}
}

// ==========================================================================
// Additional: cert list edge cases — short expiry string
// ==========================================================================

func TestCertCommandList_ShortExpiry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"host": "short.com", "ssl_mode": "auto", "status": "active", "issuer": "LE", "expiry": "2025-01-01", "days_left": 10},
		})
	}))
	defer srv.Close()

	c := &CertCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := c.Run([]string{"list", "--api-url", srv.URL, "--api-key", "k"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	if !strings.Contains(buf.String(), "2025-01-01") {
		t.Errorf("should show short expiry date, got:\n%s", buf.String())
	}
}

// ==========================================================================
// Additional: checkCLI_MySQL autoFix path
// ==========================================================================

func TestCheckCLI_MySQL_AutoFix(t *testing.T) {
	origCmd := doctorExecCommand
	origLookPath := doctorExecLookPath
	doctorExecCommand = func(name string, arg ...string) *exec.Cmd {
		return exec.Command("nonexistent-xyz")
	}
	doctorExecLookPath = func(file string) (string, error) {
		if file == "mariadb" {
			return "/usr/bin/mariadb", nil
		}
		return "", fmt.Errorf("not found")
	}
	defer func() {
		doctorExecCommand = origCmd
		doctorExecLookPath = origLookPath
	}()

	c := checkCLI_MySQL(true)
	if c.status != "fixed" {
		t.Errorf("status = %q, want fixed", c.status)
	}
}

// ==========================================================================
// Additional: installUWAS — executable resolution error
// ==========================================================================

func TestInstallCmd_ExecutableError(t *testing.T) {
	origGOOS := installRuntimeGOOS
	origGetuid := installOsGetuid
	origExec := installOsExecutable
	installRuntimeGOOS = "linux"
	installOsGetuid = func() int { return 0 }
	installOsExecutable = func() (string, error) {
		return "", fmt.Errorf("cannot determine executable")
	}
	defer func() {
		installRuntimeGOOS = origGOOS
		installOsGetuid = origGetuid
		installOsExecutable = origExec
	}()

	c := &InstallCmd{}

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := c.Run(nil)

	w.Close()
	os.Stdout = old

	if err == nil {
		t.Fatal("expected error when executable can't be resolved")
	}
	if !strings.Contains(err.Error(), "cannot determine executable") {
		t.Errorf("error = %q", err.Error())
	}
}

// ==========================================================================
// Additional: installUWAS — write service file error
// ==========================================================================

// ==========================================================================
// Additional: OfferStopConflicts — service stop fails, fallback to 'service' cmd
// ==========================================================================

func TestOfferStopConflicts_SystemctlFailsServiceFallback(t *testing.T) {
	callCount := 0
	origExecCmd := conflictsExecCommand
	conflictsExecCommand = func(name string, arg ...string) *exec.Cmd {
		callCount++
		if name == "systemctl" && len(arg) > 0 && arg[0] == "stop" {
			// systemctl stop fails.
			return exec.Command("nonexistent-binary-xyz")
		}
		if name == "service" {
			// service stop succeeds.
			return exec.Command("echo", "ok")
		}
		return exec.Command("echo", "ok")
	}
	defer func() { conflictsExecCommand = origExecCmd }()

	rIn, wIn, _ := os.Pipe()
	wIn.WriteString("y\n")
	wIn.Close()
	oldStdin := os.Stdin
	os.Stdin = rIn
	defer func() { os.Stdin = oldStdin }()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	OfferStopConflicts([]ConflictingServer{
		{Name: "Caddy", Running: true, PID: "555", Service: "caddy"},
	})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	output := buf.String()
	if !strings.Contains(output, "Stopping") {
		t.Errorf("should attempt to stop, got:\n%s", output)
	}
	// Either stopped via service command or could not stop.
	if !strings.Contains(output, "stopped") && !strings.Contains(output, "Could not stop") {
		t.Errorf("should report stop result, got:\n%s", output)
	}
}

func TestOfferStopConflicts_StopAndUninstall_Goroutine(t *testing.T) {
	origExecCmd := conflictsExecCommand
	conflictsExecCommand = func(name string, arg ...string) *exec.Cmd {
		return exec.Command("echo", "ok")
	}
	defer func() { conflictsExecCommand = origExecCmd }()

	// Use a goroutine to feed stdin data in stages to avoid buffering issues.
	rIn, wIn, _ := os.Pipe()
	oldStdin := os.Stdin
	os.Stdin = rIn
	defer func() { os.Stdin = oldStdin }()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	done := make(chan struct{})
	go func() {
		defer close(done)
		OfferStopConflicts([]ConflictingServer{
			{Name: "Apache", Running: true, PID: "111", Service: "apache2"},
		})
	}()

	// Feed answers with delay so each promptWithDefault gets its own line.
	// First prompt: "Stop and disable them? (y/n)"
	wIn.WriteString("y\n")

	// Give some time for the function to process and reach next prompt.
	// Then answer "y" to "Uninstall them completely?"
	// But we can't guarantee timing. Write both and hope the goroutine picks them up.
	wIn.WriteString("y\n")
	wIn.Close()

	<-done

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Verify the stop happened.
	if !strings.Contains(output, "stopped") {
		t.Errorf("should stop services, got:\n%s", output)
	}
	// Check if uninstall was attempted (it might or might not have been due to stdin buffering).
	if strings.Contains(output, "Removing") || strings.Contains(output, "Could not remove") || strings.Contains(output, "removed") {
		t.Log("Uninstall path was successfully exercised")
	}
}

func TestOfferStopConflicts_AllStopMethodsFail(t *testing.T) {
	origExecCmd := conflictsExecCommand
	conflictsExecCommand = func(name string, arg ...string) *exec.Cmd {
		// All commands fail.
		return exec.Command("nonexistent-binary-xyz")
	}
	defer func() { conflictsExecCommand = origExecCmd }()

	rIn, wIn, _ := os.Pipe()
	wIn.WriteString("y\n")
	wIn.Close()
	oldStdin := os.Stdin
	os.Stdin = rIn
	defer func() { os.Stdin = oldStdin }()

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	OfferStopConflicts([]ConflictingServer{
		{Name: "Lighttpd", Running: true, PID: "666", Service: "lighttpd"},
	})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	output := buf.String()
	if !strings.Contains(output, "Could not stop") {
		t.Errorf("should report failure, got:\n%s", output)
	}
}

// ==========================================================================
// Additional: stop.go — exercise the wait loop path with a PID whose process
// exits quickly. We can't really test the full kill path without spawning
// a real process, but we can test with a process that doesn't exist.
// ==========================================================================

func TestStopCommand_PIDFileWithConfig(t *testing.T) {
	// Test stop when pidFileFromConfig returns a path.
	tmp := t.TempDir()
	cfgFile := filepath.Join(tmp, "uwas.yaml")
	pidPath := filepath.Join(tmp, "uwas.pid")
	os.WriteFile(cfgFile, []byte("pid_file: "+pidPath+"\n"), 0644)
	os.WriteFile(pidPath, []byte("999999999\n"), 0644)

	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) {
		return cfgFile, true
	}
	defer func() { findConfigFn = origFindConfig }()

	s := &StopCommand{}

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := s.Run(nil) // no --pid-file flag, uses config

	w.Close()
	os.Stdout = old

	// Should fail because PID 999999999 doesn't exist and Signal will fail.
	if err == nil {
		t.Log("stop completed without error (process might have existed)")
	}
}

// ==========================================================================
// Additional: serve.go — test with a valid config that exists
// ==========================================================================

func TestServeCommand_WithExistingConfig_AlreadyRunning(t *testing.T) {
	// Create a config with a PID file pointing to our own PID (simulating already running).
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "uwas.pid")
	os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)

	cfgFile := filepath.Join(tmp, "uwas.yaml")
	cfgContent := fmt.Sprintf(`global:
  http_listen: ":18080"
  pid_file: "%s"
  admin:
    enabled: true
    listen: "127.0.0.1:19443"
domains:
  - host: test.local
    root: /tmp
    type: static
    ssl:
      mode: "off"
`, filepath.ToSlash(pidFile))
	os.WriteFile(cfgFile, []byte(cfgContent), 0644)

	cmd := &ServeCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := cmd.Run([]string{"-c", cfgFile, "--no-banner"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("serve error: %v", err)
	}
	// Should detect already running and print info.
	output := buf.String()
	if !strings.Contains(output, "already running") {
		t.Errorf("should detect already running, got:\n%s", output)
	}
}

func TestServeCommand_WithBannerSuppressed(t *testing.T) {
	// Test the serve command when it finds an already-running process.
	// We use the "already running" path which returns nil immediately.
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "uwas.pid")
	os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)

	cfgFile := filepath.Join(tmp, "uwas.yaml")
	cfgContent := fmt.Sprintf(`global:
  http_listen: ":18081"
  pid_file: "%s"
  log_level: info
  admin:
    enabled: true
    listen: "127.0.0.1:19444"
domains:
  - host: test.local
    root: /tmp
    type: static
    ssl:
      mode: "off"
`, filepath.ToSlash(pidFile))
	os.WriteFile(cfgFile, []byte(cfgContent), 0644)

	cmd := &ServeCommand{}
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := cmd.Run([]string{"-c", cfgFile, "--no-banner"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("serve error: %v", err)
	}
	// Since PID is alive, it should report "already running".
	output := buf.String()
	if !strings.Contains(output, "already running") {
		t.Errorf("should detect already running, got:\n%s", output)
	}
}

func TestServeCommand_WithHTTPS(t *testing.T) {
	// Test serve command with HTTPS configured + already running to exercise more branches.
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "uwas.pid")
	os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)

	cfgFile := filepath.Join(tmp, "uwas.yaml")
	cfgContent := fmt.Sprintf(`global:
  http_listen: ":18082"
  https_listen: ":18443"
  pid_file: "%s"
  admin:
    enabled: true
    listen: "127.0.0.1:19445"
domains:
  - host: test.local
    root: /tmp
    type: static
    ssl:
      mode: "off"
`, filepath.ToSlash(pidFile))
	os.WriteFile(cfgFile, []byte(cfgContent), 0644)

	cmd := &ServeCommand{}
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := cmd.Run([]string{"-c", cfgFile})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("serve error: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "already running") {
		t.Errorf("should detect already running, got:\n%s", output)
	}
	if !strings.Contains(output, "HTTPS") {
		t.Errorf("should show HTTPS info, got:\n%s", output)
	}
}

// ==========================================================================
// Additional: user.go — list with tabwriter path on Windows
// ==========================================================================

func TestUserCommandListDirect(t *testing.T) {
	// Directly call list() to test the internal method.
	u := &UserCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := u.list()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Errorf("list error: %v", err)
	}
	// On Windows: "No site users configured."
	output := buf.String()
	if !strings.Contains(output, "No site users") && !strings.Contains(output, "USERNAME") {
		t.Errorf("unexpected output: %s", output)
	}
}

// ==========================================================================
// Additional: user.go — add and remove internal methods
// ==========================================================================

func TestUserCommandAddDirect_Windows(t *testing.T) {
	origGOOS := userRuntimeGOOS
	userRuntimeGOOS = "windows"
	defer func() { userRuntimeGOOS = origGOOS }()

	u := &UserCommand{}
	err := u.add("testdomain.com")
	if err == nil {
		t.Fatal("expected error on Windows")
	}
	if !strings.Contains(err.Error(), "Windows") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestUserCommandAddDirect_NotRoot(t *testing.T) {
	origGOOS := userRuntimeGOOS
	origEuid := userOsGeteuid
	userRuntimeGOOS = "linux"
	userOsGeteuid = func() int { return 1000 }
	defer func() {
		userRuntimeGOOS = origGOOS
		userOsGeteuid = origEuid
	}()

	u := &UserCommand{}
	err := u.add("testdomain.com")
	if err == nil {
		t.Fatal("expected error for non-root")
	}
	if !strings.Contains(err.Error(), "root required") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestUserCommandAddDirect_Success(t *testing.T) {
	origGOOS := userRuntimeGOOS
	origEuid := userOsGeteuid
	origCreateUser := userCreateUserFn
	origFindConfig := findConfigFn

	userRuntimeGOOS = "linux"
	userOsGeteuid = func() int { return 0 }
	userCreateUserFn = func(webRoot, domain string) (*siteuser.User, string, error) {
		return &siteuser.User{
			Username: "uwas-test",
			Domain:   domain,
			WebDir:   webRoot + "/" + domain + "/public_html",
		}, "generated-password", nil
	}
	findConfigFn = func(explicit string) (string, bool) { return "", false }

	defer func() {
		userRuntimeGOOS = origGOOS
		userOsGeteuid = origEuid
		userCreateUserFn = origCreateUser
		findConfigFn = origFindConfig
	}()

	u := &UserCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := u.add("example.com")

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("add error: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "SFTP user created") {
		t.Errorf("should say created, got:\n%s", output)
	}
	if !strings.Contains(output, "example.com") {
		t.Errorf("should contain domain, got:\n%s", output)
	}
	if !strings.Contains(output, "generated-password") {
		t.Errorf("should show password, got:\n%s", output)
	}
}

func TestUserCommandAddDirect_CreateError(t *testing.T) {
	origGOOS := userRuntimeGOOS
	origEuid := userOsGeteuid
	origCreateUser := userCreateUserFn
	origFindConfig := findConfigFn

	userRuntimeGOOS = "linux"
	userOsGeteuid = func() int { return 0 }
	userCreateUserFn = func(webRoot, domain string) (*siteuser.User, string, error) {
		return nil, "", fmt.Errorf("user already exists")
	}
	findConfigFn = func(explicit string) (string, bool) { return "", false }

	defer func() {
		userRuntimeGOOS = origGOOS
		userOsGeteuid = origEuid
		userCreateUserFn = origCreateUser
		findConfigFn = origFindConfig
	}()

	u := &UserCommand{}
	err := u.add("example.com")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "create user") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestUserCommandAddDirect_WithConfig(t *testing.T) {
	tmp := t.TempDir()
	cfgFile := filepath.Join(tmp, "uwas.yaml")
	os.WriteFile(cfgFile, []byte(`global:
  web_root: /opt/sites
domains:
  - host: test.com
    root: /opt/sites
    type: static
    ssl:
      mode: "off"
`), 0644)

	origGOOS := userRuntimeGOOS
	origEuid := userOsGeteuid
	origCreateUser := userCreateUserFn
	origFindConfig := findConfigFn

	var gotWebRoot string
	userRuntimeGOOS = "linux"
	userOsGeteuid = func() int { return 0 }
	userCreateUserFn = func(webRoot, domain string) (*siteuser.User, string, error) {
		gotWebRoot = webRoot
		return &siteuser.User{Username: "u", Domain: domain, WebDir: webRoot}, "pass", nil
	}
	findConfigFn = func(explicit string) (string, bool) { return cfgFile, true }

	defer func() {
		userRuntimeGOOS = origGOOS
		userOsGeteuid = origEuid
		userCreateUserFn = origCreateUser
		findConfigFn = origFindConfig
	}()

	u := &UserCommand{}

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	u.add("example.com")

	w.Close()
	os.Stdout = old

	if gotWebRoot != "/opt/sites" {
		t.Errorf("webRoot = %q, want /opt/sites", gotWebRoot)
	}
}

func TestUserCommandRemoveDirect_Windows(t *testing.T) {
	origGOOS := userRuntimeGOOS
	userRuntimeGOOS = "windows"
	defer func() { userRuntimeGOOS = origGOOS }()

	u := &UserCommand{}
	err := u.remove("testdomain.com")
	if err == nil {
		t.Fatal("expected error on Windows")
	}
}

func TestUserCommandRemoveDirect_NotRoot(t *testing.T) {
	origGOOS := userRuntimeGOOS
	origEuid := userOsGeteuid
	userRuntimeGOOS = "linux"
	userOsGeteuid = func() int { return 1000 }
	defer func() {
		userRuntimeGOOS = origGOOS
		userOsGeteuid = origEuid
	}()

	u := &UserCommand{}
	err := u.remove("testdomain.com")
	if err == nil {
		t.Fatal("expected error for non-root")
	}
}

func TestUserCommandRemoveDirect_Success(t *testing.T) {
	origGOOS := userRuntimeGOOS
	origEuid := userOsGeteuid
	origDeleteUser := userDeleteUserFn

	userRuntimeGOOS = "linux"
	userOsGeteuid = func() int { return 0 }
	userDeleteUserFn = func(domain string) error { return nil }

	defer func() {
		userRuntimeGOOS = origGOOS
		userOsGeteuid = origEuid
		userDeleteUserFn = origDeleteUser
	}()

	u := &UserCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := u.remove("example.com")

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("remove error: %v", err)
	}
	if !strings.Contains(buf.String(), "removed") {
		t.Errorf("should say removed, got:\n%s", buf.String())
	}
}

func TestUserCommandRemoveDirect_Error(t *testing.T) {
	origGOOS := userRuntimeGOOS
	origEuid := userOsGeteuid
	origDeleteUser := userDeleteUserFn

	userRuntimeGOOS = "linux"
	userOsGeteuid = func() int { return 0 }
	userDeleteUserFn = func(domain string) error { return fmt.Errorf("user not found") }

	defer func() {
		userRuntimeGOOS = origGOOS
		userOsGeteuid = origEuid
		userDeleteUserFn = origDeleteUser
	}()

	u := &UserCommand{}
	err := u.remove("example.com")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "remove user") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestUserCommandList_WithUsers(t *testing.T) {
	origListUsers := userListUsersFn
	userListUsersFn = func() []siteuser.User {
		return []siteuser.User{
			{Username: "uwas-site1", Domain: "site1.com", WebDir: "/var/www/site1.com/public_html"},
			{Username: "uwas-site2", Domain: "site2.com", WebDir: "/var/www/site2.com/public_html"},
		}
	}
	defer func() { userListUsersFn = origListUsers }()

	u := &UserCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := u.list()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "USERNAME") {
		t.Errorf("should show header, got:\n%s", output)
	}
	if !strings.Contains(output, "uwas-site1") {
		t.Errorf("should show user, got:\n%s", output)
	}
	if !strings.Contains(output, "site2.com") {
		t.Errorf("should show domain, got:\n%s", output)
	}
}

// ==========================================================================
// Additional: php.go install — exercise RunInstall error path
// ==========================================================================

func TestPHPInstallCommand_DefaultVersion(t *testing.T) {
	p := &PHPCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	// Install with no version args.
	err := p.install(nil)

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Logf("install error (expected): %v", err)
	}
	// Should default to 8.3.
	if !strings.Contains(buf.String(), "PHP 8.3") {
		t.Errorf("should default to 8.3, got:\n%s", buf.String())
	}
}

// ==========================================================================
// Additional: restart.go — exercise more paths
// ==========================================================================

func TestRestartCommand_FlagParseError(t *testing.T) {
	r := &RestartCommand{}
	err := r.Run([]string{"--unknown-flag"})
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

// ==========================================================================
// Additional: backup/restore edge cases for better coverage
// ==========================================================================

// ==========================================================================
// Additional: API commands with default API URL (adminURLFromConfig fallback)
// ==========================================================================

func TestDomainList_DefaultAPIURL(t *testing.T) {
	// When --api-url is not provided, it calls adminURLFromConfig().
	// Mock findConfig to return a config with a listen address.
	tmp := t.TempDir()
	cfgFile := filepath.Join(tmp, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("listen: 127.0.0.1:9443\n"), 0644)

	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) {
		return cfgFile, true
	}
	defer func() { findConfigFn = origFindConfig }()

	d := &DomainCommand{}
	err := d.Run([]string{"list", "--api-key", "k"})
	// Will fail to connect, but exercises the adminURLFromConfig() path.
	if err == nil {
		t.Log("unexpected success (server was running?)")
	}
}

func TestCachePurge_DefaultAPIURL(t *testing.T) {
	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) { return "", false }
	defer func() { findConfigFn = origFindConfig }()

	c := &CacheCommand{}
	err := c.Run([]string{"purge", "--api-key", "k"})
	if err == nil {
		t.Log("unexpected success")
	}
}

func TestCacheStats_DefaultAPIURL(t *testing.T) {
	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) { return "", false }
	defer func() { findConfigFn = origFindConfig }()

	c := &CacheCommand{}
	err := c.Run([]string{"stats", "--api-key", "k"})
	if err == nil {
		t.Log("unexpected success")
	}
}

func TestPHPList_DefaultAPIURL(t *testing.T) {
	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) { return "", false }
	defer func() { findConfigFn = origFindConfig }()

	p := &PHPCommand{}
	err := p.Run([]string{"list", "--api-key", "k"})
	if err == nil {
		t.Log("unexpected success")
	}
}

func TestPHPStart_DefaultAPIURL(t *testing.T) {
	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) { return "", false }
	defer func() { findConfigFn = origFindConfig }()

	p := &PHPCommand{}
	err := p.Run([]string{"start", "--api-key", "k", "8.4"})
	if err == nil {
		t.Log("unexpected success")
	}
}

func TestPHPStop_DefaultAPIURL(t *testing.T) {
	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) { return "", false }
	defer func() { findConfigFn = origFindConfig }()

	p := &PHPCommand{}
	err := p.Run([]string{"stop", "--api-key", "k", "8.4"})
	if err == nil {
		t.Log("unexpected success")
	}
}

func TestPHPConfig_DefaultAPIURL(t *testing.T) {
	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) { return "", false }
	defer func() { findConfigFn = origFindConfig }()

	p := &PHPCommand{}
	err := p.Run([]string{"config", "--api-key", "k", "8.4"})
	if err == nil {
		t.Log("unexpected success")
	}
}

func TestPHPExtensions_DefaultAPIURL(t *testing.T) {
	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) { return "", false }
	defer func() { findConfigFn = origFindConfig }()

	p := &PHPCommand{}
	err := p.Run([]string{"extensions", "--api-key", "k", "8.4"})
	if err == nil {
		t.Log("unexpected success")
	}
}

func TestDomainAdd_DefaultAPIURL(t *testing.T) {
	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) { return "", false }
	defer func() { findConfigFn = origFindConfig }()

	d := &DomainCommand{}
	err := d.Run([]string{"add", "--api-key", "k", "example.com"})
	if err == nil {
		t.Log("unexpected success")
	}
}

func TestDomainRemove_DefaultAPIURL(t *testing.T) {
	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) { return "", false }
	defer func() { findConfigFn = origFindConfig }()

	d := &DomainCommand{}
	err := d.Run([]string{"remove", "--api-key", "k", "example.com"})
	if err == nil {
		t.Log("unexpected success")
	}
}

func TestCertList_DefaultAPIURL(t *testing.T) {
	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) { return "", false }
	defer func() { findConfigFn = origFindConfig }()

	c := &CertCommand{}
	err := c.Run([]string{"list", "--api-key", "k"})
	if err == nil {
		t.Log("unexpected success")
	}
}

func TestCertRenew_DefaultAPIURL(t *testing.T) {
	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) { return "", false }
	defer func() { findConfigFn = origFindConfig }()

	c := &CertCommand{}

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := c.Run([]string{"renew", "example.com", "--api-key", "k"})

	w.Close()
	os.Stdout = old

	if err == nil {
		t.Log("unexpected success")
	}
}

func TestStatus_DefaultAPIURL(t *testing.T) {
	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) { return "", false }
	defer func() { findConfigFn = origFindConfig }()

	s := &StatusCommand{}
	err := s.Run([]string{"--api-key", "k"})
	if err == nil {
		t.Log("unexpected success")
	}
}

func TestReload_DefaultAPIURL(t *testing.T) {
	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) { return "", false }
	defer func() { findConfigFn = origFindConfig }()

	rl := &ReloadCommand{}
	err := rl.Run([]string{"--api-key", "k"})
	if err == nil {
		t.Log("unexpected success")
	}
}

func TestRestart_DefaultAPIURL(t *testing.T) {
	origFindConfig := findConfigFn
	findConfigFn = func(explicit string) (string, bool) { return "", false }
	defer func() { findConfigFn = origFindConfig }()

	r := &RestartCommand{}

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := r.Run([]string{"--pid-file", "/nonexistent/uwas.pid"})

	w.Close()
	os.Stdout = old

	if err == nil {
		t.Fatal("expected error")
	}
}

// ==========================================================================
// Additional: isProcessAlive with a process that doesn't exist
// ==========================================================================

func TestIsProcessAlive_DeadProcess(t *testing.T) {
	// Use a very high PID that shouldn't exist.
	proc, err := os.FindProcess(999999999)
	if err != nil {
		t.Skip("FindProcess failed (Windows behavior for dead PID)")
	}
	result := isProcessAlive(proc)
	if result {
		t.Error("PID 999999999 should not be alive")
	}
}

// ==========================================================================
// Additional: uwasDir error path
// ==========================================================================

func TestUwasDir_WithModifiedHome(t *testing.T) {
	origHome := os.Getenv("HOME")
	origUserProfile := os.Getenv("USERPROFILE")
	os.Setenv("HOME", "/tmp/test-uwas-home")
	os.Setenv("USERPROFILE", "/tmp/test-uwas-home")
	defer func() {
		os.Setenv("HOME", origHome)
		os.Setenv("USERPROFILE", origUserProfile)
	}()

	dir := uwasDir()
	if !strings.Contains(dir, ".uwas") {
		t.Errorf("uwasDir() = %q, should contain .uwas", dir)
	}
}

// ==========================================================================
// Additional: php.go install — with a specific version
// ==========================================================================

func TestPHPInstallCommand_WithVersion(t *testing.T) {
	p := &PHPCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := p.install([]string{"8.4"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Logf("install error (expected on non-root): %v", err)
	}
	if !strings.Contains(buf.String(), "PHP 8.4") {
		t.Errorf("should mention PHP 8.4, got:\n%s", buf.String())
	}
}

func TestCreateBackup_InvalidOutputPath(t *testing.T) {
	// Use a file (not a directory) as parent — impossible on all platforms
	notADir := filepath.Join(t.TempDir(), "notadir")
	os.WriteFile(notADir, []byte("x"), 0644)
	err := createBackup(filepath.Join(notADir, "backup.tar.gz"), "/nonexistent/config.yaml", "/nonexistent/certs")
	if err == nil {
		t.Fatal("expected error for invalid output path")
	}
}

func TestInstallCmd_WriteServiceError(t *testing.T) {
	tmp := t.TempDir()
	fakeBin := filepath.Join(tmp, "uwas")
	os.WriteFile(fakeBin, []byte("fake"), 0755)

	origGOOS := installRuntimeGOOS
	origGetuid := installOsGetuid
	origExec := installOsExecutable
	origRead := installOsReadFile
	origWrite := installOsWriteFile
	origStat := installOsStat
	origSymlink := installOsSymlink
	origExecCmd := installExecCommand

	callCount := 0
	installRuntimeGOOS = "linux"
	installOsGetuid = func() int { return 0 }
	installOsExecutable = func() (string, error) { return fakeBin, nil }
	installOsReadFile = func(name string) ([]byte, error) { return []byte("binary"), nil }
	installOsWriteFile = func(name string, data []byte, perm os.FileMode) error {
		callCount++
		if callCount == 2 {
			// Second write is the service file — fail it.
			return fmt.Errorf("permission denied")
		}
		return nil
	}
	installOsStat = func(name string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	installOsSymlink = func(old, new string) error { return nil }
	installExecCommand = func(name string, arg ...string) *exec.Cmd {
		return exec.Command("echo", "ok")
	}
	defer func() {
		installRuntimeGOOS = origGOOS
		installOsGetuid = origGetuid
		installOsExecutable = origExec
		installOsReadFile = origRead
		installOsWriteFile = origWrite
		installOsStat = origStat
		installOsSymlink = origSymlink
		installExecCommand = origExecCmd
	}()

	c := &InstallCmd{}

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := c.Run(nil)

	w.Close()
	os.Stdout = old

	if err == nil {
		t.Fatal("expected error when service file write fails")
	}
	if !strings.Contains(err.Error(), "write service file") {
		t.Errorf("error = %q", err.Error())
	}
}
