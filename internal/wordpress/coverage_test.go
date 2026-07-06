package wordpress

import (
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
)

// ---------------------------------------------------------------------------
// validPluginSlug — full branch coverage
// ---------------------------------------------------------------------------

func TestValidPluginSlug(t *testing.T) {
	tests := []struct {
		plugin  string
		wantErr bool
	}{
		{"", true},                     // empty
		{"akismet", false},             // simple slug
		{"akismet/akismet.php", false}, // dir/file form
		{"-flag", true},                // leading dash → flag injection
		{"foo --activate", true},       // space → rejected by regex
		{"foo;rm -rf", true},           // illegal chars
		{"a.b_c-d", false},             // allowed punctuation
	}
	for _, tt := range tests {
		err := validPluginSlug(tt.plugin)
		if (err != nil) != tt.wantErr {
			t.Errorf("validPluginSlug(%q) err=%v, wantErr=%v", tt.plugin, err, tt.wantErr)
		}
	}
}

// ---------------------------------------------------------------------------
// validWPUsername — full branch coverage
// ---------------------------------------------------------------------------

func TestValidWPUsername(t *testing.T) {
	tests := []struct {
		username string
		wantErr  bool
	}{
		{"", true},            // empty
		{"-admin", true},      // leading dash
		{"admin user", false}, // space allowed
		{"normal", false},
		{"bad\x01ctl", true}, // control char
		{"quote\"x", true},   // double quote
		{"apos'x", true},     // single quote
		{"back`tick", true},  // backtick
	}
	for _, tt := range tests {
		err := validWPUsername(tt.username)
		if (err != nil) != tt.wantErr {
			t.Errorf("validWPUsername(%q) err=%v, wantErr=%v", tt.username, err, tt.wantErr)
		}
	}
}

// ---------------------------------------------------------------------------
// Plugin ops — validation error paths (no exec should run)
// ---------------------------------------------------------------------------

func TestPluginOps_ValidationErrors(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	// If validation fails, exec must not be invoked. Make exec panic to assert that.
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		t.Fatalf("exec should not run on validation failure: %s %v", name, args)
		return nil
	}

	ops := []struct {
		name string
		fn   func(string, string) (string, error)
	}{
		{"UpdatePlugin", UpdatePlugin},
		{"ActivatePlugin", ActivatePlugin},
		{"DeactivatePlugin", DeactivatePlugin},
		{"DeletePlugin", DeletePlugin},
	}
	for _, op := range ops {
		if _, err := op.fn("/var/www/wp", "-bad"); err == nil {
			t.Errorf("%s should reject invalid plugin", op.name)
		}
		if _, err := op.fn("/var/www/wp", ""); err == nil {
			t.Errorf("%s should reject empty plugin", op.name)
		}
	}
}

// ChangeUserPassword validation error path (no exec).
func TestChangeUserPassword_ValidationError(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		t.Fatalf("exec should not run on validation failure")
		return nil
	}
	if err := ChangeUserPassword("/var/www/wp", "-bad", "pw"); err == nil {
		t.Error("expected validation error for invalid username")
	}
}

// ChangeUserPassword success path.
func TestChangeUserPassword_Success(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	execCommandFn = fakeCmd("Success: Updated user.\n")
	osReadFileFn = func(name string) ([]byte, error) { return nil, os.ErrNotExist }
	if err := ChangeUserPassword(t.TempDir(), "admin", "newpass123"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// fetchWPChecksum — error branches
// ---------------------------------------------------------------------------

func newChecksumServer(t *testing.T, status int, body string) (string, func()) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	return srv.URL, srv.Close
}

func TestFetchWPChecksum_Branches(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	// httpGetFn error
	httpGetFn = func(string) (*http.Response, error) { return nil, fmt.Errorf("boom") }
	if got := fetchWPChecksum("http://x"); got != "" {
		t.Errorf("http error: want empty, got %q", got)
	}

	// non-200
	url, cleanup := newChecksumServer(t, 404, "nope")
	httpGetFn = func(string) (*http.Response, error) { return http.Get(url) }
	if got := fetchWPChecksum(url); got != "" {
		t.Errorf("non-200: want empty, got %q", got)
	}
	cleanup()

	// empty body → no fields
	url, cleanup = newChecksumServer(t, 200, "   ")
	httpGetFn = func(string) (*http.Response, error) { return http.Get(url) }
	if got := fetchWPChecksum(url); got != "" {
		t.Errorf("empty body: want empty, got %q", got)
	}
	cleanup()

	// wrong length
	url, cleanup = newChecksumServer(t, 200, "abc123  latest.tar.gz")
	httpGetFn = func(string) (*http.Response, error) { return http.Get(url) }
	if got := fetchWPChecksum(url); got != "" {
		t.Errorf("short sum: want empty, got %q", got)
	}
	cleanup()

	// 40 chars but not hex
	notHex := strings.Repeat("z", 40)
	url, cleanup = newChecksumServer(t, 200, notHex+"  latest.tar.gz")
	httpGetFn = func(string) (*http.Response, error) { return http.Get(url) }
	if got := fetchWPChecksum(url); got != "" {
		t.Errorf("non-hex: want empty, got %q", got)
	}
	cleanup()

	// valid sum
	valid := strings.Repeat("a", 40)
	url, cleanup = newChecksumServer(t, 200, valid+"  latest.tar.gz")
	httpGetFn = func(string) (*http.Response, error) { return http.Get(url) }
	if got := fetchWPChecksum(url); got != valid {
		t.Errorf("valid sum: want %q, got %q", valid, got)
	}
	cleanup()
}

// ---------------------------------------------------------------------------
// hashFileSHA1 — missing file and success
// ---------------------------------------------------------------------------

func TestHashFileSHA1(t *testing.T) {
	if got := hashFileSHA1(filepath.Join(t.TempDir(), "nope")); got != "" {
		t.Errorf("missing file: want empty, got %q", got)
	}
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	// SHA1("hello") = aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d
	if got := hashFileSHA1(p); got != "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d" {
		t.Errorf("unexpected hash %q", got)
	}
}

// ---------------------------------------------------------------------------
// detectSiteURL — all branches
// ---------------------------------------------------------------------------

func TestDetectSiteURL_Branches(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	// 1. WP_HOME define in config
	osReadFileFn = func(name string) ([]byte, error) {
		return []byte("define('WP_HOME', 'https://config.example.com');"), nil
	}
	if got := detectSiteURL("/var/www/x"); got != "https://config.example.com" {
		t.Errorf("WP_HOME branch: got %q", got)
	}

	// 2. WP_SITEURL define (when WP_HOME absent)
	osReadFileFn = func(name string) ([]byte, error) {
		return []byte("define('WP_SITEURL', 'https://siteurl.example.com');"), nil
	}
	if got := detectSiteURL("/var/www/x"); got != "https://siteurl.example.com" {
		t.Errorf("WP_SITEURL branch: got %q", got)
	}

	// 3. parent directory looks like a domain
	osReadFileFn = func(name string) ([]byte, error) { return nil, os.ErrNotExist }
	if got := detectSiteURL("/var/www/example.com/public_html"); got != "https://example.com" {
		t.Errorf("parent-dir branch: got %q", got)
	}

	// 4. base directory looks like a domain (parent doesn't)
	if got := detectSiteURL("/srv/mysite.org"); got != "https://mysite.org" {
		t.Errorf("base-dir branch: got %q", got)
	}

	// 5. nothing matches → empty
	if got := detectSiteURL("/srv/plainroot"); got != "" {
		t.Errorf("fallback empty: got %q", got)
	}
}

// ---------------------------------------------------------------------------
// wpCLI — stderr filtering / error diagnostics
// ---------------------------------------------------------------------------

// fakeCmdStderr produces a command that writes to stdout/stderr and may exit
// non-zero. It targets TestHelperProcessWithStderr below.
func fakeCmdStderr(stdout, stderr string, exit int) func(string, ...string) *exec.Cmd {
	return func(name string, args ...string) *exec.Cmd {
		cs := []string{"-test.run=TestHelperProcessWithStderr", "--", name}
		cs = append(cs, args...)
		cmd := exec.Command(os.Args[0], cs...)
		cmd.Env = append(os.Environ(),
			"GO_TEST_HELPER_STDERR_PROCESS=1",
			"GO_TEST_HELPER_STDOUT="+stdout,
			"GO_TEST_HELPER_STDERR="+stderr,
			fmt.Sprintf("GO_TEST_HELPER_EXIT=%d", exit),
		)
		return cmd
	}
}

// TestHelperProcessWithStderr is a subprocess entry-point that can emit both
// stdout and stderr and exit with a chosen code. Guarded by an env var so it
// never runs as a real test.
func TestHelperProcessWithStderr(t *testing.T) {
	if os.Getenv("GO_TEST_HELPER_STDERR_PROCESS") != "1" {
		return
	}
	fmt.Fprint(os.Stdout, os.Getenv("GO_TEST_HELPER_STDOUT"))
	fmt.Fprint(os.Stderr, os.Getenv("GO_TEST_HELPER_STDERR"))
	if os.Getenv("GO_TEST_HELPER_EXIT") == "1" {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestWpCLI_StderrRealError(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadFileFn = func(name string) ([]byte, error) { return nil, os.ErrNotExist }

	// stderr has a real error line + a deprecation line that should be filtered.
	execCommandFn = fakeCmdStderr("", "Deprecated: old thing\nError: real failure\n", 1)
	out, err := wpCLI(t.TempDir(), "plugin", "list")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "real failure") {
		t.Errorf("error should contain real failure: %v", err)
	}
	// stdout empty, stderr present → out returns stderr
	if !strings.Contains(out, "real failure") {
		t.Errorf("out should fall back to stderr: %q", out)
	}
}

func TestWpCLI_StderrOnlyNoise(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadFileFn = func(name string) ([]byte, error) { return nil, os.ErrNotExist }

	// Only deprecation/notice/PHP-warning noise → realErrors empty, err stays raw.
	execCommandFn = fakeCmdStderr("", "Deprecated: x\nNotice: y\nWarning: PHP foo\n\n", 1)
	_, err := wpCLI(t.TempDir(), "plugin", "list")
	if err == nil {
		t.Fatal("expected error (exit 1)")
	}
}

func TestWpCLI_SuccessWithStdout(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadFileFn = func(name string) ([]byte, error) { return nil, os.ErrNotExist }
	execCommandFn = fakeCmd("hello output")
	out, err := wpCLI(t.TempDir(), "core", "version")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "hello output") {
		t.Errorf("unexpected out %q", out)
	}
}

// wpCLI: detectSiteURL returns a URL → the --url flag is prepended.
func TestWpCLI_AppendsURLFlag(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	dir := t.TempDir()
	// wp-config with WP_HOME so detectSiteURL returns a non-empty URL.
	osReadFileFn = func(name string) ([]byte, error) {
		return []byte("define('WP_HOME', 'https://urlflag.example.com');"), nil
	}
	var gotURLFlag bool
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		for _, a := range args {
			if a == "--url=https://urlflag.example.com" {
				gotURLFlag = true
			}
		}
		return fakeCmd("ok")(name, args...)
	}
	if _, err := wpCLI(dir, "core", "version"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !gotURLFlag {
		t.Error("wpCLI should prepend --url flag when detectSiteURL succeeds")
	}
}

// downloadAndExtract: checksum mismatch returns an error.
func TestDownloadAndExtract_ChecksumMismatch(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	osMkdirAllFn = func(string, fs.FileMode) error { return nil }
	osStatFn = func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }

	// Tar server returns body "actual"; sha1 server returns a valid-format hash
	// of a DIFFERENT content so the comparison mismatches.
	wrongHash := strings.Repeat("b", 40)
	tarSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "actual-tar-bytes")
	}))
	defer tarSrv.Close()
	hashSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  latest.tar.gz\n", wrongHash)
	}))
	defer hashSrv.Close()
	httpGetFn = func(url string) (*http.Response, error) {
		if strings.HasSuffix(url, ".sha1") {
			return http.Get(hashSrv.URL)
		}
		return http.Get(tarSrv.URL)
	}

	var log strings.Builder
	err := downloadAndExtract(t.TempDir(), &log)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("expected checksum mismatch error, got %v", err)
	}
}

// TestDownloadAndExtract_Non200 is the regression for the silently-written
// error page: a non-200 download must fail immediately instead of writing an
// HTML/JSON error body into the tarball and failing later with "extract failed".
func TestDownloadAndExtract_Non200(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	osMkdirAllFn = func(string, fs.FileMode) error { return nil }

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, "<html>404 Not Found</html>")
	}))
	defer srv.Close()
	httpGetFn = func(string) (*http.Response, error) { return http.Get(srv.URL) }

	var log strings.Builder
	err := downloadAndExtract(t.TempDir(), &log)
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("expected an HTTP 404 error, got %v", err)
	}
}

// UpdateCore (fallback path): checksum mismatch returns an error.
func TestUpdateCore_ChecksumMismatch(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	// No wp-cli → fallback download path.
	execLookPathFn = fakeLookPathFail

	wrongHash := strings.Repeat("c", 40)
	tarSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "core-tar-bytes")
	}))
	defer tarSrv.Close()
	hashSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  latest.tar.gz\n", wrongHash)
	}))
	defer hashSrv.Close()
	httpGetFn = func(url string) (*http.Response, error) {
		if strings.HasSuffix(url, ".sha1") {
			return http.Get(hashSrv.URL)
		}
		return http.Get(tarSrv.URL)
	}

	_, err := UpdateCore(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("expected checksum mismatch error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// setWPConfigDefine
// ---------------------------------------------------------------------------

func TestSetWPConfigDefine(t *testing.T) {
	base := "<?php\ndefine('DISALLOW_FILE_EDIT', false);\nrequire_once ABSPATH . 'wp-settings.php';\n"

	// Replace existing define with true.
	out := setWPConfigDefine(base, "DISALLOW_FILE_EDIT", true)
	if strings.Contains(out, "false") {
		t.Errorf("old define should be removed: %q", out)
	}
	if !strings.Contains(out, "define('DISALLOW_FILE_EDIT', true);") {
		t.Errorf("new define missing: %q", out)
	}
	// Define inserted before require_once.
	if strings.Index(out, "DISALLOW_FILE_EDIT', true") > strings.Index(out, "require_once") {
		t.Error("define should appear before require_once")
	}

	// New constant set to false.
	out = setWPConfigDefine(base, "FORCE_SSL_ADMIN", false)
	if !strings.Contains(out, "define('FORCE_SSL_ADMIN', false);") {
		t.Errorf("false define missing: %q", out)
	}

	// No require_once anchor → content returned unchanged (no insertion).
	noAnchor := "<?php\n// nothing\n"
	out = setWPConfigDefine(noAnchor, "WP_DEBUG", true)
	if strings.Contains(out, "WP_DEBUG") {
		t.Errorf("should not insert without anchor: %q", out)
	}
}

// ---------------------------------------------------------------------------
// Harden — full options exercised
// ---------------------------------------------------------------------------

func boolPtr(b bool) *bool { return &b }

func TestHarden_AllOptionsEnable(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	webRoot := t.TempDir()
	configPath := filepath.Join(webRoot, "wp-config.php")
	if err := os.WriteFile(configPath, []byte("<?php\nrequire_once ABSPATH . 'wp-settings.php';\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// Use real filesystem hooks.
	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile
	osMkdirAllFn = os.MkdirAll

	opts := HardenOptions{
		DisableFileEdit: boolPtr(true),
		ForceSSLAdmin:   boolPtr(true),
		DisableWPCron:   boolPtr(true),
		DisableXMLRPC:   boolPtr(true),
		BlockDirListing: boolPtr(true),
	}
	log, err := Harden(webRoot, opts)
	if err != nil {
		t.Fatalf("Harden: %v", err)
	}
	for _, want := range []string{"DISALLOW_FILE_EDIT", "FORCE_SSL_ADMIN", "WP-Cron disabled", "XML-RPC disabled", "Directory listing blocked"} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q: %s", want, log)
		}
	}
	cfg, _ := os.ReadFile(configPath)
	if !strings.Contains(string(cfg), "define('DISABLE_WP_CRON', true);") {
		t.Errorf("wp-config not updated: %s", cfg)
	}
	muPath := filepath.Join(webRoot, "wp-content", "mu-plugins", "uwas-security.php")
	if _, err := os.Stat(muPath); err != nil {
		t.Errorf("mu-plugin not written: %v", err)
	}
	ht, _ := os.ReadFile(filepath.Join(webRoot, ".htaccess"))
	if !strings.Contains(string(ht), "-Indexes") {
		t.Errorf(".htaccess missing -Indexes: %s", ht)
	}
}

func TestHarden_AllOptionsDisable(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	webRoot := t.TempDir()
	configPath := filepath.Join(webRoot, "wp-config.php")
	os.WriteFile(configPath, []byte("<?php\ndefine('FORCE_SSL_ADMIN', true);\nrequire_once ABSPATH . 'wp-settings.php';\n"), 0600)
	// Pre-create mu-plugin and .htaccess with -Indexes so the disable paths fire.
	muDir := filepath.Join(webRoot, "wp-content", "mu-plugins")
	os.MkdirAll(muDir, 0755)
	os.WriteFile(filepath.Join(muDir, "uwas-security.php"), []byte("<?php"), 0644)
	os.WriteFile(filepath.Join(webRoot, ".htaccess"), []byte("Options -Indexes\n\nrest\n"), 0644)

	osReadFileFn = os.ReadFile
	osWriteFileFn = os.WriteFile
	osMkdirAllFn = os.MkdirAll

	opts := HardenOptions{
		DisableFileEdit: boolPtr(false),
		ForceSSLAdmin:   boolPtr(false),
		DisableWPCron:   boolPtr(false),
		DisableXMLRPC:   boolPtr(false),
		BlockDirListing: boolPtr(false),
	}
	log, err := Harden(webRoot, opts)
	if err != nil {
		t.Fatalf("Harden: %v", err)
	}
	for _, want := range []string{"File editing enabled", "SSL admin enforcement removed", "WP-Cron enabled", "XML-RPC enabled", "Directory listing allowed"} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q: %s", want, log)
		}
	}
	// mu-plugin removed.
	if _, err := os.Stat(filepath.Join(muDir, "uwas-security.php")); !os.IsNotExist(err) {
		t.Errorf("mu-plugin should be removed")
	}
	ht, _ := os.ReadFile(filepath.Join(webRoot, ".htaccess"))
	if strings.Contains(string(ht), "-Indexes") {
		t.Errorf(".htaccess should no longer contain -Indexes: %s", ht)
	}
}

// Harden: write wp-config fails after change.
func TestHarden_WriteConfigFails(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	osReadFileFn = func(name string) ([]byte, error) {
		return []byte("<?php\nrequire_once ABSPATH;\n"), nil
	}
	osWriteFileFn = func(name string, data []byte, perm fs.FileMode) error {
		return fmt.Errorf("disk full")
	}
	_, err := Harden("/var/www/wp", HardenOptions{DisableFileEdit: boolPtr(true)})
	if err == nil {
		t.Error("expected write error")
	}
}

// Harden: mu-plugin write fails.
func TestHarden_MuPluginWriteFails(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	osReadFileFn = func(name string) ([]byte, error) {
		return []byte("<?php\nrequire_once ABSPATH;\n"), nil
	}
	osMkdirAllFn = func(string, fs.FileMode) error { return nil }
	osWriteFileFn = func(name string, data []byte, perm fs.FileMode) error {
		if strings.HasSuffix(name, "uwas-security.php") {
			return fmt.Errorf("perm denied")
		}
		return nil
	}
	_, err := Harden("/var/www/wp", HardenOptions{DisableXMLRPC: boolPtr(true)})
	if err == nil {
		t.Error("expected mu-plugin write error")
	}
}

// ---------------------------------------------------------------------------
// GetSecurityStatus — exercise many branches
// ---------------------------------------------------------------------------

func TestGetSecurityStatus_FullConfig(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	webRoot := t.TempDir()
	config := `<?php
define('DISALLOW_FILE_EDIT', true);
define('WP_DEBUG', true);
define('FORCE_SSL_ADMIN', true);
define('DISABLE_WP_CRON', true);
define('WP_AUTO_UPDATE_CORE', true);
add_filter('auto_update_plugin', '__return_true');
add_filter('auto_update_theme', '__return_true');
$table_prefix = 'wp_custom_';
`
	osReadFileFn = func(name string) ([]byte, error) {
		if strings.HasSuffix(name, "wp-config.php") {
			return []byte(config), nil
		}
		if strings.HasSuffix(name, ".htaccess") {
			return []byte("Options -Indexes\n"), nil
		}
		if strings.HasSuffix(name, "version.php") {
			return []byte("$wp_version = '6.5';"), nil
		}
		if strings.HasSuffix(name, "uwas-security.php") {
			return []byte("add_filter('xmlrpc_enabled', '__return_false');"), nil
		}
		return nil, os.ErrNotExist
	}
	// wp-cli reports a PHP version after some noise.
	execCommandFn = fakeCmd("Deprecated: noise\n8.2.10\n")

	st := GetSecurityStatus(webRoot)
	if !st.FileEditDisabled || !st.DebugEnabled || !st.SSLForced || !st.WPCronDisabled {
		t.Errorf("flags wrong: %+v", st)
	}
	if st.AutoUpdatesCore != "true" {
		t.Errorf("AutoUpdatesCore=%q", st.AutoUpdatesCore)
	}
	if !st.AutoUpdatesPlugins || !st.AutoUpdatesThemes {
		t.Errorf("auto updates plugins/themes wrong: %+v", st)
	}
	if st.TablePrefix != "wp_custom_" {
		t.Errorf("TablePrefix=%q", st.TablePrefix)
	}
	if st.PHPVersion != "8.2.10" {
		t.Errorf("PHPVersion=%q", st.PHPVersion)
	}
	if !st.DirectoryListing {
		t.Error("DirectoryListing should be true")
	}
	if !st.XMLRPCDisabled {
		t.Error("XMLRPCDisabled should be true (mu-plugin)")
	}
}

func TestGetSecurityStatus_MinorAutoUpdate(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	config := "<?php\ndefine('WP_AUTO_UPDATE_CORE', 'minor');\n"
	osReadFileFn = func(name string) ([]byte, error) {
		if strings.HasSuffix(name, "wp-config.php") {
			return []byte(config), nil
		}
		return nil, os.ErrNotExist
	}
	execCommandFn = fakeCmdFail("no wp-cli output")

	st := GetSecurityStatus("/var/www/wp")
	if st.AutoUpdatesCore != "minor" {
		t.Errorf("AutoUpdatesCore=%q want minor", st.AutoUpdatesCore)
	}
}

func TestGetSecurityStatus_DefaultAutoUpdate(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	osReadFileFn = func(name string) ([]byte, error) {
		if strings.HasSuffix(name, "wp-config.php") {
			return []byte("<?php\n// plain\n"), nil
		}
		return nil, os.ErrNotExist
	}
	execCommandFn = fakeCmdFail("")

	st := GetSecurityStatus("/var/www/wp")
	if st.AutoUpdatesCore != "default" {
		t.Errorf("AutoUpdatesCore=%q want default", st.AutoUpdatesCore)
	}
}

func TestGetSecurityStatus_ConfigMissing(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadFileFn = func(name string) ([]byte, error) { return nil, os.ErrNotExist }
	st := GetSecurityStatus("/var/www/wp")
	// Returns early; only WPVersion (unknown) is set.
	if st.FileEditDisabled {
		t.Error("expected zero-value status when config missing")
	}
}

// checkXMLRPCDisabled: mu-plugin without marker, falls back to wp-config.
func TestCheckXMLRPCDisabled_Branches(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	// mu-plugin exists but lacks marker; wp-config has the filter.
	osReadFileFn = func(name string) ([]byte, error) {
		return []byte("<?php // nothing useful"), nil
	}
	if !checkXMLRPCDisabled("/wp", "add_filter('xmlrpc_enabled', ...)") {
		t.Error("should detect via wp-config")
	}

	// mu-plugin missing, wp-config also lacks → false.
	osReadFileFn = func(name string) ([]byte, error) { return nil, os.ErrNotExist }
	if checkXMLRPCDisabled("/wp", "nothing") {
		t.Error("should be false")
	}
}

// ---------------------------------------------------------------------------
// OptimizeDatabase — full path with IDs to delete
// ---------------------------------------------------------------------------

func TestOptimizeDatabase_WithData(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadFileFn = func(name string) ([]byte, error) { return nil, os.ErrNotExist }

	// For list commands return IDs; delete/optimize/transient succeed.
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		out := ""
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "list") && strings.Contains(joined, "format=ids") {
			out = "1 2 3"
		}
		return fakeCmd(out)(name, args...)
	}

	res, err := OptimizeDatabase(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RevisionsDeleted != 3 {
		t.Errorf("RevisionsDeleted=%d want 3", res.RevisionsDeleted)
	}
	if res.SpamDeleted != 3 {
		t.Errorf("SpamDeleted=%d want 3", res.SpamDeleted)
	}
	if res.TrashDeleted != 3 {
		t.Errorf("TrashDeleted=%d want 3", res.TrashDeleted)
	}
	if res.TransientsCleaned != 1 {
		t.Errorf("TransientsCleaned=%d want 1", res.TransientsCleaned)
	}
	if res.TablesOptimized != 1 {
		t.Errorf("TablesOptimized=%d want 1", res.TablesOptimized)
	}
	if !strings.Contains(res.Output, "Revisions deleted: 3") {
		t.Errorf("output missing revisions: %s", res.Output)
	}
}

// ---------------------------------------------------------------------------
// EnrichSite — with wp-cli present
// ---------------------------------------------------------------------------

func TestEnrichSite_WithWPCLI(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)

	// hasWPCLI() uses execLookPathFn.
	execLookPathFn = fakeLookPathOK
	osReadFileFn = func(name string) ([]byte, error) { return nil, os.ErrNotExist }

	execCommandFn = func(name string, args ...string) *exec.Cmd {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "plugin list") {
			return fakeCmd(`[{"name":"akismet","status":"active","version":"5.0","update":"available"}]`)(name, args...)
		}
		if strings.Contains(joined, "theme list") {
			return fakeCmd(`[{"name":"twentytwentyfour","status":"active","version":"1.0","update":"none"}]`)(name, args...)
		}
		return fakeCmd("")(name, args...)
	}

	site := &SiteInfo{WebRoot: t.TempDir()}
	EnrichSite(site)
	if len(site.Plugins) != 1 || site.Plugins[0].Name != "akismet" {
		t.Errorf("plugins not enriched: %+v", site.Plugins)
	}
	if len(site.Themes) != 1 {
		t.Errorf("themes not enriched: %+v", site.Themes)
	}
	if site.Health.PluginUpdates != 1 {
		t.Errorf("PluginUpdates=%d want 1", site.Health.PluginUpdates)
	}
	if site.Health.ThemeUpdates != 0 {
		t.Errorf("ThemeUpdates=%d want 0", site.Health.ThemeUpdates)
	}
}

func TestEnrichSite_NoWPCLI(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	execLookPathFn = fakeLookPathFail
	site := &SiteInfo{WebRoot: "/var/www/wp"}
	before := site.UpdatedAt
	EnrichSite(site)
	if site.UpdatedAt != before {
		t.Error("EnrichSite should no-op without wp-cli")
	}
}

// ---------------------------------------------------------------------------
// ListUsers — error branches
// ---------------------------------------------------------------------------

func TestListUsers_WPCLIError(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadFileFn = func(name string) ([]byte, error) { return nil, os.ErrNotExist }
	execCommandFn = fakeCmdFail("boom")
	if _, err := ListUsers("/var/www/wp"); err == nil {
		t.Error("expected wp user list error")
	}
}

func TestListUsers_BadJSON(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadFileFn = func(name string) ([]byte, error) { return nil, os.ErrNotExist }
	execCommandFn = fakeCmd("not json at all")
	if _, err := ListUsers(t.TempDir()); err == nil {
		t.Error("expected parse error")
	}
}

func TestListUsers_InvalidRoles(t *testing.T) {
	snap := saveHooks()
	defer restoreHooks(snap)
	osReadFileFn = func(name string) ([]byte, error) { return nil, os.ErrNotExist }
	// roles is an object → parseWPCLIRolesField returns error.
	execCommandFn = fakeCmd(`[{"ID":1,"user_login":"a","user_email":"a@x","roles":{"k":"v"}}]`)
	if _, err := ListUsers(t.TempDir()); err == nil {
		t.Error("expected invalid roles error")
	}
}

// ---------------------------------------------------------------------------
// parseWPCLIRolesField / parseWPCLIStringField — extra branches
// ---------------------------------------------------------------------------

func TestParseWPCLIRolesField_Branches(t *testing.T) {
	// null → empty
	if s, err := parseWPCLIRolesField([]byte("null")); err != nil || s != "" {
		t.Errorf("null: %q %v", s, err)
	}
	// string
	if s, err := parseWPCLIRolesField([]byte(`"administrator"`)); err != nil || s != "administrator" {
		t.Errorf("string: %q %v", s, err)
	}
	// array
	if s, err := parseWPCLIRolesField([]byte(`["editor","author"]`)); err != nil || s != "editor,author" {
		t.Errorf("array: %q %v", s, err)
	}
	// number → falls through to parseWPCLIStringField
	if s, err := parseWPCLIRolesField([]byte(`42`)); err != nil || s != "42" {
		t.Errorf("number: %q %v", s, err)
	}
	// object → error
	if _, err := parseWPCLIRolesField([]byte(`{"x":1}`)); err == nil {
		t.Error("object should error")
	}
}

func TestParseWPCLIStringField_Branches(t *testing.T) {
	if s, err := parseWPCLIStringField([]byte("")); err != nil || s != "" {
		t.Errorf("empty: %q %v", s, err)
	}
	if s, err := parseWPCLIStringField([]byte("null")); err != nil || s != "" {
		t.Errorf("null: %q %v", s, err)
	}
	if s, err := parseWPCLIStringField([]byte(`"hi"`)); err != nil || s != "hi" {
		t.Errorf("string: %q %v", s, err)
	}
	if s, err := parseWPCLIStringField([]byte(`7`)); err != nil || s != "7" {
		t.Errorf("number: %q %v", s, err)
	}
	if _, err := parseWPCLIStringField([]byte(`[1,2]`)); err == nil {
		t.Error("array should error")
	}
}
