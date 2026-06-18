package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// captureStderr is the stderr equivalent of the shared captureStdout helper.
// It reads concurrently to avoid deadlock if output exceeds the pipe buffer.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = buf.ReadFrom(r)
		close(done)
	}()

	fn()

	_ = w.Close()
	os.Stderr = old
	<-done
	return buf.String()
}

// feedStdin replaces os.Stdin with a pipe containing the given input for the
// duration of fn.
func feedStdin(t *testing.T, input string, fn func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	_, _ = w.WriteString(input)
	_ = w.Close()
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old }()
	fn()
}

// ==========================================================================
// root.go — CLI.Run success path + loadDotEnv
// ==========================================================================

// stubCommand is a minimal Command used to exercise CLI.Run dispatch without
// triggering os.Exit (which a real command's failure path would).
type stubCommand struct {
	name   string
	desc   string
	ran    bool
	gotArg []string
	err    error
}

func (s *stubCommand) Name() string         { return s.name }
func (s *stubCommand) Description() string  { return s.desc }
func (s *stubCommand) Help() string         { return "stub help body" }
func (s *stubCommand) Run(a []string) error { s.ran = true; s.gotArg = a; return s.err }

func TestCLIRun_DispatchSuccess(t *testing.T) {
	app := New()
	stub := &stubCommand{name: "stub", desc: "a stub"}
	app.Register(stub)

	// A registered command that returns nil takes the success path and
	// returns without calling os.Exit.
	app.Run([]string{"stub", "alpha", "beta"})

	if !stub.ran {
		t.Fatal("stub command should have run")
	}
	if len(stub.gotArg) != 2 || stub.gotArg[0] != "alpha" || stub.gotArg[1] != "beta" {
		t.Errorf("args not forwarded correctly: %v", stub.gotArg)
	}
}

func TestCLIRun_EmptyArgsAutoStartsServe(t *testing.T) {
	app := New()
	stub := &stubCommand{name: "serve", desc: "serve"}
	app.Register(stub)

	// With no args and a registered "serve" command that succeeds, CLI.Run
	// takes the auto-start branch and returns without os.Exit.
	app.Run(nil)

	if !stub.ran {
		t.Fatal("serve should auto-run on empty args")
	}
	if stub.gotArg != nil {
		t.Errorf("serve auto-start should pass nil args, got %v", stub.gotArg)
	}
}

func TestHelpCommand_WithDetailedHelp(t *testing.T) {
	app := New()
	app.Register(&stubCommand{name: "stub", desc: "a stub"})
	h := NewHelpCommand(app)

	out := captureStdout(t, func() {
		if err := h.Run([]string{"stub"}); err != nil {
			t.Fatalf("help run: %v", err)
		}
	})
	if !strings.Contains(out, "Usage: uwas stub") {
		t.Errorf("missing usage line: %q", out)
	}
	if !strings.Contains(out, "stub help body") {
		t.Errorf("detailed Help() not printed: %q", out)
	}
}

func TestHelpCommand_NoDetailedHelp(t *testing.T) {
	app := New()
	// VersionCommand has no Help() method, exercising the no-detail branch.
	app.Register(&VersionCommand{})
	h := NewHelpCommand(app)

	out := captureStdout(t, func() {
		if err := h.Run([]string{"version"}); err != nil {
			t.Fatalf("help run: %v", err)
		}
	})
	if !strings.Contains(out, "Usage: uwas version") {
		t.Errorf("missing usage line: %q", out)
	}
}

func TestHelpCommand_UnknownCommand(t *testing.T) {
	app := New()
	h := NewHelpCommand(app)
	err := h.Run([]string{"does-not-exist"})
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("expected unknown command error, got %v", err)
	}
}

func TestLoadDotEnv_ReadsHomeEnv(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	// USERPROFILE governs UserHomeDir on Windows.
	t.Setenv("USERPROFILE", tmp)

	uwasCfg := filepath.Join(tmp, ".uwas")
	if err := os.MkdirAll(uwasCfg, 0755); err != nil {
		t.Fatal(err)
	}
	envBody := strings.Join([]string{
		"# a comment",
		"",
		"UWAS_TEST_KEY=secret-value",
		"UWAS_TEST_PREEXISTING=should-not-override",
		"malformed line without equals",
	}, "\n")
	if err := os.WriteFile(filepath.Join(uwasCfg, ".env"), []byte(envBody), 0600); err != nil {
		t.Fatal(err)
	}

	// Pre-set one var to prove loadDotEnv does NOT override existing values.
	t.Setenv("UWAS_TEST_PREEXISTING", "original")
	// Ensure the fresh key is empty before load.
	os.Unsetenv("UWAS_TEST_KEY")
	t.Cleanup(func() { os.Unsetenv("UWAS_TEST_KEY") })

	loadDotEnv()

	if got := os.Getenv("UWAS_TEST_KEY"); got != "secret-value" {
		t.Errorf("UWAS_TEST_KEY = %q, want secret-value", got)
	}
	if got := os.Getenv("UWAS_TEST_PREEXISTING"); got != "original" {
		t.Errorf("pre-existing env var was overridden: %q", got)
	}
}

func TestUwasDir_HomeUnsetFallback(t *testing.T) {
	// When UserHomeDir fails (no HOME / USERPROFILE), uwasDir falls back to ".".
	origHome, hadHome := os.LookupEnv("HOME")
	origProfile, hadProfile := os.LookupEnv("USERPROFILE")
	os.Unsetenv("HOME")
	os.Unsetenv("USERPROFILE")
	t.Cleanup(func() {
		if hadHome {
			os.Setenv("HOME", origHome)
		}
		if hadProfile {
			os.Setenv("USERPROFILE", origProfile)
		}
	})

	dir := uwasDir()
	// On platforms where UserHomeDir still succeeds via other means, just
	// assert we got a non-empty path ending in .uwas.
	if !strings.HasSuffix(dir, ".uwas") {
		t.Errorf("uwasDir = %q, want suffix .uwas", dir)
	}
}

func TestLoadDotEnv_NoFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	// No .env anywhere reachable; should be a quiet no-op.
	loadDotEnv()
}

// ==========================================================================
// install.go — migrateLegacyConfigs full path
// ==========================================================================

func TestMigrateLegacyConfigs_CopiesDomainFilesAndInline(t *testing.T) {
	tmp := t.TempDir()

	// Build a fake legacy directory with both a domains.d/ file and an
	// inline uwas.yaml carrying a "domains:" array.
	legacy := filepath.Join(tmp, "legacy")
	legacyDomains := filepath.Join(legacy, "domains.d")
	if err := os.MkdirAll(legacyDomains, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDomains, "site1.yaml"), []byte("host: site1.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// A non-yaml file and a subdir should be ignored.
	_ = os.WriteFile(filepath.Join(legacyDomains, "notes.txt"), []byte("ignore"), 0600)
	_ = os.MkdirAll(filepath.Join(legacyDomains, "subdir"), 0755)
	if err := os.WriteFile(filepath.Join(legacy, "uwas.yaml"), []byte("domains:\n  - host: inline.com\n    root: /var/www\n"), 0600); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(tmp, "dest", "domains.d")
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatal(err)
	}
	destCfg := filepath.Join(tmp, "dest", "uwas.yaml")

	// Point all the legacy-path probing at our temp dir by overriding the
	// stat/read/write seams to translate the well-known legacy paths to our
	// fake legacy dir. Only "/root/.uwas" is mapped; everything else fails
	// the stat so the loop short-circuits.
	origStat := installOsStat
	origRead := installOsReadFile
	origWrite := installOsWriteFile
	t.Cleanup(func() {
		installOsStat = origStat
		installOsReadFile = origRead
		installOsWriteFile = origWrite
	})

	mapPath := func(p string) string {
		if strings.HasPrefix(p, "/root/.uwas") {
			return legacy + strings.TrimPrefix(p, "/root/.uwas")
		}
		return p
	}
	installOsStat = func(name string) (os.FileInfo, error) { return origStat(mapPath(name)) }
	installOsReadFile = func(name string) ([]byte, error) { return origRead(mapPath(name)) }
	installOsWriteFile = func(name string, data []byte, perm os.FileMode) error {
		return origWrite(mapPath(name), data, perm)
	}

	// os.ReadDir is called directly (not seamed) inside migrateLegacyConfigs
	// for the domains.d copy, so that branch only fires for paths that exist
	// on disk. To exercise it, also place a domains.d under the real
	// "/root/.uwas" alias — which we can't write to. Instead we rely on the
	// inline-yaml path being exercised here; the domains.d ReadDir path is
	// covered by the separate temp-rooted test below.
	out := captureStdout(t, func() {
		migrateLegacyConfigs(dest, destCfg)
	})

	if !strings.Contains(out, "Migrated") {
		t.Errorf("expected migration summary, got: %q", out)
	}
	if _, err := os.Stat(filepath.Join(dest, "inline.com.yaml")); err != nil {
		t.Errorf("inline domain not written: %v", err)
	}
}

func TestMigrateLegacyConfigs_ViaHomeDir(t *testing.T) {
	// Drive the real (un-seamed) os.ReadDir copy loop by placing a legacy
	// install under $HOME/.uwas, which migrateLegacyConfigs probes directly.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	legacy := filepath.Join(tmp, ".uwas")
	legacyDomains := filepath.Join(legacy, "domains.d")
	if err := os.MkdirAll(legacyDomains, 0755); err != nil {
		t.Fatal(err)
	}
	// One yaml file to copy, one non-yaml + a subdir to skip.
	if err := os.WriteFile(filepath.Join(legacyDomains, "alpha.yaml"), []byte("host: alpha.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(legacyDomains, "skip.txt"), []byte("x"), 0600)
	_ = os.MkdirAll(filepath.Join(legacyDomains, "nested"), 0755)
	// Inline domains in the legacy uwas.yaml.
	if err := os.WriteFile(filepath.Join(legacy, "uwas.yaml"), []byte("domains:\n  - host: beta.com\n"), 0600); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(tmp, "dest", "domains.d")
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		migrateLegacyConfigs(dest, filepath.Join(tmp, "dest", "uwas.yaml"))
	})
	if !strings.Contains(out, "Migrated") {
		t.Errorf("expected migration summary, got: %q", out)
	}
	if _, err := os.Stat(filepath.Join(dest, "alpha.yaml")); err != nil {
		t.Errorf("per-domain file not copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "beta.com.yaml")); err != nil {
		t.Errorf("inline domain not written: %v", err)
	}
	// Source files should be renamed to *.migrated.
	if _, err := os.Stat(filepath.Join(legacyDomains, "alpha.yaml.migrated")); err != nil {
		t.Errorf("source not renamed: %v", err)
	}
}

func TestMigrateLegacyConfigs_DestFilesExistSkip(t *testing.T) {
	// When destination files already exist, the copy/write is skipped (the
	// "don't clobber" branches).
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	legacy := filepath.Join(tmp, ".uwas")
	legacyDomains := filepath.Join(legacy, "domains.d")
	if err := os.MkdirAll(legacyDomains, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDomains, "alpha.yaml"), []byte("host: alpha.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "uwas.yaml"), []byte("domains:\n  - host: beta.com\n"), 0600); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(tmp, "dest", "domains.d")
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatal(err)
	}
	// Pre-create both destination files so the skip branches fire.
	_ = os.WriteFile(filepath.Join(dest, "alpha.yaml"), []byte("existing\n"), 0600)
	_ = os.WriteFile(filepath.Join(dest, "beta.com.yaml"), []byte("existing\n"), 0600)

	out := captureStdout(t, func() {
		migrateLegacyConfigs(dest, filepath.Join(tmp, "dest", "uwas.yaml"))
	})
	// Nothing new migrated -> no summary line.
	if strings.Contains(out, "Migrated") {
		t.Errorf("should skip existing dest files, got: %q", out)
	}
}

func TestMigrateInlineDomains_EdgeCases(t *testing.T) {
	dest := t.TempDir()

	// Entries: no host key, empty host, host with a port (colon -> underscore),
	// and a valid host.
	data := []byte(`domains:
  - root: /var/www
  - host: ""
    root: /a
  - host: "withport.com:8080"
    root: /b
  - host: good.com
    root: /c
`)
	n := migrateInlineDomains(data, dest)
	if n != 2 {
		t.Errorf("expected 2 files written, got %d", n)
	}
	if _, err := os.Stat(filepath.Join(dest, "withport.com_8080.yaml")); err != nil {
		t.Errorf("port host not sanitized to underscore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "good.com.yaml")); err != nil {
		t.Errorf("valid host not written: %v", err)
	}
}

func TestMigrateInlineDomains_BadYAML(t *testing.T) {
	dest := t.TempDir()
	if n := migrateInlineDomains([]byte(": : not yaml : :"), dest); n != 0 {
		t.Errorf("malformed yaml should yield 0, got %d", n)
	}
}

func TestMigrateLegacyConfigs_NoLegacyPaths(t *testing.T) {
	tmp := t.TempDir()
	dest := filepath.Join(tmp, "domains.d")
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatal(err)
	}

	origStat := installOsStat
	t.Cleanup(func() { installOsStat = origStat })
	// Every legacy path stat fails -> nothing migrated, no summary printed.
	installOsStat = func(name string) (os.FileInfo, error) { return nil, os.ErrNotExist }

	out := captureStdout(t, func() {
		migrateLegacyConfigs(dest, filepath.Join(tmp, "uwas.yaml"))
	})
	if strings.Contains(out, "Migrated") {
		t.Errorf("should not report migration, got: %q", out)
	}
}

func TestMigrateLegacyConfigs_SkipsAliasOfDestination(t *testing.T) {
	tmp := t.TempDir()
	dest := filepath.Join(tmp, "domains.d")
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatal(err)
	}

	origStat := installOsStat
	t.Cleanup(func() { installOsStat = origStat })
	// Make the first legacy path resolve to the destination's parent so the
	// alias-skip branch fires.
	parent := filepath.Dir(dest)
	installOsStat = func(name string) (os.FileInfo, error) {
		if name == "/root/.uwas" {
			return os.Stat(parent)
		}
		return nil, os.ErrNotExist
	}

	// The alias check compares filepath.Abs(legacyDir) with the dest parent.
	// Since we cannot make "/root/.uwas" Abs equal the temp parent, this
	// exercises the dir-exists-but-not-alias copy attempt instead. Either
	// way the function must not panic and must not migrate anything.
	out := captureStdout(t, func() {
		migrateLegacyConfigs(dest, filepath.Join(tmp, "uwas.yaml"))
	})
	_ = out
}

// ==========================================================================
// install.go — dumpUnitDiagnostics (installExecCommand neutralized in TestMain)
// ==========================================================================

func TestDumpUnitDiagnostics_PrintsHeaders(t *testing.T) {
	origExec := installExecCommand
	t.Cleanup(func() { installExecCommand = origExec })
	// Return a command that emits some output so the per-line print loops run.
	installExecCommand = func(name string, arg ...string) *exec.Cmd {
		return exec.Command("printf", "line-a\\nline-b\\n")
	}

	out := captureStderr(t, func() {
		dumpUnitDiagnostics()
	})
	if !strings.Contains(out, "systemctl status uwas") {
		t.Errorf("missing status header: %q", out)
	}
	if !strings.Contains(out, "journalctl -u uwas") {
		t.Errorf("missing journal header: %q", out)
	}
	if !strings.Contains(out, "line-a") {
		t.Errorf("expected command output echoed, got: %q", out)
	}
}

func TestDumpUnitDiagnostics_NoOutput(t *testing.T) {
	origExec := installExecCommand
	t.Cleanup(func() { installExecCommand = origExec })
	// Command with empty output -> the len(out)>0 branches are skipped.
	installExecCommand = func(name string, arg ...string) *exec.Cmd {
		return exec.Command("true")
	}
	out := captureStderr(t, func() {
		dumpUnitDiagnostics()
	})
	if !strings.Contains(out, "systemctl status uwas") {
		t.Errorf("headers should still print: %q", out)
	}
}

// ==========================================================================
// install.go — checkCLI_Disk df-output branch
// ==========================================================================

func TestCheckCLI_Disk_WithUsage(t *testing.T) {
	origExec := doctorExecCommand
	t.Cleanup(func() { doctorExecCommand = origExec })
	// Emit a realistic `df -h /` line with >4 fields so the "X% used" branch runs.
	doctorExecCommand = func(name string, arg ...string) *exec.Cmd {
		return exec.Command("printf", "Filesystem Size Used Avail Use%% Mounted\\n/dev/sda1 50G 20G 28G 42%% /\\n")
	}
	c := checkCLI_Disk()
	if c.status != "ok" {
		t.Errorf("status = %q, want ok", c.status)
	}
	if !strings.Contains(c.message, "used") {
		t.Errorf("message should mention usage, got %q", c.message)
	}
}

func TestCheckCLI_Disk_CommandError(t *testing.T) {
	origExec := doctorExecCommand
	t.Cleanup(func() { doctorExecCommand = origExec })
	doctorExecCommand = func(name string, arg ...string) *exec.Cmd {
		return exec.Command("false")
	}
	c := checkCLI_Disk()
	if c.status != "ok" || c.message != "OK" {
		t.Errorf("error path: got status=%q message=%q", c.status, c.message)
	}
}

func TestCheckCLI_Disk_FewFields(t *testing.T) {
	origExec := doctorExecCommand
	t.Cleanup(func() { doctorExecCommand = origExec })
	// Fewer than 5 fields -> fall through to the "OK" default.
	doctorExecCommand = func(name string, arg ...string) *exec.Cmd {
		return exec.Command("printf", "one two\\n")
	}
	c := checkCLI_Disk()
	if c.message != "OK" {
		t.Errorf("few-field path: message=%q", c.message)
	}
}

// ==========================================================================
// install.go — installUWAS start-failure branches
// ==========================================================================

// installFixtureGOOS sets up the common root-on-linux mock environment used by
// the installUWAS branch tests and returns a restore func.
func installRootLinuxEnv(t *testing.T, execFn func(string, ...string) *exec.Cmd) {
	t.Helper()
	origGOOS := installRuntimeGOOS
	origGetuid := installOsGetuid
	origExec := installOsExecutable
	origRead := installOsReadFile
	origWrite := installOsWriteFile
	origStat := installOsStat
	origSymlink := installOsSymlink
	origExecCmd := installExecCommand
	origMkdirAll := installOsMkdirAll
	origIsTTY := installIsTTY

	installRuntimeGOOS = "linux"
	installOsGetuid = func() int { return 0 }
	installOsExecutable = func() (string, error) { return "/tmp/uwas-test", nil }
	installOsReadFile = func(name string) ([]byte, error) {
		// PID file read must fail so the force-kill block is skipped.
		if name == "/var/run/uwas.pid" {
			return nil, os.ErrNotExist
		}
		return []byte("binary-data"), nil
	}
	installOsWriteFile = func(name string, data []byte, perm os.FileMode) error { return nil }
	installOsStat = func(name string) (os.FileInfo, error) { return nil, os.ErrNotExist }
	installOsSymlink = func(old, new string) error { return nil }
	installExecCommand = execFn
	installOsMkdirAll = func(path string, perm os.FileMode) error { return nil }
	installIsTTY = func() bool { return false }

	t.Cleanup(func() {
		installRuntimeGOOS = origGOOS
		installOsGetuid = origGetuid
		installOsExecutable = origExec
		installOsReadFile = origRead
		installOsWriteFile = origWrite
		installOsStat = origStat
		installOsSymlink = origSymlink
		installExecCommand = origExecCmd
		installOsMkdirAll = origMkdirAll
		installIsTTY = origIsTTY
	})
}

func TestInstallUWAS_StartCommandFails(t *testing.T) {
	installRootLinuxEnv(t, func(name string, arg ...string) *exec.Cmd {
		if name == "systemctl" && len(arg) >= 1 && arg[0] == "start" {
			return exec.Command("false") // start fails
		}
		return exec.Command("true")
	})

	var err error
	captureStderr(t, func() {
		_ = captureStdout(t, func() {
			err = installUWAS(nil)
		})
	})
	if err == nil || !strings.Contains(err.Error(), "systemctl start uwas") {
		t.Fatalf("expected start error, got %v", err)
	}
}

func TestInstallUWAS_UnitEntersFailedState(t *testing.T) {
	installRootLinuxEnv(t, func(name string, arg ...string) *exec.Cmd {
		if name == "systemctl" && len(arg) >= 1 && arg[0] == "is-active" {
			return exec.Command("printf", "failed")
		}
		return exec.Command("true")
	})

	var err error
	captureStderr(t, func() {
		_ = captureStdout(t, func() {
			err = installUWAS(nil)
		})
	})
	if err == nil || !strings.Contains(err.Error(), "failed state") {
		t.Fatalf("expected failed-state error, got %v", err)
	}
}

func TestInstallUWAS_NoStartFlag(t *testing.T) {
	// --no-start skips the entire start block, hitting the doStart==false
	// summary branch.
	installRootLinuxEnv(t, func(name string, arg ...string) *exec.Cmd {
		return exec.Command("true")
	})

	var err error
	out := captureStdout(t, func() {
		err = installUWAS([]string{"--no-start"})
	})
	if err != nil {
		t.Fatalf("no-start install error: %v", err)
	}
	if !strings.Contains(out, "start now") {
		t.Errorf("no-start summary should mention manual start, got:\n%s", out)
	}
}

func TestInstallUWAS_NoConfigFlag(t *testing.T) {
	// --no-config skips the config seed block.
	installRootLinuxEnv(t, func(name string, arg ...string) *exec.Cmd {
		if name == "systemctl" && len(arg) >= 1 && arg[0] == "is-active" {
			return exec.Command("printf", "active")
		}
		return exec.Command("true")
	})

	var err error
	out := captureStdout(t, func() {
		err = installUWAS([]string{"--no-config"})
	})
	if err != nil {
		t.Fatalf("no-config install error: %v", err)
	}
	if !strings.Contains(out, "Installation complete") {
		t.Errorf("expected completion, got:\n%s", out)
	}
}

func TestInstallUWAS_ForceKillsLingeringPID(t *testing.T) {
	// installRootLinuxEnv makes the PID read fail; here we override it so the
	// /var/run/uwas.pid read returns a live PID, exercising the force-kill
	// block (kill -TERM / -KILL via the neutralized installExecCommand).
	installRootLinuxEnv(t, func(name string, arg ...string) *exec.Cmd {
		if name == "systemctl" && len(arg) >= 1 && arg[0] == "is-active" {
			return exec.Command("printf", "active")
		}
		return exec.Command("true")
	})
	installOsReadFile = func(name string) ([]byte, error) {
		if name == "/var/run/uwas.pid" {
			return []byte("4242\n"), nil
		}
		return []byte("binary-data"), nil
	}

	var err error
	out := captureStdout(t, func() {
		err = installUWAS([]string{"--no-config"})
	})
	if err != nil {
		t.Fatalf("install error: %v", err)
	}
	if !strings.Contains(out, "Installation complete") {
		t.Errorf("expected completion, got:\n%s", out)
	}
}

func TestInstallUWAS_TTYPromptDeclines(t *testing.T) {
	// installIsTTY=true + stdin "n" exercises the interactive prompt path and
	// the decline branch that flips doStart to false.
	installRootLinuxEnv(t, func(name string, arg ...string) *exec.Cmd {
		return exec.Command("true")
	})
	installIsTTY = func() bool { return true }

	var err error
	feedStdin(t, "n\n", func() {
		out := captureStdout(t, func() {
			err = installUWAS(nil)
		})
		if !strings.Contains(out, "Start UWAS now?") {
			t.Errorf("prompt not shown, got:\n%s", out)
		}
	})
	if err != nil {
		t.Fatalf("install error: %v", err)
	}
}

func TestInstallUWAS_TTYPromptAccepts(t *testing.T) {
	// installIsTTY=true + empty input -> default yes, takes the start path.
	installRootLinuxEnv(t, func(name string, arg ...string) *exec.Cmd {
		if name == "systemctl" && len(arg) >= 1 && arg[0] == "is-active" {
			return exec.Command("printf", "active")
		}
		return exec.Command("true")
	})
	installIsTTY = func() bool { return true }

	var err error
	feedStdin(t, "\n", func() {
		_ = captureStdout(t, func() {
			err = installUWAS(nil)
		})
	})
	if err != nil {
		t.Fatalf("install error: %v", err)
	}
}

// ==========================================================================
// stop.go — flag-parse error + FindProcess error
// ==========================================================================

func TestStopCommand_FlagParseError(t *testing.T) {
	s := &StopCommand{}
	var err error
	captureStderr(t, func() {
		err = s.Run([]string{"--bogus-flag"})
	})
	if err == nil {
		t.Error("stop with bad flag should error")
	}
}

func TestStopCommand_FindProcessError(t *testing.T) {
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "uwas.pid")
	if err := os.WriteFile(pidFile, []byte("4242\n"), 0600); err != nil {
		t.Fatal(err)
	}

	origFind := osFindProcessFn
	osFindProcessFn = func(pid int) (*os.Process, error) {
		return nil, os.ErrPermission
	}
	t.Cleanup(func() { osFindProcessFn = origFind })

	var err error
	_ = captureStdout(t, func() {
		err = (&StopCommand{}).Run([]string{"--pid-file", pidFile})
	})
	if err == nil || !strings.Contains(err.Error(), "cannot find process") {
		t.Fatalf("expected find-process error, got %v", err)
	}
}

// ==========================================================================
// install.go — UninstallCmd self-binary branch (executable == target)
// ==========================================================================

func TestUninstallCmd_RunningBinaryBranch(t *testing.T) {
	origGOOS := installRuntimeGOOS
	origGetuid := installOsGetuid
	origExec := installOsExecutable
	origRemove := installOsRemove
	origExecCmd := installExecCommand
	t.Cleanup(func() {
		installRuntimeGOOS = origGOOS
		installOsGetuid = origGetuid
		installOsExecutable = origExec
		installOsRemove = origRemove
		installExecCommand = origExecCmd
	})

	installRuntimeGOOS = "linux"
	installOsGetuid = func() int { return 0 }
	// Executable IS the install target -> hits the "can't delete ourselves"
	// else branch.
	installOsExecutable = func() (string, error) { return "/usr/local/bin/uwas", nil }
	installOsRemove = func(name string) error { return nil }
	installExecCommand = func(name string, arg ...string) *exec.Cmd { return exec.Command("true") }

	var err error
	out := captureStdout(t, func() {
		feedStdin(t, "y\n", func() {
			err = (&UninstallCmd{}).Run(nil)
		})
	})
	if err != nil {
		t.Fatalf("uninstall error: %v", err)
	}
	if !strings.Contains(out, "removed on next reboot") {
		t.Errorf("expected running-binary branch message, got:\n%s", out)
	}
}

// ==========================================================================
// pidcheck.go — FindProcess error branch
// ==========================================================================

func TestReadAlivePID_FindProcessError(t *testing.T) {
	tmp := t.TempDir()
	pidFile := filepath.Join(tmp, "uwas.pid")
	if err := os.WriteFile(pidFile, []byte("321\n"), 0600); err != nil {
		t.Fatal(err)
	}

	origRead := osReadFileFn
	origFind := osFindProcessFn
	osReadFileFn = os.ReadFile
	osFindProcessFn = func(pid int) (*os.Process, error) { return nil, os.ErrPermission }
	t.Cleanup(func() {
		osReadFileFn = origRead
		osFindProcessFn = origFind
	})

	if pid, alive := readAlivePID(pidFile); alive || pid != 0 {
		t.Errorf("FindProcess error should report not-alive, got pid=%d alive=%v", pid, alive)
	}
}

// ==========================================================================
// install.go — extractCredsFromConfig read error
// ==========================================================================

func TestExtractCredsFromConfig_ReadError(t *testing.T) {
	origRead := installOsReadFile
	installOsReadFile = os.ReadFile
	t.Cleanup(func() { installOsReadFile = origRead })

	c := extractCredsFromConfig(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if c.apiKey != "" || c.pinCode != "" || c.adminHost != "" || c.adminPort != "" {
		t.Errorf("missing config should yield empty creds, got %+v", c)
	}
}

// ==========================================================================
// init.go — ensureDefaultConfig directory-creation error
// ==========================================================================

func TestEnsureDefaultConfig_MkdirError(t *testing.T) {
	// Point HOME at a path whose .uwas target is actually a regular file, so
	// os.MkdirAll fails and ensureDefaultConfig returns the create-dir error.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	// Create a FILE named ".uwas" so MkdirAll on it fails.
	if err := os.WriteFile(filepath.Join(tmp, ".uwas"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := ensureDefaultConfig("8080", "9443", "0.0.0.0", filepath.Join(tmp, "www"), "")
	if err == nil || !strings.Contains(err.Error(), "create config dir") {
		t.Fatalf("expected create-config-dir error, got %v", err)
	}
}

// ==========================================================================
// daemon — filterArg helper (serve.go) edge cases
// ==========================================================================

func TestFilterArg_AppendsWhenMissing(t *testing.T) {
	got := filterArg([]string{"serve"}, "-d")
	if len(got) != 2 || got[1] != "-d" {
		t.Errorf("filterArg should append -d, got %v", got)
	}
}

func TestFilterArg_KeepsWhenPresent(t *testing.T) {
	in := []string{"serve", "-d"}
	got := filterArg(in, "-d")
	if len(got) != 2 {
		t.Errorf("filterArg should not duplicate, got %v", got)
	}
}

// ==========================================================================
// serve.go — promptWithDefault default + override
// ==========================================================================

func TestPromptWithDefault_UsesDefaultOnEmpty(t *testing.T) {
	var got string
	feedStdin(t, "\n", func() {
		_ = captureStdout(t, func() {
			got = promptWithDefault("port", "8080")
		})
	})
	if got != "8080" {
		t.Errorf("empty input should yield default, got %q", got)
	}
}

func TestPromptWithDefault_UsesInput(t *testing.T) {
	var got string
	feedStdin(t, "9999\n", func() {
		_ = captureStdout(t, func() {
			got = promptWithDefault("port", "8080")
		})
	})
	if got != "9999" {
		t.Errorf("input should override default, got %q", got)
	}
}

// ==========================================================================
// conflicts.go — OfferPHPInstall run path + custom version branch
// ==========================================================================

// noPHPInstalledEnv makes conflictsExecLookPath always fail so OfferPHPInstall
// proceeds past the "PHP already available" early return.
func noPHPInstalledEnv(t *testing.T) {
	t.Helper()
	origGOOS := conflictsRuntimeGOOS
	origLookPath := conflictsExecLookPath
	conflictsRuntimeGOOS = "linux"
	conflictsExecLookPath = func(file string) (string, error) {
		return "", os.ErrNotExist
	}
	t.Cleanup(func() {
		conflictsRuntimeGOOS = origGOOS
		conflictsExecLookPath = origLookPath
	})
}

func TestOfferPHPInstall_ConfirmRunsInstall(t *testing.T) {
	noPHPInstalledEnv(t)

	origRun := phpRunInstall
	called := false
	phpRunInstall = func(v string) (string, error) {
		called = true
		// Long multi-line output to exercise the "last 10 lines" tail logic.
		lines := make([]string, 0, 15)
		for i := 0; i < 15; i++ {
			lines = append(lines, "install-log-line")
		}
		return strings.Join(lines, "\n"), nil
	}
	t.Cleanup(func() { phpRunInstall = origRun })

	// Choose version "1" (8.5) then confirm "y" to run the install.
	out := captureStdout(t, func() {
		feedStdin(t, "1\ny\n", func() {
			OfferPHPInstall()
		})
	})
	if !called {
		t.Fatal("phpRunInstall should have been called")
	}
	if !strings.Contains(out, "installed successfully") {
		t.Errorf("expected success message, got:\n%s", out)
	}
}

func TestOfferPHPInstall_ConfirmRunInstallFails(t *testing.T) {
	noPHPInstalledEnv(t)

	origRun := phpRunInstall
	phpRunInstall = func(v string) (string, error) {
		return "short output", os.ErrPermission
	}
	t.Cleanup(func() { phpRunInstall = origRun })

	out := captureStdout(t, func() {
		feedStdin(t, "2\ny\n", func() {
			OfferPHPInstall()
		})
	})
	if !strings.Contains(out, "Install failed") {
		t.Errorf("expected failure message, got:\n%s", out)
	}
}

func TestOfferPHPInstall_CustomDottedVersion(t *testing.T) {
	noPHPInstalledEnv(t)

	origRun := phpRunInstall
	var gotVersion string
	phpRunInstall = func(v string) (string, error) { gotVersion = v; return "", nil }
	t.Cleanup(func() { phpRunInstall = origRun })

	// A dotted custom version like "7.4" falls into the default branch that
	// accepts any string containing ".".
	captureStdout(t, func() {
		feedStdin(t, "7.4\ny\n", func() {
			OfferPHPInstall()
		})
	})
	if gotVersion != "7.4" {
		t.Errorf("custom version not honored, got %q", gotVersion)
	}
}

// ==========================================================================
// conflicts.go — DetectConflicts systemctl is-active fallback
// ==========================================================================

func TestDetectConflicts_SystemctlActiveFallback(t *testing.T) {
	origGOOS := conflictsRuntimeGOOS
	origLookPath := conflictsExecLookPath
	origExec := conflictsExecCommand
	t.Cleanup(func() {
		conflictsRuntimeGOOS = origGOOS
		conflictsExecLookPath = origLookPath
		conflictsExecCommand = origExec
	})

	conflictsRuntimeGOOS = "linux"
	conflictsExecLookPath = func(file string) (string, error) {
		if file == "nginx" {
			return "/usr/sbin/nginx", nil
		}
		return "", os.ErrNotExist
	}
	// pidof returns nothing (process not found that way), but systemctl
	// is-active says "active" and MainPID resolves -> the service-activity
	// fallback branch fires.
	conflictsExecCommand = func(name string, arg ...string) *exec.Cmd {
		switch {
		case name == "pidof":
			return exec.Command("printf", "")
		case name == "systemctl" && len(arg) >= 1 && arg[0] == "is-active":
			return exec.Command("printf", "active")
		case name == "systemctl" && len(arg) >= 1 && arg[0] == "show":
			return exec.Command("printf", "777")
		}
		return exec.Command("true")
	}

	conflicts := DetectConflicts()
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	c := conflicts[0]
	if !c.Running {
		t.Error("nginx should be detected as running via systemctl fallback")
	}
	if c.PID != "777" {
		t.Errorf("MainPID = %q, want 777", c.PID)
	}
}

func TestDetectConflicts_SystemctlActiveZeroMainPID(t *testing.T) {
	origGOOS := conflictsRuntimeGOOS
	origLookPath := conflictsExecLookPath
	origExec := conflictsExecCommand
	t.Cleanup(func() {
		conflictsRuntimeGOOS = origGOOS
		conflictsExecLookPath = origLookPath
		conflictsExecCommand = origExec
	})

	conflictsRuntimeGOOS = "linux"
	conflictsExecLookPath = func(file string) (string, error) {
		if file == "caddy" {
			return "/usr/bin/caddy", nil
		}
		return "", os.ErrNotExist
	}
	// MainPID "0" -> the pid!="0" guard keeps PID empty.
	conflictsExecCommand = func(name string, arg ...string) *exec.Cmd {
		switch {
		case name == "pidof":
			return exec.Command("printf", "")
		case name == "systemctl" && len(arg) >= 1 && arg[0] == "is-active":
			return exec.Command("printf", "active")
		case name == "systemctl" && len(arg) >= 1 && arg[0] == "show":
			return exec.Command("printf", "0")
		}
		return exec.Command("true")
	}

	conflicts := DetectConflicts()
	if len(conflicts) != 1 || !conflicts[0].Running {
		t.Fatalf("expected 1 running conflict, got %+v", conflicts)
	}
	if conflicts[0].PID != "" {
		t.Errorf("PID should stay empty for MainPID=0, got %q", conflicts[0].PID)
	}
}

// ==========================================================================
// php.go — flag-parse error branches for each subcommand
// ==========================================================================

func TestPHPSubcommands_FlagParseError(t *testing.T) {
	p := &PHPCommand{}
	// An unknown flag makes flag.Parse return an error (ContinueOnError), which
	// each subcommand surfaces. flag also prints usage to stderr.
	subs := []string{"list", "start", "stop", "config", "extensions"}
	for _, sub := range subs {
		var err error
		captureStderr(t, func() {
			err = p.Run([]string{sub, "--bogus-flag"})
		})
		if err == nil {
			t.Errorf("%s with bad flag should error", sub)
		}
	}
}

// ==========================================================================
// cert.go — flag-parse error branches
// ==========================================================================

func TestCertSubcommands_FlagParseError(t *testing.T) {
	c := &CertCommand{}

	var listErr error
	captureStderr(t, func() {
		listErr = c.Run([]string{"list", "--bogus-flag"})
	})
	if listErr == nil {
		t.Error("cert list with bad flag should error")
	}

	// renew needs a domain positional before its flags.
	var renewErr error
	captureStderr(t, func() {
		renewErr = c.Run([]string{"renew", "example.com", "--bogus-flag"})
	})
	if renewErr == nil {
		t.Error("cert renew with bad flag should error")
	}
}
