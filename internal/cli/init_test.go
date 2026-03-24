package cli

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestFindConfigExplicitExists(t *testing.T) {
	// Create a temp config file.
	tmpFile, err := os.CreateTemp("", "uwas-find-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString("test: true\n")
	tmpFile.Close()

	path, found := findConfig(tmpFile.Name())
	if !found {
		t.Error("expected found=true for existing explicit path")
	}
	if path != tmpFile.Name() {
		t.Errorf("path = %q, want %q", path, tmpFile.Name())
	}
}

func TestFindConfigExplicitNotExists(t *testing.T) {
	path, found := findConfig("/nonexistent/path/uwas-does-not-exist.yaml")
	if found {
		t.Error("expected found=false for nonexistent explicit path")
	}
	if path != "/nonexistent/path/uwas-does-not-exist.yaml" {
		t.Errorf("path = %q, expected the explicit path back", path)
	}
}

func TestFindConfigSearchOrder(t *testing.T) {
	// Test with empty explicit path.
	// Save current dir and cd to a temp dir.
	origDir, _ := os.Getwd()
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	// Create uwas.yaml in current dir.
	os.WriteFile(filepath.Join(tmpDir, "uwas.yaml"), []byte("test"), 0644)

	path, found := findConfig("")
	if !found {
		t.Error("expected found=true for uwas.yaml in current dir")
	}
	if path != "uwas.yaml" {
		t.Errorf("path = %q, want uwas.yaml", path)
	}
}

func TestFindConfigYml(t *testing.T) {
	origDir, _ := os.Getwd()
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	// Create uwas.yml (not yaml).
	os.WriteFile(filepath.Join(tmpDir, "uwas.yml"), []byte("test"), 0644)

	path, found := findConfig("")
	if !found {
		t.Error("expected found=true for uwas.yml in current dir")
	}
	if path != "uwas.yml" {
		t.Errorf("path = %q, want uwas.yml", path)
	}
}

func TestFindConfigSubdir(t *testing.T) {
	origDir, _ := os.Getwd()
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	// Create .uwas/uwas.yaml in current dir.
	os.MkdirAll(filepath.Join(tmpDir, ".uwas"), 0755)
	os.WriteFile(filepath.Join(tmpDir, ".uwas", "uwas.yaml"), []byte("test"), 0644)

	path, found := findConfig("")
	if !found {
		t.Error("expected found=true for .uwas/uwas.yaml")
	}
	if !strings.Contains(path, ".uwas") {
		t.Errorf("path = %q, expected to contain .uwas", path)
	}
}

func TestFindConfigNoneFound(t *testing.T) {
	origDir, _ := os.Getwd()
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	// Override HOME so that ~/.uwas/uwas.yaml doesn't match.
	origHome := os.Getenv("HOME")
	origUserProfile := os.Getenv("USERPROFILE")
	os.Setenv("HOME", tmpDir)
	os.Setenv("USERPROFILE", tmpDir)
	defer func() {
		os.Setenv("HOME", origHome)
		os.Setenv("USERPROFILE", origUserProfile)
	}()

	path, found := findConfig("")
	if found {
		t.Error("expected found=false when no config exists")
	}
	// Should return default path.
	if path == "" {
		t.Error("path should not be empty")
	}
}

func TestFindConfigExplicitUwasYaml(t *testing.T) {
	// Special case: explicit == "uwas.yaml" triggers search behavior.
	origDir, _ := os.Getwd()
	tmpDir := t.TempDir()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	// Don't create any config file.
	path, found := findConfig("uwas.yaml")
	if found {
		// Only found if uwas.yaml actually exists in current dir.
		t.Log("uwas.yaml found -- may exist in working dir")
	}
	_ = path
}

func TestEnsureDefaultConfig(t *testing.T) {
	// Override home dir by using a temp dir approach.
	// Since ensureDefaultConfig uses uwasDir() which calls os.UserHomeDir(),
	// we'll just test that the function doesn't panic and creates expected
	// structure by examining the output.

	// Save and restore environment.
	origHome := os.Getenv("HOME")
	origUserProfile := os.Getenv("USERPROFILE")
	tmpDir := t.TempDir()

	os.Setenv("HOME", tmpDir)
	os.Setenv("USERPROFILE", tmpDir)
	defer func() {
		os.Setenv("HOME", origHome)
		os.Setenv("USERPROFILE", origUserProfile)
	}()

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	webRoot := filepath.Join(tmpDir, "www")
	cfgPath, err := ensureDefaultConfig("8080", "9443", "0.0.0.0", webRoot, "")

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("ensureDefaultConfig error: %v", err)
	}

	if cfgPath == "" {
		t.Fatal("config path should not be empty")
	}

	// Verify the config file was created.
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Error("config file should exist")
	}

	// Verify subdirectories were created.
	uwas := filepath.Join(tmpDir, ".uwas")
	for _, sub := range []string{"domains.d", "certs", "cache", "logs", "backups"} {
		subDir := filepath.Join(uwas, sub)
		if info, err := os.Stat(subDir); err != nil || !info.IsDir() {
			t.Errorf("expected directory %s to exist", subDir)
		}
	}

	// Verify web root and index.html were created.
	if info, err := os.Stat(webRoot); err != nil || !info.IsDir() {
		t.Errorf("expected web root %s to exist", webRoot)
	}
	indexPath := filepath.Join(webRoot, "index.html")
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		t.Error("index.html should exist")
	}

	// Verify .env file was created.
	envPath := filepath.Join(uwas, ".env")
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		t.Error(".env file should exist")
	}
}

func TestEnsureDefaultConfigIdempotent(t *testing.T) {
	origHome := os.Getenv("HOME")
	origUserProfile := os.Getenv("USERPROFILE")
	tmpDir := t.TempDir()

	os.Setenv("HOME", tmpDir)
	os.Setenv("USERPROFILE", tmpDir)
	defer func() {
		os.Setenv("HOME", origHome)
		os.Setenv("USERPROFILE", origUserProfile)
	}()

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	// First call creates everything.
	path1, err := ensureDefaultConfig("8080", "9443", "0.0.0.0", "/var/www", "")

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("first call error: %v", err)
	}

	// Read the config content.
	content1, err := os.ReadFile(path1)
	if err != nil {
		t.Fatal(err)
	}

	old = os.Stdout
	_, w, _ = os.Pipe()
	os.Stdout = w

	// Second call should return existing path without overwriting.
	path2, err := ensureDefaultConfig("9999", "1111", "0.0.0.0", "/var/www", "")

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("second call error: %v", err)
	}

	if path1 != path2 {
		t.Errorf("paths should match: %q != %q", path1, path2)
	}

	// Content should not have changed (not overwritten).
	content2, err := os.ReadFile(path2)
	if err != nil {
		t.Fatal(err)
	}
	if string(content1) != string(content2) {
		t.Error("config content should not change on second call")
	}
}

func TestGenerateAPIKey(t *testing.T) {
	key := generateAPIKey()

	if len(key) != 32 {
		t.Errorf("key length = %d, want 32", len(key))
	}

	// Should be valid hex.
	_, err := hex.DecodeString(key)
	if err != nil {
		t.Errorf("key should be valid hex: %v", err)
	}

	// Two calls should produce different keys.
	key2 := generateAPIKey()
	if key == key2 {
		t.Error("two keys should be different")
	}
}

func TestGenerateDefaultConfigValidYAML(t *testing.T) {
	content := generateDefaultConfig("8080", "9443", "0.0.0.0", "abcdef1234567890abcdef1234567890", "/tmp/uwas", "/var/www", "test@example.com")

	// Should be parseable as YAML.
	var parsed map[string]any
	err := yaml.Unmarshal([]byte(content), &parsed)
	if err != nil {
		t.Fatalf("generated config is not valid YAML: %v", err)
	}

	// Check key fields exist.
	global, ok := parsed["global"].(map[string]any)
	if !ok {
		t.Fatal("expected global key")
	}
	if global["http_listen"] != ":8080" {
		t.Errorf("http_listen = %v", global["http_listen"])
	}

	domains, ok := parsed["domains"].([]any)
	if !ok {
		t.Fatal("expected domains key")
	}
	if len(domains) == 0 {
		t.Error("expected at least one domain")
	}
}

func TestGenerateDefaultConfigContainsAPIKey(t *testing.T) {
	apiKey := "deadbeef12345678deadbeef12345678"
	content := generateDefaultConfig("80", "9443", "0.0.0.0", apiKey, "/tmp/uwas", "/var/www", "")

	if !strings.Contains(content, apiKey) {
		t.Error("generated config should contain the API key")
	}
}

func TestGenerateDefaultConfigContainsPorts(t *testing.T) {
	content := generateDefaultConfig("8888", "7777", "0.0.0.0", "key", "/tmp/uwas", "/var/www", "")

	if !strings.Contains(content, ":8888") {
		t.Error("should contain HTTP port")
	}
	if !strings.Contains(content, "7777") {
		t.Error("should contain admin port")
	}
}

func TestUwasDir(t *testing.T) {
	dir := uwasDir()
	if dir == "" {
		t.Error("uwasDir should not be empty")
	}
	if !strings.HasSuffix(dir, ".uwas") {
		t.Errorf("dir = %q, should end with .uwas", dir)
	}
}

func TestDefaultConfigPath(t *testing.T) {
	path := defaultConfigPath()
	if path == "" {
		t.Error("defaultConfigPath should not be empty")
	}
	if !strings.HasSuffix(path, "uwas.yaml") {
		t.Errorf("path = %q, should end with uwas.yaml", path)
	}
	if !strings.Contains(path, ".uwas") {
		t.Errorf("path = %q, should contain .uwas directory", path)
	}
}

func TestPromptWithDefaultReturnsDefault(t *testing.T) {
	// Create a pipe that provides an empty line (just newline).
	r, w, _ := os.Pipe()
	w.WriteString("\n")
	w.Close()

	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	// Capture stdout (the prompt text).
	oldStdout := os.Stdout
	_, wOut, _ := os.Pipe()
	os.Stdout = wOut

	result := promptWithDefault("Enter value", "default-value")

	wOut.Close()
	os.Stdout = oldStdout

	if result != "default-value" {
		t.Errorf("result = %q, want default-value", result)
	}
}

func TestPromptWithDefaultReturnsInput(t *testing.T) {
	r, w, _ := os.Pipe()
	w.WriteString("custom-input\n")
	w.Close()

	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	oldStdout := os.Stdout
	_, wOut, _ := os.Pipe()
	os.Stdout = wOut

	result := promptWithDefault("Enter value", "default-value")

	wOut.Close()
	os.Stdout = oldStdout

	if result != "custom-input" {
		t.Errorf("result = %q, want custom-input", result)
	}
}

// ========== PHP command tests ==========

func TestPHPCommandNameDescription(t *testing.T) {
	p := &PHPCommand{}
	if p.Name() != "php" {
		t.Errorf("Name() = %q, want php", p.Name())
	}
	if p.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

func TestPHPCommandHelp(t *testing.T) {
	p := &PHPCommand{}
	h := p.Help()
	if !strings.Contains(h, "list") {
		t.Error("Help should mention list")
	}
	if !strings.Contains(h, "start") {
		t.Error("Help should mention start")
	}
	if !strings.Contains(h, "stop") {
		t.Error("Help should mention stop")
	}
	if !strings.Contains(h, "extensions") {
		t.Error("Help should mention extensions")
	}
}

func TestPHPCommandRunNoArgs(t *testing.T) {
	p := &PHPCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := p.Run(nil)

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

func TestPHPCommandUnknownSubcommand(t *testing.T) {
	p := &PHPCommand{}
	err := p.Run([]string{"bogus"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestPHPListCommand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/php" {
			t.Errorf("path = %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode([]map[string]any{
			{
				"version":     "8.4",
				"binary":      "/usr/bin/php8.4",
				"sapi":        "cgi",
				"running":     true,
				"listen_addr": "127.0.0.1:9000",
				"pid":         1234,
			},
			{
				"version": "8.3",
				"binary":  "/usr/bin/php8.3",
				"sapi":    "cgi",
				"running": false,
			},
		})
	}))
	defer srv.Close()

	p := &PHPCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := p.Run([]string{"list", "--api-url", srv.URL, "--api-key", "k"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	if !strings.Contains(output, "8.4") {
		t.Errorf("should contain version, got:\n%s", output)
	}
	if !strings.Contains(output, "running") {
		t.Errorf("should contain running status, got:\n%s", output)
	}
	if !strings.Contains(output, "VERSION") {
		t.Errorf("should contain header, got:\n%s", output)
	}
}

func TestPHPListEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "[]")
	}))
	defer srv.Close()

	p := &PHPCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := p.Run([]string{"list", "--api-url", srv.URL, "--api-key", "k"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	if !strings.Contains(buf.String(), "No PHP installations") {
		t.Error("should say no PHP installations")
	}
}

func TestPHPStartCommand(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		w.WriteHeader(200)
		fmt.Fprint(w, `{"status":"started"}`)
	}))
	defer srv.Close()

	p := &PHPCommand{}

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := p.Run([]string{"start", "--api-url", srv.URL, "--api-key", "k", "--port", "9001", "8.4"})

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("start error: %v", err)
	}
	if gotMethod != "POST" {
		t.Errorf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/api/v1/php/8.4/start" {
		t.Errorf("path = %s", gotPath)
	}
}

func TestPHPStartMissingVersion(t *testing.T) {
	p := &PHPCommand{}
	err := p.Run([]string{"start", "--api-url", "http://localhost:0"})
	if err == nil {
		t.Fatal("expected error for missing version")
	}
	if !strings.Contains(err.Error(), "version is required") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestPHPStopCommand(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		fmt.Fprint(w, `{"status":"stopped"}`)
	}))
	defer srv.Close()

	p := &PHPCommand{}

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := p.Run([]string{"stop", "--api-url", srv.URL, "--api-key", "k", "8.4"})

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("stop error: %v", err)
	}
	if gotPath != "/api/v1/php/8.4/stop" {
		t.Errorf("path = %s", gotPath)
	}
}

func TestPHPStopMissingVersion(t *testing.T) {
	p := &PHPCommand{}
	err := p.Run([]string{"stop", "--api-url", "http://localhost:0"})
	if err == nil {
		t.Fatal("expected error for missing version")
	}
	if !strings.Contains(err.Error(), "version is required") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestPHPConfigCommand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"memory_limit":    "256M",
			"upload_max_size": "50M",
			"max_execution":   30,
		})
	}))
	defer srv.Close()

	p := &PHPCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := p.Run([]string{"config", "--api-url", srv.URL, "--api-key", "k", "8.4"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("config error: %v", err)
	}
	if !strings.Contains(buf.String(), "Configuration") {
		t.Error("should contain Configuration header")
	}
}

func TestPHPConfigMissingVersion(t *testing.T) {
	p := &PHPCommand{}
	err := p.Run([]string{"config", "--api-url", "http://localhost:0"})
	if err == nil {
		t.Fatal("expected error for missing version")
	}
	if !strings.Contains(err.Error(), "version is required") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestPHPExtensionsCommand(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]string{"curl", "mbstring", "openssl", "pdo_mysql"})
	}))
	defer srv.Close()

	p := &PHPCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := p.Run([]string{"extensions", "--api-url", srv.URL, "--api-key", "k", "8.4"})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)

	if err != nil {
		t.Fatalf("extensions error: %v", err)
	}
	if !strings.Contains(buf.String(), "curl") {
		t.Error("should list extensions")
	}
	if !strings.Contains(buf.String(), "Extensions (4)") {
		t.Error("should show extension count")
	}
}

func TestPHPExtensionsMissingVersion(t *testing.T) {
	p := &PHPCommand{}
	err := p.Run([]string{"extensions", "--api-url", "http://localhost:0"})
	if err == nil {
		t.Fatal("expected error for missing version")
	}
	if !strings.Contains(err.Error(), "version is required") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestPHPExtensionsAlias(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]string{"curl"})
	}))
	defer srv.Close()

	p := &PHPCommand{}

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := p.Run([]string{"ext", "--api-url", srv.URL, "--api-key", "k", "8.4"})

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Fatalf("ext alias error: %v", err)
	}
}

func TestMigrateCommandApacheSubcommand(t *testing.T) {
	// Create a minimal Apache config.
	content := `
<VirtualHost *:80>
    ServerName test-apache.com
    DocumentRoot /var/www/test
</VirtualHost>
`
	tmpFile := writeTempFile(t, content, "apache-migrate-*.conf")
	defer os.Remove(tmpFile)

	m := &MigrateCommand{}

	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := m.Run([]string{"apache", tmpFile})

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if err != nil {
		t.Fatalf("apache migration error: %v", err)
	}
	if !strings.Contains(output, "test-apache.com") {
		t.Errorf("output should contain host, got:\n%s", output)
	}
}
