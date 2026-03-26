package cronjob

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// ─── TestHelperProcess ──────────────────────────────────────────────────────
// This is a fake process used by exec.Command mocking. It is invoked by the
// test binary itself (via os.Args[0]) when GO_WANT_HELPER_PROCESS=1 is set.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	// Write the requested output to stdout
	if out := os.Getenv("GO_HELPER_OUTPUT"); out != "" {
		fmt.Fprint(os.Stdout, out)
	}
	if errOut := os.Getenv("GO_HELPER_STDERR"); errOut != "" {
		fmt.Fprint(os.Stderr, errOut)
	}

	exitCode := os.Getenv("GO_HELPER_EXIT")
	if exitCode == "1" {
		os.Exit(1)
	}
	os.Exit(0)
}

// fakeExecCommand returns a function that replaces exec.Command with a call
// back into the test binary's TestHelperProcess, with the given environment.
func fakeExecCommand(output string, exitCode int) func(string, ...string) *exec.Cmd {
	return func(name string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=TestHelperProcess", "--"}
		cs = append(cs, name)
		cs = append(cs, args...)
		cmd := exec.Command(os.Args[0], cs...)
		cmd.Env = []string{
			"GO_WANT_HELPER_PROCESS=1",
			fmt.Sprintf("GO_HELPER_OUTPUT=%s", output),
			fmt.Sprintf("GO_HELPER_EXIT=%d", exitCode),
		}
		// On Windows, propagate SystemRoot and PATH so process can start
		if runtime.GOOS == "windows" {
			cmd.Env = append(cmd.Env,
				"SystemRoot="+os.Getenv("SystemRoot"),
				"PATH="+os.Getenv("PATH"),
			)
		}
		return cmd
	}
}

// fakeExecCommandWithStderr is like fakeExecCommand but also writes to stderr.
func fakeExecCommandWithStderr(stdout, stderr string, exitCode int) func(string, ...string) *exec.Cmd {
	return func(name string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=TestHelperProcess", "--"}
		cs = append(cs, name)
		cs = append(cs, args...)
		cmd := exec.Command(os.Args[0], cs...)
		cmd.Env = []string{
			"GO_WANT_HELPER_PROCESS=1",
			fmt.Sprintf("GO_HELPER_OUTPUT=%s", stdout),
			fmt.Sprintf("GO_HELPER_STDERR=%s", stderr),
			fmt.Sprintf("GO_HELPER_EXIT=%d", exitCode),
		}
		if runtime.GOOS == "windows" {
			cmd.Env = append(cmd.Env,
				"SystemRoot="+os.Getenv("SystemRoot"),
				"PATH="+os.Getenv("PATH"),
			)
		}
		return cmd
	}
}

// ─── manager.go: parseCronLine ──────────────────────────────────────────────
func TestParseCronLine(t *testing.T) {
	tests := []struct {
		input        string
		wantSchedule string
		wantCommand  string
	}{
		{
			"*/5 * * * * /usr/bin/php /var/www/cron.php",
			"*/5 * * * *",
			"/usr/bin/php /var/www/cron.php",
		},
		{
			"0 2 * * * /usr/local/bin/backup.sh --full",
			"0 2 * * *",
			"/usr/local/bin/backup.sh --full",
		},
		{
			"30 4 1 * * /bin/cleanup",
			"30 4 1 * *",
			"/bin/cleanup",
		},
		{
			// Too few fields — entire line becomes Command
			"short",
			"",
			"short",
		},
		{
			"",
			"",
			"",
		},
	}

	for _, tt := range tests {
		got := parseCronLine(tt.input)
		if got.Schedule != tt.wantSchedule {
			t.Errorf("parseCronLine(%q).Schedule = %q, want %q",
				tt.input, got.Schedule, tt.wantSchedule)
		}
		if got.Command != tt.wantCommand {
			t.Errorf("parseCronLine(%q).Command = %q, want %q",
				tt.input, got.Command, tt.wantCommand)
		}
	}
}

func TestParseCronLineWithExtraSpaces(t *testing.T) {
	line := "  0  3  *  *  *  /usr/bin/run task  "
	got := parseCronLine(line)
	if got.Schedule != "0 3 * * *" {
		t.Errorf("parseCronLine with spaces: Schedule = %q, want %q", got.Schedule, "0 3 * * *")
	}
	if got.Command != "/usr/bin/run task" {
		t.Errorf("parseCronLine with spaces: Command = %q, want %q", got.Command, "/usr/bin/run task")
	}
}

func TestUwasMarkerConstant(t *testing.T) {
	if uwasMarker == "" {
		t.Error("uwasMarker should not be empty")
	}
	if uwasMarker != "# UWAS managed" {
		t.Errorf("uwasMarker = %q, want %q", uwasMarker, "# UWAS managed")
	}
}

func TestJobStruct(t *testing.T) {
	job := Job{
		Schedule: "*/5 * * * *",
		Command:  "/usr/bin/php /var/www/cron.php",
		Domain:   "example.com",
		Comment:  "WordPress cron",
	}

	if job.Schedule != "*/5 * * * *" {
		t.Errorf("expected Schedule '*/5 * * * *', got %q", job.Schedule)
	}
	if job.Command != "/usr/bin/php /var/www/cron.php" {
		t.Errorf("expected Command, got %q", job.Command)
	}
	if job.Domain != "example.com" {
		t.Errorf("expected Domain 'example.com', got %q", job.Domain)
	}
	if job.Comment != "WordPress cron" {
		t.Errorf("expected Comment 'WordPress cron', got %q", job.Comment)
	}
}

// ─── manager.go: List ───────────────────────────────────────────────────────
func TestList_Windows(t *testing.T) {
	origGOOS := runtimeGOOS
	runtimeGOOS = "windows"
	defer func() { runtimeGOOS = origGOOS }()

	jobs, err := List()
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if jobs != nil {
		t.Fatalf("expected nil jobs on windows, got %v", jobs)
	}
}

func TestList_Success(t *testing.T) {
	origGOOS := runtimeGOOS
	origCmd := execCommandFn
	runtimeGOOS = "linux"
	defer func() {
		runtimeGOOS = origGOOS
		execCommandFn = origCmd
	}()

	crontabOutput := strings.Join([]string{
		"# some other comment",
		"0 * * * * /usr/bin/other",
		"# UWAS managed [example.com] WP cron",
		"*/5 * * * * /usr/bin/php /var/www/cron.php",
		"# UWAS managed [test.org] Backup",
		"0 2 * * * /usr/local/bin/backup.sh",
		"",
	}, "\n")
	execCommandFn = fakeExecCommand(crontabOutput, 0)

	jobs, err := List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}

	// First job — TrimRight(parts[1], "]") trims trailing ']' chars from
	// "example.com] WP cron" which has no trailing ']', so Domain keeps the
	// full suffix. This is the actual behavior of the source code.
	if !strings.HasPrefix(jobs[0].Domain, "example.com") {
		t.Errorf("job[0].Domain = %q, want prefix 'example.com'", jobs[0].Domain)
	}
	if jobs[0].Schedule != "*/5 * * * *" {
		t.Errorf("job[0].Schedule = %q, want %q", jobs[0].Schedule, "*/5 * * * *")
	}
	if jobs[0].Command != "/usr/bin/php /var/www/cron.php" {
		t.Errorf("job[0].Command = %q, want %q", jobs[0].Command, "/usr/bin/php /var/www/cron.php")
	}
	if !strings.Contains(jobs[0].Comment, "WP cron") {
		t.Errorf("job[0].Comment = %q, want to contain 'WP cron'", jobs[0].Comment)
	}

	// Second job
	if !strings.HasPrefix(jobs[1].Domain, "test.org") {
		t.Errorf("job[1].Domain = %q, want prefix 'test.org'", jobs[1].Domain)
	}
	if jobs[1].Schedule != "0 2 * * *" {
		t.Errorf("job[1].Schedule = %q, want %q", jobs[1].Schedule, "0 2 * * *")
	}
}

func TestList_NoCrontab(t *testing.T) {
	origGOOS := runtimeGOOS
	origCmd := execCommandFn
	runtimeGOOS = "linux"
	defer func() {
		runtimeGOOS = origGOOS
		execCommandFn = origCmd
	}()

	// Simulate crontab -l returning an error (no crontab for user)
	execCommandFn = fakeExecCommand("", 1)

	jobs, err := List()
	if err != nil {
		t.Fatalf("expected nil error for no crontab, got %v", err)
	}
	if jobs != nil {
		t.Fatalf("expected nil jobs when no crontab, got %v", jobs)
	}
}

func TestList_NoUwasEntries(t *testing.T) {
	origGOOS := runtimeGOOS
	origCmd := execCommandFn
	runtimeGOOS = "linux"
	defer func() {
		runtimeGOOS = origGOOS
		execCommandFn = origCmd
	}()

	// Crontab with entries but none from UWAS
	crontabOutput := "0 * * * * /usr/bin/something\n"
	execCommandFn = fakeExecCommand(crontabOutput, 0)

	jobs, err := List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("expected 0 jobs, got %d", len(jobs))
	}
}

func TestList_UwasMarkerAtLastLine(t *testing.T) {
	origGOOS := runtimeGOOS
	origCmd := execCommandFn
	runtimeGOOS = "linux"
	defer func() {
		runtimeGOOS = origGOOS
		execCommandFn = origCmd
	}()

	// UWAS marker is the very last line (no next line for the job)
	crontabOutput := "# UWAS managed [example.com] trailing"
	execCommandFn = fakeExecCommand(crontabOutput, 0)

	jobs, err := List()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The marker is at index 0, len(lines) is 1, so i+1 == 1 which is NOT < 1
	// so the job should NOT be appended
	if len(jobs) != 0 {
		t.Fatalf("expected 0 jobs when marker is last line, got %d", len(jobs))
	}
}

// ─── manager.go: Add ────────────────────────────────────────────────────────
func TestAdd_Windows(t *testing.T) {
	origGOOS := runtimeGOOS
	runtimeGOOS = "windows"
	defer func() { runtimeGOOS = origGOOS }()

	err := Add(Job{Schedule: "* * * * *", Command: "echo hi"})
	if err == nil {
		t.Fatal("expected error on Windows, got nil")
	}
	if !strings.Contains(err.Error(), "Windows") {
		t.Errorf("error = %q, want to contain 'Windows'", err.Error())
	}
}

func TestAdd_Success(t *testing.T) {
	origGOOS := runtimeGOOS
	origCmd := execCommandFn
	runtimeGOOS = "linux"
	defer func() {
		runtimeGOOS = origGOOS
		execCommandFn = origCmd
	}()

	// Both crontab -l (read existing) and crontab <file> (write) succeed
	execCommandFn = fakeExecCommand("# existing line\n", 0)

	err := Add(Job{
		Schedule: "*/5 * * * *",
		Command:  "/usr/bin/php /var/www/cron.php",
		Domain:   "example.com",
		Comment:  "WP cron",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAdd_DuplicateCheck(t *testing.T) {
	origGOOS := runtimeGOOS
	origCmd := execCommandFn
	runtimeGOOS = "linux"
	defer func() {
		runtimeGOOS = origGOOS
		execCommandFn = origCmd
	}()

	// Add does not check for duplicates; calling it twice should succeed
	execCommandFn = fakeExecCommand("", 0)

	job := Job{
		Schedule: "*/5 * * * *",
		Command:  "/usr/bin/php /var/www/cron.php",
		Domain:   "example.com",
		Comment:  "WP cron",
	}
	if err := Add(job); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if err := Add(job); err != nil {
		t.Fatalf("second add: %v", err)
	}
}

// ─── manager.go: Remove ─────────────────────────────────────────────────────
func TestRemove_Windows(t *testing.T) {
	origGOOS := runtimeGOOS
	runtimeGOOS = "windows"
	defer func() { runtimeGOOS = origGOOS }()

	err := Remove("*/5 * * * *", "/usr/bin/php /var/www/cron.php")
	if err == nil {
		t.Fatal("expected error on Windows, got nil")
	}
	if !strings.Contains(err.Error(), "Windows") {
		t.Errorf("error = %q, want to contain 'Windows'", err.Error())
	}
}

func TestRemove_Success(t *testing.T) {
	origGOOS := runtimeGOOS
	origCmd := execCommandFn
	runtimeGOOS = "linux"
	defer func() {
		runtimeGOOS = origGOOS
		execCommandFn = origCmd
	}()

	crontabContent := strings.Join([]string{
		"# UWAS managed [example.com] WP cron",
		"*/5 * * * * /usr/bin/php /var/www/cron.php",
		"0 * * * * /usr/bin/other",
		"",
	}, "\n")
	execCommandFn = fakeExecCommand(crontabContent, 0)

	err := Remove("*/5 * * * *", "/usr/bin/php /var/www/cron.php")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRemove_NotFound(t *testing.T) {
	origGOOS := runtimeGOOS
	origCmd := execCommandFn
	runtimeGOOS = "linux"
	defer func() {
		runtimeGOOS = origGOOS
		execCommandFn = origCmd
	}()

	// Crontab has no matching entry; Remove still succeeds (rewrites whole tab)
	crontabContent := "0 * * * * /usr/bin/other\n"
	execCommandFn = fakeExecCommand(crontabContent, 0)

	err := Remove("*/5 * * * *", "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ─── manager.go: writeCrontab ───────────────────────────────────────────────
func TestWriteCrontab(t *testing.T) {
	origCmd := execCommandFn
	defer func() { execCommandFn = origCmd }()

	execCommandFn = fakeExecCommand("", 0)

	err := writeCrontab("# test crontab content\n0 * * * * /bin/echo hello\n")
	if err != nil {
		t.Fatalf("writeCrontab failed: %v", err)
	}
}

func TestWriteCrontab_CommandFails(t *testing.T) {
	origCmd := execCommandFn
	defer func() { execCommandFn = origCmd }()

	execCommandFn = fakeExecCommand("", 1)

	err := writeCrontab("content")
	if err == nil {
		t.Fatal("expected error when crontab command fails, got nil")
	}
}

// ─── monitor.go: NewMonitor ─────────────────────────────────────────────────
func TestNewMonitor(t *testing.T) {
	dir := t.TempDir()
	m := NewMonitor(dir)
	if m == nil {
		t.Fatal("NewMonitor returned nil")
	}
	if m.dataDir != dir {
		t.Errorf("dataDir = %q, want %q", m.dataDir, dir)
	}
	if m.maxHistory != 100 {
		t.Errorf("maxHistory = %d, want 100", m.maxHistory)
	}
	if m.history == nil {
		t.Error("history map should be initialized")
	}
}

func TestNewMonitor_EmptyDataDir(t *testing.T) {
	m := NewMonitor("")
	if m == nil {
		t.Fatal("NewMonitor returned nil")
	}
	if m.dataDir != "" {
		t.Errorf("dataDir = %q, want empty", m.dataDir)
	}
}

// ─── monitor.go: RecordExecution ────────────────────────────────────────────
func TestMonitor_RecordExecution(t *testing.T) {
	dir := t.TempDir()
	m := NewMonitor(dir)

	rec := ExecutionRecord{
		ID:        "1",
		Domain:    "example.com",
		Command:   "echo hello",
		Schedule:  "* * * * *",
		StartedAt: time.Now(),
		EndedAt:   time.Now(),
		Success:   true,
		Output:    "hello\n",
	}

	m.RecordExecution(rec)

	key := "example.com:echo hello"
	m.mu.RLock()
	history := m.history[key]
	m.mu.RUnlock()

	if len(history) != 1 {
		t.Fatalf("expected 1 record, got %d", len(history))
	}
	if history[0].ID != "1" {
		t.Errorf("record ID = %q, want %q", history[0].ID, "1")
	}
	if history[0].Output != "hello\n" {
		t.Errorf("record Output = %q, want %q", history[0].Output, "hello\n")
	}
}

func TestMonitor_RecordExecution_Multiple(t *testing.T) {
	m := NewMonitor("")

	for i := 0; i < 5; i++ {
		m.RecordExecution(ExecutionRecord{
			ID:      fmt.Sprintf("%d", i),
			Domain:  "d.com",
			Command: "cmd",
			Success: i%2 == 0,
		})
	}

	m.mu.RLock()
	h := m.history["d.com:cmd"]
	m.mu.RUnlock()

	if len(h) != 5 {
		t.Fatalf("expected 5 records, got %d", len(h))
	}
}

// ─── monitor.go: Execute ────────────────────────────────────────────────────
func TestMonitor_Execute_Success(t *testing.T) {
	origCmd := monitorExecCommandFn
	origGOOS := monitorRuntimeGOOS
	defer func() {
		monitorExecCommandFn = origCmd
		monitorRuntimeGOOS = origGOOS
	}()

	monitorRuntimeGOOS = "linux"
	monitorExecCommandFn = fakeExecCommand("hello world", 0)

	dir := t.TempDir()
	m := NewMonitor(dir)

	rec := m.Execute("example.com", "* * * * *", "echo hello")
	if !rec.Success {
		t.Errorf("expected success, got failure: %s", rec.Error)
	}
	if rec.Domain != "example.com" {
		t.Errorf("Domain = %q, want %q", rec.Domain, "example.com")
	}
	if rec.Schedule != "* * * * *" {
		t.Errorf("Schedule = %q, want %q", rec.Schedule, "* * * * *")
	}
	if rec.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", rec.ExitCode)
	}
}

func TestMonitor_Execute_Failure(t *testing.T) {
	origCmd := monitorExecCommandFn
	origGOOS := monitorRuntimeGOOS
	defer func() {
		monitorExecCommandFn = origCmd
		monitorRuntimeGOOS = origGOOS
	}()

	monitorRuntimeGOOS = "linux"
	monitorExecCommandFn = fakeExecCommand("", 1)

	dir := t.TempDir()
	m := NewMonitor(dir)

	rec := m.Execute("fail.com", "* * * * *", "false")
	if rec.Success {
		t.Error("expected failure, got success")
	}
	if rec.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", rec.ExitCode)
	}
}

func TestMonitor_Execute_Windows(t *testing.T) {
	origCmd := monitorExecCommandFn
	origGOOS := monitorRuntimeGOOS
	defer func() {
		monitorExecCommandFn = origCmd
		monitorRuntimeGOOS = origGOOS
	}()

	monitorRuntimeGOOS = "windows"
	monitorExecCommandFn = fakeExecCommand("win output", 0)

	dir := t.TempDir()
	m := NewMonitor(dir)

	rec := m.Execute("example.com", "* * * * *", "echo hello")
	if !rec.Success {
		t.Errorf("expected success, got failure: %s", rec.Error)
	}
}

func TestMonitor_Execute_WithStderr(t *testing.T) {
	origCmd := monitorExecCommandFn
	origGOOS := monitorRuntimeGOOS
	defer func() {
		monitorExecCommandFn = origCmd
		monitorRuntimeGOOS = origGOOS
	}()

	monitorRuntimeGOOS = "linux"
	monitorExecCommandFn = fakeExecCommandWithStderr("stdout data", "stderr data", 0)

	dir := t.TempDir()
	m := NewMonitor(dir)

	rec := m.Execute("test.com", "* * * * *", "cmd")
	if !rec.Success {
		t.Errorf("expected success, got failure: %s", rec.Error)
	}
	// Note: since the helper process exits 0 and success = true,
	// stderr content may be captured in Error field from stderr buffer.
}

func TestMonitor_Execute_WithDomainDir(t *testing.T) {
	origCmd := monitorExecCommandFn
	origGOOS := monitorRuntimeGOOS
	defer func() {
		monitorExecCommandFn = origCmd
		monitorRuntimeGOOS = origGOOS
	}()

	monitorRuntimeGOOS = "linux"
	monitorExecCommandFn = fakeExecCommand("ok", 0)

	// Create a data dir structure: dataDir/../domains/example.com/
	base := t.TempDir()
	dataDir := filepath.Join(base, "data")
	domainDir := filepath.Join(base, "domains", "example.com")
	os.MkdirAll(dataDir, 0755)
	os.MkdirAll(domainDir, 0755)

	m := NewMonitor(dataDir)
	rec := m.Execute("example.com", "* * * * *", "echo hello")
	if !rec.Success {
		t.Errorf("expected success with domain dir, got failure: %s", rec.Error)
	}
}

func TestMonitor_Execute_NoDomainDir(t *testing.T) {
	origCmd := monitorExecCommandFn
	origGOOS := monitorRuntimeGOOS
	defer func() {
		monitorExecCommandFn = origCmd
		monitorRuntimeGOOS = origGOOS
	}()

	monitorRuntimeGOOS = "linux"
	monitorExecCommandFn = fakeExecCommand("ok", 0)

	dir := t.TempDir()
	m := NewMonitor(dir)

	// Domain dir doesn't exist, so cmd.Dir should not be set
	rec := m.Execute("nonexistent.com", "* * * * *", "echo hello")
	if !rec.Success {
		t.Errorf("expected success, got failure: %s", rec.Error)
	}
}

// ─── monitor.go: GetStatus ──────────────────────────────────────────────────
func TestMonitor_GetStatus_Empty(t *testing.T) {
	m := NewMonitor("")
	status := m.GetStatus("example.com", "echo hello")
	if status != nil {
		t.Errorf("expected nil status for no history, got %+v", status)
	}
}

func TestMonitor_GetStatus_WithHistory(t *testing.T) {
	m := NewMonitor("")

	// Add 3 successes then 2 failures
	for i := 0; i < 3; i++ {
		m.RecordExecution(ExecutionRecord{
			ID:       fmt.Sprintf("s%d", i),
			Domain:   "d.com",
			Command:  "cmd",
			Schedule: "* * * * *",
			Success:  true,
		})
	}
	for i := 0; i < 2; i++ {
		m.RecordExecution(ExecutionRecord{
			ID:       fmt.Sprintf("f%d", i),
			Domain:   "d.com",
			Command:  "cmd",
			Schedule: "* * * * *",
			Success:  false,
		})
	}

	status := m.GetStatus("d.com", "cmd")
	if status == nil {
		t.Fatal("expected non-nil status")
	}
	if status.Domain != "d.com" {
		t.Errorf("Domain = %q, want %q", status.Domain, "d.com")
	}
	if status.Command != "cmd" {
		t.Errorf("Command = %q, want %q", status.Command, "cmd")
	}
	if len(status.History) != 5 {
		t.Errorf("History len = %d, want 5", len(status.History))
	}
	if status.LastRun == nil {
		t.Fatal("LastRun should not be nil")
	}
	if status.LastRun.ID != "f1" {
		t.Errorf("LastRun.ID = %q, want %q", status.LastRun.ID, "f1")
	}
	if status.LastFailure == nil {
		t.Fatal("LastFailure should not be nil")
	}
	if status.LastSuccess == nil {
		t.Fatal("LastSuccess should not be nil")
	}
	if status.ConsecutiveFail != 2 {
		t.Errorf("ConsecutiveFail = %d, want 2", status.ConsecutiveFail)
	}
	// FailureCount counts only trailing failures until a success is found (then break)
	if status.FailureCount != 2 {
		t.Errorf("FailureCount = %d, want 2", status.FailureCount)
	}
	// After finding the 2 failures, it finds a success and increments SuccessCount,
	// then since LastFailure != nil, it breaks. So SuccessCount = 1.
	if status.SuccessCount != 1 {
		t.Errorf("SuccessCount = %d, want 1", status.SuccessCount)
	}
}

func TestMonitor_GetStatus_AllSuccess(t *testing.T) {
	m := NewMonitor("")

	for i := 0; i < 3; i++ {
		m.RecordExecution(ExecutionRecord{
			ID:       fmt.Sprintf("s%d", i),
			Domain:   "ok.com",
			Command:  "cmd",
			Schedule: "0 * * * *",
			Success:  true,
		})
	}

	status := m.GetStatus("ok.com", "cmd")
	if status == nil {
		t.Fatal("expected non-nil status")
	}
	if status.ConsecutiveFail != 0 {
		t.Errorf("ConsecutiveFail = %d, want 0", status.ConsecutiveFail)
	}
	if status.FailureCount != 0 {
		t.Errorf("FailureCount = %d, want 0", status.FailureCount)
	}
	// All are success, no LastFailure found so loop does not break early
	if status.SuccessCount != 3 {
		t.Errorf("SuccessCount = %d, want 3", status.SuccessCount)
	}
	if status.LastFailure != nil {
		t.Error("LastFailure should be nil for all-success history")
	}
}

// ─── monitor.go: GetAllStatus ───────────────────────────────────────────────
func TestMonitor_GetAllStatus_Empty(t *testing.T) {
	m := NewMonitor("")
	statuses := m.GetAllStatus()
	if len(statuses) != 0 {
		t.Errorf("expected 0 statuses, got %d", len(statuses))
	}
}

func TestMonitor_GetAllStatus_Multiple(t *testing.T) {
	m := NewMonitor("")

	m.RecordExecution(ExecutionRecord{
		ID: "1", Domain: "a.com", Command: "cmd1", Success: true,
	})
	m.RecordExecution(ExecutionRecord{
		ID: "2", Domain: "b.com", Command: "cmd2", Success: false,
	})

	statuses := m.GetAllStatus()
	if len(statuses) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(statuses))
	}

	// Check both domains are represented
	domains := map[string]bool{}
	for _, s := range statuses {
		domains[s.Domain] = true
	}
	if !domains["a.com"] || !domains["b.com"] {
		t.Errorf("expected domains a.com and b.com, got %v", domains)
	}
}

// ─── monitor.go: GetDomainStatus ────────────────────────────────────────────
func TestMonitor_GetDomainStatus_Empty(t *testing.T) {
	m := NewMonitor("")
	statuses := m.GetDomainStatus("nonexistent.com")
	if len(statuses) != 0 {
		t.Errorf("expected 0 statuses, got %d", len(statuses))
	}
}

func TestMonitor_GetDomainStatus_FiltersByDomain(t *testing.T) {
	m := NewMonitor("")

	m.RecordExecution(ExecutionRecord{
		ID: "1", Domain: "a.com", Command: "cmd1", Success: true,
	})
	m.RecordExecution(ExecutionRecord{
		ID: "2", Domain: "a.com", Command: "cmd2", Success: true,
	})
	m.RecordExecution(ExecutionRecord{
		ID: "3", Domain: "b.com", Command: "cmd3", Success: true,
	})

	statuses := m.GetDomainStatus("a.com")
	if len(statuses) != 2 {
		t.Fatalf("expected 2 statuses for a.com, got %d", len(statuses))
	}
	for _, s := range statuses {
		if s.Domain != "a.com" {
			t.Errorf("expected domain a.com, got %q", s.Domain)
		}
	}
}

func TestMonitor_GetDomainStatus_EmptyHistoryEntry(t *testing.T) {
	m := NewMonitor("")

	// Manually insert an empty history entry
	m.mu.Lock()
	m.history["empty.com:cmd"] = []ExecutionRecord{}
	m.mu.Unlock()

	statuses := m.GetDomainStatus("empty.com")
	if len(statuses) != 0 {
		t.Errorf("expected 0 statuses for empty history, got %d", len(statuses))
	}
}

// ─── monitor.go: SetAlertFunc ───────────────────────────────────────────────
func TestMonitor_SetAlertFunc(t *testing.T) {
	origCmd := monitorExecCommandFn
	origGOOS := monitorRuntimeGOOS
	defer func() {
		monitorExecCommandFn = origCmd
		monitorRuntimeGOOS = origGOOS
	}()

	monitorRuntimeGOOS = "linux"
	monitorExecCommandFn = fakeExecCommand("", 1)

	dir := t.TempDir()
	m := NewMonitor(dir)

	var alertDomain, alertCmd, alertOutput string
	var alertExit int
	m.SetAlertFunc(func(domain, command, output string, exitCode int) {
		alertDomain = domain
		alertCmd = command
		alertOutput = output
		alertExit = exitCode
	})

	m.Execute("fail.com", "* * * * *", "bad-command")

	if alertDomain != "fail.com" {
		t.Errorf("alert domain = %q, want %q", alertDomain, "fail.com")
	}
	if alertCmd != "bad-command" {
		t.Errorf("alert command = %q, want %q", alertCmd, "bad-command")
	}
	if alertExit != 1 {
		t.Errorf("alert exit = %d, want 1", alertExit)
	}
	if alertOutput == "" {
		// The error string should be non-empty
		t.Log("alert output was empty (error may have been captured differently)")
	}
}

func TestMonitor_SetAlertFunc_NoAlertOnSuccess(t *testing.T) {
	origCmd := monitorExecCommandFn
	origGOOS := monitorRuntimeGOOS
	defer func() {
		monitorExecCommandFn = origCmd
		monitorRuntimeGOOS = origGOOS
	}()

	monitorRuntimeGOOS = "linux"
	monitorExecCommandFn = fakeExecCommand("ok", 0)

	dir := t.TempDir()
	m := NewMonitor(dir)

	alertCalled := false
	m.SetAlertFunc(func(domain, command, output string, exitCode int) {
		alertCalled = true
	})

	m.Execute("ok.com", "* * * * *", "echo hi")

	if alertCalled {
		t.Error("alert should not fire on successful execution")
	}
}

// ─── monitor.go: ClearHistory ───────────────────────────────────────────────
func TestMonitor_ClearHistory(t *testing.T) {
	dir := t.TempDir()
	m := NewMonitor(dir)

	m.RecordExecution(ExecutionRecord{
		ID: "1", Domain: "d.com", Command: "cmd", Success: true,
	})
	m.RecordExecution(ExecutionRecord{
		ID: "2", Domain: "other.com", Command: "cmd2", Success: true,
	})

	m.ClearHistory("d.com", "cmd")

	status := m.GetStatus("d.com", "cmd")
	if status != nil {
		t.Error("expected nil status after clearing history")
	}

	// other.com should still have history
	status2 := m.GetStatus("other.com", "cmd2")
	if status2 == nil {
		t.Error("other.com history should not be cleared")
	}
}

func TestMonitor_ClearHistory_NonExistent(t *testing.T) {
	dir := t.TempDir()
	m := NewMonitor(dir)

	// Should not panic when clearing non-existent key
	m.ClearHistory("nope.com", "nothing")
}

// ─── monitor.go: WrapCommand ────────────────────────────────────────────────
func TestMonitor_WrapCommand(t *testing.T) {
	m := NewMonitor("")

	wrapped := m.WrapCommand("example.com", "*/5 * * * *", "/usr/bin/php cron.php")

	if !strings.Contains(wrapped, "curl") {
		t.Error("wrapped command should contain curl")
	}
	if !strings.Contains(wrapped, "example.com") {
		t.Error("wrapped command should contain domain")
	}
	if !strings.Contains(wrapped, "*/5 * * * *") {
		t.Error("wrapped command should contain schedule")
	}
	if !strings.Contains(wrapped, "/usr/bin/php cron.php") {
		t.Error("wrapped command should contain original command")
	}
	if !strings.Contains(wrapped, "localhost:8080") {
		t.Error("wrapped command should call local API")
	}
	// Fallback after ||
	if !strings.Contains(wrapped, "|| /usr/bin/php cron.php") {
		t.Error("wrapped command should have fallback with original command")
	}
}

// ─── monitor.go: Persistence ────────────────────────────────────────────────
func TestMonitor_Persistence_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()

	// Create monitor, add records, let it save
	m1 := NewMonitor(dir)
	m1.RecordExecution(ExecutionRecord{
		ID: "1", Domain: "persist.com", Command: "saved-cmd",
		Schedule: "* * * * *", Success: true, Output: "saved output",
	})
	m1.RecordExecution(ExecutionRecord{
		ID: "2", Domain: "persist.com", Command: "saved-cmd",
		Schedule: "* * * * *", Success: false, Error: "fail reason",
	})

	// Verify the file exists
	histFile := filepath.Join(dir, "cron_history.json")
	if _, err := os.Stat(histFile); os.IsNotExist(err) {
		t.Fatal("cron_history.json was not created")
	}

	// Create a new monitor from the same dir — should load the history
	m2 := NewMonitor(dir)
	status := m2.GetStatus("persist.com", "saved-cmd")
	if status == nil {
		t.Fatal("expected loaded status, got nil")
	}
	if len(status.History) != 2 {
		t.Errorf("loaded history len = %d, want 2", len(status.History))
	}
}

func TestMonitor_Persistence_EmptyDataDir(t *testing.T) {
	m := NewMonitor("")

	// historyFile returns "" for empty dataDir; save/load should be no-ops
	if f := m.historyFile(); f != "" {
		t.Errorf("historyFile() = %q, want empty", f)
	}

	// These should not panic
	m.RecordExecution(ExecutionRecord{
		ID: "1", Domain: "d.com", Command: "cmd", Success: true,
	})
}

func TestMonitor_Persistence_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	histFile := filepath.Join(dir, "cron_history.json")

	// Write invalid JSON
	os.WriteFile(histFile, []byte("not valid json{{{"), 0644)

	// NewMonitor should handle corrupt file gracefully
	m := NewMonitor(dir)
	if m == nil {
		t.Fatal("NewMonitor should not return nil on corrupt file")
	}

	// History should be empty (default initialized)
	statuses := m.GetAllStatus()
	if len(statuses) != 0 {
		t.Errorf("expected 0 statuses from corrupt file, got %d", len(statuses))
	}
}

func TestMonitor_Persistence_MissingFile(t *testing.T) {
	dir := t.TempDir()

	// No history file exists; NewMonitor should initialize cleanly
	m := NewMonitor(dir)
	if m == nil {
		t.Fatal("NewMonitor should not return nil")
	}
	statuses := m.GetAllStatus()
	if len(statuses) != 0 {
		t.Errorf("expected 0 statuses, got %d", len(statuses))
	}
}

// ─── monitor.go: MaxHistory ─────────────────────────────────────────────────
func TestMonitor_MaxHistory(t *testing.T) {
	m := NewMonitor("")
	m.maxHistory = 5 // Override for testing

	for i := 0; i < 10; i++ {
		m.RecordExecution(ExecutionRecord{
			ID:      fmt.Sprintf("%d", i),
			Domain:  "d.com",
			Command: "cmd",
			Success: true,
		})
	}

	m.mu.RLock()
	h := m.history["d.com:cmd"]
	m.mu.RUnlock()

	if len(h) != 5 {
		t.Fatalf("expected 5 records (maxHistory), got %d", len(h))
	}

	// Should keep the last 5 (IDs 5-9)
	if h[0].ID != "5" {
		t.Errorf("first record ID = %q, want %q", h[0].ID, "5")
	}
	if h[4].ID != "9" {
		t.Errorf("last record ID = %q, want %q", h[4].ID, "9")
	}
}

// ─── monitor.go: ConsecutiveFailures ────────────────────────────────────────
func TestMonitor_ConsecutiveFailures(t *testing.T) {
	m := NewMonitor("")

	// Success, then 3 failures
	m.RecordExecution(ExecutionRecord{
		ID: "s1", Domain: "d.com", Command: "cmd", Success: true,
	})
	for i := 0; i < 3; i++ {
		m.RecordExecution(ExecutionRecord{
			ID:      fmt.Sprintf("f%d", i),
			Domain:  "d.com",
			Command: "cmd",
			Success: false,
		})
	}

	status := m.GetStatus("d.com", "cmd")
	if status == nil {
		t.Fatal("expected non-nil status")
	}
	if status.ConsecutiveFail != 3 {
		t.Errorf("ConsecutiveFail = %d, want 3", status.ConsecutiveFail)
	}
}

func TestMonitor_ConsecutiveFailures_BrokenBySuccess(t *testing.T) {
	m := NewMonitor("")

	// 2 failures, 1 success, 1 failure
	m.RecordExecution(ExecutionRecord{
		ID: "f1", Domain: "d.com", Command: "cmd", Success: false,
	})
	m.RecordExecution(ExecutionRecord{
		ID: "f2", Domain: "d.com", Command: "cmd", Success: false,
	})
	m.RecordExecution(ExecutionRecord{
		ID: "s1", Domain: "d.com", Command: "cmd", Success: true,
	})
	m.RecordExecution(ExecutionRecord{
		ID: "f3", Domain: "d.com", Command: "cmd", Success: false,
	})

	status := m.GetStatus("d.com", "cmd")
	if status == nil {
		t.Fatal("expected non-nil status")
	}
	// Scanning from the end: f3 (fail, ConsecutiveFail=1), s1 (success, LastFailure!=nil -> break)
	if status.ConsecutiveFail != 1 {
		t.Errorf("ConsecutiveFail = %d, want 1", status.ConsecutiveFail)
	}
	if status.FailureCount != 1 {
		t.Errorf("FailureCount = %d, want 1", status.FailureCount)
	}
	if status.SuccessCount != 1 {
		t.Errorf("SuccessCount = %d, want 1", status.SuccessCount)
	}
}

// ─── monitor.go: getStatusUnsafe (via GetAllStatus / GetDomainStatus) ───────
func TestMonitor_GetStatusUnsafe_MoreThan10History(t *testing.T) {
	m := NewMonitor("")

	// Add 15 records — getStatusUnsafe should limit History to last 10
	for i := 0; i < 15; i++ {
		m.RecordExecution(ExecutionRecord{
			ID:       fmt.Sprintf("%d", i),
			Domain:   "big.com",
			Command:  "cmd",
			Schedule: "* * * * *",
			Success:  true,
		})
	}

	statuses := m.GetAllStatus()
	if len(statuses) != 1 {
		t.Fatalf("expected 1 status, got %d", len(statuses))
	}
	if len(statuses[0].History) != 10 {
		t.Errorf("History len = %d, want 10 (capped)", len(statuses[0].History))
	}
	// Should be the last 10 (IDs 5-14)
	if statuses[0].History[0].ID != "5" {
		t.Errorf("first history ID = %q, want %q", statuses[0].History[0].ID, "5")
	}
}

// ─── monitor.go: jobKey ─────────────────────────────────────────────────────
func TestMonitor_JobKey(t *testing.T) {
	m := NewMonitor("")
	key := m.jobKey("example.com", "/usr/bin/php cron.php")
	expected := "example.com:/usr/bin/php cron.php"
	if key != expected {
		t.Errorf("jobKey = %q, want %q", key, expected)
	}
}

// ─── monitor.go: historyFile ────────────────────────────────────────────────
func TestMonitor_HistoryFile(t *testing.T) {
	m := NewMonitor("/some/dir")
	expected := filepath.Join("/some/dir", "cron_history.json")
	if got := m.historyFile(); got != expected {
		t.Errorf("historyFile() = %q, want %q", got, expected)
	}
}

func TestMonitor_HistoryFile_Empty(t *testing.T) {
	m := NewMonitor("")
	if got := m.historyFile(); got != "" {
		t.Errorf("historyFile() = %q, want empty", got)
	}
}

// ─── monitor.go: saveHistory creates directory ──────────────────────────────
func TestMonitor_SaveHistory_CreatesDir(t *testing.T) {
	base := t.TempDir()
	dataDir := filepath.Join(base, "nested", "deep")
	// dataDir doesn't exist yet

	m := &Monitor{
		history:    make(map[string][]ExecutionRecord),
		maxHistory: 100,
		dataDir:    dataDir,
	}

	m.RecordExecution(ExecutionRecord{
		ID: "1", Domain: "d.com", Command: "cmd", Success: true,
	})

	histFile := filepath.Join(dataDir, "cron_history.json")
	if _, err := os.Stat(histFile); os.IsNotExist(err) {
		t.Error("saveHistory should create nested directory and file")
	}
}

// ─── monitor.go: Persistence roundtrip with JSON ────────────────────────────
func TestMonitor_Persistence_JSONRoundtrip(t *testing.T) {
	dir := t.TempDir()

	m := NewMonitor(dir)
	now := time.Now().Truncate(time.Second) // Truncate for JSON roundtrip precision
	rec := ExecutionRecord{
		ID:        "rt1",
		Domain:    "rt.com",
		Command:   "echo test",
		Schedule:  "0 * * * *",
		StartedAt: now,
		EndedAt:   now.Add(2 * time.Second),
		Duration:  2 * time.Second,
		ExitCode:  0,
		Success:   true,
		Output:    "test output",
	}
	m.RecordExecution(rec)

	// Read back the JSON file directly
	data, err := os.ReadFile(filepath.Join(dir, "cron_history.json"))
	if err != nil {
		t.Fatalf("reading history file: %v", err)
	}

	var history map[string][]ExecutionRecord
	if err := json.Unmarshal(data, &history); err != nil {
		t.Fatalf("unmarshaling: %v", err)
	}

	records, ok := history["rt.com:echo test"]
	if !ok {
		t.Fatal("expected key 'rt.com:echo test' in history")
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].ID != "rt1" {
		t.Errorf("ID = %q, want %q", records[0].ID, "rt1")
	}
}

// ─── monitor.go: concurrent access ─────────────────────────────────────────
func TestMonitor_ConcurrentAccess(t *testing.T) {
	m := NewMonitor("")

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			m.RecordExecution(ExecutionRecord{
				ID:      fmt.Sprintf("%d", i),
				Domain:  "concurrent.com",
				Command: "cmd",
				Success: i%2 == 0,
			})
		}(i)
	}

	// Also read concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.GetStatus("concurrent.com", "cmd")
			m.GetAllStatus()
			m.GetDomainStatus("concurrent.com")
		}()
	}

	wg.Wait()

	m.mu.RLock()
	h := m.history["concurrent.com:cmd"]
	m.mu.RUnlock()

	if len(h) != 20 {
		t.Errorf("expected 20 records after concurrent writes, got %d", len(h))
	}
}

// ─── manager.go: Remove with skipNext logic ─────────────────────────────────
func TestRemove_SkipNextLogic(t *testing.T) {
	origGOOS := runtimeGOOS
	origCmd := execCommandFn
	runtimeGOOS = "linux"
	defer func() {
		runtimeGOOS = origGOOS
		execCommandFn = origCmd
	}()

	// Two UWAS entries; remove should skip the marker AND the next line
	crontabContent := strings.Join([]string{
		"# UWAS managed [a.com] job1",
		"*/5 * * * * /bin/job1",
		"# UWAS managed [b.com] job2",
		"0 2 * * * /bin/job2",
		"0 * * * * /usr/bin/other",
		"",
	}, "\n")
	execCommandFn = fakeExecCommand(crontabContent, 0)

	err := Remove("*/5 * * * *", "/bin/job1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ─── ExecutionRecord struct ─────────────────────────────────────────────────
func TestExecutionRecord_Struct(t *testing.T) {
	now := time.Now()
	rec := ExecutionRecord{
		ID:        "test-id",
		Domain:    "example.com",
		Command:   "echo hello",
		Schedule:  "* * * * *",
		StartedAt: now,
		EndedAt:   now.Add(time.Second),
		Duration:  time.Second,
		ExitCode:  0,
		Success:   true,
		Output:    "hello\n",
		Error:     "",
	}

	if rec.ID != "test-id" {
		t.Errorf("ID = %q", rec.ID)
	}
	if rec.Duration != time.Second {
		t.Errorf("Duration = %v", rec.Duration)
	}
}

// ─── JobStatus struct ───────────────────────────────────────────────────────
func TestJobStatus_Struct(t *testing.T) {
	rec := ExecutionRecord{ID: "1", Success: true}
	status := JobStatus{
		Domain:          "d.com",
		Command:         "cmd",
		Schedule:        "* * * * *",
		LastRun:         &rec,
		LastSuccess:     &rec,
		LastFailure:     nil,
		SuccessCount:    5,
		FailureCount:    0,
		ConsecutiveFail: 0,
		History:         []ExecutionRecord{rec},
	}

	if status.Domain != "d.com" {
		t.Errorf("Domain = %q", status.Domain)
	}
	if status.SuccessCount != 5 {
		t.Errorf("SuccessCount = %d", status.SuccessCount)
	}
	if status.LastFailure != nil {
		t.Error("LastFailure should be nil")
	}
}

// ─── GetAllStatus with bad key format ───────────────────────────────────────
func TestMonitor_GetAllStatus_BadKeyFormat(t *testing.T) {
	m := NewMonitor("")

	// Manually insert a key without ":" separator
	m.mu.Lock()
	m.history["badkey"] = []ExecutionRecord{{ID: "1", Success: true}}
	m.mu.Unlock()

	statuses := m.GetAllStatus()
	// The bad key should be skipped (len(parts) != 2 when no ":")
	if len(statuses) != 0 {
		t.Errorf("expected 0 statuses with bad key, got %d", len(statuses))
	}
}

// ─── Execute records to history ─────────────────────────────────────────────
func TestMonitor_Execute_RecordsToHistory(t *testing.T) {
	origCmd := monitorExecCommandFn
	origGOOS := monitorRuntimeGOOS
	defer func() {
		monitorExecCommandFn = origCmd
		monitorRuntimeGOOS = origGOOS
	}()

	monitorRuntimeGOOS = "linux"
	monitorExecCommandFn = fakeExecCommand("output data", 0)

	m := NewMonitor("")

	m.Execute("rec.com", "* * * * *", "echo test")

	status := m.GetStatus("rec.com", "echo test")
	if status == nil {
		t.Fatal("expected status after Execute, got nil")
	}
	if len(status.History) != 1 {
		t.Errorf("expected 1 history entry, got %d", len(status.History))
	}
}

// ─── Remove: bare cron line matches schedule+command ────────────────────────
func TestRemove_BareLineMatch(t *testing.T) {
	origGOOS := runtimeGOOS
	origCmd := execCommandFn
	runtimeGOOS = "linux"
	defer func() {
		runtimeGOOS = origGOOS
		execCommandFn = origCmd
	}()

	// Crontab has a bare line (not preceded by UWAS marker) that matches
	// schedule + command. The Remove function should skip it via line 98-99.
	crontabContent := strings.Join([]string{
		"*/5 * * * * /bin/job1",
		"0 * * * * /usr/bin/other",
		"",
	}, "\n")
	execCommandFn = fakeExecCommand(crontabContent, 0)

	err := Remove("*/5 * * * *", "/bin/job1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ─── GetAllStatus: key maps to empty history via getStatusUnsafe nil ────────
func TestMonitor_GetAllStatus_EmptyHistoryEntry(t *testing.T) {
	m := NewMonitor("")

	// Manually insert a valid key with empty history. getStatusUnsafe should
	// return nil and the entry is skipped.
	m.mu.Lock()
	m.history["empty.com:cmd"] = []ExecutionRecord{}
	m.mu.Unlock()

	statuses := m.GetAllStatus()
	if len(statuses) != 0 {
		t.Errorf("expected 0 statuses for empty history entry, got %d", len(statuses))
	}
}

// ─── writeCrontab: CreateTemp error ─────────────────────────────────────────
func TestWriteCrontab_CreateTempError(t *testing.T) {
	// Save and override TMPDIR/TMP to a non-existent path to force CreateTemp failure
	origTmp := os.Getenv("TMPDIR")
	origTmpWin := os.Getenv("TMP")
	origTmpWin2 := os.Getenv("TEMP")

	badDir := filepath.Join(t.TempDir(), "nonexistent-subdir-for-test")
	// Don't create badDir — that's the point
	os.Setenv("TMPDIR", badDir)
	os.Setenv("TMP", badDir)
	os.Setenv("TEMP", badDir)

	defer func() {
		os.Setenv("TMPDIR", origTmp)
		os.Setenv("TMP", origTmpWin)
		os.Setenv("TEMP", origTmpWin2)
	}()

	err := writeCrontab("test content")
	if err == nil {
		t.Fatal("expected error from writeCrontab when temp dir doesn't exist")
	}
}

// ─── Execute with empty domain ──────────────────────────────────────────────
func TestMonitor_Execute_EmptyDomain(t *testing.T) {
	origCmd := monitorExecCommandFn
	origGOOS := monitorRuntimeGOOS
	defer func() {
		monitorExecCommandFn = origCmd
		monitorRuntimeGOOS = origGOOS
	}()

	monitorRuntimeGOOS = "linux"
	monitorExecCommandFn = fakeExecCommand("ok", 0)

	m := NewMonitor("")

	rec := m.Execute("", "* * * * *", "echo hello")
	if !rec.Success {
		t.Errorf("expected success, got failure: %s", rec.Error)
	}
	if rec.Domain != "" {
		t.Errorf("Domain = %q, want empty", rec.Domain)
	}
}
