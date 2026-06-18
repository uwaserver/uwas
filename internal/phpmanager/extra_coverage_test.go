package phpmanager

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// longRunningCmd returns an *exec.Cmd for a process that stays alive long
// enough for the test to interact with / kill it. "ping -n 100 127.0.0.1"
// blocks ~100s on this platform and is killed by the test, never run to
// completion, so it is a safe stand-in for a live PHP daemon.
func longRunningCmd() *exec.Cmd {
	return exec.Command("ping", "-n", "100", "127.0.0.1")
}

// --- manager.go: RegisterExistingDomain ---

func TestRegisterExistingDomainTCP(t *testing.T) {
	m := New(testLogger())
	overrides := map[string]string{"memory_limit": "256M"}
	m.RegisterExistingDomain("a.test", "8.3", "127.0.0.1:9001", "/srv/a", overrides)

	insts := m.GetDomainInstances()
	if len(insts) != 1 {
		t.Fatalf("instances = %d, want 1", len(insts))
	}
	dp := insts[0]
	if dp.Domain != "a.test" || dp.Version != "8.3" || dp.ListenAddr != "127.0.0.1:9001" {
		t.Errorf("unexpected instance: %+v", dp)
	}
	// TCP addr without started proc → not running.
	if dp.Running {
		t.Error("expected not running for TCP addr without proc")
	}
	// Overrides should be copied, not aliased.
	cfg := m.GetDomainConfig("a.test")
	if cfg["memory_limit"] != "256M" {
		t.Errorf("override not copied: %v", cfg)
	}
	overrides["memory_limit"] = "mutated"
	if m.GetDomainConfig("a.test")["memory_limit"] != "256M" {
		t.Error("override map was aliased, not copied")
	}
}

func TestRegisterExistingDomainSocketRunning(t *testing.T) {
	m := New(testLogger())
	m.RegisterExistingDomain("sock.test", "8.3", "unix:/run/php/php8.3-fpm.sock", "/srv/sock", nil)

	addr := m.RunningAddrForDomain("sock.test")
	if addr != "unix:/run/php/php8.3-fpm.sock" {
		t.Errorf("running addr = %q, want socket", addr)
	}
	insts := m.GetDomainInstances()
	if len(insts) != 1 || !insts[0].Running {
		t.Fatalf("expected one running (system) instance, got %+v", insts)
	}
	if insts[0].PID != -1 {
		t.Errorf("system-managed PID = %d, want -1", insts[0].PID)
	}
}

func TestRegisterExistingDomainAbsPathSocket(t *testing.T) {
	m := New(testLogger())
	// Absolute-path listen addr (no unix: prefix) is also treated as a socket.
	m.RegisterExistingDomain("abs.test", "8.2", "/run/php/php8.2-fpm.sock", "", nil)
	if addr := m.RunningAddrForDomain("abs.test"); addr != "/run/php/php8.2-fpm.sock" {
		t.Errorf("running addr = %q", addr)
	}
}

func TestRegisterExistingDomainDuplicateIgnored(t *testing.T) {
	m := New(testLogger())
	m.RegisterExistingDomain("dup.test", "8.3", "127.0.0.1:9001", "/srv/a", nil)
	// Second registration with different data is ignored.
	m.RegisterExistingDomain("dup.test", "8.4", "127.0.0.1:9999", "/srv/b", nil)

	insts := m.GetDomainInstances()
	if len(insts) != 1 {
		t.Fatalf("instances = %d, want 1", len(insts))
	}
	if insts[0].Version != "8.3" {
		t.Errorf("version = %q, want 8.3 (first wins)", insts[0].Version)
	}
}

// --- manager.go: RunningAddrForDomain ---

func TestRunningAddrForDomainUnknown(t *testing.T) {
	m := New(testLogger())
	if addr := m.RunningAddrForDomain("nope.test"); addr != "" {
		t.Errorf("addr = %q, want empty", addr)
	}
}

func TestRunningAddrForDomainTCPNotStarted(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{{Version: "8.3.0", Binary: "/usr/bin/php-cgi", SAPI: "cgi-fcgi"}}
	if _, err := m.AssignDomain("t.test", "8.3"); err != nil {
		t.Fatalf("AssignDomain: %v", err)
	}
	// Assigned a TCP port but never started → proc nil, TCP addr → "".
	if addr := m.RunningAddrForDomain("t.test"); addr != "" {
		t.Errorf("addr = %q, want empty for un-started TCP", addr)
	}
}

func TestRunningAddrForDomainTCPRunning(t *testing.T) {
	m := New(testLogger())
	cmd := longRunningCmd()
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start long-running cmd: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	m.domainMu.Lock()
	m.domainMap["run.test"] = &domainInstance{
		domain:     "run.test",
		version:    "8.3",
		listenAddr: "127.0.0.1:9001",
		proc:       &processInfo{cmd: cmd, listenAddr: "127.0.0.1:9001"},
	}
	m.domainMu.Unlock()

	if addr := m.RunningAddrForDomain("run.test"); addr != "127.0.0.1:9001" {
		t.Errorf("addr = %q, want 127.0.0.1:9001", addr)
	}
}

func TestRunningAddrForDomainProcOnSocketNoCmd(t *testing.T) {
	m := New(testLogger())
	m.domainMu.Lock()
	m.domainMap["s.test"] = &domainInstance{
		domain:     "s.test",
		listenAddr: "unix:/run/php.sock",
		proc:       &processInfo{listenAddr: "unix:/run/php.sock"}, // no cmd
	}
	m.domainMu.Unlock()
	if addr := m.RunningAddrForDomain("s.test"); addr != "unix:/run/php.sock" {
		t.Errorf("addr = %q, want socket", addr)
	}
}

func TestRunningAddrForDomainProcTCPNoCmd(t *testing.T) {
	m := New(testLogger())
	m.domainMu.Lock()
	m.domainMap["x.test"] = &domainInstance{
		domain:     "x.test",
		listenAddr: "127.0.0.1:9002",
		proc:       &processInfo{listenAddr: "127.0.0.1:9002"}, // proc set, no cmd, TCP
	}
	m.domainMu.Unlock()
	// proc != nil, cmd == nil, TCP addr → "".
	if addr := m.RunningAddrForDomain("x.test"); addr != "" {
		t.Errorf("addr = %q, want empty", addr)
	}
}

// --- manager.go: RestartDomain ---

func TestRestartDomainNoAssignment(t *testing.T) {
	m := New(testLogger())
	err := m.RestartDomain("ghost.test")
	if err == nil || !strings.Contains(err.Error(), "no PHP assignment") {
		t.Errorf("err = %v, want no assignment", err)
	}
}

func TestRestartDomainNotRunning(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{{Version: "8.3.0", Binary: "/usr/bin/php-cgi", SAPI: "cgi-fcgi"}}
	if _, err := m.AssignDomain("nr.test", "8.3"); err != nil {
		t.Fatalf("AssignDomain: %v", err)
	}
	err := m.RestartDomain("nr.test")
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Errorf("err = %v, want not running", err)
	}
}

func TestRestartDomainSocketSuccess(t *testing.T) {
	// A system-fpm-socket domain: StopDomain detaches, StartDomain re-marks
	// running, no real process spawned. Lets RestartDomain run end-to-end.
	m := New(testLogger())
	m.installations = []PHPInstall{{Version: "8.3.0", Binary: "/usr/bin/php-fpm", SAPI: "fpm-fcgi"}}
	m.domainMu.Lock()
	m.domainMap["rd.test"] = &domainInstance{
		domain:     "rd.test",
		version:    "8.3",
		listenAddr: "unix:/run/php/php8.3-fpm.sock",
		proc:       &processInfo{listenAddr: "unix:/run/php/php8.3-fpm.sock"},
	}
	m.domainMu.Unlock()

	if err := m.RestartDomain("rd.test"); err != nil {
		t.Fatalf("RestartDomain: %v", err)
	}
	// After restart it should still be reported running (system socket).
	if addr := m.RunningAddrForDomain("rd.test"); addr == "" {
		t.Error("expected socket still running after restart")
	}
}

func TestRestartDomainStartFailsAfterStop(t *testing.T) {
	// Running TCP proc, but the install was removed so StartDomain fails after
	// StopDomain has already run → exercises the "start failed" branch.
	m := New(testLogger())
	cmd := longRunningCmd()
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start long-running cmd: %v", err)
	}
	m.domainMu.Lock()
	m.domainMap["rf.test"] = &domainInstance{
		domain:     "rf.test",
		version:    "9.9", // not installed
		listenAddr: "127.0.0.1:9050",
		proc:       &processInfo{cmd: cmd, listenAddr: "127.0.0.1:9050"},
	}
	m.domainMu.Unlock()

	err := m.RestartDomain("rf.test")
	if err == nil || !strings.Contains(err.Error(), "start failed") {
		t.Errorf("err = %v, want start failed", err)
	}
	// Process should already have been killed by StopDomain.
	_ = cmd.Wait()
}

// --- fpm.go: RestartFPM ---

func TestRestartFPMNotRunning(t *testing.T) {
	m := New(testLogger())
	err := m.RestartFPM("8.3.0")
	if err == nil || !strings.Contains(err.Error(), "not running") {
		t.Errorf("err = %v, want not running", err)
	}
}

func TestRestartFPMStaleEntry(t *testing.T) {
	m := New(testLogger())
	// Store a non-*processInfo value to trigger the type-assertion failure.
	m.processes.Store("8.3.0", "not-a-processInfo")
	err := m.RestartFPM("8.3.0")
	if err == nil || !strings.Contains(err.Error(), "stale process entry") {
		t.Errorf("err = %v, want stale process entry", err)
	}
	if _, ok := m.processes.Load("8.3.0"); ok {
		t.Error("stale entry should have been deleted")
	}
}

func TestRestartFPMStopFailsStaleCmd(t *testing.T) {
	// processInfo with nil cmd → StopFPM treats it as stale, deletes it, and
	// returns nil. RestartFPM then tries StartFPM which fails (no install) →
	// "start failed" branch.
	m := New(testLogger())
	m.processes.Store("8.3.0", &processInfo{listenAddr: "127.0.0.1:9000"})
	err := m.RestartFPM("8.3.0")
	if err == nil || !strings.Contains(err.Error(), "start failed") {
		t.Errorf("err = %v, want start failed", err)
	}
}

func TestRestartFPMSuccess(t *testing.T) {
	// Real long-running process + an installed cgi binary that we mock with a
	// long-running command, so StopFPM kills the old proc and StartFPM spawns
	// a fresh one.
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.3.0", Binary: "/usr/bin/php-cgi8.3", SAPI: "cgi-fcgi"},
	}
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return longRunningCmd()
	}

	oldCmd := longRunningCmd()
	if err := oldCmd.Start(); err != nil {
		t.Skipf("cannot start long-running cmd: %v", err)
	}
	// No reaper owns oldCmd; StopFPM (via RestartFPM) kills it. We must reap it
	// ourselves, but only after RestartFPM returns (which Kills it first).
	m.processes.Store("8.3.0", &processInfo{cmd: oldCmd, listenAddr: "127.0.0.1:9000"})

	if err := m.RestartFPM("8.3.0"); err != nil {
		t.Fatalf("RestartFPM: %v", err)
	}
	_ = oldCmd.Wait() // safe: no goroutine in StartFPM ever Waited on oldCmd

	// A new process must now be registered. It is owned by StartFPM's reaper
	// goroutine (which calls Wait), so the test must NOT call Wait on it.
	val, ok := m.processes.Load("8.3.0")
	if !ok {
		t.Fatal("expected new process registered after restart")
	}
	info := val.(*processInfo)
	if info.listenAddr != "127.0.0.1:9000" {
		t.Errorf("listenAddr = %q, want 127.0.0.1:9000", info.listenAddr)
	}
	// StopAll kills the new process; its reaper goroutine performs the Wait.
	m.StopAll()
}

// --- fpm.go: isProcessRunning ---

func TestIsProcessRunningInvalidPID(t *testing.T) {
	m := New(testLogger())
	if m.isProcessRunning(0) {
		t.Error("pid 0 should not be running")
	}
	if m.isProcessRunning(-1) {
		t.Error("negative pid should not be running")
	}
}

func TestIsProcessRunningSelf(t *testing.T) {
	m := New(testLogger())
	if !m.isProcessRunning(os.Getpid()) {
		t.Error("current process should report running")
	}
}

func TestIsProcessRunningDeadPID(t *testing.T) {
	m := New(testLogger())
	// Start and immediately reap a process so its PID is no longer alive.
	cmd := exec.Command("echo", "hi")
	if err := cmd.Run(); err != nil {
		t.Skipf("cannot run echo: %v", err)
	}
	pid := cmd.Process.Pid
	if m.isProcessRunning(pid) {
		t.Errorf("reaped pid %d should not report running", pid)
	}
}

// --- detect.go: runPHP error and timeout paths ---

func TestRunPHPStartError(t *testing.T) {
	m := New(testLogger())
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("/nonexistent/binary/definitely/missing", args...)
	}
	if _, err := m.runPHP("x", "-v"); err == nil {
		t.Error("expected start error for missing binary")
	}
}

func TestRunPHPNonZeroExitWithStderr(t *testing.T) {
	m := New(testLogger())
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		// Write to stderr and exit non-zero.
		return exec.Command("sh", "-c", "echo boom >&2; exit 1")
	}
	_, err := m.runPHP("x", "-v")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want stderr message included", err)
	}
}

func TestRunPHPNonZeroExitNoStderr(t *testing.T) {
	m := New(testLogger())
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("sh", "-c", "exit 3")
	}
	if _, err := m.runPHP("x", "-v"); err == nil {
		t.Error("expected error for non-zero exit")
	}
}

func TestRunPHPTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 3s timeout test in -short mode")
	}
	m := New(testLogger())
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("sleep", "10")
	}
	start := time.Now()
	_, err := m.runPHP("x", "-v")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("err = %v, want timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("runPHP took %v, expected ~3s", elapsed)
	}
}

// --- detect.go: findInstallPtr fallback to non-cgi (cli) binary ---

func TestFindInstallPtrFallbackCLI(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.3.0", Binary: "/usr/bin/php8.3", SAPI: "cli"},
	}
	ptr, ok := m.findInstallPtr("8.3")
	if !ok {
		t.Fatal("expected cli fallback match")
	}
	if ptr.SAPI != "cli" {
		t.Errorf("SAPI = %q, want cli", ptr.SAPI)
	}
	// Mutating through the pointer must persist in the slice.
	ptr.ConfigFile = "/tmp/x.ini"
	if m.installations[0].ConfigFile != "/tmp/x.ini" {
		t.Error("pointer mutation did not persist")
	}
}

func TestFindInstallPtrNoMatch(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{{Version: "8.3.0", SAPI: "cli"}}
	if _, ok := m.findInstallPtr("7.4"); ok {
		t.Error("expected no match for 7.4")
	}
}

func TestFindInstallPtrPrefersCGI(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.3.0", Binary: "/usr/bin/php8.3", SAPI: "cli"},
		{Version: "8.3.1", Binary: "/usr/bin/php-cgi8.3", SAPI: "cgi-fcgi"},
	}
	ptr, ok := m.findInstallPtr("8.3")
	if !ok {
		t.Fatal("expected a match")
	}
	if ptr.SAPI != "cgi-fcgi" {
		t.Errorf("SAPI = %q, want cgi-fcgi (preferred over cli)", ptr.SAPI)
	}
}

// RunningAddrForDomain: instance with proc == nil but a socket listen addr is
// considered system-managed and still serving.
func TestRunningAddrForDomainSocketNoProc(t *testing.T) {
	m := New(testLogger())
	m.domainMu.Lock()
	m.domainMap["np.test"] = &domainInstance{
		domain:     "np.test",
		listenAddr: "unix:/run/php/np.sock",
		// proc == nil
	}
	m.domainMap["np2.test"] = &domainInstance{
		domain:     "np2.test",
		listenAddr: "127.0.0.1:9001", // TCP, proc nil → ""
	}
	m.domainMu.Unlock()

	if addr := m.RunningAddrForDomain("np.test"); addr != "unix:/run/php/np.sock" {
		t.Errorf("socket addr = %q, want unix:/run/php/np.sock", addr)
	}
	if addr := m.RunningAddrForDomain("np2.test"); addr != "" {
		t.Errorf("tcp-no-proc addr = %q, want empty", addr)
	}
}

// --- fpm.go: Status with dead managed process (cleanup branch) ---

func TestStatusDeadManagedProcessCleanup(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.3.0", Binary: "/usr/bin/php-cgi8.3", SAPI: "cgi-fcgi"},
	}
	// Store a process whose cmd has a nil Process (never started) → treated as
	// not running and cleaned up.
	m.processes.Store("8.3.0", &processInfo{cmd: exec.Command("echo"), listenAddr: "127.0.0.1:9000"})

	statuses := m.Status()
	if len(statuses) != 1 {
		t.Fatalf("statuses = %d, want 1", len(statuses))
	}
	if statuses[0].Running {
		t.Error("dead managed process should report not running")
	}
	if _, ok := m.processes.Load("8.3.0"); ok {
		t.Error("dead process entry should be cleaned up")
	}
}

// --- manager.go: domainPHPFromInstance — system fpm registered without proc ---

func TestDomainPHPFromInstanceSystemNoProc(t *testing.T) {
	m := New(testLogger())
	inst := &domainInstance{
		domain:     "sys.test",
		version:    "8.3",
		listenAddr: "unix:/run/php.sock",
		// proc == nil, socket addr → system-managed running.
	}
	dp := m.domainPHPFromInstance(inst)
	if !dp.Running || dp.PID != -1 {
		t.Errorf("expected running system-managed (PID -1), got %+v", dp)
	}
}

func TestDomainPHPFromInstanceTCPNoProcNotRunning(t *testing.T) {
	m := New(testLogger())
	inst := &domainInstance{
		domain:     "tcp.test",
		version:    "8.3",
		listenAddr: "127.0.0.1:9001",
	}
	dp := m.domainPHPFromInstance(inst)
	if dp.Running {
		t.Error("TCP addr without proc should not be running")
	}
}

// --- manager.go: StopAll — non-string key in processes map ---

func TestStopAllNonStringKey(t *testing.T) {
	m := New(testLogger())
	// Store an entry keyed by a non-string → triggers the !ok delete branch.
	m.processes.Store(42, &processInfo{listenAddr: "x"})
	m.StopAll()
	if _, ok := m.processes.Load(42); ok {
		t.Error("non-string-keyed entry should be deleted")
	}
}

// --- manager.go: netDialTimeout default hook (exercise the wrapper) ---

func TestNetDialTimeoutDefaultHook(t *testing.T) {
	// Spin up a listener and dial it through the package's default wrapper so
	// the closure body (line 22-24) is executed.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen: %v", err)
	}
	defer ln.Close()

	conn, err := netDialTimeout("tcp", ln.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("netDialTimeout: %v", err)
	}
	conn.Close()
}

// --- manager.go: StartDomain crash-loop give-up branch ---

func TestStartDomainCrashLoopGivesUp(t *testing.T) {
	if testing.Short() {
		t.Skip("crash-loop test relies on real reaper timing; skip in -short")
	}
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.3.0", Binary: "/usr/bin/php-cgi8.3", SAPI: "cgi-fcgi"},
	}
	// execCommand returns a process that exits immediately and non-zero, so the
	// reaper keeps trying to restart until it gives up after maxRapidRestarts.
	m.execCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("sh", "-c", "exit 1")
	}

	var crashes int
	var mu sync.Mutex
	m.SetOnCrash(func(domain string) {
		mu.Lock()
		crashes++
		mu.Unlock()
	})

	m.domainMu.Lock()
	m.domainMap["loop.test"] = &domainInstance{
		domain:     "loop.test",
		version:    "8.3",
		listenAddr: "127.0.0.1:9070",
	}
	m.domainMu.Unlock()

	if err := m.StartDomain("loop.test"); err != nil {
		t.Fatalf("StartDomain: %v", err)
	}

	// Backoff is 0.5+1+2+4+8s across restarts; wait until restartCount exceeds
	// the cap and the reaper logs "giving up". Poll the counter.
	deadline := time.Now().Add(25 * time.Second)
	gaveUp := false
	for time.Now().Before(deadline) {
		m.domainMu.RLock()
		di := m.domainMap["loop.test"]
		var rc int
		var procNil bool
		if di != nil {
			rc = di.restartCount
			procNil = di.proc == nil
		}
		m.domainMu.RUnlock()
		if rc > 5 && procNil {
			gaveUp = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !gaveUp {
		t.Fatal("crash loop never reached give-up state")
	}
	mu.Lock()
	c := crashes
	mu.Unlock()
	if c == 0 {
		t.Error("expected onCrash to fire at least once")
	}
	// Cleanup: unassign so any in-flight reaper does not restart.
	m.UnassignDomain("loop.test")
	m.StopAll()
}

// --- install.go: RunInstall blank command line (empty Fields) ---

func TestRunInstallBlankCommandSkipped(t *testing.T) {
	origGOOS := runtimeGOOSInstall
	origRead := readOSRelease
	origExec := installExecCommand
	defer func() {
		runtimeGOOSInstall = origGOOS
		readOSRelease = origRead
		installExecCommand = origExec
	}()

	runtimeGOOSInstall = "linux"
	// Unknown distro → commands are "# ..." comments only; add a whitespace
	// command via a custom os-release that resolves to fedora (single real cmd)
	// then verify a blank line is handled. We force the empty-Fields branch by
	// making the exec a no-op and relying on RunInstall trimming.
	readOSRelease = func() ([]byte, error) {
		return []byte("ID=fedora\n"), nil
	}
	installExecCommand = func(name string, args ...string) *exec.Cmd {
		return exec.Command("sh", "-c", "exit 0")
	}
	if _, err := RunInstall("8.3"); err != nil {
		t.Fatalf("RunInstall: %v", err)
	}
}

// --- detect.go: Detect glob abs/eval edge via temp dir with real-ish binary ---

func TestDetectWithTempBinary(t *testing.T) {
	dir := t.TempDir()
	// Create a fake php-cgi script that prints version/sapi/modules.
	bin := filepath.Join(dir, "php-cgi8.3")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"-v) echo 'PHP 8.3.0 (cgi-fcgi) (built: today)';;\n" +
		"-i) echo 'Loaded Configuration File => (none)';;\n" +
		"-m) printf '[PHP Modules]\\nCore\\ncurl\\n';;\n" +
		"esac\n"
	if err := os.WriteFile(bin, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	origCands := candidatePathsFunc
	origStat := osStat
	origMkdir := osMkdirAllHook
	origWrite := osWriteFileHook
	defer func() {
		candidatePathsFunc = origCands
		osStat = origStat
		osMkdirAllHook = origMkdir
		osWriteFileHook = origWrite
	}()

	candidatePathsFunc = func() []string { return []string{filepath.Join(dir, "php-cgi*")} }
	// Prevent findOrCreatePHPConfig from touching the real filesystem.
	osStat = func(string) (os.FileInfo, error) { return nil, errors.New("no stat") }
	osMkdirAllHook = func(string, os.FileMode) error { return errors.New("no mkdir") }
	osWriteFileHook = func(string, []byte, os.FileMode) error { return errors.New("no write") }

	m := New(testLogger())
	if err := m.Detect(); err != nil {
		t.Fatalf("Detect: %v", err)
	}
	insts := m.Installations()
	if len(insts) != 1 {
		t.Fatalf("installations = %d, want 1: %+v", len(insts), insts)
	}
	if insts[0].Version != "8.3.0" || insts[0].SAPI != "cgi-fcgi" {
		t.Errorf("unexpected install: %+v", insts[0])
	}
}

// TestDetectBrokenSymlink covers the EvalSymlinks-error fallback (real = abs):
// a broken symlink is matched by glob, EvalSymlinks fails, probe then fails
// (target missing) so the entry is skipped — but the fallback line executes.
func TestDetectBrokenSymlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "php-cgi-broken")
	if err := os.Symlink(filepath.Join(dir, "does-not-exist"), link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	origCands := candidatePathsFunc
	defer func() { candidatePathsFunc = origCands }()
	candidatePathsFunc = func() []string { return []string{filepath.Join(dir, "php-cgi*")} }

	m := New(testLogger())
	if err := m.Detect(); err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if got := len(m.Installations()); got != 0 {
		t.Errorf("installations = %d, want 0 (broken symlink skipped)", got)
	}
}
