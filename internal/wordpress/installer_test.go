package wordpress

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeTarHandlerFunc returns a function suitable for assigning to httpGetFn.
// It serves tarContent for normal URLs and its SHA256 hash for .sha256 URLs.
// It creates two internal test servers so URL-based routing works correctly
// even though httpGetFn discards the original URL.
func fakeTarHandlerFunc(tarContent string) (func(string) (*http.Response, error), func()) {
	hash := sha256.Sum256([]byte(tarContent))
	hashHex := hex.EncodeToString(hash[:])

	tarSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		io.WriteString(w, tarContent)
	}))
	hashSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  latest.tar.gz\n", hashHex)
	}))

	fn := func(url string) (*http.Response, error) {
		if strings.HasSuffix(url, ".sha256") {
			return http.Get(hashSrv.URL)
		}
		return http.Get(tarSrv.URL)
	}
	cleanup := func() {
		tarSrv.Close()
		hashSrv.Close()
	}
	return fn, cleanup
}

// ---------------------------------------------------------------------------
// helpers: save and restore hook variables between tests
// ---------------------------------------------------------------------------

type hookSnapshot struct {
	goos      string
	execCmd   func(string, ...string) *exec.Cmd
	lookPath  func(string) (string, error)
	httpGet   func(string) (*http.Response, error)
	stat      func(string) (os.FileInfo, error)
	readFile  func(string) ([]byte, error)
	writeFile func(string, []byte, fs.FileMode) error
	mkdirAll  func(string, fs.FileMode) error
	removeAll func(string) error
	rename    func(string, string) error
	readDir   func(string) ([]os.DirEntry, error)
	walkFn    func(string, filepath.WalkFunc) error
}

func saveHooks() hookSnapshot {
	return hookSnapshot{
		goos:      runtimeGOOS,
		execCmd:   execCommandFn,
		lookPath:  execLookPathFn,
		httpGet:   httpGetFn,
		stat:      osStatFn,
		readFile:  osReadFileFn,
		writeFile: osWriteFileFn,
		mkdirAll:  osMkdirAllFn,
		removeAll: osRemoveAllFn,
		rename:    osRenameFn,
		readDir:   osReadDirFn,
		walkFn:    filepathWalkFn,
	}
}

func restoreHooks(s hookSnapshot) {
	runtimeGOOS = s.goos
	execCommandFn = s.execCmd
	execLookPathFn = s.lookPath
	httpGetFn = s.httpGet
	osStatFn = s.stat
	osReadFileFn = s.readFile
	osWriteFileFn = s.writeFile
	osMkdirAllFn = s.mkdirAll
	osRemoveAllFn = s.removeAll
	osRenameFn = s.rename
	osReadDirFn = s.readDir
	filepathWalkFn = s.walkFn
}

// fakeCmd returns a *exec.Cmd that just succeeds (exit 0) with optional stdout.
func fakeCmd(stdout string) func(string, ...string) *exec.Cmd {
	return func(name string, args ...string) *exec.Cmd {
		// Use Go test binary itself as the subprocess via TestHelperProcess.
		cs := []string{"-test.run=TestHelperProcess", "--", name}
		cs = append(cs, args...)
		cmd := exec.Command(os.Args[0], cs...)
		cmd.Env = append(os.Environ(),
			"GO_TEST_HELPER_PROCESS=1",
			"GO_TEST_HELPER_STDOUT="+stdout,
			"GO_TEST_HELPER_EXIT=0",
		)
		return cmd
	}
}

// fakeCmdFail returns a *exec.Cmd that will always fail when executed.
// Uses a nonexistent executable path to guarantee failure.
func fakeCmdFail(stderr string) func(string, ...string) *exec.Cmd {
	return func(name string, args ...string) *exec.Cmd {
		// Point to a nonexistent binary — Run/CombinedOutput/Output will all error.
		cmd := exec.Command("__nonexistent_binary_for_test__")
		return cmd
	}
}

// TestHelperProcess is the subprocess entry-point used by fakeCmd/fakeCmdFail.
// It is NOT a real test — the guard variable prevents it from running normally.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_TEST_HELPER_PROCESS") != "1" {
		return
	}
	fmt.Fprint(os.Stdout, os.Getenv("GO_TEST_HELPER_STDOUT"))
	if os.Getenv("GO_TEST_HELPER_EXIT") == "1" {
		os.Exit(1)
	}
	os.Exit(0)
}

// fakeLookPathOK simulates a binary being present.
func fakeLookPathOK(name string) (string, error) { return "/usr/bin/" + name, nil }

// fakeLookPathFail simulates a missing binary.
func fakeLookPathFail(name string) (string, error) { return "", fmt.Errorf("not found: %s", name) }

// ---------------------------------------------------------------------------
// TestEscSQL
// ---------------------------------------------------------------------------

func TestEscSQL(t *testing.T) {
	tests := []struct{ in, want string }{
		{"hello", "hello"},
		{"it's", `it\'s`},
		{`back\slash`, `back\\slash`},
		{`mix'n\match`, `mix\'n\\match`},
	}
	for _, tt := range tests {
		got := escSQL(tt.in)
		if got != tt.want {
			t.Errorf("escSQL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// TestSanitizeDBName
// ---------------------------------------------------------------------------

func TestSanitizeDBName(t *testing.T) {
	tests := []struct {
		domain string
		want   string
	}{
		{"example.com", "wp_example_com"},
		{"my-site.org", "wp_my_site_org"},
		{"sub.domain.example.com", "wp_sub_domain_example_com"},
	}
	for _, tt := range tests {
		got := sanitizeDBName(tt.domain)
		if got != tt.want {
			t.Errorf("sanitizeDBName(%q) = %q, want %q", tt.domain, got, tt.want)
		}
	}
}

func TestSanitizeDBNameMaxLength(t *testing.T) {
	long := "a.very.long.domain.name.that.exceeds.thirty.two.characters.example.com"
	got := sanitizeDBName(long)
	if len(got) > 35 { // "wp_" + 32
		t.Errorf("name too long: %d chars", len(got))
	}
}

// ---------------------------------------------------------------------------
// TestGenerateSecret
// ---------------------------------------------------------------------------

func TestGenerateSecret(t *testing.T) {
	s1 := generateSecret(16)
	s2 := generateSecret(16)
	if len(s1) != 16 {
		t.Errorf("length = %d, want 16", len(s1))
	}
	if s1 == s2 {
		t.Error("secrets should be different")
	}
	// Larger length
	s3 := generateSecret(32)
	if len(s3) != 32 {
		t.Errorf("length = %d, want 32", len(s3))
	}
}

// ---------------------------------------------------------------------------
// TestGenerateWPConfig
// ---------------------------------------------------------------------------

func TestGenerateWPConfig(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	// Use real filesystem for config generation
	osWriteFileFn = os.WriteFile

	dir := t.TempDir()
	err := generateWPConfig(dir, "testdb", "testuser", "testpass", "localhost")
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "wp-config.php"))
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	checks := []string{
		"'testdb'",
		"'testuser'",
		"'testpass'",
		"'localhost'",
		"AUTH_KEY",
		"SECURE_AUTH_KEY",
		"FORCE_SSL_ADMIN",
		"DISALLOW_FILE_EDIT",
		"wp-settings.php",
	}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("wp-config.php should contain %q", check)
		}
	}
}

// ---------------------------------------------------------------------------
// TestInstall_Success
// ---------------------------------------------------------------------------

func TestInstall_Success(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	webRoot := t.TempDir()

	// Mock exec: lookpath mysql found, all commands succeed
	execLookPathFn = fakeLookPathOK
	execCommandFn = fakeCmd("OK\n")

	// Mock HTTP: serve a tiny response (we don't really extract, tar will "succeed" via fakeCmd)
	var httpCleanup func()
	httpGetFn, httpCleanup = fakeTarHandlerFunc("fake-tar-content")
	defer httpCleanup()

	// Use real filesystem hooks for file ops
	osStatFn = os.Stat
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile
	osMkdirAllFn = os.MkdirAll
	osRemoveAllFn = os.RemoveAll
	osRenameFn = os.Rename
	osReadDirFn = os.ReadDir

	result := Install(InstallRequest{
		Domain:  "test.com",
		WebRoot: webRoot,
	})

	if result.Status != "done" {
		t.Fatalf("expected status=done, got %q, error=%s, output=%s", result.Status, result.Error, result.Output)
	}
	if result.AdminURL == "" {
		t.Error("AdminURL should not be empty")
	}
	if result.DBName != "wp_test_com" {
		t.Errorf("DBName = %q, want wp_test_com", result.DBName)
	}
	// wp-config.php should exist
	if _, err := os.Stat(filepath.Join(webRoot, "wp-config.php")); err != nil {
		t.Error("wp-config.php should exist")
	}
	// .htaccess should exist
	if _, err := os.Stat(filepath.Join(webRoot, ".htaccess")); err != nil {
		t.Error(".htaccess should exist")
	}
	// mu-plugin dir should exist
	if _, err := os.Stat(filepath.Join(webRoot, "wp-content", "mu-plugins", "uwas-rewrite.php")); err != nil {
		t.Error("mu-plugin should exist")
	}
}

// ---------------------------------------------------------------------------
// TestInstall_DBFailure
// ---------------------------------------------------------------------------

func TestInstall_DBFailure(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	webRoot := t.TempDir()

	// mysql lookpath fails => DB creation is skipped (non-fatal)
	execLookPathFn = fakeLookPathFail
	execCommandFn = fakeCmd("")

	var httpCleanup func()
	httpGetFn, httpCleanup = fakeTarHandlerFunc("fake-tar")
	defer httpCleanup()

	osStatFn = os.Stat
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile
	osMkdirAllFn = os.MkdirAll
	osRemoveAllFn = os.RemoveAll
	osRenameFn = os.Rename
	osReadDirFn = os.ReadDir

	result := Install(InstallRequest{
		Domain:  "dbfail.com",
		WebRoot: webRoot,
	})

	// Should still succeed — DB failure is non-fatal
	if result.Status != "done" {
		t.Fatalf("expected status=done, got %q, error=%s", result.Status, result.Error)
	}
	if !strings.Contains(result.Output, "MySQL setup failed") {
		t.Error("output should mention MySQL setup failed")
	}
}

// ---------------------------------------------------------------------------
// TestInstall_DownloadFailure
// ---------------------------------------------------------------------------

func TestInstall_DownloadFailure(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	webRoot := t.TempDir()

	execLookPathFn = fakeLookPathFail
	execCommandFn = fakeCmd("")
	osStatFn = os.Stat
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile
	osMkdirAllFn = os.MkdirAll

	// HTTP fails
	httpGetFn = func(url string) (*http.Response, error) {
		return nil, fmt.Errorf("network down")
	}

	result := Install(InstallRequest{
		Domain:  "fail.com",
		WebRoot: webRoot,
	})

	if result.Status != "error" {
		t.Fatalf("expected status=error, got %q", result.Status)
	}
	if !strings.Contains(result.Error, "download failed") {
		t.Errorf("error should mention download failed, got %q", result.Error)
	}
}

// ---------------------------------------------------------------------------
// TestInstall_PlaceholderRemoval
// ---------------------------------------------------------------------------

func TestInstall_PlaceholderRemoval(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	webRoot := t.TempDir()

	// Create a placeholder index.html
	os.WriteFile(filepath.Join(webRoot, "index.html"), []byte("<h1>Site is ready</h1>"), 0644)

	execLookPathFn = fakeLookPathFail
	execCommandFn = fakeCmd("")

	var httpCleanup func()
	httpGetFn, httpCleanup = fakeTarHandlerFunc("fake-tar")
	defer httpCleanup()

	osStatFn = os.Stat
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile
	osMkdirAllFn = os.MkdirAll
	osRemoveAllFn = os.RemoveAll
	osRenameFn = os.Rename
	osReadDirFn = os.ReadDir

	result := Install(InstallRequest{Domain: "placeholder.com", WebRoot: webRoot})
	if result.Status != "done" {
		t.Fatalf("expected done, got %q: %s", result.Status, result.Error)
	}
	if !strings.Contains(result.Output, "Removed placeholder index.html") {
		t.Error("should report placeholder removal")
	}
	// index.html should be gone
	if _, err := os.Stat(filepath.Join(webRoot, "index.html")); err == nil {
		t.Error("index.html should have been removed")
	}
}

// ---------------------------------------------------------------------------
// TestInstall_WindowsSkipsPermissions
// ---------------------------------------------------------------------------

func TestInstall_WindowsSkipsPermissions(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "windows"
	webRoot := t.TempDir()

	execLookPathFn = fakeLookPathFail
	execCommandFn = fakeCmd("")

	var httpCleanup func()
	httpGetFn, httpCleanup = fakeTarHandlerFunc("fake")
	defer httpCleanup()

	osStatFn = os.Stat
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile
	osMkdirAllFn = os.MkdirAll
	osRemoveAllFn = os.RemoveAll
	osRenameFn = os.Rename
	osReadDirFn = os.ReadDir

	result := Install(InstallRequest{Domain: "win.com", WebRoot: webRoot})
	if result.Status != "done" {
		t.Fatalf("expected done, got %q: %s", result.Status, result.Error)
	}
	// On windows, no permission messages
	if strings.Contains(result.Output, "www-data") {
		t.Error("Windows install should not set www-data permissions")
	}
}

// ---------------------------------------------------------------------------
// TestDetectSites_Found
// ---------------------------------------------------------------------------

func TestDetectSites_Found(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "windows" // avoids exec calls for stat owner
	osStatFn = os.Stat
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile
	osReadDirFn = os.ReadDir
	execLookPathFn = fakeLookPathFail // no wp-cli
	execCommandFn = fakeCmd("")

	dir := t.TempDir()
	// Create wp-config.php with DB info
	wpConfig := `<?php
define('DB_NAME', 'mydb');
define('DB_USER', 'myuser');
define('DB_HOST', 'localhost');
define('WP_DEBUG', false);
define('FORCE_SSL_ADMIN', true);
define('DISALLOW_FILE_EDIT', true);
require_once ABSPATH . 'wp-settings.php';
`
	os.WriteFile(filepath.Join(dir, "wp-config.php"), []byte(wpConfig), 0644)

	// Create version.php
	os.MkdirAll(filepath.Join(dir, "wp-includes"), 0755)
	os.WriteFile(filepath.Join(dir, "wp-includes", "version.php"), []byte("<?php\n$wp_version = '6.4.2';\n"), 0644)

	// Create wp-content so writable check works
	os.MkdirAll(filepath.Join(dir, "wp-content"), 0755)

	domains := []DomainInfo{{Host: "example.com", WebRoot: dir}}
	sites := DetectSites(domains)

	if len(sites) != 1 {
		t.Fatalf("expected 1 site, got %d", len(sites))
	}
	s := sites[0]
	if s.Domain != "example.com" {
		t.Errorf("Domain = %q", s.Domain)
	}
	if s.DBName != "mydb" {
		t.Errorf("DBName = %q", s.DBName)
	}
	if s.DBUser != "myuser" {
		t.Errorf("DBUser = %q", s.DBUser)
	}
	if s.DBHost != "localhost" {
		t.Errorf("DBHost = %q", s.DBHost)
	}
	if s.Version != "6.4.2" {
		t.Errorf("Version = %q", s.Version)
	}
	if !s.Health.SSL {
		t.Error("SSL should be true")
	}
	if !s.Health.FileEdit {
		t.Error("FileEdit should be true")
	}
}

// ---------------------------------------------------------------------------
// TestDetectSites_NotFound
// ---------------------------------------------------------------------------

func TestDetectSites_NotFound(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "windows"
	osStatFn = os.Stat
	execLookPathFn = fakeLookPathFail

	dir := t.TempDir() // empty dir — no wp-config.php
	domains := []DomainInfo{{Host: "empty.com", WebRoot: dir}}
	sites := DetectSites(domains)
	if len(sites) != 0 {
		t.Errorf("expected 0 sites, got %d", len(sites))
	}
}

// ---------------------------------------------------------------------------
// TestDetectSites_EmptyWebRoot
// ---------------------------------------------------------------------------

func TestDetectSites_EmptyWebRoot(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osStatFn = os.Stat

	domains := []DomainInfo{{Host: "noroot.com", WebRoot: ""}}
	sites := DetectSites(domains)
	if len(sites) != 0 {
		t.Errorf("expected 0 sites, got %d", len(sites))
	}
}

// ---------------------------------------------------------------------------
// TestDetectSites_WithPlugins
// ---------------------------------------------------------------------------

func TestDetectSites_WithPlugins(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "windows"
	osStatFn = os.Stat
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile
	osReadDirFn = os.ReadDir
	execLookPathFn = fakeLookPathFail
	execCommandFn = fakeCmd("")

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "wp-config.php"), []byte("<?php\ndefine('DB_NAME','db');\ndefine('DB_USER','u');\ndefine('DB_HOST','h');\nrequire_once ABSPATH . 'wp-settings.php';\n"), 0644)
	os.MkdirAll(filepath.Join(dir, "wp-includes"), 0755)
	os.WriteFile(filepath.Join(dir, "wp-includes", "version.php"), []byte("<?php\n$wp_version = '6.5';\n"), 0644)

	// Create plugin dirs
	plugDir := filepath.Join(dir, "wp-content", "plugins", "akismet")
	os.MkdirAll(plugDir, 0755)
	os.WriteFile(filepath.Join(plugDir, "akismet.php"), []byte("<?php\n/*\n * Plugin Name: Akismet\n * Version: 5.3\n */\n"), 0644)

	domains := []DomainInfo{{Host: "pluginsite.com", WebRoot: dir}}
	sites := DetectSites(domains)
	if len(sites) != 1 {
		t.Fatalf("expected 1 site, got %d", len(sites))
	}
	if len(sites[0].Plugins) == 0 {
		t.Error("expected plugins to be detected via scanPluginDirs")
	}
	found := false
	for _, p := range sites[0].Plugins {
		if p.Name == "akismet" {
			found = true
			if p.Version != "5.3" {
				t.Errorf("akismet version = %q, want 5.3", p.Version)
			}
		}
	}
	if !found {
		t.Error("akismet plugin not found")
	}
}

// ---------------------------------------------------------------------------
// TestIsWordPress_True / _False
// ---------------------------------------------------------------------------

func TestIsWordPress_True(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osStatFn = os.Stat

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "wp-config.php"), []byte("<?php\n"), 0644)
	if !IsWordPress(dir) {
		t.Error("expected true")
	}
}

func TestIsWordPress_False(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osStatFn = os.Stat

	dir := t.TempDir()
	if IsWordPress(dir) {
		t.Error("expected false for empty dir")
	}
}

// ---------------------------------------------------------------------------
// TestParseWPConfig
// ---------------------------------------------------------------------------

func TestParseWPConfig(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadFileFn = os.ReadFile

	dir := t.TempDir()
	path := filepath.Join(dir, "wp-config.php")
	content := `<?php
define('DB_NAME', 'proddb');
define('DB_USER', 'produser');
define('DB_HOST', '127.0.0.1');
`
	os.WriteFile(path, []byte(content), 0644)
	name, user, host := parseWPConfig(path)
	if name != "proddb" {
		t.Errorf("name = %q", name)
	}
	if user != "produser" {
		t.Errorf("user = %q", user)
	}
	if host != "127.0.0.1" {
		t.Errorf("host = %q", host)
	}
}

func TestParseWPConfig_MissingFile(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadFileFn = os.ReadFile

	name, user, host := parseWPConfig("/nonexistent/wp-config.php")
	if name != "" || user != "" || host != "" {
		t.Error("missing file should return empty strings")
	}
}

// ---------------------------------------------------------------------------
// TestDetectWPVersion
// ---------------------------------------------------------------------------

func TestDetectWPVersion(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadFileFn = os.ReadFile

	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "wp-includes"), 0755)
	os.WriteFile(filepath.Join(dir, "wp-includes", "version.php"),
		[]byte("<?php\n$wp_version = '6.4.3';\n"), 0644)

	v := detectWPVersion(dir)
	if v != "6.4.3" {
		t.Errorf("version = %q, want 6.4.3", v)
	}
}

func TestDetectWPVersion_Missing(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadFileFn = os.ReadFile

	dir := t.TempDir()
	v := detectWPVersion(dir)
	if v != "unknown" {
		t.Errorf("version = %q, want unknown", v)
	}
}

func TestDetectWPVersion_NoMatch(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadFileFn = os.ReadFile

	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "wp-includes"), 0755)
	os.WriteFile(filepath.Join(dir, "wp-includes", "version.php"),
		[]byte("<?php\n// no version here\n"), 0644)

	v := detectWPVersion(dir)
	if v != "unknown" {
		t.Errorf("version = %q, want unknown", v)
	}
}

// ---------------------------------------------------------------------------
// TestCheckWPHealth
// ---------------------------------------------------------------------------

func TestCheckWPHealth(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadFileFn = os.ReadFile

	dir := t.TempDir()
	wpConfigPath := filepath.Join(dir, "wp-config.php")
	os.WriteFile(wpConfigPath, []byte(`<?php
define('WP_DEBUG', true);
define('FORCE_SSL_ADMIN', true);
define('DISALLOW_FILE_EDIT', true);
`), 0644)

	h := checkWPHealth(wpConfigPath, dir)
	if !h.Debug {
		t.Error("Debug should be true")
	}
	if !h.SSL {
		t.Error("SSL should be true")
	}
	if !h.FileEdit {
		t.Error("FileEdit should be true")
	}
}

func TestCheckWPHealth_AllFalse(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadFileFn = os.ReadFile

	dir := t.TempDir()
	wpConfigPath := filepath.Join(dir, "wp-config.php")
	os.WriteFile(wpConfigPath, []byte("<?php\n// minimal config\n"), 0644)

	h := checkWPHealth(wpConfigPath, dir)
	if h.Debug || h.SSL || h.FileEdit {
		t.Error("all health flags should be false for minimal config")
	}
}

// ---------------------------------------------------------------------------
// TestCheckPermissions
// ---------------------------------------------------------------------------

func TestCheckPermissions(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "windows" // skip exec stat calls
	osStatFn = os.Stat
	osWriteFileFn = os.WriteFile
	execCommandFn = fakeCmd("")

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "wp-config.php"), []byte("<?php\n"), 0644)
	os.MkdirAll(filepath.Join(dir, "wp-content", "uploads"), 0755)
	os.WriteFile(filepath.Join(dir, ".htaccess"), []byte("# test"), 0644)

	r := checkPermissions(dir)
	if r.WPConfig == "missing" {
		t.Error("WPConfig should not be missing")
	}
	if r.WPContent == "missing" {
		t.Error("WPContent should not be missing")
	}
	if !r.Writable {
		t.Error("wp-content should be writable in temp dir")
	}
}

func TestCheckPermissions_MissingFiles(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "windows"
	osStatFn = os.Stat
	osWriteFileFn = os.WriteFile
	execCommandFn = fakeCmd("")

	dir := t.TempDir() // empty

	r := checkPermissions(dir)
	if r.WPConfig != "missing" {
		t.Errorf("WPConfig = %q, want missing", r.WPConfig)
	}
	if r.Htaccess != "missing" {
		t.Errorf("Htaccess = %q, want missing", r.Htaccess)
	}
}

func TestCheckPermissions_LinuxOwner(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "linux"
	osStatFn = os.Stat
	osWriteFileFn = os.WriteFile
	execCommandFn = fakeCmd("www-data:www-data")

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "wp-config.php"), []byte("<?php\n"), 0644)
	os.MkdirAll(filepath.Join(dir, "wp-content"), 0755)

	r := checkPermissions(dir)
	if r.Owner != "www-data:www-data" {
		t.Errorf("Owner = %q, want www-data:www-data", r.Owner)
	}
}

// ---------------------------------------------------------------------------
// TestPermString
// ---------------------------------------------------------------------------

func TestPermString_Missing(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osStatFn = os.Stat

	got := permString("/nonexistent/file/path")
	if got != "missing" {
		t.Errorf("got %q, want missing", got)
	}
}

func TestPermString_Exists(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	runtimeGOOS = "windows"
	osStatFn = os.Stat

	dir := t.TempDir()
	path := filepath.Join(dir, "testfile")
	os.WriteFile(path, []byte("x"), 0644)

	got := permString(path)
	if got == "missing" {
		t.Error("should not be missing")
	}
	// Should be a numeric perm string like "0666" (Windows) or "0644" (Linux)
	if len(got) < 4 {
		t.Errorf("unexpected perm string: %q", got)
	}
}

// ---------------------------------------------------------------------------
// TestHasWPCLI
// ---------------------------------------------------------------------------

func TestHasWPCLI_True(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	execLookPathFn = fakeLookPathOK
	if !hasWPCLI() {
		t.Error("expected true")
	}
}

func TestHasWPCLI_False(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	execLookPathFn = fakeLookPathFail
	if hasWPCLI() {
		t.Error("expected false")
	}
}

func TestResolveWPCLIBinary_FallbackPath(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execLookPathFn = func(name string) (string, error) {
		if name == "/usr/local/bin/wp" {
			return name, nil
		}
		return "", fmt.Errorf("not found: %s", name)
	}

	got, err := resolveWPCLIBinary()
	if err != nil {
		t.Fatalf("resolveWPCLIBinary: %v", err)
	}
	if got != "/usr/local/bin/wp" {
		t.Fatalf("binary = %q, want %q", got, "/usr/local/bin/wp")
	}
}

func TestListUsers_NumericID(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execLookPathFn = fakeLookPathOK
	execCommandFn = fakeCmd(`[{"ID":1,"user_login":"admin","user_email":"admin@example.com","roles":"administrator","user_registered":"2026-01-01 00:00:00"}]`)

	users, err := ListUsers(t.TempDir())
	if err != nil {
		t.Fatalf("ListUsers returned error: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(users))
	}
	if users[0].ID != "1" {
		t.Fatalf("ID = %q, want %q", users[0].ID, "1")
	}
	if users[0].Role != "administrator" {
		t.Fatalf("Role = %q, want %q", users[0].Role, "administrator")
	}
}

func TestListUsers_RolesArray(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execLookPathFn = fakeLookPathOK
	execCommandFn = fakeCmd("Notice: noisy output\n[{\"ID\":\"2\",\"user_login\":\"editor\",\"user_email\":\"editor@example.com\",\"roles\":[\"editor\",\"author\"]}]")

	users, err := ListUsers(t.TempDir())
	if err != nil {
		t.Fatalf("ListUsers returned error: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(users))
	}
	if users[0].Role != "editor,author" {
		t.Fatalf("Role = %q, want %q", users[0].Role, "editor,author")
	}
}

func TestListUsers_InvalidIDType(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execLookPathFn = fakeLookPathOK
	execCommandFn = fakeCmd(`[{"ID":{"bad":"value"},"user_login":"admin","user_email":"admin@example.com","roles":"administrator"}]`)

	_, err := ListUsers(t.TempDir())
	if err == nil {
		t.Fatal("expected parse error for invalid ID type")
	}
	if !strings.Contains(err.Error(), "invalid ID") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestUpdateCore_WithWPCLI
// ---------------------------------------------------------------------------

func TestUpdateCore_WithWPCLI(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execLookPathFn = fakeLookPathOK
	execCommandFn = fakeCmd("Success: WordPress updated.\n")

	out, err := UpdateCore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Success") {
		t.Errorf("output = %q", out)
	}
}

// ---------------------------------------------------------------------------
// TestUpdateCore_WithoutWPCLI
// ---------------------------------------------------------------------------

func TestUpdateCore_WithoutWPCLI(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execLookPathFn = fakeLookPathFail
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile
	osMkdirAllFn = os.MkdirAll
	osRemoveAllFn = os.RemoveAll

	var httpCleanup func()
	httpGetFn, httpCleanup = fakeTarHandlerFunc("fake-tar")
	defer httpCleanup()

	webRoot := t.TempDir()

	// When tar is called, create the wordpress dir structure in the temp dir
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		for i, a := range args {
			if a == "-C" && i+1 < len(args) {
				tmpDir := args[i+1]
				wpDir := filepath.Join(tmpDir, "wordpress")
				os.MkdirAll(wpDir, 0755)
				os.WriteFile(filepath.Join(wpDir, "index.php"), []byte("<?php\n"), 0644)
				break
			}
		}
		return fakeCmd("")(name, args...)
	}
	filepathWalkFn = filepath.Walk

	out, err := UpdateCore(webRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "WP-CLI not found") {
		t.Errorf("should mention WP-CLI not found, got: %s", out)
	}
}

// ---------------------------------------------------------------------------
// TestUpdateCore_DownloadFails
// ---------------------------------------------------------------------------

func TestUpdateCore_DownloadFails(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execLookPathFn = fakeLookPathFail
	httpGetFn = func(url string) (*http.Response, error) {
		return nil, fmt.Errorf("network error")
	}

	_, err := UpdateCore(t.TempDir())
	if err == nil {
		t.Error("expected error")
	}
	if !strings.Contains(err.Error(), "download failed") {
		t.Errorf("error = %q", err)
	}
}

// ---------------------------------------------------------------------------
// TestReinstallWordPress
// ---------------------------------------------------------------------------

func TestReinstallWordPress(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execLookPathFn = fakeLookPathOK
	execCommandFn = fakeCmd("Reinstalled.\n")

	out, err := ReinstallWordPress(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Reinstalled") {
		t.Errorf("output = %q", out)
	}
}

// ---------------------------------------------------------------------------
// TestUpdatePlugin_Success / _NoWPCLI
// ---------------------------------------------------------------------------

func TestUpdatePlugin_Success(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execCommandFn = fakeCmd("Plugin updated.\n")
	out, err := UpdatePlugin(t.TempDir(), "akismet")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Plugin updated") {
		t.Errorf("output = %q", out)
	}
}

func TestUpdatePlugin_NoWPCLI(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execCommandFn = fakeCmdFail("wp: not found")
	_, err := UpdatePlugin(t.TempDir(), "akismet")
	if err == nil {
		t.Error("expected error when wp-cli is missing")
	}
}

// ---------------------------------------------------------------------------
// TestUpdateAllPlugins
// ---------------------------------------------------------------------------

func TestUpdateAllPlugins(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execCommandFn = fakeCmd("All plugins updated.\n")
	out, err := UpdateAllPlugins(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "All plugins updated") {
		t.Errorf("output = %q", out)
	}
}

// ---------------------------------------------------------------------------
// TestActivatePlugin
// ---------------------------------------------------------------------------

func TestActivatePlugin(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execCommandFn = fakeCmd("Plugin activated.\n")
	out, err := ActivatePlugin(t.TempDir(), "hello-dolly")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "activated") {
		t.Errorf("output = %q", out)
	}
}

// ---------------------------------------------------------------------------
// TestDeactivatePlugin
// ---------------------------------------------------------------------------

func TestDeactivatePlugin(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execCommandFn = fakeCmd("Plugin deactivated.\n")
	out, err := DeactivatePlugin(t.TempDir(), "hello-dolly")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "deactivated") {
		t.Errorf("output = %q", out)
	}
}

// ---------------------------------------------------------------------------
// TestDeletePlugin
// ---------------------------------------------------------------------------

func TestDeletePlugin(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execCommandFn = fakeCmd("Plugin deleted.\n")
	out, err := DeletePlugin(t.TempDir(), "hello-dolly")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "deleted") {
		t.Errorf("output = %q", out)
	}
}

// ---------------------------------------------------------------------------
// TestUpdateTheme
// ---------------------------------------------------------------------------

func TestUpdateTheme(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execCommandFn = fakeCmd("Theme updated.\n")
	out, err := UpdateTheme(t.TempDir(), "twentytwentyfour")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Theme updated") {
		t.Errorf("output = %q", out)
	}
}

// ---------------------------------------------------------------------------
// TestFixPermissions_Linux
// ---------------------------------------------------------------------------

func TestFixPermissions_Linux(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execCommandFn = fakeCmd("")
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile
	osMkdirAllFn = os.MkdirAll

	dir := t.TempDir()
	// Create wp-config.php without FS_METHOD
	os.WriteFile(filepath.Join(dir, "wp-config.php"), []byte("<?php\nrequire_once ABSPATH . 'wp-settings.php';\n"), 0644)

	out, err := FixPermissions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Directories set to 755") {
		t.Errorf("should report dir perms: %s", out)
	}
	if !strings.Contains(out, "Files set to 644") {
		t.Errorf("should report file perms: %s", out)
	}
	if !strings.Contains(out, "wp-content set to 775") {
		t.Errorf("should report wp-content: %s", out)
	}
	if !strings.Contains(out, "Owner set to www-data") {
		t.Errorf("should report owner: %s", out)
	}
	if !strings.Contains(out, "FS_METHOD") {
		t.Errorf("should add FS_METHOD: %s", out)
	}
	// upgrade, uploads, .tmp dirs
	if !strings.Contains(out, ".tmp directories created") {
		t.Errorf("should create dirs: %s", out)
	}
}

func TestFixPermissions_ExecFails(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execCommandFn = fakeCmdFail("permission denied")
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile
	osMkdirAllFn = os.MkdirAll

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "wp-config.php"), []byte("<?php\ndefine('FS_METHOD','direct');\ndefine('WP_TEMP_DIR', __DIR__);\nrequire_once ABSPATH . 'wp-settings.php';\n"), 0644)

	out, err := FixPermissions(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Should mention chmod output when it fails
	if !strings.Contains(out, "chmod dirs") {
		t.Errorf("should report chmod failure: %s", out)
	}
}

func TestFixPermissions_AddWPTempDir(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execCommandFn = fakeCmd("")
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile
	osMkdirAllFn = os.MkdirAll

	dir := t.TempDir()
	// wp-config.php has FS_METHOD but NOT WP_TEMP_DIR
	os.WriteFile(filepath.Join(dir, "wp-config.php"), []byte("<?php\ndefine('FS_METHOD', 'direct');\nrequire_once ABSPATH . 'wp-settings.php';\n"), 0644)

	out, err := FixPermissions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "WP_TEMP_DIR") {
		t.Errorf("should add WP_TEMP_DIR: %s", out)
	}
}

// ---------------------------------------------------------------------------
// TestFixPermissions_Windows (exec commands still called but succeed via mock)
// ---------------------------------------------------------------------------

func TestFixPermissions_Windows(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "windows"
	execCommandFn = fakeCmd("")
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile
	osMkdirAllFn = os.MkdirAll

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "wp-config.php"), []byte("<?php\ndefine('FS_METHOD','direct');\ndefine('WP_TEMP_DIR','/tmp');\n"), 0644)

	_, err := FixPermissions(dir)
	if err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// TestSetDebugMode_Enable
// ---------------------------------------------------------------------------

func TestSetDebugMode_Enable(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile

	dir := t.TempDir()
	wpConfig := filepath.Join(dir, "wp-config.php")
	os.WriteFile(wpConfig, []byte("<?php\ndefine('WP_DEBUG', false);\nrequire_once ABSPATH . 'wp-settings.php';\n"), 0644)

	err := SetDebugMode(dir, true)
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(wpConfig)
	content := string(data)
	if !strings.Contains(content, "'WP_DEBUG', true") {
		t.Error("should contain WP_DEBUG true")
	}
	if !strings.Contains(content, "'WP_DEBUG_LOG', true") {
		t.Error("should contain WP_DEBUG_LOG true")
	}
	if !strings.Contains(content, "'WP_DEBUG_DISPLAY', true") {
		t.Error("should contain WP_DEBUG_DISPLAY true")
	}
}

// ---------------------------------------------------------------------------
// TestSetDebugMode_Disable
// ---------------------------------------------------------------------------

func TestSetDebugMode_Disable(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile

	dir := t.TempDir()
	wpConfig := filepath.Join(dir, "wp-config.php")
	os.WriteFile(wpConfig, []byte("<?php\ndefine('WP_DEBUG', true);\ndefine('WP_DEBUG_LOG', true);\ndefine('WP_DEBUG_DISPLAY', true);\nrequire_once ABSPATH . 'wp-settings.php';\n"), 0644)

	err := SetDebugMode(dir, false)
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(wpConfig)
	content := string(data)
	if strings.Contains(content, "'WP_DEBUG', true") {
		t.Error("should not contain WP_DEBUG true after disable")
	}
	if !strings.Contains(content, "'WP_DEBUG', false") {
		t.Error("should contain WP_DEBUG false")
	}
	if strings.Contains(content, "WP_DEBUG_LOG") {
		t.Error("should not contain WP_DEBUG_LOG after disable")
	}
}

// ---------------------------------------------------------------------------
// TestSetDebugMode_FileNotFound
// ---------------------------------------------------------------------------

func TestSetDebugMode_FileNotFound(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile

	err := SetDebugMode("/nonexistent/path", true)
	if err == nil {
		t.Error("expected error")
	}
	if !strings.Contains(err.Error(), "read wp-config.php") {
		t.Errorf("error = %q", err)
	}
}

// ---------------------------------------------------------------------------
// TestSetDebugMode_NoRequireOnce (append fallback)
// ---------------------------------------------------------------------------

func TestSetDebugMode_NoRequireOnce(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile

	dir := t.TempDir()
	wpConfig := filepath.Join(dir, "wp-config.php")
	os.WriteFile(wpConfig, []byte("<?php\n// no require_once here\n"), 0644)

	err := SetDebugMode(dir, true)
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(wpConfig)
	content := string(data)
	if !strings.Contains(content, "'WP_DEBUG', true") {
		t.Error("debug block should be appended")
	}
}

// ---------------------------------------------------------------------------
// TestSetDebugMode_ThatsAll (alternative insert point)
// ---------------------------------------------------------------------------

func TestSetDebugMode_ThatsAll(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile

	dir := t.TempDir()
	wpConfig := filepath.Join(dir, "wp-config.php")
	os.WriteFile(wpConfig, []byte("<?php\n/* That's all, stop editing! */\n"), 0644)

	err := SetDebugMode(dir, true)
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(wpConfig)
	content := string(data)
	if !strings.Contains(content, "'WP_DEBUG', true") {
		t.Error("debug block should be before That's all")
	}
}

// ---------------------------------------------------------------------------
// TestListPlugins
// ---------------------------------------------------------------------------

func TestListPlugins_WithWPCLI(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	jsonOut := `[{"name":"akismet","status":"active","version":"5.3","update":"5.4"},{"name":"hello-dolly","status":"inactive","version":"1.7.2","update":""}]`
	execCommandFn = fakeCmd(jsonOut)
	osReadDirFn = os.ReadDir

	dir := t.TempDir()
	plugins := listPlugins(dir)
	if len(plugins) != 2 {
		t.Fatalf("expected 2 plugins, got %d", len(plugins))
	}
	if plugins[0].Name != "akismet" || plugins[0].Update != "5.4" {
		t.Errorf("plugin[0] = %+v", plugins[0])
	}
}

func TestListPlugins_WPCLIFails(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execCommandFn = fakeCmdFail("error")
	osReadDirFn = os.ReadDir
	osReadFileFn = os.ReadFile

	dir := t.TempDir()
	// Create a plugin dir for fallback
	plugDir := filepath.Join(dir, "wp-content", "plugins", "test-plugin")
	os.MkdirAll(plugDir, 0755)
	os.WriteFile(filepath.Join(plugDir, "test-plugin.php"), []byte("<?php\n/*\n * Version: 1.0\n */\n"), 0644)

	plugins := listPlugins(dir)
	if len(plugins) != 1 {
		t.Fatalf("expected 1 plugin via fallback, got %d", len(plugins))
	}
	if plugins[0].Name != "test-plugin" {
		t.Errorf("name = %q", plugins[0].Name)
	}
}

func TestListPlugins_BadJSON(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execCommandFn = fakeCmd("not json")
	osReadDirFn = os.ReadDir
	osReadFileFn = os.ReadFile

	dir := t.TempDir()
	// Should fall back to scanPluginDirs
	plugins := listPlugins(dir)
	if plugins != nil {
		t.Errorf("expected nil plugins from fallback on empty dir, got %d", len(plugins))
	}
}

// ---------------------------------------------------------------------------
// TestListThemes
// ---------------------------------------------------------------------------

func TestListThemes_WithWPCLI(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	jsonOut := `[{"name":"twentytwentyfour","status":"active","version":"1.0","update":"1.1"}]`
	execCommandFn = fakeCmd(jsonOut)

	themes := listThemes(t.TempDir())
	if len(themes) != 1 {
		t.Fatalf("expected 1 theme, got %d", len(themes))
	}
	if themes[0].Name != "twentytwentyfour" || themes[0].Update != "1.1" {
		t.Errorf("theme = %+v", themes[0])
	}
}

func TestListThemes_WPCLIFails(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execCommandFn = fakeCmdFail("error")
	themes := listThemes(t.TempDir())
	if themes != nil {
		t.Error("expected nil on error")
	}
}

func TestListThemes_BadJSON(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execCommandFn = fakeCmd("{bad json")
	themes := listThemes(t.TempDir())
	if themes != nil {
		t.Error("expected nil on bad json")
	}
}

// ---------------------------------------------------------------------------
// TestScanPluginDirs
// ---------------------------------------------------------------------------

func TestScanPluginDirs(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadDirFn = os.ReadDir
	osReadFileFn = os.ReadFile

	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "wp-content", "plugins")

	// Plugin with version
	p1 := filepath.Join(pluginDir, "my-plugin")
	os.MkdirAll(p1, 0755)
	os.WriteFile(filepath.Join(p1, "my-plugin.php"), []byte("<?php\n/*\n * Plugin Name: My Plugin\n * Version: 2.1.0\n */\n"), 0644)

	// Plugin without version
	p2 := filepath.Join(pluginDir, "other")
	os.MkdirAll(p2, 0755)
	os.WriteFile(filepath.Join(p2, "other.php"), []byte("<?php\n// no version header\n"), 0644)

	// File (not dir) in plugins — should be skipped
	os.WriteFile(filepath.Join(pluginDir, "index.php"), []byte("<?php\n"), 0644)

	plugins := scanPluginDirs(dir)
	if len(plugins) != 2 {
		t.Fatalf("expected 2 plugins, got %d", len(plugins))
	}

	found := map[string]string{}
	for _, p := range plugins {
		found[p.Name] = p.Version
	}
	if v, ok := found["my-plugin"]; !ok || v != "2.1.0" {
		t.Errorf("my-plugin version = %q", v)
	}
	if v, ok := found["other"]; !ok || v != "" {
		t.Errorf("other version = %q, want empty", v)
	}
}

func TestScanPluginDirs_NoDirExists(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadDirFn = os.ReadDir

	plugins := scanPluginDirs(t.TempDir())
	if plugins != nil {
		t.Errorf("expected nil, got %d plugins", len(plugins))
	}
}

// ---------------------------------------------------------------------------
// TestParsePluginVersion
// ---------------------------------------------------------------------------

func TestParsePluginVersion(t *testing.T) {
	tests := []struct {
		content string
		want    string
	}{
		{"<?php\n/*\n * Version: 1.2.3\n */\n", "1.2.3"},
		{"<?php\n/*\n * Plugin Name: X\n * Version: 4.5\n */\n", "4.5"},
		{"<?php\n// no version\n", ""},
		{"* Version: 0.1-beta", "0.1-beta"},
		{" * Version: 3.0.0", "3.0.0"},
	}
	for _, tt := range tests {
		got := parsePluginVersion(tt.content)
		if got != tt.want {
			t.Errorf("parsePluginVersion(%q...) = %q, want %q", tt.content[:20], got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// TestCountUpdates
// ---------------------------------------------------------------------------

func TestCountUpdates(t *testing.T) {
	plugins := []PluginInfo{
		{Name: "a", Update: "2.0"},
		{Name: "b", Update: ""},
		{Name: "c", Update: "none"},
		{Name: "d", Update: "3.0"},
	}
	got := countUpdates(plugins)
	if got != 2 {
		t.Errorf("countUpdates = %d, want 2", got)
	}
}

func TestCountUpdates_Empty(t *testing.T) {
	got := countUpdates(nil)
	if got != 0 {
		t.Errorf("countUpdates(nil) = %d", got)
	}
}

func TestCountUpdates2(t *testing.T) {
	themes := []ThemeInfo{
		{Name: "a", Update: "2.0"},
		{Name: "b", Update: ""},
		{Name: "c", Update: "none"},
	}
	got := countUpdates2(themes)
	if got != 1 {
		t.Errorf("countUpdates2 = %d, want 1", got)
	}
}

// ---------------------------------------------------------------------------
// TestCreateMySQLDB
// ---------------------------------------------------------------------------

func TestCreateMySQLDB_Success(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execLookPathFn = fakeLookPathOK
	execCommandFn = fakeCmd("Query OK\n")

	var log strings.Builder
	err := createMySQLDB("testdb", "testuser", "testpass", "localhost", &log)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCreateMySQLDB_NoMySQL(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execLookPathFn = fakeLookPathFail

	var log strings.Builder
	err := createMySQLDB("testdb", "testuser", "testpass", "localhost", &log)
	if err == nil {
		t.Error("expected error")
	}
	if !strings.Contains(err.Error(), "mysql client not found") {
		t.Errorf("err = %q", err)
	}
}

func TestCreateMySQLDB_FallbackSudo(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execLookPathFn = fakeLookPathOK
	callCount := 0
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		callCount++
		if callCount == 1 {
			// First call (mysql -u root) fails
			return fakeCmdFail("Access denied")(name, args...)
		}
		// Second call (sudo mysql) succeeds
		return fakeCmd("OK")(name, args...)
	}

	var log strings.Builder
	err := createMySQLDB("testdb", "testuser", "testpass", "localhost", &log)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCreateMySQLDB_RejectsUnsafeIdentifiers(t *testing.T) {
	tests := []struct {
		name string
		db   string
		user string
		want string
	}{
		{"db_backtick", "wp`evil", "wp_user", "invalid database name"},
		{"db_option", "--all-databases", "wp_user", "invalid database name"},
		{"user_quote", "wp_test", "bad'user", "invalid database user"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := saveHooks()
			defer restoreHooks(snap)

			execLookPathFn = fakeLookPathOK
			execCalled := false
			execCommandFn = func(name string, args ...string) *exec.Cmd {
				execCalled = true
				return fakeCmd("OK")(name, args...)
			}

			var log strings.Builder
			err := createMySQLDB(tt.db, tt.user, "testpass", "localhost", &log)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			if execCalled {
				t.Fatal("mysql command should not run for unsafe identifiers")
			}
		})
	}
}

func TestCreateMySQLDB_UsesSafeSQLIdentifier(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execLookPathFn = fakeLookPathOK
	var gotSQL string
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		if len(args) >= 4 && args[2] == "-e" {
			gotSQL = args[3]
		}
		return fakeCmd("Query OK\n")(name, args...)
	}

	var log strings.Builder
	err := createMySQLDB("wp-test", "wp_user", "testpass", "localhost", &log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotSQL, "CREATE DATABASE IF NOT EXISTS `wp-test`") {
		t.Fatalf("CREATE DATABASE did not use backtick identifier: %s", gotSQL)
	}
	if !strings.Contains(gotSQL, "GRANT ALL PRIVILEGES ON `wp-test`.*") {
		t.Fatalf("GRANT did not use backtick identifier: %s", gotSQL)
	}
}

// ---------------------------------------------------------------------------
// TestEnsurePHPExtensions
// ---------------------------------------------------------------------------

func TestEnsurePHPExtensions_Windows(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	runtimeGOOS = "windows"

	var log strings.Builder
	ensurePHPExtensions(&log)
	if log.Len() != 0 {
		t.Errorf("Windows should skip, got: %s", log.String())
	}
}

func TestEnsurePHPExtensions_MysqliOK(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	runtimeGOOS = "linux"

	callNum := 0
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		callNum++
		if callNum == 1 {
			// php -r ... returns version
			return fakeCmd("8.2")(name, args...)
		}
		// php -m returns modules list
		return fakeCmd("mysqli\ncurl\n")(name, args...)
	}

	var log strings.Builder
	ensurePHPExtensions(&log)
	if !strings.Contains(log.String(), "mysqli extension: OK") {
		t.Errorf("should report OK: %s", log.String())
	}
}

func TestEnsurePHPExtensions_NoPHP(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	runtimeGOOS = "linux"

	execCommandFn = fakeCmdFail("")

	var log strings.Builder
	ensurePHPExtensions(&log)
	if !strings.Contains(log.String(), "Could not detect PHP version") {
		t.Errorf("should report PHP detection failure: %s", log.String())
	}
}

func TestEnsurePHPExtensions_InstallViaApt(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	runtimeGOOS = "linux"

	callNum := 0
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		callNum++
		if callNum == 1 {
			return fakeCmd("8.2")(name, args...)
		}
		if callNum == 2 {
			// php -m — no mysqli
			return fakeCmd("curl\ngd\n")(name, args...)
		}
		// apt install
		return fakeCmd("installed\n")(name, args...)
	}
	execLookPathFn = fakeLookPathOK // apt found

	var log strings.Builder
	ensurePHPExtensions(&log)
	if !strings.Contains(log.String(), "mysqli extension missing") {
		t.Errorf("should report missing: %s", log.String())
	}
	if !strings.Contains(log.String(), "PHP extensions installed") {
		t.Errorf("should report installed: %s", log.String())
	}
}

func TestEnsurePHPExtensions_InstallViaDnf(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	runtimeGOOS = "linux"

	callNum := 0
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		callNum++
		if callNum == 1 {
			return fakeCmd("8.1")(name, args...)
		}
		if callNum == 2 {
			return fakeCmd("curl\n")(name, args...)
		}
		return fakeCmd("installed\n")(name, args...)
	}
	execLookPathFn = func(name string) (string, error) {
		if name == "apt" {
			return "", fmt.Errorf("not found")
		}
		return "/usr/bin/dnf", nil // dnf found
	}

	var log strings.Builder
	ensurePHPExtensions(&log)
	if !strings.Contains(log.String(), "mysqli extension missing") {
		t.Errorf("should report missing: %s", log.String())
	}
}

func TestEnsurePHPExtensions_AptFails(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	runtimeGOOS = "linux"

	callNum := 0
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		callNum++
		if callNum == 1 {
			return fakeCmd("8.2")(name, args...)
		}
		if callNum == 2 {
			return fakeCmd("curl\n")(name, args...)
		}
		// apt install fails
		return fakeCmdFail("E: Unable to locate package")(name, args...)
	}
	execLookPathFn = fakeLookPathOK // apt found

	var log strings.Builder
	ensurePHPExtensions(&log)
	if !strings.Contains(log.String(), "apt install failed") {
		t.Errorf("should report apt failure: %s", log.String())
	}
}

func TestEnsurePHPExtensions_DnfFails(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	runtimeGOOS = "linux"

	callNum := 0
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		callNum++
		if callNum == 1 {
			return fakeCmd("8.1")(name, args...)
		}
		if callNum == 2 {
			return fakeCmd("curl\n")(name, args...)
		}
		return fakeCmdFail("Error: No matching packages")(name, args...)
	}
	execLookPathFn = func(name string) (string, error) {
		if name == "apt" {
			return "", fmt.Errorf("not found")
		}
		return "/usr/bin/dnf", nil
	}

	var log strings.Builder
	ensurePHPExtensions(&log)
	if !strings.Contains(log.String(), "dnf install failed") {
		t.Errorf("should report dnf failure: %s", log.String())
	}
}

// ---------------------------------------------------------------------------
// TestDownloadAndExtract
// ---------------------------------------------------------------------------

func TestDownloadAndExtract_Success(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	var httpCleanup func()
	httpGetFn, httpCleanup = fakeTarHandlerFunc("fake-tar-data")
	defer httpCleanup()

	execCommandFn = fakeCmd("")
	osMkdirAllFn = os.MkdirAll
	osStatFn = os.Stat
	osReadDirFn = os.ReadDir
	osRenameFn = os.Rename
	osRemoveAllFn = os.RemoveAll

	dir := t.TempDir()
	webRoot := filepath.Join(dir, "site")

	var log strings.Builder
	err := downloadAndExtract(webRoot, &log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "Downloaded") {
		t.Errorf("log = %s", log.String())
	}
}

func TestDownloadAndExtract_HTTPFails(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	httpGetFn = func(url string) (*http.Response, error) {
		return nil, fmt.Errorf("connection refused")
	}
	osMkdirAllFn = os.MkdirAll

	var log strings.Builder
	err := downloadAndExtract(t.TempDir(), &log)
	if err == nil {
		t.Error("expected error")
	}
}

func TestDownloadAndExtract_ExtractFails(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	var httpCleanup func()
	httpGetFn, httpCleanup = fakeTarHandlerFunc("bad")
	defer httpCleanup()

	execCommandFn = fakeCmdFail("tar: Error")
	osMkdirAllFn = os.MkdirAll

	var log strings.Builder
	err := downloadAndExtract(t.TempDir(), &log)
	if err == nil {
		t.Error("expected extract error")
	}
	if !strings.Contains(err.Error(), "extract failed") {
		t.Errorf("err = %q", err)
	}
}

func TestDownloadAndExtract_MovesFiles(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	var httpCleanup func()
	httpGetFn, httpCleanup = fakeTarHandlerFunc("fake")
	defer httpCleanup()

	parentDir := t.TempDir()
	webRoot := filepath.Join(parentDir, "site")
	os.MkdirAll(webRoot, 0755)

	// Create fake extracted "wordpress" dir in parent (what tar would produce)
	wpExtracted := filepath.Join(parentDir, "wordpress")
	os.MkdirAll(wpExtracted, 0755)
	os.WriteFile(filepath.Join(wpExtracted, "index.php"), []byte("<?php\n"), 0644)

	execCommandFn = fakeCmd("") // tar "succeeds"
	osMkdirAllFn = os.MkdirAll
	osStatFn = os.Stat
	osReadDirFn = os.ReadDir
	osRenameFn = os.Rename
	osRemoveAllFn = os.RemoveAll

	var log strings.Builder
	err := downloadAndExtract(webRoot, &log)
	if err != nil {
		t.Fatal(err)
	}

	// index.php should have been moved to webRoot
	if _, err := os.Stat(filepath.Join(webRoot, "index.php")); err != nil {
		t.Error("index.php should be in webRoot after move")
	}
}

// ---------------------------------------------------------------------------
// TestSetWordPressPermissions
// ---------------------------------------------------------------------------

func TestSetWordPressPermissions_Linux(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	runtimeGOOS = "linux"
	execCommandFn = fakeCmd("")

	var log strings.Builder
	setWordPressPermissions(t.TempDir(), &log)
	if !strings.Contains(log.String(), "Permissions set") {
		t.Errorf("log = %s", log.String())
	}
}

func TestSetWordPressPermissions_Windows(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	runtimeGOOS = "windows"

	var log strings.Builder
	setWordPressPermissions(t.TempDir(), &log)
	if log.Len() != 0 {
		t.Errorf("Windows should skip, got: %s", log.String())
	}
}

// ---------------------------------------------------------------------------
// TestWpCLI
// ---------------------------------------------------------------------------

func TestWpCLI_Success(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execCommandFn = fakeCmd("wp output")
	out, err := wpCLI(t.TempDir(), "core", "version")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "wp output") {
		t.Errorf("out = %q", out)
	}
}

func TestWpCLI_Failure(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execCommandFn = fakeCmdFail("wp: command not found")
	_, err := wpCLI(t.TempDir(), "core", "version")
	if err == nil {
		t.Error("expected error")
	}
}

func TestWpCLI_UsesResolvedBinary(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execLookPathFn = func(name string) (string, error) {
		if name == "/usr/local/bin/wp" {
			return name, nil
		}
		return "", fmt.Errorf("not found: %s", name)
	}

	var calledBinary string
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		calledBinary = name
		return fakeCmd("wp output")(name, args...)
	}

	out, err := wpCLI(t.TempDir(), "core", "version")
	if err != nil {
		t.Fatalf("wpCLI: %v", err)
	}
	if !strings.Contains(out, "wp output") {
		t.Fatalf("out = %q", out)
	}
	if calledBinary != "/usr/local/bin/wp" {
		t.Fatalf("called binary = %q, want %q", calledBinary, "/usr/local/bin/wp")
	}
}

// ---------------------------------------------------------------------------
// TestInstallRequestDefaults
// ---------------------------------------------------------------------------

func TestInstallRequestDefaults(t *testing.T) {
	req := InstallRequest{
		Domain:  "test.com",
		WebRoot: t.TempDir(),
	}
	if req.DBHost == "" {
		req.DBHost = "localhost"
	}
	if req.DBName == "" {
		req.DBName = sanitizeDBName(req.Domain)
	}
	if req.DBName != "wp_test_com" {
		t.Errorf("DBName = %q, want wp_test_com", req.DBName)
	}
}

// ---------------------------------------------------------------------------
// TestInstall_GenerateConfigFails
// ---------------------------------------------------------------------------

func TestInstall_GenerateConfigFails(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "windows"
	webRoot := t.TempDir()

	execLookPathFn = fakeLookPathFail
	execCommandFn = fakeCmd("")

	var httpCleanup func()
	httpGetFn, httpCleanup = fakeTarHandlerFunc("fake")
	defer httpCleanup()

	osStatFn = os.Stat
	osReadFileFn = os.ReadFile
	osMkdirAllFn = os.MkdirAll
	osRemoveAllFn = os.RemoveAll
	osRenameFn = os.Rename
	osReadDirFn = os.ReadDir

	// Make wp-config write fail
	osWriteFileFn = func(name string, data []byte, perm fs.FileMode) error {
		if strings.HasSuffix(name, "wp-config.php") {
			return fmt.Errorf("disk full")
		}
		return os.WriteFile(name, data, perm)
	}

	result := Install(InstallRequest{Domain: "fail.com", WebRoot: webRoot})
	if result.Status != "error" {
		t.Fatalf("expected error, got %q", result.Status)
	}
	if !strings.Contains(result.Error, "wp-config generation failed") {
		t.Errorf("error = %q", result.Error)
	}
}

// ---------------------------------------------------------------------------
// Type structure tests
// ---------------------------------------------------------------------------

func TestSiteInfoStructure(t *testing.T) {
	s := SiteInfo{
		Domain:    "test.com",
		WebRoot:   "/var/www/test",
		Version:   "6.4",
		UpdatedAt: time.Now(),
	}
	if s.Domain != "test.com" {
		t.Error("unexpected domain")
	}
}

func TestPluginInfoStructure(t *testing.T) {
	p := PluginInfo{Name: "test", Version: "1.0", Status: "active", Update: "2.0"}
	if p.Update != "2.0" {
		t.Error("unexpected update")
	}
}

func TestThemeInfoStructure(t *testing.T) {
	th := ThemeInfo{Name: "twenty", Version: "1.0", Status: "active", Update: ""}
	if th.Update != "" {
		t.Error("unexpected update")
	}
}

func TestDomainInfoStructure(t *testing.T) {
	d := DomainInfo{Host: "test.com", WebRoot: "/var/www"}
	if d.Host != "test.com" {
		t.Error("unexpected host")
	}
}

// ---------------------------------------------------------------------------
// TestEscSQL edge cases
// ---------------------------------------------------------------------------

func TestEscSQL_Empty(t *testing.T) {
	if got := escSQL(""); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestEscSQL_NoSpecial(t *testing.T) {
	if got := escSQL("plain"); got != "plain" {
		t.Errorf("got %q", got)
	}
}

// ---------------------------------------------------------------------------
// TestPermString_LinuxOwner
// ---------------------------------------------------------------------------

func TestPermString_LinuxOwner(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	runtimeGOOS = "linux"
	osStatFn = os.Stat
	execCommandFn = fakeCmd("root:root")

	dir := t.TempDir()
	path := filepath.Join(dir, "testfile")
	os.WriteFile(path, []byte("x"), 0644)

	got := permString(path)
	if !strings.Contains(got, "root:root") {
		t.Errorf("expected owner info, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// TestGenerateWPConfig_WriteFail
// ---------------------------------------------------------------------------

func TestGenerateWPConfig_WriteFail(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	osWriteFileFn = func(name string, data []byte, perm fs.FileMode) error {
		return fmt.Errorf("write failed")
	}

	err := generateWPConfig(t.TempDir(), "db", "user", "pass", "host")
	if err == nil {
		t.Error("expected error")
	}
}

// ---------------------------------------------------------------------------
// Additional edge-case coverage
// ---------------------------------------------------------------------------

func TestDetectSites_WPCLIAvailable(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "windows"
	osStatFn = os.Stat
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile
	osReadDirFn = os.ReadDir

	// WP-CLI available
	execLookPathFn = fakeLookPathOK
	jsonPlugins := `[{"name":"akismet","status":"active","version":"5.3","update":"5.4"}]`
	jsonThemes := `[{"name":"twenty","status":"active","version":"1.0","update":""}]`
	callNum := 0
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		callNum++
		// listPlugins call
		if callNum == 1 {
			return fakeCmd(jsonPlugins)(name, args...)
		}
		// listThemes call
		if callNum == 2 {
			return fakeCmd(jsonThemes)(name, args...)
		}
		return fakeCmd("")(name, args...)
	}

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "wp-config.php"), []byte("<?php\ndefine('DB_NAME','db');\ndefine('DB_USER','u');\ndefine('DB_HOST','h');\n"), 0644)
	os.MkdirAll(filepath.Join(dir, "wp-content"), 0755)

	sites := DetectSites([]DomainInfo{{Host: "cli.com", WebRoot: dir}})
	if len(sites) != 1 {
		t.Fatalf("expected 1 site, got %d", len(sites))
	}
	// DetectSites no longer calls wp-cli (fast path), so plugins come from scanPluginDirs
	// Test EnrichSite for wp-cli plugin/theme enrichment
	EnrichSite(&sites[0])
	if len(sites[0].Plugins) != 1 {
		t.Errorf("expected 1 plugin after enrich, got %d", len(sites[0].Plugins))
	}
	if sites[0].Health.PluginUpdates != 1 {
		t.Errorf("PluginUpdates = %d, want 1", sites[0].Health.PluginUpdates)
	}
}

func TestUpdateCore_ExtractFails(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execLookPathFn = fakeLookPathFail
	execCommandFn = fakeCmdFail("tar error")

	var httpCleanup func()
	httpGetFn, httpCleanup = fakeTarHandlerFunc("data")
	defer httpCleanup()
	osRemoveAllFn = os.RemoveAll

	_, err := UpdateCore(t.TempDir())
	if err == nil {
		t.Error("expected error")
	}
	if !strings.Contains(err.Error(), "extract failed") {
		t.Errorf("err = %q", err)
	}
}

func TestUpdateCore_WalkCopiesCoreFiles(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execLookPathFn = fakeLookPathFail
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile
	osMkdirAllFn = os.MkdirAll
	osRemoveAllFn = os.RemoveAll
	filepathWalkFn = filepath.Walk

	webRoot := t.TempDir()

	// Create a fake tarball server
	var httpCleanup func()
	httpGetFn, httpCleanup = fakeTarHandlerFunc("fake")
	defer httpCleanup()

	// The tar command "succeeds" and we manually create the wordpress dir it would produce
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		// When tar is called, create the wordpress dir structure
		for i, a := range args {
			if a == "-C" && i+1 < len(args) {
				tmpDir := args[i+1]
				wpDir := filepath.Join(tmpDir, "wordpress")
				os.MkdirAll(filepath.Join(wpDir, "wp-admin"), 0755)
				os.WriteFile(filepath.Join(wpDir, "index.php"), []byte("<?php\n"), 0644)
				os.WriteFile(filepath.Join(wpDir, "wp-config.php"), []byte("SKIP"), 0644)
				os.WriteFile(filepath.Join(wpDir, ".htaccess"), []byte("SKIP"), 0644)
				os.WriteFile(filepath.Join(wpDir, "wp-config-sample.php"), []byte("SKIP"), 0644)
				os.MkdirAll(filepath.Join(wpDir, "wp-content", "plugins"), 0755)
				os.WriteFile(filepath.Join(wpDir, "wp-admin", "admin.php"), []byte("<?php\n"), 0644)
				break
			}
		}
		return fakeCmd("")(name, args...)
	}

	out, err := UpdateCore(webRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "WordPress core updated") {
		t.Errorf("out = %s", out)
	}

	// Core file should exist
	if _, err := os.Stat(filepath.Join(webRoot, "index.php")); err != nil {
		t.Error("index.php should be copied")
	}
	// wp-admin/admin.php should exist
	if _, err := os.Stat(filepath.Join(webRoot, "wp-admin", "admin.php")); err != nil {
		t.Error("wp-admin/admin.php should be copied")
	}
	// wp-config.php should NOT be copied
	if _, err := os.Stat(filepath.Join(webRoot, "wp-config.php")); err == nil {
		data, _ := os.ReadFile(filepath.Join(webRoot, "wp-config.php"))
		if string(data) == "SKIP" {
			t.Error("wp-config.php should be skipped during update")
		}
	}
	// wp-content should NOT be copied (SkipDir)
	if _, err := os.Stat(filepath.Join(webRoot, "wp-content", "plugins")); err == nil {
		t.Error("wp-content should be skipped during update")
	}
}

// ---------------------------------------------------------------------------
// TestInstall_FullDefaults
// ---------------------------------------------------------------------------

func TestInstall_FullDefaults(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "windows"
	webRoot := t.TempDir()

	execLookPathFn = fakeLookPathFail
	execCommandFn = fakeCmd("")

	var httpCleanup func()
	httpGetFn, httpCleanup = fakeTarHandlerFunc("data")
	defer httpCleanup()

	osStatFn = os.Stat
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile
	osMkdirAllFn = os.MkdirAll
	osRemoveAllFn = os.RemoveAll
	osRenameFn = os.Rename
	osReadDirFn = os.ReadDir

	result := Install(InstallRequest{
		Domain:  "defaults.com",
		WebRoot: webRoot,
	})

	if result.Status != "done" {
		t.Fatalf("status = %q, error = %s", result.Status, result.Error)
	}
	// All defaults should be filled
	if result.DBName != "wp_defaults_com" {
		t.Errorf("DBName = %q", result.DBName)
	}
	if result.DBUser != "wp_defaults_com" {
		t.Errorf("DBUser = %q", result.DBUser)
	}
	if result.DBPass == "" {
		t.Error("DBPass should be auto-generated")
	}
	if result.AdminURL != "https://defaults.com/wp-admin/install.php" {
		t.Errorf("AdminURL = %q", result.AdminURL)
	}
}

// ---------------------------------------------------------------------------
// TestSetDebugMode_DisableNoRequireOnce
// ---------------------------------------------------------------------------

func TestSetDebugMode_DisableNoRequireOnce(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile

	dir := t.TempDir()
	wpConfig := filepath.Join(dir, "wp-config.php")
	os.WriteFile(wpConfig, []byte("<?php\ndefine('WP_DEBUG', true);\n"), 0644)

	err := SetDebugMode(dir, false)
	if err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(wpConfig)
	content := string(data)
	// Since there's no require_once, WP_DEBUG false should NOT be inserted
	if strings.Contains(content, "'WP_DEBUG', true") {
		t.Error("old debug should be removed")
	}
}

// ---------------------------------------------------------------------------
// TestCheckWPHealth_DebugVariant
// ---------------------------------------------------------------------------

func TestCheckWPHealth_DebugNoSpace(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadFileFn = os.ReadFile

	dir := t.TempDir()
	wpConfigPath := filepath.Join(dir, "wp-config.php")
	os.WriteFile(wpConfigPath, []byte("<?php\ndefine('WP_DEBUG',true);\n"), 0644)

	h := checkWPHealth(wpConfigPath, dir)
	if !h.Debug {
		t.Error("Debug should be true for no-space variant")
	}
}

// ---------------------------------------------------------------------------
// TestInstall_HtaccessWriteFails (covers line 163-164)
// ---------------------------------------------------------------------------

func TestInstall_HtaccessWriteFails(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	runtimeGOOS = "windows"
	webRoot := t.TempDir()

	execLookPathFn = fakeLookPathFail
	execCommandFn = fakeCmd("")

	var httpCleanup func()
	httpGetFn, httpCleanup = fakeTarHandlerFunc("fake")
	defer httpCleanup()

	osStatFn = os.Stat
	osReadFileFn = os.ReadFile
	osMkdirAllFn = os.MkdirAll
	osRemoveAllFn = os.RemoveAll
	osRenameFn = os.Rename
	osReadDirFn = os.ReadDir

	htaccessFailed := false
	muPluginFailed := false
	osWriteFileFn = func(name string, data []byte, perm fs.FileMode) error {
		if strings.HasSuffix(name, ".htaccess") {
			htaccessFailed = true
			return fmt.Errorf("permission denied")
		}
		if strings.HasSuffix(name, "uwas-rewrite.php") {
			muPluginFailed = true
			return fmt.Errorf("permission denied")
		}
		return os.WriteFile(name, data, perm)
	}

	result := Install(InstallRequest{Domain: "htfail.com", WebRoot: webRoot})
	if result.Status != "done" {
		t.Fatalf("expected done, got %q: %s", result.Status, result.Error)
	}
	if !htaccessFailed {
		t.Error("htaccess write should have been attempted")
	}
	if !muPluginFailed {
		t.Error("mu-plugin write should have been attempted")
	}
	if !strings.Contains(result.Output, "Warning: failed to create .htaccess") {
		t.Error("should warn about .htaccess failure")
	}
	if !strings.Contains(result.Output, "Warning: failed to create mu-plugin") {
		t.Error("should warn about mu-plugin failure")
	}
}

// ---------------------------------------------------------------------------
// TestDownloadAndExtract_CreateFails (covers line 234)
// ---------------------------------------------------------------------------

func TestDownloadAndExtract_CreateFails(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	var httpCleanup func()
	httpGetFn, httpCleanup = fakeTarHandlerFunc("data")
	defer httpCleanup()

	// Make MkdirAll work for webRoot but os.Create will fail for the tarPath
	// by creating a directory with the same name as the tar file
	osMkdirAllFn = os.MkdirAll
	osStatFn = os.Stat

	// Pre-create a directory where the tar file should go, causing os.Create to fail
	tarPath := filepath.Join(os.TempDir(), "wordpress-latest.tar.gz")
	os.MkdirAll(tarPath, 0755)
	defer os.RemoveAll(tarPath)

	var log strings.Builder
	err := downloadAndExtract(t.TempDir(), &log)
	if err == nil {
		t.Error("expected error from os.Create failure")
	}
}

// ---------------------------------------------------------------------------
// TestUpdateCore_WalkError (covers line 778-780, 809-811)
// ---------------------------------------------------------------------------

func TestUpdateCore_WalkError(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execLookPathFn = fakeLookPathFail
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile
	osMkdirAllFn = os.MkdirAll
	osRemoveAllFn = os.RemoveAll

	var httpCleanup func()
	httpGetFn, httpCleanup = fakeTarHandlerFunc("fake")
	defer httpCleanup()

	webRoot := t.TempDir()

	execCommandFn = func(name string, args ...string) *exec.Cmd {
		for i, a := range args {
			if a == "-C" && i+1 < len(args) {
				tmpDir := args[i+1]
				wpDir := filepath.Join(tmpDir, "wordpress")
				os.MkdirAll(wpDir, 0755)
				os.WriteFile(filepath.Join(wpDir, "readme.html"), []byte("hi"), 0644)
				break
			}
		}
		return fakeCmd("")(name, args...)
	}

	// Mock Walk to return an error on callback
	filepathWalkFn = func(root string, fn filepath.WalkFunc) error {
		// Simulate Walk calling the callback with an error
		return fn(root, nil, fmt.Errorf("walk error"))
	}

	_, err := UpdateCore(webRoot)
	if err == nil {
		t.Error("expected error from Walk")
	}
	if !strings.Contains(err.Error(), "copy failed") {
		t.Errorf("err = %q", err)
	}
}

// ---------------------------------------------------------------------------
// TestUpdateCore_WalkReadFileFails (covers line 803-805)
// ---------------------------------------------------------------------------

func TestUpdateCore_WalkReadFileFails(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execLookPathFn = fakeLookPathFail
	osMkdirAllFn = os.MkdirAll
	osRemoveAllFn = os.RemoveAll

	var httpCleanup func()
	httpGetFn, httpCleanup = fakeTarHandlerFunc("fake")
	defer httpCleanup()

	webRoot := t.TempDir()

	execCommandFn = func(name string, args ...string) *exec.Cmd {
		for i, a := range args {
			if a == "-C" && i+1 < len(args) {
				tmpDir := args[i+1]
				wpDir := filepath.Join(tmpDir, "wordpress")
				os.MkdirAll(wpDir, 0755)
				os.WriteFile(filepath.Join(wpDir, "readme.html"), []byte("hi"), 0644)
				break
			}
		}
		return fakeCmd("")(name, args...)
	}

	filepathWalkFn = filepath.Walk

	// Make osReadFileFn fail for files during Walk
	osReadFileFn = func(name string) ([]byte, error) {
		if strings.HasSuffix(name, "readme.html") {
			return nil, fmt.Errorf("read error")
		}
		return os.ReadFile(name)
	}
	osWriteFileFn = os.WriteFile

	_, err := UpdateCore(webRoot)
	if err == nil {
		t.Error("expected error from readFile failure")
	}
	if !strings.Contains(err.Error(), "copy failed") {
		t.Errorf("err = %q", err)
	}
}

// ---------------------------------------------------------------------------
// TestUpdateCore_WpContentFileSkipped (covers line 791)
// ---------------------------------------------------------------------------

func TestUpdateCore_WpContentFileSkipped(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execLookPathFn = fakeLookPathFail
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile
	osMkdirAllFn = os.MkdirAll
	osRemoveAllFn = os.RemoveAll

	var httpCleanup func()
	httpGetFn, httpCleanup = fakeTarHandlerFunc("fake")
	defer httpCleanup()

	webRoot := t.TempDir()

	execCommandFn = func(name string, args ...string) *exec.Cmd {
		for i, a := range args {
			if a == "-C" && i+1 < len(args) {
				tmpDir := args[i+1]
				wpDir := filepath.Join(tmpDir, "wordpress")
				os.MkdirAll(wpDir, 0755)
				// Create a file (not dir) at wp-content path — this triggers the non-dir
				// branch in the skipDirs check (line 791)
				os.WriteFile(filepath.Join(wpDir, "wp-content"), []byte("file-not-dir"), 0644)
				os.WriteFile(filepath.Join(wpDir, "index.php"), []byte("<?php\n"), 0644)
				break
			}
		}
		return fakeCmd("")(name, args...)
	}

	filepathWalkFn = filepath.Walk

	out, err := UpdateCore(webRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "WordPress core updated") {
		t.Error("should succeed")
	}
	// index.php should be copied, but "wp-content" file should be skipped
	if _, err := os.Stat(filepath.Join(webRoot, "index.php")); err != nil {
		t.Error("index.php should be copied")
	}
}

// ---------------------------------------------------------------------------
// TestUpdateCore_CreateFileFails (covers line 755-757)
// ---------------------------------------------------------------------------

func TestUpdateCore_CreateFileFails(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execLookPathFn = fakeLookPathFail
	osRemoveAllFn = os.RemoveAll

	var httpCleanup func()
	httpGetFn, httpCleanup = fakeTarHandlerFunc("fake")
	defer httpCleanup()

	// Pre-create a directory at the tarPath location to make os.Create fail
	tarPath := filepath.Join(os.TempDir(), "wordpress-update.tar.gz")
	os.MkdirAll(tarPath, 0755)
	defer os.RemoveAll(tarPath)

	_, err := UpdateCore(t.TempDir())
	if err == nil {
		t.Error("expected error from os.Create failure")
	}
}

// ---------------------------------------------------------------------------
// TestScanPluginDirs_DotEntries (covers line 685-686)
// ---------------------------------------------------------------------------

// fakeDirEntry implements fs.DirEntry for test mocking.
type fakeDirEntry struct {
	name  string
	isDir bool
}

func (f fakeDirEntry) Name() string               { return f.name }
func (f fakeDirEntry) IsDir() bool                { return f.isDir }
func (f fakeDirEntry) Type() fs.FileMode          { return 0 }
func (f fakeDirEntry) Info() (fs.FileInfo, error) { return nil, nil }

func TestScanPluginDirs_DotEntries(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	osReadDirFn = func(name string) ([]os.DirEntry, error) {
		return []os.DirEntry{
			fakeDirEntry{name: ".", isDir: true},
			fakeDirEntry{name: "..", isDir: true},
			fakeDirEntry{name: "real-plugin", isDir: true},
		}, nil
	}
	osReadFileFn = func(name string) ([]byte, error) {
		return nil, fmt.Errorf("not found")
	}

	plugins := scanPluginDirs("/fake")
	if len(plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(plugins))
	}
	if plugins[0].Name != "real-plugin" {
		t.Errorf("name = %q", plugins[0].Name)
	}
}

// ---------------------------------------------------------------------------
// ChangeUserPassword tests
// ---------------------------------------------------------------------------

func TestChangeUserPasswordError(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execCommandFn = fakeCmdFail("user not found")

	err := ChangeUserPassword("/var/www/wp", "admin", "newpassword123")
	if err == nil {
		t.Error("expected error for failed password change")
	}
}

// ---------------------------------------------------------------------------
// GetSecurityStatus tests
// ---------------------------------------------------------------------------

func TestGetSecurityStatus(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	// Mock file existence checks
	osStatFn = func(name string) (os.FileInfo, error) {
		return nil, nil // file exists
	}

	// Mock config file reading
	osReadFileFn = func(name string) ([]byte, error) {
		return []byte("define('DISALLOW_FILE_EDIT', true);"), nil
	}

	execCommandFn = fakeCmd(`{"core": "up to date", "plugins": 0, "themes": 0}`)

	status := GetSecurityStatus("/var/www/wp")
	// Just verify it doesn't panic - function returns a struct
	_ = status
}

// ---------------------------------------------------------------------------
// containsDefineTrue tests
// ---------------------------------------------------------------------------

func TestContainsDefineTrue(t *testing.T) {
	tests := []struct {
		content string
		key     string
		want    bool
	}{
		{"define('TEST', true);", "TEST", true},
		{"define('TEST', false);", "TEST", false},
		{"define( 'TEST' , true );", "TEST", true},
		// Note: function only matches single quotes
		{"define('OTHER', true);", "TEST", false},
		{"", "TEST", false},
	}

	for _, tt := range tests {
		got := containsDefineTrue(tt.content, tt.key)
		if got != tt.want {
			t.Errorf("containsDefineTrue(%q, %q) = %v, want %v", tt.content, tt.key, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Harden tests
// ---------------------------------------------------------------------------

func TestHardenConfigNotFound(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	osStatFn = func(name string) (os.FileInfo, error) {
		return nil, os.ErrNotExist
	}

	_, err := Harden("/var/www/wp", HardenOptions{})
	if err == nil {
		t.Error("expected error when config not found")
	}
}

// ---------------------------------------------------------------------------
// OptimizeDatabase tests
// ---------------------------------------------------------------------------

func TestOptimizeDatabase(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	execCommandFn = fakeCmd("Database optimized.")

	_, err := OptimizeDatabase("/var/www/wp")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
