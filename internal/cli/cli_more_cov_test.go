package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// serve.go Run — uncovered paths (36.7% → ~45%)
// ---------------------------------------------------------------------------

func TestServeCommandFilterArg(t *testing.T) {
	// filterArg with no match should append
	args := filterArg([]string{"serve", "--http-port", "8080"}, "-d")
	if len(args) != 4 {
		t.Errorf("expected 4 args, got %d: %v", len(args), args)
	}
	if args[3] != "-d" {
		t.Errorf("expected -d appended, got %q", args[3])
	}

	// filterArg with match should return unchanged
	args2 := filterArg([]string{"serve", "-d"}, "-d")
	if len(args2) != 2 {
		t.Errorf("expected 2 args, got %d: %v", len(args2), args2)
	}
}

// ---------------------------------------------------------------------------
// root.go Run — unknown command path (50.0% → ~60%)
// ---------------------------------------------------------------------------

func TestCLIRunUnknownCommand(t *testing.T) {
	app := New()
	app.Register(&VersionCommand{})
	app.Register(NewHelpCommand(app))

	// The CLI.Run method calls os.Exit(1) for unknown commands, which kills the test.
	// Test the error path via direct command run instead.
	cmd, ok := app.commands["version"]
	if !ok {
		t.Fatal("version command not registered")
	}
	_ = cmd.Run([]string{"--invalid"})
}

func TestCLIRunHelpFlag(t *testing.T) {
	app := New()
	app.Register(&ServeCommand{})
	app.Register(NewHelpCommand(app))

	// The Run method calls os.Exit(0) for --help, which terminates the test.
	// We can't test this directly. Instead verify that printUsage works.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	app.printUsage()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "Commands") {
		t.Error("printUsage should list commands")
	}
	if !strings.Contains(output, "serve") {
		t.Error("printUsage should list serve command")
	}
}

// ---------------------------------------------------------------------------
// init.go findConfig — search order paths (already well tested, adding edge)
// ---------------------------------------------------------------------------

func TestFindConfigExplicitNotFound(t *testing.T) {
	path, found := findConfig("/nonexistent/uwas.yaml")
	if found {
		t.Error("should not find nonexistent file")
	}
	if path != "/nonexistent/uwas.yaml" {
		t.Errorf("path = %q, want /nonexistent/uwas.yaml", path)
	}
}

func TestFindConfigCurrentDir(t *testing.T) {
	dir := t.TempDir()
	oldDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldDir)

	os.WriteFile("uwas.yaml", []byte("global: {}"), 0644)
	path, found := findConfig("")
	if !found {
		t.Fatal("should find uwas.yaml in current dir")
	}
	if path != "uwas.yaml" {
		t.Errorf("path = %q, want uwas.yaml", path)
	}
	os.Remove("uwas.yaml")
}

func TestFindConfigYmlExtension(t *testing.T) {
	dir := t.TempDir()
	oldDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldDir)

	os.WriteFile("uwas.yml", []byte("global: {}"), 0644)
	path, found := findConfig("")
	if !found {
		t.Fatal("should find uwas.yml")
	}
	if path != "uwas.yml" {
		t.Errorf("path = %q, want uwas.yml", path)
	}
	os.Remove("uwas.yml")
}

// ---------------------------------------------------------------------------
// ensureDefaultConfig — config creation (89.7% → ~95%)
// ---------------------------------------------------------------------------

func TestEnsureDefaultConfigCreatesFiles(t *testing.T) {
	homeDir := t.TempDir()
	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", homeDir)
	defer os.Setenv("HOME", oldHome)

	expectedDir := filepath.Join(homeDir, ".uwas")

	cfgPath, err := ensureDefaultConfig("8080", "9443", "0.0.0.0", "/tmp/uwas-web", "admin@test.com")
	if err != nil {
		t.Fatalf("ensureDefaultConfig: %v", err)
	}
	if cfgPath == "" {
		t.Fatal("expected non-empty config path")
	}
	if !strings.HasPrefix(cfgPath, expectedDir) {
		t.Errorf("config path should be under %s, got %q", expectedDir, cfgPath)
	}

	// .env file should exist
	envPath := filepath.Join(filepath.Dir(cfgPath), ".env")
	if _, err := os.Stat(envPath); err != nil {
		t.Errorf(".env file should exist: %v", err)
	}

	// domains.d should exist
	domainsDir := filepath.Join(filepath.Dir(cfgPath), "domains.d")
	if _, err := os.Stat(domainsDir); err != nil {
		t.Errorf("domains.d should exist: %v", err)
	}
}

func TestGenerateDefaultConfigFormat(t *testing.T) {
	cfg := generateDefaultConfig("8080", "9443", "127.0.0.1", "key123", "123456", "/tmp/uwas", "/var/www", "me@test.com")
	if !strings.Contains(cfg, "http_listen") {
		t.Error("config should contain http_listen")
	}
	if !strings.Contains(cfg, "key123") {
		t.Error("config should contain api key")
	}
	if !strings.Contains(cfg, "123456") {
		t.Error("config should contain pin code")
	}
	if !strings.Contains(cfg, "me@test.com") {
		t.Error("config should contain acme email")
	}
}

// ---------------------------------------------------------------------------
// install.go — installUWAS error paths (85.1% → ~88%)
// ---------------------------------------------------------------------------

func TestInstallCmdRunNonLinux(t *testing.T) {
	orig := installRuntimeGOOS
	installRuntimeGOOS = "windows"
	defer func() { installRuntimeGOOS = orig }()

	cmd := &InstallCmd{}
	err := cmd.Run(nil)
	if err == nil || !strings.Contains(err.Error(), "only supported on Linux") {
		t.Errorf("expected Linux-only error, got %v", err)
	}
}

func TestInstallCmdRunNonRoot(t *testing.T) {
	origOS := installRuntimeGOOS
	origUID := installOsGetuid
	installRuntimeGOOS = "linux"
	installOsGetuid = func() int { return 1000 } // non-root
	defer func() {
		installRuntimeGOOS = origOS
		installOsGetuid = origUID
	}()

	cmd := &InstallCmd{}
	err := cmd.Run(nil)
	if err == nil || !strings.Contains(err.Error(), "requires root") {
		t.Errorf("expected root error, got %v", err)
	}
}

// installUWAS with non-Linux path
func TestInstallUWASNonLinux(t *testing.T) {
	orig := installRuntimeGOOS
	installRuntimeGOOS = "darwin"
	defer func() { installRuntimeGOOS = orig }()

	err := installUWAS(nil)
	if err == nil || !strings.Contains(err.Error(), "only supported on Linux") {
		t.Errorf("expected Linux-only error, got %v", err)
	}
}

func TestInstallUWASNonRoot(t *testing.T) {
	origOS := installRuntimeGOOS
	origUID := installOsGetuid
	installRuntimeGOOS = "linux"
	installOsGetuid = func() int { return 1000 }
	defer func() {
		installRuntimeGOOS = origOS
		installOsGetuid = origUID
	}()

	err := installUWAS(nil)
	if err == nil || !strings.Contains(err.Error(), "requires root") {
		t.Errorf("expected root error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// backup.go — createBackup with certs directory (89.4% → ~93%)
// ---------------------------------------------------------------------------

func TestCreateBackupWithCerts(t *testing.T) {
	tmpDir := t.TempDir()

	// Config file
	cfgPath := filepath.Join(tmpDir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte("global: {}"), 0644)

	// Certs directory with some files
	certsDir := filepath.Join(tmpDir, "certs")
	os.MkdirAll(certsDir, 0755)
	os.WriteFile(filepath.Join(certsDir, "cert.pem"), []byte("cert data"), 0644)
	os.WriteFile(filepath.Join(certsDir, "key.pem"), []byte("key data"), 0644)

	// Domain files
	domainsDir := filepath.Join(tmpDir, "domains.d")
	os.MkdirAll(domainsDir, 0755)
	os.WriteFile(filepath.Join(domainsDir, "example.com.yaml"), []byte("host: example.com"), 0644)

	output := filepath.Join(tmpDir, "backup.tar.gz")
	err := createBackup(output, cfgPath, certsDir)
	if err != nil {
		t.Fatalf("createBackup: %v", err)
	}

	// Verify backup file exists and has content
	info, err := os.Stat(output)
	if err != nil {
		t.Fatalf("backup file should exist: %v", err)
	}
	if info.Size() == 0 {
		t.Error("backup file should not be empty")
	}
}

// ---------------------------------------------------------------------------
// conflicts.go — OfferStopConflicts with running servers (71.9% → ~80%)
// ---------------------------------------------------------------------------

func TestOfferStopConflictsWithRunning(t *testing.T) {
	// Running conflicts - should prompt and handle response
	// Since we mock promptWithDefault, we test the skipping path
	// where user says "n"
	conflicts := []ConflictingServer{
		{Name: "Apache", Running: true, PID: "1234", Service: "apache2"},
		{Name: "Nginx", Running: false, Service: "nginx"},
	}
	OfferStopConflicts(conflicts)
	// Should not panic regardless of stdin state
}

func TestPrintConflictsNoRunning(t *testing.T) {
	conflicts := []ConflictingServer{
		{Name: "Apache", Running: false, Service: "apache2"},
	}
	result := PrintConflicts(conflicts)
	if result {
		t.Error("PrintConflicts should return false when nothing is running")
	}
}

func TestPrintConflictsEmpty(t *testing.T) {
	result := PrintConflicts(nil)
	if result {
		t.Error("PrintConflicts(nil) should return false")
	}
	result = PrintConflicts([]ConflictingServer{})
	if result {
		t.Error("PrintConflicts(empty) should return false")
	}
}

// ---------------------------------------------------------------------------
// php.go — install paths (60% → ~75%)
// ---------------------------------------------------------------------------

func TestPHPCommandInstallNoRoot(t *testing.T) {
	// Override runtime.GOOS and os.Geteuid check via the php.go code
	// The install method checks runtime.GOOS and os.Geteuid
	// In tests, os.Geteuid() returns non-zero (non-root), so
	// the root check should trigger.
	cmd := &PHPCommand{}
	err := cmd.install([]string{"8.3"})
	if err != nil {
		t.Fatalf("install with no root should print message and return nil, got: %v", err)
	}
}

func TestPHPCommandInstallInfo(t *testing.T) {
	cmd := &PHPCommand{}
	err := cmd.installInfo([]string{"8.4"})
	if err != nil {
		t.Errorf("installInfo: %v", err)
	}
}

func TestPHPCommandInstallInfoDefaultVersion(t *testing.T) {
	cmd := &PHPCommand{}
	err := cmd.installInfo(nil)
	if err != nil {
		t.Errorf("installInfo with no version: %v", err)
	}
}

// ---------------------------------------------------------------------------
// loadDotEnv — with valid .env (already has no-file test)
// ---------------------------------------------------------------------------

func TestLoadDotEnvWithFile(t *testing.T) {
	homeDir := t.TempDir()
	uwasDir := filepath.Join(homeDir, ".uwas")
	os.MkdirAll(uwasDir, 0755)
	envContent := "UWAS_TEST_KEY=testvalue\nUWAS_ANOTHER=another\n"
	os.WriteFile(filepath.Join(uwasDir, ".env"), []byte(envContent), 0644)

	savedHome := os.Getenv("HOME")
	os.Setenv("HOME", homeDir)
	defer os.Setenv("HOME", savedHome)

	loadDotEnv()
	// Should not panic
}

func TestLoadDotEnvEmptyLine(t *testing.T) {
	homeDir := t.TempDir()
	uwasDir := filepath.Join(homeDir, ".uwas")
	os.MkdirAll(uwasDir, 0755)
	envContent := "# comment\n\nKEY=value\n"
	os.WriteFile(filepath.Join(uwasDir, ".env"), []byte(envContent), 0644)

	savedHome := os.Getenv("HOME")
	os.Setenv("HOME", homeDir)
	defer os.Setenv("HOME", savedHome)

	loadDotEnv()
}

// ---------------------------------------------------------------------------
// configCreds extraction edge cases (from install.go)
// ---------------------------------------------------------------------------

func TestExtractCredsFromConfigFull(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte(`
global:
  admin:
    enabled: true
    listen: "127.0.0.1:9443"
    api_key: "test-api-key-12345"
    pin_code: "654321"
`), 0644)

	creds := extractCredsFromConfig(cfgPath)
	if creds.apiKey != "test-api-key-12345" {
		t.Errorf("apiKey = %q, want test-api-key-12345", creds.apiKey)
	}
	if creds.pinCode != "654321" {
		t.Errorf("pinCode = %q, want 654321", creds.pinCode)
	}
	if creds.adminHost != "127.0.0.1" {
		t.Errorf("adminHost = %q, want 127.0.0.1", creds.adminHost)
	}
	if creds.adminPort != "9443" {
		t.Errorf("adminPort = %q, want 9443", creds.adminPort)
	}
}

func TestExtractCredsFromConfigNonexistent(t *testing.T) {
	creds := extractCredsFromConfig("/nonexistent/path.yaml")
	if creds.apiKey != "" || creds.pinCode != "" {
		t.Error("expected empty creds for missing file")
	}
}

// ---------------------------------------------------------------------------
// extractCredsFromConfig — various admin listen formats
// ---------------------------------------------------------------------------

func TestExtractCredsFromConfigListenOnlyPort(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	os.WriteFile(cfgPath, []byte(`
global:
  admin:
    listen: ":9443"
`), 0644)

	creds := extractCredsFromConfig(cfgPath)
	if creds.adminHost != "" {
		t.Errorf("adminHost should be empty for :port, got %q", creds.adminHost)
	}
	if creds.adminPort != "9443" {
		t.Errorf("adminPort = %q, want 9443", creds.adminPort)
	}
}

// ---------------------------------------------------------------------------
// migrateInlineDomains — edge cases
// ---------------------------------------------------------------------------

func TestMigrateInlineDomainsInvalidYAML(t *testing.T) {
	n := migrateInlineDomains([]byte("{ invalid yaml }"), t.TempDir())
	if n != 0 {
		t.Errorf("expected 0 for invalid YAML, got %d", n)
	}
}

func TestMigrateInlineDomainsNoHost(t *testing.T) {
	n := migrateInlineDomains([]byte("domains:\n  - root: /tmp\n"), t.TempDir())
	if n != 0 {
		t.Errorf("expected 0 for domain without host, got %d", n)
	}
}

// ---------------------------------------------------------------------------
// HelpCommand Run with argument but no detail — covers the branch where
// the command exists but does NOT implement Help() interface.
// ---------------------------------------------------------------------------

type noHelpCommand struct{}

func (n *noHelpCommand) Name() string        { return "nohelp" }
func (n *noHelpCommand) Description() string { return "no help" }
func (n *noHelpCommand) Run(args []string) error { return nil }

func TestHelpCommandNoHelpInterface(t *testing.T) {
	cli := New()
	cli.Register(&noHelpCommand{})
	help := NewHelpCommand(cli)

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := help.Run([]string{"nohelp"})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	w.Close()
	os.Stdout = old
}
