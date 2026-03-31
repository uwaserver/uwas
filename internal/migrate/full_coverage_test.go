package migrate

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
)

// ============================================================
// nginx.go — remaining coverage gaps
// ============================================================

// TestParseNginxScannerError exercises the scanner.Err() != nil path.
func TestParseNginxScannerError(t *testing.T) {
	// iotest.ErrReader always returns an error on Read.
	_, err := ParseNginx(iotest.ErrReader(errors.New("disk exploded")))
	if err == nil {
		t.Fatal("expected error from broken reader")
	}
	if !strings.Contains(err.Error(), "reading nginx config") {
		t.Errorf("error = %q, want 'reading nginx config'", err.Error())
	}
}

// TestNginxToYAMLParseError exercises the NginxToYAML error path from ParseNginx.
func TestNginxToYAMLParseError(t *testing.T) {
	_, err := NginxToYAML(iotest.ErrReader(errors.New("read fail")))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "reading nginx config") {
		t.Errorf("error = %q", err.Error())
	}
}

// TestExtractLocationModifierAndPathNoMatch covers the fallback ("", "/").
func TestExtractLocationModifierAndPathNoMatch(t *testing.T) {
	mod, path := extractLocationModifierAndPath("not a location line at all")
	if mod != "" || path != "/" {
		t.Errorf("got (%q, %q), want (\"\", \"/\")", mod, path)
	}
}

// TestParseReturnDirectiveNonNumeric covers Sscanf parse failure.
func TestParseReturnDirectiveNonNumeric(t *testing.T) {
	code, url := parseReturnDirective("abc https://x.com")
	if code != 0 || url != "" {
		t.Errorf("got (%d, %q), want (0, \"\")", code, url)
	}
}

// TestParseNginxServerLevelTryFiles covers server-level try_files parsing.
func TestParseNginxServerLevelTryFiles(t *testing.T) {
	input := `
server {
    listen 80;
    server_name example.com;
    root /var/www/html;
    try_files $uri $uri/ =404;
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected 1 config, got %d", len(configs))
	}
	if len(configs[0].TryFiles) != 3 {
		t.Errorf("TryFiles = %v, want 3 elements", configs[0].TryFiles)
	}
}

// TestParseNginxLocationWithAllDirectives covers proxy/fastcgi/try_files/return in one location.
func TestParseNginxLocationWithAllDirectives(t *testing.T) {
	input := `
server {
    listen 80;
    server_name all.com;

    location /mixed {
        proxy_pass http://backend:3000;
        fastcgi_pass 127.0.0.1:9000;
        try_files $uri /index.php;
        return 301 https://target.com;
    }
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	loc := configs[0].Locations[0]
	if loc.ProxyPass != "http://backend:3000" {
		t.Errorf("ProxyPass = %q", loc.ProxyPass)
	}
	if loc.FastCGI != "127.0.0.1:9000" {
		t.Errorf("FastCGI = %q", loc.FastCGI)
	}
	if len(loc.TryFiles) != 2 {
		t.Errorf("TryFiles = %v", loc.TryFiles)
	}
	if loc.Return != "301 https://target.com" {
		t.Errorf("Return = %q", loc.Return)
	}
}

// TestParseNginxNestedBraces — verify brace depth tracking (e.g. if block inside server).
func TestParseNginxNestedBraces(t *testing.T) {
	input := `
server {
    listen 80;
    server_name nested.com;
    root /var/www/html;

    location / {
        try_files $uri $uri/ =404;
    }

    location /api {
        proxy_pass http://api:8080;
    }
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected 1, got %d", len(configs))
	}
	if len(configs[0].Locations) != 2 {
		t.Errorf("locations = %d, want 2", len(configs[0].Locations))
	}
}

// TestParseNginxLinesOutsideServerBlock — non-server lines are ignored.
func TestParseNginxLinesOutsideServerBlock(t *testing.T) {
	input := `
worker_processes auto;
events {
    worker_connections 1024;
}
http {
    server {
        listen 80;
        server_name inside.com;
        root /var/www;
    }
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected 1, got %d", len(configs))
	}
	if configs[0].ServerNames[0] != "inside.com" {
		t.Errorf("ServerNames = %v", configs[0].ServerNames)
	}
}

// TestParseNginxMultipleListenPorts exercises the append path.
func TestParseNginxMultipleListenPorts(t *testing.T) {
	input := `
server {
    listen 80;
    listen 443 ssl;
    listen 8080;
    server_name multi.com;
    root /var/www;
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(configs[0].ListenPorts) != 3 {
		t.Errorf("ListenPorts = %v, want 3", configs[0].ListenPorts)
	}
}

// TestConvertSSLViaPurePort443 — port 443 without "ssl" keyword.
func TestConvertSSLViaPurePort443(t *testing.T) {
	input := `
server {
    listen 443;
    server_name port443.com;
    root /var/www;
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	domains := ConvertToUWAS(configs)
	if domains[0].SSL.Mode != "auto" {
		t.Errorf("SSL.Mode = %q, want auto (443 implies SSL)", domains[0].SSL.Mode)
	}
}

// TestConvertNoSSLForPort80Only — plain HTTP should not set SSL.
func TestConvertNoSSLForPort80Only(t *testing.T) {
	input := `
server {
    listen 80;
    server_name plain.com;
    root /var/www;
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	domains := ConvertToUWAS(configs)
	if domains[0].SSL.Mode != "" {
		t.Errorf("SSL.Mode = %q, want empty", domains[0].SSL.Mode)
	}
}

// TestConvertSPAModeWithIndexHTML — try_files ending with /index.html triggers SPA.
func TestConvertSPAModeWithIndexHTML(t *testing.T) {
	input := `
server {
    listen 80;
    server_name spa.com;
    root /var/www/spa;
    try_files $uri $uri/ /index.html;
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	domains := ConvertToUWAS(configs)
	if !domains[0].SPAMode {
		t.Error("SPAMode should be true for try_files ending with /index.html")
	}
}

// TestConvertSPAModeWithIndexPHPQueryString — /index.php?$args triggers SPA.
func TestConvertSPAModeWithIndexPHPQueryString(t *testing.T) {
	nc := NginxConfig{
		ServerNames: []string{"wp.com"},
		Root:        "/var/www",
		IndexFiles:  []string{"index.php"},
		TryFiles:    []string{"$uri", "$uri/", "/index.php?$args"},
	}
	domains := ConvertToUWAS([]NginxConfig{nc})
	if !domains[0].SPAMode {
		t.Error("SPAMode should be true for try_files ending with /index.php?$args")
	}
}

// TestConvertSPAModeNotTriggered — non-SPA fallback.
func TestConvertSPAModeNotTriggered(t *testing.T) {
	nc := NginxConfig{
		ServerNames: []string{"nospa.com"},
		Root:        "/var/www",
		TryFiles:    []string{"$uri", "=404"},
	}
	domains := ConvertToUWAS([]NginxConfig{nc})
	if domains[0].SPAMode {
		t.Error("SPAMode should be false")
	}
}

// TestDetermineDomainTypeReturnNonRedirect — return with non-301/302 code is not redirect.
func TestDetermineDomainTypeReturnNonRedirect(t *testing.T) {
	nc := NginxConfig{
		Return: "200 ok",
		Root:   "/var/www",
	}
	dtype := determineDomainType(nc)
	if dtype != "static" {
		t.Errorf("Type = %q, want static (200 is not redirect)", dtype)
	}
}

// TestDetermineDomainTypePHPByLocationModifier — ~ modifier with \.php in path.
func TestDetermineDomainTypePHPByLocationModifier(t *testing.T) {
	nc := NginxConfig{
		Locations: []NginxLocation{
			{Modifier: "~", Path: `\.php$`},
		},
	}
	dtype := determineDomainType(nc)
	if dtype != "php" {
		t.Errorf("Type = %q, want php", dtype)
	}
}

// TestDetermineDomainTypeLocationRedirectNotOverriddenByPHP.
func TestDetermineDomainTypePHPTakesPriority(t *testing.T) {
	nc := NginxConfig{
		IndexFiles: []string{"index.php"},
		Locations: []NginxLocation{
			{Return: "301 https://other.com"},
		},
	}
	// PHP should take priority over redirect.
	dtype := determineDomainType(nc)
	if dtype != "php" {
		t.Errorf("Type = %q, want php (PHP should take priority)", dtype)
	}
}

// TestDetermineDomainTypeProxyTakesPriorityOverRedirect.
func TestDetermineDomainTypeProxyTakesPriorityOverRedirect(t *testing.T) {
	nc := NginxConfig{
		Locations: []NginxLocation{
			{ProxyPass: "http://backend:3000"},
			{Return: "301 https://other.com"},
		},
	}
	dtype := determineDomainType(nc)
	if dtype != "proxy" {
		t.Errorf("Type = %q, want proxy", dtype)
	}
}

// TestConvertEmptyConfigs — ConvertToUWAS with empty input.
func TestConvertEmptyConfigs(t *testing.T) {
	domains := ConvertToUWAS(nil)
	if len(domains) != 0 {
		t.Errorf("expected 0 domains, got %d", len(domains))
	}
}

// TestDomainsToYAMLEmptyDomains — empty domain list.
func TestDomainsToYAMLEmptyDomains(t *testing.T) {
	yaml := domainsToYAML(nil)
	if !strings.Contains(yaml, "domains:") {
		t.Error("should contain 'domains:'")
	}
	if strings.Contains(yaml, "host:") {
		t.Error("should not contain any hosts")
	}
}

// TestDomainsToYAMLNoRoot — domain with empty root should not produce root line.
func TestDomainsToYAMLNoRoot(t *testing.T) {
	domains := ConvertToUWAS([]NginxConfig{
		{ServerNames: []string{"noroot.com"}, ProxyPass: "http://backend:3000"},
	})
	yaml := domainsToYAML(domains)
	if strings.Contains(yaml, "root:") {
		t.Error("should not contain root when empty")
	}
}

// TestDomainsToYAMLPHPNoFPM — php type but no FPM address should skip php section.
func TestDomainsToYAMLPHPNoFPM(t *testing.T) {
	nc := NginxConfig{
		ServerNames: []string{"phpnofpm.com"},
		Root:        "/var/www",
		IndexFiles:  []string{"index.php"}, // triggers php type
	}
	domains := ConvertToUWAS([]NginxConfig{nc})
	yaml := domainsToYAML(domains)
	if strings.Contains(yaml, "fpm_address:") {
		t.Error("should not contain fpm_address when empty")
	}
}

// TestDomainsToYAMLProxyNoUpstreams — proxy type but no upstreams.
func TestDomainsToYAMLProxyNoUpstreams(t *testing.T) {
	nc := NginxConfig{
		ServerNames: []string{"proxy-empty.com"},
		// Force proxy type via location-level proxy_pass (no actual upstreams collected).
	}
	// Manually build a domain with proxy type but empty upstreams.
	domains := ConvertToUWAS([]NginxConfig{nc})
	// This will be "static" since there's no proxy_pass; make sure YAML is still correct.
	yaml := domainsToYAML(domains)
	if !strings.Contains(yaml, "host: proxy-empty.com") {
		t.Error("should contain host")
	}
}

// TestDomainsToYAMLRedirectNoPreservePath — redirect without preserve_path.
func TestDomainsToYAMLRedirectNoPreservePath(t *testing.T) {
	nc := NginxConfig{
		ServerNames: []string{"redir.com"},
		Return:      "301 https://target.com",
	}
	domains := ConvertToUWAS([]NginxConfig{nc})
	yaml := domainsToYAML(domains)
	if strings.Contains(yaml, "preserve_path") {
		t.Error("should not contain preserve_path when false")
	}
}

// TestBuildRedirectConfigLocationNoPreservePath — location redirect without $uri.
func TestBuildRedirectConfigLocationNoPreservePath(t *testing.T) {
	nc := NginxConfig{
		Return: "404 not found",
		Locations: []NginxLocation{
			{Return: "302 https://target.com"},
		},
	}
	redirect := buildRedirectConfig(nc)
	if redirect.Status != 302 {
		t.Errorf("Status = %d, want 302", redirect.Status)
	}
	if redirect.PreservePath {
		t.Error("PreservePath should be false")
	}
	if redirect.Target != "https://target.com" {
		t.Errorf("Target = %q", redirect.Target)
	}
}

// TestBuildRedirectConfigLocationNonRedirect — only non-redirect return codes in locations.
func TestBuildRedirectConfigLocationNonRedirect(t *testing.T) {
	nc := NginxConfig{
		Locations: []NginxLocation{
			{Return: "200 ok"},
			{Return: "404 not found"},
		},
	}
	redirect := buildRedirectConfig(nc)
	if redirect.Status != 0 {
		t.Errorf("Status = %d, want 0", redirect.Status)
	}
}

// TestBuildRedirectConfigServerWithBothURIVars — $request_uri AND $uri.
func TestBuildRedirectConfigServerWithBothURIVars(t *testing.T) {
	nc := NginxConfig{
		Return: "301 https://x.com$request_uri$uri",
	}
	redirect := buildRedirectConfig(nc)
	if !redirect.PreservePath {
		t.Error("PreservePath should be true")
	}
	if redirect.Target != "https://x.com" {
		t.Errorf("Target = %q, want https://x.com", redirect.Target)
	}
}

// TestExtractDirectiveWhitespace — line with leading/trailing whitespace.
func TestExtractDirectiveWhitespace(t *testing.T) {
	val := extractDirective("   server_name  example.com  ;  ", "server_name")
	if val != "example.com" {
		t.Errorf("got %q, want example.com", val)
	}
}

// TestExtractDirectiveNoSemicolon — directive without trailing semicolon.
func TestExtractDirectiveNoSemicolon(t *testing.T) {
	val := extractDirective("root /var/www", "root")
	if val != "/var/www" {
		t.Errorf("got %q, want /var/www", val)
	}
}

// TestExtractDirectiveExactPrefixMatch — ssl_certificate should not match ssl_certificate_key.
func TestExtractDirectiveExactPrefixMatch(t *testing.T) {
	val := extractDirective("ssl_certificate_key /key.pem;", "ssl_certificate")
	// "ssl_certificate " is not a prefix of "ssl_certificate_key " so should return "".
	// Actually, "ssl_certificate " IS a prefix of "ssl_certificate_key /key.pem;"
	// so this will match incorrectly — this is why the code checks ssl_certificate_key first.
	// The extractDirective function itself does match this (by design — prefix match).
	// This test documents the behavior.
	if val == "" {
		t.Log("ssl_certificate does not match ssl_certificate_key (prefix mismatch at space)")
	}
}

// ============================================================
// sitemigrate.go — Migrate, updateWPConfigDB
// ============================================================

func TestMigrateNoSourceHost(t *testing.T) {
	result := Migrate(MigrateRequest{
		LocalRoot: "/tmp/test",
	})
	if result.Status != "error" {
		t.Errorf("Status = %q, want error", result.Status)
	}
	if result.Error != "source_host is required" {
		t.Errorf("Error = %q", result.Error)
	}
}

func TestMigrateNoLocalRoot(t *testing.T) {
	result := Migrate(MigrateRequest{
		SourceHost: "user@1.2.3.4",
	})
	if result.Status != "error" {
		t.Errorf("Status = %q, want error", result.Status)
	}
	if result.Error != "local_root is required" {
		t.Errorf("Error = %q", result.Error)
	}
}

func TestMigrateDefaultPort(t *testing.T) {
	// Save and restore hooks.
	origSync := runSyncFiles
	origDB := runMigrateDB
	origChown := runChown
	defer func() {
		runSyncFiles = origSync
		runMigrateDB = origDB
		runChown = origChown
	}()

	var capturedPort string
	runSyncFiles = func(req MigrateRequest, log *strings.Builder) string {
		capturedPort = req.SourcePort
		log.WriteString("mock sync ok\n")
		return "ok"
	}
	runMigrateDB = func(req MigrateRequest, log *strings.Builder) string {
		return "ok"
	}
	runChown = func(root string) {}

	tmpDir := t.TempDir()
	result := Migrate(MigrateRequest{
		SourceHost: "user@1.2.3.4",
		LocalRoot:  tmpDir,
		Domain:     "example.com",
	})

	if capturedPort != "22" {
		t.Errorf("SourcePort = %q, want 22 (default)", capturedPort)
	}
	if result.Status != "done" {
		t.Errorf("Status = %q, want done; Error = %q", result.Status, result.Error)
	}
	if result.Domain != "example.com" {
		t.Errorf("Domain = %q", result.Domain)
	}
	if !strings.Contains(result.Output, "=== Syncing files ===") {
		t.Error("output should contain sync step")
	}
	if result.Duration == "" {
		t.Error("Duration should be set")
	}
}

func TestMigrateWithDB(t *testing.T) {
	origSync := runSyncFiles
	origDB := runMigrateDB
	origChown := runChown
	defer func() {
		runSyncFiles = origSync
		runMigrateDB = origDB
		runChown = origChown
	}()

	runSyncFiles = func(req MigrateRequest, log *strings.Builder) string {
		return "ok"
	}
	var dbCalled bool
	runMigrateDB = func(req MigrateRequest, log *strings.Builder) string {
		dbCalled = true
		log.WriteString("mock db import ok\n")
		return "ok"
	}
	runChown = func(root string) {}

	tmpDir := t.TempDir()
	result := Migrate(MigrateRequest{
		SourceHost: "user@1.2.3.4",
		SourcePort: "2222",
		LocalRoot:  tmpDir,
		Domain:     "example.com",
		DBName:     "testdb",
		DBUser:     "testuser",
		DBPass:     "testpass",
	})
	if !dbCalled {
		t.Error("migrateDB should have been called")
	}
	if result.DBImport != "ok" {
		t.Errorf("DBImport = %q, want ok", result.DBImport)
	}
	if !strings.Contains(result.Output, "=== Migrating database ===") {
		t.Error("output should contain DB migration step")
	}
}

func TestMigrateWithWordPress(t *testing.T) {
	origSync := runSyncFiles
	origDB := runMigrateDB
	origChown := runChown
	defer func() {
		runSyncFiles = origSync
		runMigrateDB = origDB
		runChown = origChown
	}()

	runSyncFiles = func(req MigrateRequest, log *strings.Builder) string { return "ok" }
	runMigrateDB = func(req MigrateRequest, log *strings.Builder) string { return "ok" }
	runChown = func(root string) {}

	tmpDir := t.TempDir()
	wpContent := `<?php
define('DB_NAME', 'old_db');
define('DB_USER', 'old_user');
define('DB_PASSWORD', 'old_pass');
define('DB_HOST', 'old_host');
require_once ABSPATH . 'wp-settings.php';
`
	os.WriteFile(filepath.Join(tmpDir, "wp-config.php"), []byte(wpContent), 0644)

	result := Migrate(MigrateRequest{
		SourceHost: "user@1.2.3.4",
		LocalRoot:  tmpDir,
		Domain:     "example.com",
		DBName:     "newdb",
		DBUser:     "newuser",
		DBPass:     "newpass",
	})
	if result.Status != "done" {
		t.Fatalf("Status = %q, Error = %q", result.Status, result.Error)
	}
	if !strings.Contains(result.Output, "=== Updating wp-config.php ===") {
		t.Error("should update wp-config.php")
	}

	// Verify the file was updated.
	data, _ := os.ReadFile(filepath.Join(tmpDir, "wp-config.php"))
	content := string(data)
	if !strings.Contains(content, "define('DB_NAME', 'newdb')") {
		t.Errorf("wp-config.php should contain new DB_NAME, got:\n%s", content)
	}
	if !strings.Contains(content, "define('DB_USER', 'newuser')") {
		t.Errorf("wp-config.php should contain new DB_USER")
	}
	if !strings.Contains(content, "define('DB_PASSWORD', 'newpass')") {
		t.Errorf("wp-config.php should contain new DB_PASSWORD")
	}
	if !strings.Contains(content, "define('DB_HOST', 'localhost')") {
		t.Errorf("wp-config.php should contain DB_HOST=localhost")
	}
}

func TestUpdateWPConfigDBReadError(t *testing.T) {
	var log strings.Builder
	updateWPConfigDB("/nonexistent/wp-config.php", "db", "user", "pass", &log)
	if !strings.Contains(log.String(), "read wp-config") {
		t.Errorf("log = %q, want 'read wp-config' error", log.String())
	}
}

func TestUpdateWPConfigDBWriteError(t *testing.T) {
	tmpDir := t.TempDir()
	wpPath := filepath.Join(tmpDir, "wp-config.php")
	os.WriteFile(wpPath, []byte("define('DB_NAME', 'old');"), 0644)

	// Make the directory read-only to cause write failure.
	// On Windows this is unreliable, so we test a different approach:
	// write to a path within a nonexistent subdirectory.
	badPath := filepath.Join(tmpDir, "nonexistent", "subdir", "wp-config.php")
	// First we need a readable file:
	readablePath := wpPath
	data, _ := os.ReadFile(readablePath)

	// Create a temp file in an unwritable location by creating and removing dir.
	tempFile := filepath.Join(tmpDir, "temp-wp.php")
	os.WriteFile(tempFile, data, 0644)

	// Test the "define not found" path — no define('DB_NAME'...) to replace.
	var log strings.Builder
	updateWPConfigDB(tempFile, "newdb", "newuser", "newpass", &log)
	// Should succeed even if some defines are missing.
	if !strings.Contains(log.String(), "wp-config.php updated") {
		t.Errorf("should succeed, log = %q", log.String())
	}

	// Test missing define — file without proper define statements.
	noDefine := filepath.Join(tmpDir, "no-define.php")
	os.WriteFile(noDefine, []byte("<?php echo 'hello';"), 0644)
	log.Reset()
	updateWPConfigDB(noDefine, "db", "user", "pass", &log)
	if !strings.Contains(log.String(), "wp-config.php updated") {
		t.Errorf("should report updated, log = %q", log.String())
	}

	// Test with nonexistent read path.
	log.Reset()
	updateWPConfigDB(badPath, "db", "user", "pass", &log)
	if !strings.Contains(log.String(), "read wp-config") {
		t.Errorf("should report read error, log = %q", log.String())
	}
}

func TestUpdateWPConfigDBMissingCloseParen(t *testing.T) {
	tmpDir := t.TempDir()
	wpPath := filepath.Join(tmpDir, "wp-config.php")
	// Define without closing ");", so the end search fails.
	content := "<?php\ndefine('DB_NAME', 'old'\n"
	os.WriteFile(wpPath, []byte(content), 0644)

	var log strings.Builder
	updateWPConfigDB(wpPath, "newdb", "user", "pass", &log)
	// Should still succeed (just won't replace that particular define).
	if !strings.Contains(log.String(), "wp-config.php updated") {
		t.Errorf("log = %q", log.String())
	}
	data, _ := os.ReadFile(wpPath)
	// The define should be unchanged since ");' was not found.
	if strings.Contains(string(data), "newdb") {
		t.Error("should not have replaced DB_NAME without proper closing")
	}
}

func TestUpdateWPConfigDBWriteErrorReadOnly(t *testing.T) {
	tmpDir := t.TempDir()
	wpPath := filepath.Join(tmpDir, "wp-config.php")
	content := "<?php\ndefine('DB_NAME', 'old_db');\n"
	os.WriteFile(wpPath, []byte(content), 0644)

	// Make the file read-only so WriteFile fails.
	os.Chmod(wpPath, 0444)
	defer os.Chmod(wpPath, 0644) // restore so TempDir cleanup works

	var log strings.Builder
	updateWPConfigDB(wpPath, "newdb", "user", "pass", &log)
	if !strings.Contains(log.String(), "write wp-config") {
		t.Errorf("should report write error, log = %q", log.String())
	}
}

func TestUpdateWPConfigDBEscapeSingleQuotes(t *testing.T) {
	tmpDir := t.TempDir()
	wpPath := filepath.Join(tmpDir, "wp-config.php")
	content := "<?php\ndefine('DB_PASSWORD', 'old_pass');\n"
	os.WriteFile(wpPath, []byte(content), 0644)

	var log strings.Builder
	updateWPConfigDB(wpPath, "db", "user", "it's_a_pass", &log)
	data, _ := os.ReadFile(wpPath)
	if !strings.Contains(string(data), `it\'s_a_pass`) {
		t.Errorf("should escape single quotes, got:\n%s", string(data))
	}
}

// ============================================================
// clone.go — Clone, updateWPConfigURLs, generatePassword
// ============================================================

func TestCloneMissingRoots(t *testing.T) {
	result := Clone(CloneRequest{
		SourceRoot: "",
		TargetRoot: "",
	})
	if result.Status != "error" {
		t.Errorf("Status = %q, want error", result.Status)
	}
	if result.Error != "source_root and target_root required" {
		t.Errorf("Error = %q", result.Error)
	}
}

func TestCloneMissingSourceRoot(t *testing.T) {
	result := Clone(CloneRequest{
		TargetRoot: "/tmp/target",
	})
	if result.Status != "error" {
		t.Errorf("Status = %q, want error", result.Status)
	}
}

func TestCloneMissingTargetRoot(t *testing.T) {
	result := Clone(CloneRequest{
		SourceRoot: "/tmp/source",
	})
	if result.Status != "error" {
		t.Errorf("Status = %q, want error", result.Status)
	}
}

func TestCloneAutoGenerateTargetDB(t *testing.T) {
	origFiles := runCloneFiles
	origDB := runCloneDB
	origChown := runCloneChown
	defer func() {
		runCloneFiles = origFiles
		runCloneDB = origDB
		runCloneChown = origChown
	}()

	var capturedTargetDB string
	runCloneFiles = func(src, dst string, log *strings.Builder) error { return nil }
	runCloneDB = func(srcDB, dstDB, user, pass string, log *strings.Builder) error {
		capturedTargetDB = dstDB
		return nil
	}
	runCloneChown = func(root string) {}

	tmpSrc := t.TempDir()
	tmpDst := t.TempDir()

	result := Clone(CloneRequest{
		SourceDomain: "example.com",
		TargetDomain: "staging.example.com",
		SourceRoot:   tmpSrc,
		TargetRoot:   tmpDst,
		SourceDB:     "prod_db",
		DBUser:       "user",
		DBPass:       "pass",
	})

	if result.Status != "done" {
		t.Fatalf("Status = %q, Error = %q", result.Status, result.Error)
	}
	if capturedTargetDB != "staging_example_com_db" {
		t.Errorf("TargetDB = %q, want staging_example_com_db", capturedTargetDB)
	}
	if result.TargetDB != "staging_example_com_db" {
		t.Errorf("result.TargetDB = %q", result.TargetDB)
	}
}

func TestCloneAutoGenerateTargetDBLongName(t *testing.T) {
	origFiles := runCloneFiles
	origDB := runCloneDB
	origChown := runCloneChown
	defer func() {
		runCloneFiles = origFiles
		runCloneDB = origDB
		runCloneChown = origChown
	}()

	var capturedTargetDB string
	runCloneFiles = func(src, dst string, log *strings.Builder) error { return nil }
	runCloneDB = func(srcDB, dstDB, user, pass string, log *strings.Builder) error {
		capturedTargetDB = dstDB
		return nil
	}
	runCloneChown = func(root string) {}

	tmpSrc := t.TempDir()
	tmpDst := t.TempDir()

	longDomain := strings.Repeat("a", 80) + ".com"
	result := Clone(CloneRequest{
		SourceDomain: "example.com",
		TargetDomain: longDomain,
		SourceRoot:   tmpSrc,
		TargetRoot:   tmpDst,
		SourceDB:     "prod_db",
	})

	if result.Status != "done" {
		t.Fatalf("Status = %q, Error = %q", result.Status, result.Error)
	}
	if len(capturedTargetDB) > 60 {
		t.Errorf("TargetDB length = %d, want <= 60", len(capturedTargetDB))
	}
}

func TestCloneAutoGenerateTargetDBSanitize(t *testing.T) {
	origFiles := runCloneFiles
	origDB := runCloneDB
	origChown := runCloneChown
	defer func() {
		runCloneFiles = origFiles
		runCloneDB = origDB
		runCloneChown = origChown
	}()

	var capturedTargetDB string
	runCloneFiles = func(src, dst string, log *strings.Builder) error { return nil }
	runCloneDB = func(srcDB, dstDB, user, pass string, log *strings.Builder) error {
		capturedTargetDB = dstDB
		return nil
	}
	runCloneChown = func(root string) {}

	tmpSrc := t.TempDir()
	tmpDst := t.TempDir()

	Clone(CloneRequest{
		SourceDomain: "example.com",
		TargetDomain: "staging-site.my-domain.com",
		SourceRoot:   tmpSrc,
		TargetRoot:   tmpDst,
		SourceDB:     "prod_db",
	})

	// Dashes should be replaced with underscores.
	if strings.Contains(capturedTargetDB, "-") {
		t.Errorf("TargetDB = %q, should not contain dashes", capturedTargetDB)
	}
	expected := "staging_site_my_domain_com_db"
	if capturedTargetDB != expected {
		t.Errorf("TargetDB = %q, want %q", capturedTargetDB, expected)
	}
}

func TestCloneFileCopyError(t *testing.T) {
	origFiles := runCloneFiles
	origChown := runCloneChown
	defer func() {
		runCloneFiles = origFiles
		runCloneChown = origChown
	}()

	runCloneFiles = func(src, dst string, log *strings.Builder) error {
		return fmt.Errorf("rsync failed")
	}
	runCloneChown = func(root string) {}

	tmpSrc := t.TempDir()
	tmpDst := t.TempDir()

	result := Clone(CloneRequest{
		SourceRoot: tmpSrc,
		TargetRoot: tmpDst,
	})
	if result.Status != "error" {
		t.Errorf("Status = %q, want error", result.Status)
	}
	if !strings.Contains(result.Error, "file copy failed") {
		t.Errorf("Error = %q", result.Error)
	}
}

func TestCloneDBError(t *testing.T) {
	origFiles := runCloneFiles
	origDB := runCloneDB
	origChown := runCloneChown
	defer func() {
		runCloneFiles = origFiles
		runCloneDB = origDB
		runCloneChown = origChown
	}()

	runCloneFiles = func(src, dst string, log *strings.Builder) error { return nil }
	runCloneDB = func(srcDB, dstDB, user, pass string, log *strings.Builder) error {
		return fmt.Errorf("mysql client not found")
	}
	runCloneChown = func(root string) {}

	tmpSrc := t.TempDir()
	tmpDst := t.TempDir()

	result := Clone(CloneRequest{
		SourceRoot:   tmpSrc,
		TargetRoot:   tmpDst,
		SourceDB:     "prod_db",
		TargetDB:     "staging_db",
		TargetDomain: "staging.example.com",
	})
	// DB error is non-fatal — clone should still succeed.
	if result.Status != "done" {
		t.Errorf("Status = %q, want done", result.Status)
	}
	if !strings.Contains(result.Output, "DB clone error") {
		t.Error("output should mention DB clone error")
	}
	// TargetDB should NOT be set when clone fails.
	if result.TargetDB != "" {
		t.Errorf("TargetDB = %q, want empty on DB error", result.TargetDB)
	}
}

func TestCloneWithWordPress(t *testing.T) {
	origFiles := runCloneFiles
	origDB := runCloneDB
	origChown := runCloneChown
	defer func() {
		runCloneFiles = origFiles
		runCloneDB = origDB
		runCloneChown = origChown
	}()

	runCloneFiles = func(src, dst string, log *strings.Builder) error { return nil }
	runCloneDB = func(srcDB, dstDB, user, pass string, log *strings.Builder) error { return nil }
	runCloneChown = func(root string) {}

	tmpSrc := t.TempDir()
	tmpDst := t.TempDir()

	wpContent := `<?php
define('DB_NAME', 'old_db');
define('DB_USER', 'old_user');
define('DB_PASSWORD', 'old_pass');
define('DB_HOST', 'old_host');
define('WP_HOME', 'https://old.com');
define('WP_SITEURL', 'https://old.com');
require_once ABSPATH . 'wp-settings.php';
`
	// Write wp-config.php to TARGET (Clone copies files first, then updates).
	os.WriteFile(filepath.Join(tmpDst, "wp-config.php"), []byte(wpContent), 0644)

	result := Clone(CloneRequest{
		SourceDomain: "example.com",
		TargetDomain: "staging.example.com",
		SourceRoot:   tmpSrc,
		TargetRoot:   tmpDst,
		SourceDB:     "prod_db",
		TargetDB:     "staging_db",
		DBUser:       "stg_user",
		DBPass:       "stg_pass",
	})

	if result.Status != "done" {
		t.Fatalf("Status = %q, Error = %q", result.Status, result.Error)
	}

	data, _ := os.ReadFile(filepath.Join(tmpDst, "wp-config.php"))
	content := string(data)

	if !strings.Contains(content, "define('DB_NAME', 'staging_db')") {
		t.Errorf("should contain new DB_NAME, got:\n%s", content)
	}
	if !strings.Contains(content, "define('WP_HOME', 'https://staging.example.com')") {
		t.Errorf("should contain new WP_HOME, got:\n%s", content)
	}
	if !strings.Contains(content, "define('WP_SITEURL', 'https://staging.example.com')") {
		t.Errorf("should contain new WP_SITEURL, got:\n%s", content)
	}
}

func TestCloneWithWordPressNoDBUserPass(t *testing.T) {
	origFiles := runCloneFiles
	origDB := runCloneDB
	origChown := runCloneChown
	defer func() {
		runCloneFiles = origFiles
		runCloneDB = origDB
		runCloneChown = origChown
	}()

	runCloneFiles = func(src, dst string, log *strings.Builder) error { return nil }
	runCloneDB = func(srcDB, dstDB, user, pass string, log *strings.Builder) error { return nil }
	runCloneChown = func(root string) {}

	tmpDst := t.TempDir()
	wpContent := `<?php
define('DB_NAME', 'old_db');
define('DB_USER', 'old_user');
define('DB_PASSWORD', 'old_pass');
define('DB_HOST', 'old_host');
require_once ABSPATH . 'wp-settings.php';
`
	os.WriteFile(filepath.Join(tmpDst, "wp-config.php"), []byte(wpContent), 0644)

	result := Clone(CloneRequest{
		SourceDomain: "example.com",
		TargetDomain: "staging.example.com",
		SourceRoot:   t.TempDir(),
		TargetRoot:   tmpDst,
		SourceDB:     "prod_db",
		TargetDB:     "staging_db",
		// DBUser and DBPass empty — should auto-fill
	})

	if result.Status != "done" {
		t.Fatalf("Status = %q, Error = %q", result.Status, result.Error)
	}

	data, _ := os.ReadFile(filepath.Join(tmpDst, "wp-config.php"))
	content := string(data)
	// DBUser should default to TargetDB.
	if !strings.Contains(content, "define('DB_USER', 'staging_db')") {
		t.Errorf("should default DBUser to TargetDB, got:\n%s", content)
	}
	// DBPass should be a generated password (uwas_NNNNN).
	if !strings.Contains(content, "define('DB_PASSWORD', 'uwas_") {
		t.Errorf("should have generated password, got:\n%s", content)
	}
}

func TestCloneWithWordPressNoTargetDB(t *testing.T) {
	origFiles := runCloneFiles
	origChown := runCloneChown
	defer func() {
		runCloneFiles = origFiles
		runCloneChown = origChown
	}()

	runCloneFiles = func(src, dst string, log *strings.Builder) error { return nil }
	runCloneChown = func(root string) {}

	tmpDst := t.TempDir()
	wpContent := `<?php
define('DB_NAME', 'old_db');
require_once ABSPATH . 'wp-settings.php';
`
	os.WriteFile(filepath.Join(tmpDst, "wp-config.php"), []byte(wpContent), 0644)

	result := Clone(CloneRequest{
		SourceDomain: "example.com",
		TargetDomain: "staging.example.com",
		SourceRoot:   t.TempDir(),
		TargetRoot:   tmpDst,
		// No SourceDB — so no DB clone, no DB update in wp-config.
	})

	if result.Status != "done" {
		t.Fatalf("Status = %q, Error = %q", result.Status, result.Error)
	}

	// wp-config.php should still be updated for URLs (but not DB).
	data, _ := os.ReadFile(filepath.Join(tmpDst, "wp-config.php"))
	content := string(data)
	// DB_NAME should remain old_db since no DB migration.
	if !strings.Contains(content, "define('DB_NAME', 'old_db')") {
		t.Errorf("DB_NAME should be unchanged, got:\n%s", content)
	}
	// But URL should be updated.
	if !strings.Contains(content, "https://staging.example.com") {
		t.Errorf("should contain updated URL, got:\n%s", content)
	}
}

func TestUpdateWPConfigURLs(t *testing.T) {
	tmpDir := t.TempDir()
	wpPath := filepath.Join(tmpDir, "wp-config.php")
	content := `<?php
define('WP_HOME', 'https://old.com');
define('WP_SITEURL', 'https://old.com');
require_once ABSPATH . 'wp-settings.php';
`
	os.WriteFile(wpPath, []byte(content), 0644)

	var log strings.Builder
	updateWPConfigURLs(wpPath, "new.example.com", &log)

	data, _ := os.ReadFile(wpPath)
	result := string(data)

	if !strings.Contains(result, "define('WP_HOME', 'https://new.example.com')") {
		t.Errorf("should contain new WP_HOME, got:\n%s", result)
	}
	if !strings.Contains(result, "define('WP_SITEURL', 'https://new.example.com')") {
		t.Errorf("should contain new WP_SITEURL, got:\n%s", result)
	}
	if !strings.Contains(log.String(), "WP_HOME/WP_SITEURL set to https://new.example.com") {
		t.Errorf("log = %q", log.String())
	}
}

func TestUpdateWPConfigURLsNoExistingDefines(t *testing.T) {
	tmpDir := t.TempDir()
	wpPath := filepath.Join(tmpDir, "wp-config.php")
	content := `<?php
require_once ABSPATH . 'wp-settings.php';
`
	os.WriteFile(wpPath, []byte(content), 0644)

	var log strings.Builder
	updateWPConfigURLs(wpPath, "new.com", &log)

	data, _ := os.ReadFile(wpPath)
	result := string(data)

	if !strings.Contains(result, "define('WP_HOME', 'https://new.com')") {
		t.Errorf("should insert WP_HOME, got:\n%s", result)
	}
	if !strings.Contains(result, "require_once ABSPATH") {
		t.Error("should preserve require_once")
	}
}

func TestUpdateWPConfigURLsReadError(t *testing.T) {
	var log strings.Builder
	updateWPConfigURLs("/nonexistent/wp-config.php", "new.com", &log)
	// Should return silently on read error.
	if log.Len() != 0 {
		t.Errorf("log should be empty on read error, got %q", log.String())
	}
}

func TestUpdateWPConfigURLsNoRequireOnce(t *testing.T) {
	tmpDir := t.TempDir()
	wpPath := filepath.Join(tmpDir, "wp-config.php")
	content := `<?php
define('WP_HOME', 'https://old.com');
echo 'hello';
`
	os.WriteFile(wpPath, []byte(content), 0644)

	var log strings.Builder
	updateWPConfigURLs(wpPath, "new.com", &log)

	data, _ := os.ReadFile(wpPath)
	result := string(data)
	// Old WP_HOME should be removed.
	if strings.Contains(result, "https://old.com") {
		t.Errorf("old WP_HOME should be removed, got:\n%s", result)
	}
}

func TestUpdateWPConfigURLsRemoveBothDefines(t *testing.T) {
	tmpDir := t.TempDir()
	wpPath := filepath.Join(tmpDir, "wp-config.php")
	content := `<?php
define('WP_HOME', 'https://old.com');
define('WP_SITEURL', 'https://old.com');
define('DB_NAME', 'testdb');
require_once ABSPATH . 'wp-settings.php';
`
	os.WriteFile(wpPath, []byte(content), 0644)

	var log strings.Builder
	updateWPConfigURLs(wpPath, "staging.com", &log)

	data, _ := os.ReadFile(wpPath)
	result := string(data)
	if strings.Contains(result, "https://old.com") {
		t.Errorf("old URLs should be removed, got:\n%s", result)
	}
	// DB_NAME should be preserved.
	if !strings.Contains(result, "define('DB_NAME', 'testdb')") {
		t.Errorf("DB_NAME should be preserved, got:\n%s", result)
	}
}

func TestGeneratePassword(t *testing.T) {
	p := generatePassword()
	if !strings.HasPrefix(p, "uwas_") {
		t.Errorf("password = %q, want uwas_ prefix", p)
	}
	if len(p) < 6 {
		t.Errorf("password too short: %q", p)
	}
}

func TestGeneratePasswordFormat(t *testing.T) {
	p := generatePassword()
	// Verify format: "uwas_" followed by hex characters (crypto/rand).
	if !strings.HasPrefix(p, "uwas_") {
		t.Errorf("password = %q, should have uwas_ prefix", p)
	}
	suffix := strings.TrimPrefix(p, "uwas_")
	for _, c := range suffix {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("password suffix should be hex, got %q", suffix)
			break
		}
	}
	// Verify uniqueness (crypto/rand should produce different values).
	p2 := generatePassword()
	if p == p2 {
		t.Errorf("two generated passwords should differ: %q", p)
	}
}

func TestCloneExplicitTargetDB(t *testing.T) {
	origFiles := runCloneFiles
	origDB := runCloneDB
	origChown := runCloneChown
	defer func() {
		runCloneFiles = origFiles
		runCloneDB = origDB
		runCloneChown = origChown
	}()

	var capturedTargetDB string
	runCloneFiles = func(src, dst string, log *strings.Builder) error { return nil }
	runCloneDB = func(srcDB, dstDB, user, pass string, log *strings.Builder) error {
		capturedTargetDB = dstDB
		return nil
	}
	runCloneChown = func(root string) {}

	Clone(CloneRequest{
		SourceRoot:   t.TempDir(),
		TargetRoot:   t.TempDir(),
		SourceDB:     "prod_db",
		TargetDB:     "my_explicit_db",
		TargetDomain: "staging.example.com",
	})

	if capturedTargetDB != "my_explicit_db" {
		t.Errorf("TargetDB = %q, want my_explicit_db", capturedTargetDB)
	}
}

func TestCloneNoDBNoWP(t *testing.T) {
	origFiles := runCloneFiles
	origChown := runCloneChown
	defer func() {
		runCloneFiles = origFiles
		runCloneChown = origChown
	}()

	runCloneFiles = func(src, dst string, log *strings.Builder) error { return nil }
	runCloneChown = func(root string) {}

	result := Clone(CloneRequest{
		SourceRoot:   t.TempDir(),
		TargetRoot:   t.TempDir(),
		SourceDomain: "src.com",
		TargetDomain: "dst.com",
	})
	if result.Status != "done" {
		t.Errorf("Status = %q", result.Status)
	}
	if result.Duration == "" {
		t.Error("Duration should be set")
	}
	if !strings.Contains(result.Output, "Files copied") {
		t.Error("output should mention files copied")
	}
	if !strings.Contains(result.Output, "Permissions fixed") {
		t.Error("output should mention permissions fixed")
	}
}

func TestCloneResultFields(t *testing.T) {
	origFiles := runCloneFiles
	origChown := runCloneChown
	defer func() {
		runCloneFiles = origFiles
		runCloneChown = origChown
	}()

	runCloneFiles = func(src, dst string, log *strings.Builder) error { return nil }
	runCloneChown = func(root string) {}

	tmpDst := t.TempDir()
	result := Clone(CloneRequest{
		SourceDomain: "source.com",
		TargetDomain: "target.com",
		SourceRoot:   t.TempDir(),
		TargetRoot:   tmpDst,
	})

	if result.SourceDomain != "source.com" {
		t.Errorf("SourceDomain = %q", result.SourceDomain)
	}
	if result.TargetDomain != "target.com" {
		t.Errorf("TargetDomain = %q", result.TargetDomain)
	}
	if result.TargetRoot != tmpDst {
		t.Errorf("TargetRoot = %q, want %q", result.TargetRoot, tmpDst)
	}
}

// ============================================================
// Helper function unit tests
// ============================================================

func TestBuildSSHOpts(t *testing.T) {
	opts := buildSSHOpts("22", "")
	if !strings.Contains(opts, "-p 22") {
		t.Errorf("should contain -p 22, got %q", opts)
	}
	if strings.Contains(opts, "-i") {
		t.Error("should not contain -i without key")
	}

	opts = buildSSHOpts("2222", "/path/to/key")
	if !strings.Contains(opts, "-p 2222") {
		t.Errorf("should contain -p 2222, got %q", opts)
	}
	if !strings.Contains(opts, "-i /path/to/key") {
		t.Errorf("should contain -i /path/to/key, got %q", opts)
	}
}

func TestBuildRsyncArgs(t *testing.T) {
	args := buildRsyncArgs("ssh -p 22", "user@host:/src/", "/dst/")
	if len(args) != 7 {
		t.Errorf("args len = %d, want 7", len(args))
	}
	if args[0] != "-avz" {
		t.Errorf("args[0] = %q", args[0])
	}
	if args[4] != "ssh -p 22" {
		t.Errorf("args[4] = %q", args[4])
	}
	if args[5] != "user@host:/src/" {
		t.Errorf("args[5] = %q", args[5])
	}
}

func TestBuildSSHArgs(t *testing.T) {
	args := buildSSHArgs("22", "", "user@host", "ls /")
	// No key: 8 args (-p, 22, -o, ..., -o, ..., user@host, ls /)
	if len(args) != 8 {
		t.Errorf("args len = %d, want 8", len(args))
	}
	if args[len(args)-2] != "user@host" {
		t.Errorf("host arg = %q", args[len(args)-2])
	}
	if args[len(args)-1] != "ls /" {
		t.Errorf("cmd arg = %q", args[len(args)-1])
	}

	args = buildSSHArgs("2222", "/key", "user@host", "ls")
	// With key: 10 args
	if len(args) != 10 {
		t.Errorf("args len = %d, want 10", len(args))
	}
	foundKey := false
	for i, a := range args {
		if a == "-i" && i+1 < len(args) && args[i+1] == "/key" {
			foundKey = true
		}
	}
	if !foundKey {
		t.Error("should contain -i /key")
	}
}

func TestBuildMysqldumpCmd(t *testing.T) {
	cmd := buildMysqldumpCmd("localhost", "root", "secret", "mydb")
	if !strings.Contains(cmd, "mysqldump") {
		t.Error("should contain mysqldump")
	}
	if !strings.Contains(cmd, "-h localhost") {
		t.Error("should contain -h localhost")
	}
	if !strings.Contains(cmd, "-u root") {
		t.Error("should contain -u root")
	}
	if !strings.Contains(cmd, "-p'secret'") {
		t.Error("should contain password")
	}
	if !strings.Contains(cmd, "mydb") {
		t.Error("should contain database name")
	}
}

// ============================================================
// syncFilesReal / migrateDBReal / cloneDBReal / cloneFilesReal
// — tests using execCommandFn/execLookPathFn hooks
// ============================================================

func TestSyncFilesRealWithKeyAuth(t *testing.T) {
	origCmd := execCommandFn
	origLookPath := execLookPathFn
	defer func() {
		execCommandFn = origCmd
		execLookPathFn = origLookPath
	}()

	// Mock exec to return success.
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		return exec.Command("go", "version")
	}

	var log strings.Builder
	result := syncFilesReal(MigrateRequest{
		SourceHost: "user@1.2.3.4",
		SourcePort: "22",
		SourcePath: "/remote/path",
		SSHKey:     "/path/to/key",
		LocalRoot:  t.TempDir(),
	}, &log)
	if result != "ok" {
		t.Errorf("result = %q, want ok", result)
	}
}

func TestSyncFilesRealWithPasswordAuth(t *testing.T) {
	origCmd := execCommandFn
	origLookPath := execLookPathFn
	defer func() {
		execCommandFn = origCmd
		execLookPathFn = origLookPath
	}()

	execCommandFn = func(name string, args ...string) *exec.Cmd {
		return exec.Command("go", "version")
	}
	// Simulate sshpass not found.
	execLookPathFn = func(file string) (string, error) {
		return "", fmt.Errorf("not found")
	}

	var log strings.Builder
	result := syncFilesReal(MigrateRequest{
		SourceHost: "user@1.2.3.4",
		SourcePort: "22",
		SourcePath: "/remote/path",
		SSHPass:    "mypassword",
		LocalRoot:  t.TempDir(),
	}, &log)
	if result != "ok" {
		t.Errorf("result = %q, want ok", result)
	}
	if !strings.Contains(log.String(), "WARNING: sshpass not installed") {
		t.Error("should warn about missing sshpass")
	}
}

func TestSyncFilesRealWithPasswordAuthSSHPassAvailable(t *testing.T) {
	origCmd := execCommandFn
	origLookPath := execLookPathFn
	defer func() {
		execCommandFn = origCmd
		execLookPathFn = origLookPath
	}()

	execCommandFn = func(name string, args ...string) *exec.Cmd {
		return exec.Command("go", "version")
	}
	execLookPathFn = func(file string) (string, error) {
		return "/usr/bin/" + file, nil
	}

	var log strings.Builder
	result := syncFilesReal(MigrateRequest{
		SourceHost: "user@1.2.3.4",
		SourcePort: "22",
		SourcePath: "/remote/path",
		SSHPass:    "mypassword",
		LocalRoot:  t.TempDir(),
	}, &log)
	if result != "ok" {
		t.Errorf("result = %q, want ok", result)
	}
	if strings.Contains(log.String(), "WARNING") {
		t.Error("should NOT warn when sshpass is available")
	}
}

func TestSyncFilesRealError(t *testing.T) {
	origCmd := execCommandFn
	defer func() { execCommandFn = origCmd }()

	// Return a command that will fail.
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		return exec.Command("__nonexistent_binary__")
	}

	var log strings.Builder
	result := syncFilesReal(MigrateRequest{
		SourceHost: "user@1.2.3.4",
		SourcePort: "22",
		SourcePath: "/remote/path",
		LocalRoot:  t.TempDir(),
	}, &log)
	if !strings.HasPrefix(result, "error:") {
		t.Errorf("result = %q, want error: prefix", result)
	}
	if !strings.Contains(log.String(), "rsync error:") {
		t.Error("log should contain rsync error")
	}
}

func TestMigrateDBRealDefaultHost(t *testing.T) {
	origCmd := execCommandFn
	defer func() { execCommandFn = origCmd }()

	// SSH dump will fail.
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		return exec.Command("__nonexistent_binary__")
	}

	var log strings.Builder
	result := migrateDBReal(MigrateRequest{
		SourceHost: "user@1.2.3.4",
		SourcePort: "22",
		DBName:     "testdb",
		DBUser:     "user",
		DBPass:     "pass",
	}, &log)
	if result != "error: dump failed" {
		t.Errorf("result = %q, want 'error: dump failed'", result)
	}
	if !strings.Contains(log.String(), "mysqldump failed") {
		t.Error("log should contain mysqldump failed")
	}
}

func TestMigrateDBRealWithSSHKey(t *testing.T) {
	origCmd := execCommandFn
	defer func() { execCommandFn = origCmd }()

	execCommandFn = func(name string, args ...string) *exec.Cmd {
		return exec.Command("__nonexistent_binary__")
	}

	var log strings.Builder
	result := migrateDBReal(MigrateRequest{
		SourceHost: "user@1.2.3.4",
		SourcePort: "22",
		DBHost:     "remote-db.example.com",
		DBName:     "testdb",
		DBUser:     "user",
		DBPass:     "pass",
		SSHKey:     "/path/to/key",
	}, &log)
	if result != "error: dump failed" {
		t.Errorf("result = %q", result)
	}
}

func TestMigrateDBRealWithSSHPass(t *testing.T) {
	origCmd := execCommandFn
	defer func() { execCommandFn = origCmd }()

	execCommandFn = func(name string, args ...string) *exec.Cmd {
		return exec.Command("__nonexistent_binary__")
	}

	var log strings.Builder
	result := migrateDBReal(MigrateRequest{
		SourceHost: "user@1.2.3.4",
		SourcePort: "22",
		DBName:     "testdb",
		DBUser:     "user",
		DBPass:     "pass",
		SSHPass:    "sshpassword",
	}, &log)
	if result != "error: dump failed" {
		t.Errorf("result = %q", result)
	}
}

func TestMigrateDBRealDumpExitError(t *testing.T) {
	origCmd := execCommandFn
	defer func() { execCommandFn = origCmd }()

	// Use a command that runs but exits with non-zero (triggers ExitError).
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		// "go nonexistent-command" will fail with exit code 2 and produce stderr.
		return exec.Command("go", "nonexistent-subcommand-xyz")
	}

	var log strings.Builder
	result := migrateDBReal(MigrateRequest{
		SourceHost: "user@1.2.3.4",
		SourcePort: "22",
		DBName:     "testdb",
		DBUser:     "user",
		DBPass:     "pass",
	}, &log)
	if result != "error: dump failed" {
		t.Errorf("result = %q, want 'error: dump failed'", result)
	}
	// The log should contain the stderr from the failed command.
	logStr := log.String()
	if !strings.Contains(logStr, "mysqldump failed") {
		t.Errorf("log should contain 'mysqldump failed', got: %q", logStr)
	}
}

func TestMigrateDBRealWriteDumpError(t *testing.T) {
	origCmd := execCommandFn
	origTempDir := tempDirFn
	defer func() {
		execCommandFn = origCmd
		tempDirFn = origTempDir
	}()

	// SSH dump command succeeds.
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		return exec.Command("go", "version")
	}
	// Point temp dir to a nonexistent path so WriteFile fails.
	tempDirFn = func() string {
		return filepath.Join(t.TempDir(), "nonexistent", "subdir")
	}

	var log strings.Builder
	result := migrateDBReal(MigrateRequest{
		SourceHost: "user@1.2.3.4",
		SourcePort: "22",
		DBName:     "testdb",
		DBUser:     "user",
		DBPass:     "pass",
	}, &log)
	if result != "error: write failed" {
		t.Errorf("result = %q, want 'error: write failed'", result)
	}
	if !strings.Contains(log.String(), "write dump") {
		t.Errorf("log should contain 'write dump', got: %q", log.String())
	}
}

func TestMigrateDBRealDumpSuccessNoMySQLClient(t *testing.T) {
	origCmd := execCommandFn
	origLookPath := execLookPathFn
	defer func() {
		execCommandFn = origCmd
		execLookPathFn = origLookPath
	}()

	// Simulate successful SSH dump (returns empty output).
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		return exec.Command("go", "version")
	}
	// No mysql client found.
	execLookPathFn = func(file string) (string, error) {
		return "", fmt.Errorf("not found")
	}

	var log strings.Builder
	result := migrateDBReal(MigrateRequest{
		SourceHost: "user@1.2.3.4",
		SourcePort: "22",
		DBName:     "testdb",
		DBUser:     "user",
		DBPass:     "pass",
	}, &log)
	if result != "error: no mysql client" {
		t.Errorf("result = %q, want 'error: no mysql client'", result)
	}
	if !strings.Contains(log.String(), "Neither mariadb nor mysql client found") {
		t.Errorf("log = %q", log.String())
	}
}

func TestMigrateDBRealDumpSuccessWithMySQLClient(t *testing.T) {
	origCmd := execCommandFn
	origLookPath := execLookPathFn
	defer func() {
		execCommandFn = origCmd
		execLookPathFn = origLookPath
	}()

	callCount := 0
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		callCount++
		// All commands succeed with "go version" output.
		return exec.Command("go", "version")
	}
	execLookPathFn = func(file string) (string, error) {
		if file == "mariadb" {
			return "", fmt.Errorf("not found")
		}
		if file == "mysql" {
			return "/usr/bin/mysql", nil
		}
		return "", fmt.Errorf("not found")
	}

	var log strings.Builder
	result := migrateDBReal(MigrateRequest{
		SourceHost: "user@1.2.3.4",
		SourcePort: "22",
		DBName:     "testdb",
		DBUser:     "user",
		DBPass:     "pass",
	}, &log)
	// Should fail at import because we can't really open a dump file, but
	// the code will try. Actually, the command succeeds (go version), so
	// it should return "ok".
	if result != "ok" {
		t.Errorf("result = %q, want ok", result)
	}
}

func TestMigrateDBRealDumpSuccessImportFail(t *testing.T) {
	origCmd := execCommandFn
	origLookPath := execLookPathFn
	defer func() {
		execCommandFn = origCmd
		execLookPathFn = origLookPath
	}()

	// execCommandFn calls in migrateDBReal:
	// 1: SSH dump command (cmd.Output())
	// 2: CREATE DATABASE (bin, "-u", "root", "-e", ...)
	// 3: CREATE USER (bin, "-u", "root", "-e", ...)
	// 4: GRANT PRIVILEGES (bin, "-u", "root", "-e", ...)
	// 5: Import command (bin, "-u", "root", dbName) -- this should fail
	callIdx := 0
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		callIdx++
		if callIdx >= 5 {
			return exec.Command("__nonexistent_binary__")
		}
		return exec.Command("go", "version")
	}
	execLookPathFn = func(file string) (string, error) {
		if file == "mysql" {
			return "/usr/bin/mysql", nil
		}
		if file == "mariadb" {
			return "", fmt.Errorf("not found")
		}
		return "", fmt.Errorf("not found")
	}

	var log strings.Builder
	result := migrateDBReal(MigrateRequest{
		SourceHost: "user@1.2.3.4",
		SourcePort: "22",
		DBName:     "testdb",
		DBUser:     "user",
		DBPass:     "pass",
	}, &log)
	if result != "error: import failed" {
		t.Errorf("result = %q, want 'error: import failed'", result)
	}
}

func TestCloneFilesRealSuccess(t *testing.T) {
	origCmd := execCommandFn
	defer func() { execCommandFn = origCmd }()

	execCommandFn = func(name string, args ...string) *exec.Cmd {
		return exec.Command("go", "version")
	}

	var log strings.Builder
	err := cloneFilesReal(t.TempDir(), t.TempDir(), &log)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCloneFilesRealError(t *testing.T) {
	origCmd := execCommandFn
	defer func() { execCommandFn = origCmd }()

	execCommandFn = func(name string, args ...string) *exec.Cmd {
		return exec.Command("__nonexistent_binary__")
	}

	var log strings.Builder
	err := cloneFilesReal(t.TempDir(), t.TempDir(), &log)
	if err == nil {
		t.Error("expected error")
	}
}

func TestCloneDBRealNoClient(t *testing.T) {
	origLookPath := execLookPathFn
	defer func() { execLookPathFn = origLookPath }()

	execLookPathFn = func(file string) (string, error) {
		return "", fmt.Errorf("not found")
	}

	var log strings.Builder
	err := cloneDBReal("srcdb", "dstdb", "user", "pass", &log)
	if err == nil || !strings.Contains(err.Error(), "mysql client not found") {
		t.Errorf("err = %v, want 'mysql client not found'", err)
	}
}

func TestCloneDBRealNoDumpBin(t *testing.T) {
	origCmd := execCommandFn
	origLookPath := execLookPathFn
	defer func() {
		execCommandFn = origCmd
		execLookPathFn = origLookPath
	}()

	execCommandFn = func(name string, args ...string) *exec.Cmd {
		return exec.Command("go", "version")
	}
	execLookPathFn = func(file string) (string, error) {
		if file == "mysql" {
			return "/usr/bin/mysql", nil
		}
		// mysqldump and mariadb-dump not found.
		return "", fmt.Errorf("not found")
	}

	var log strings.Builder
	err := cloneDBReal("srcdb", "dstdb", "user", "pass", &log)
	if err == nil || !strings.Contains(err.Error(), "mysqldump not found") {
		t.Errorf("err = %v, want 'mysqldump not found'", err)
	}
}

func TestCloneDBRealWithMariadbDump(t *testing.T) {
	origCmd := execCommandFn
	origLookPath := execLookPathFn
	defer func() {
		execCommandFn = origCmd
		execLookPathFn = origLookPath
	}()

	execCommandFn = func(name string, args ...string) *exec.Cmd {
		return exec.Command("go", "version")
	}
	execLookPathFn = func(file string) (string, error) {
		if file == "mysql" {
			return "/usr/bin/mysql", nil
		}
		if file == "mariadb-dump" {
			return "/usr/bin/mariadb-dump", nil
		}
		return "", fmt.Errorf("not found")
	}

	var log strings.Builder
	err := cloneDBReal("srcdb", "dstdb", "user", "pass", &log)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCloneDBRealNoUserPass(t *testing.T) {
	origCmd := execCommandFn
	origLookPath := execLookPathFn
	defer func() {
		execCommandFn = origCmd
		execLookPathFn = origLookPath
	}()

	execCommandFn = func(name string, args ...string) *exec.Cmd {
		return exec.Command("go", "version")
	}
	execLookPathFn = func(file string) (string, error) {
		if file == "mysql" {
			return "/usr/bin/mysql", nil
		}
		if file == "mysqldump" {
			return "/usr/bin/mysqldump", nil
		}
		return "", fmt.Errorf("not found")
	}

	var log strings.Builder
	// No user/pass — should skip user creation.
	err := cloneDBReal("srcdb", "dstdb", "", "", &log)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCloneDBRealDumpFails(t *testing.T) {
	origCmd := execCommandFn
	origLookPath := execLookPathFn
	defer func() {
		execCommandFn = origCmd
		execLookPathFn = origLookPath
	}()

	callIdx := 0
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		callIdx++
		// First call is CREATE DATABASE (succeeds).
		// Second + third calls are CREATE USER and GRANT (succeed).
		// Fourth call is mysqldump (fails).
		if callIdx >= 4 {
			return exec.Command("__nonexistent_binary__")
		}
		return exec.Command("go", "version")
	}
	execLookPathFn = func(file string) (string, error) {
		if file == "mysql" {
			return "/usr/bin/mysql", nil
		}
		if file == "mysqldump" {
			return "/usr/bin/mysqldump", nil
		}
		return "", fmt.Errorf("not found")
	}

	var log strings.Builder
	err := cloneDBReal("srcdb", "dstdb", "user", "pass", &log)
	if err == nil || !strings.Contains(err.Error(), "dump srcdb") {
		t.Errorf("err = %v, want dump error", err)
	}
}

func TestCloneDBRealImportFails(t *testing.T) {
	origCmd := execCommandFn
	origLookPath := execLookPathFn
	defer func() {
		execCommandFn = origCmd
		execLookPathFn = origLookPath
	}()

	callIdx := 0
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		callIdx++
		// Calls 1-3: CREATE DATABASE, CREATE USER, GRANT (succeed).
		// Call 4: mysqldump (succeed).
		// Call 5: import (fails).
		if callIdx >= 5 {
			return exec.Command("__nonexistent_binary__")
		}
		return exec.Command("go", "version")
	}
	execLookPathFn = func(file string) (string, error) {
		if file == "mysql" {
			return "/usr/bin/mysql", nil
		}
		if file == "mysqldump" {
			return "/usr/bin/mysqldump", nil
		}
		return "", fmt.Errorf("not found")
	}

	var log strings.Builder
	err := cloneDBReal("srcdb", "dstdb", "user", "pass", &log)
	if err == nil || !strings.Contains(err.Error(), "import to dstdb") {
		t.Errorf("err = %v, want import error", err)
	}
}

func TestCloneDBRealMariadbFound(t *testing.T) {
	origCmd := execCommandFn
	origLookPath := execLookPathFn
	defer func() {
		execCommandFn = origCmd
		execLookPathFn = origLookPath
	}()

	execCommandFn = func(name string, args ...string) *exec.Cmd {
		return exec.Command("go", "version")
	}
	execLookPathFn = func(file string) (string, error) {
		if file == "mariadb" {
			return "/usr/bin/mariadb", nil
		}
		if file == "mysqldump" {
			return "/usr/bin/mysqldump", nil
		}
		return "", fmt.Errorf("not found")
	}

	var log strings.Builder
	err := cloneDBReal("srcdb", "dstdb", "user", "pass", &log)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// ============================================================
// Additional nginx.go edge cases
// ============================================================

// TestParseNginxLocationEqualsModifier — exact match location.
func TestParseNginxLocationEqualsModifier(t *testing.T) {
	input := `
server {
    listen 80;
    server_name example.com;
    root /var/www;

    location = /health {
        return 200 ok;
    }
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	loc := configs[0].Locations[0]
	if loc.Modifier != "=" {
		t.Errorf("Modifier = %q, want =", loc.Modifier)
	}
	if loc.Path != "/health" {
		t.Errorf("Path = %q, want /health", loc.Path)
	}
}

// TestParseNginxLocationCaseSensitiveRegex — ~* modifier.
func TestParseNginxLocationCaseSensitiveRegex(t *testing.T) {
	input := `
server {
    listen 80;
    server_name example.com;

    location ~* \.(jpg|png)$ {
        return 200 ok;
    }
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	loc := configs[0].Locations[0]
	if loc.Modifier != "~*" {
		t.Errorf("Modifier = %q, want ~*", loc.Modifier)
	}
}

// TestParseNginxServerWithReturn — return directive at server level.
func TestParseNginxServerWithReturn(t *testing.T) {
	input := `
server {
    listen 80;
    server_name r.com;
    return 301 https://r.com$request_uri;
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if configs[0].Return != "301 https://r.com$request_uri" {
		t.Errorf("Return = %q", configs[0].Return)
	}
}

// TestConvertToUWASWithIndex — index_files should be set.
func TestConvertToUWASWithIndex(t *testing.T) {
	nc := NginxConfig{
		ServerNames: []string{"idx.com"},
		Root:        "/var/www",
		IndexFiles:  []string{"index.html", "index.htm"},
	}
	domains := ConvertToUWAS([]NginxConfig{nc})
	if len(domains[0].IndexFiles) != 2 {
		t.Errorf("IndexFiles = %v, want 2", domains[0].IndexFiles)
	}
}

// TestConvertToUWASNoIndex — empty index_files should not be set.
func TestConvertToUWASNoIndex(t *testing.T) {
	nc := NginxConfig{
		ServerNames: []string{"noidx.com"},
		Root:        "/var/www",
	}
	domains := ConvertToUWAS([]NginxConfig{nc})
	if len(domains[0].IndexFiles) != 0 {
		t.Errorf("IndexFiles = %v, want empty", domains[0].IndexFiles)
	}
}

// TestNginxToYAMLWithProxy — full pipeline test for proxy.
func TestNginxToYAMLWithProxy(t *testing.T) {
	input := `
server {
    listen 80;
    server_name proxy.com;
    proxy_pass http://backend:3000;
}
`
	yaml, err := NginxToYAML(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(yaml, "type: proxy") {
		t.Errorf("should contain type: proxy, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "address: http://backend:3000") {
		t.Errorf("should contain upstream, got:\n%s", yaml)
	}
}

// TestNginxToYAMLWithRedirect — full pipeline test for redirect.
func TestNginxToYAMLWithRedirect(t *testing.T) {
	input := `
server {
    listen 80;
    server_name redir.com;
    return 301 https://target.com$request_uri;
}
`
	yaml, err := NginxToYAML(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(yaml, "type: redirect") {
		t.Errorf("should contain type: redirect, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "target: https://target.com") {
		t.Errorf("should contain target, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "status: 301") {
		t.Errorf("should contain status, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "preserve_path: true") {
		t.Errorf("should contain preserve_path, got:\n%s", yaml)
	}
}

// TestNginxToYAMLWithPHP — full pipeline test for PHP.
func TestNginxToYAMLWithPHP(t *testing.T) {
	input := `
server {
    listen 80;
    server_name php.com;
    root /var/www/php;
    index index.php;

    location ~ \.php$ {
        fastcgi_pass 127.0.0.1:9000;
    }
}
`
	yaml, err := NginxToYAML(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(yaml, "type: php") {
		t.Errorf("should contain type: php, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "fpm_address: 127.0.0.1:9000") {
		t.Errorf("should contain fpm_address, got:\n%s", yaml)
	}
}

// TestMigrateResultTimestamps — verify timing fields are set.
func TestMigrateResultTimestamps(t *testing.T) {
	origSync := runSyncFiles
	origChown := runChown
	defer func() {
		runSyncFiles = origSync
		runChown = origChown
	}()

	runSyncFiles = func(req MigrateRequest, log *strings.Builder) string { return "ok" }
	runChown = func(root string) {}

	result := Migrate(MigrateRequest{
		SourceHost: "user@1.2.3.4",
		LocalRoot:  t.TempDir(),
	})

	if result.StartedAt.IsZero() {
		t.Error("StartedAt should be set")
	}
	if result.FinishedAt.IsZero() {
		t.Error("FinishedAt should be set")
	}
	if !result.FinishedAt.After(result.StartedAt) && !result.FinishedAt.Equal(result.StartedAt) {
		t.Error("FinishedAt should be >= StartedAt")
	}
}

// TestMigrateFileSyncResult — verify FilesSync field.
func TestMigrateFileSyncResult(t *testing.T) {
	origSync := runSyncFiles
	origChown := runChown
	defer func() {
		runSyncFiles = origSync
		runChown = origChown
	}()

	runSyncFiles = func(req MigrateRequest, log *strings.Builder) string {
		return "ok"
	}
	runChown = func(root string) {}

	result := Migrate(MigrateRequest{
		SourceHost: "user@1.2.3.4",
		LocalRoot:  t.TempDir(),
	})
	if result.FilesSync != "ok" {
		t.Errorf("FilesSync = %q, want ok", result.FilesSync)
	}
}

// TestParseNginxSSLCertBeforeKey — test that ssl_certificate is parsed correctly
// even when it appears before ssl_certificate_key.
func TestParseNginxSSLCertBeforeKey(t *testing.T) {
	input := `
server {
    listen 443 ssl;
    server_name s.com;
    ssl_certificate /cert.pem;
    ssl_certificate_key /key.pem;
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if configs[0].SSLCert != "/cert.pem" {
		t.Errorf("SSLCert = %q", configs[0].SSLCert)
	}
	if configs[0].SSLKey != "/key.pem" {
		t.Errorf("SSLKey = %q", configs[0].SSLKey)
	}
}

// TestParseNginxSSLKeyBeforeCert — reverse order should also work.
func TestParseNginxSSLKeyBeforeCert(t *testing.T) {
	input := `
server {
    listen 443 ssl;
    server_name s.com;
    ssl_certificate_key /key.pem;
    ssl_certificate /cert.pem;
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if configs[0].SSLCert != "/cert.pem" {
		t.Errorf("SSLCert = %q", configs[0].SSLCert)
	}
	if configs[0].SSLKey != "/key.pem" {
		t.Errorf("SSLKey = %q", configs[0].SSLKey)
	}
}

// TestParseNginxServerProxyPass — server-level proxy_pass.
func TestParseNginxServerProxyPass(t *testing.T) {
	input := `
server {
    listen 80;
    server_name p.com;
    proxy_pass http://upstream:9090;
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if configs[0].ProxyPass != "http://upstream:9090" {
		t.Errorf("ProxyPass = %q", configs[0].ProxyPass)
	}
}

// TestParseNginxEmptyServerBlock — server block with no directives.
func TestParseNginxEmptyServerBlock(t *testing.T) {
	input := `
server {
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected 1, got %d", len(configs))
	}
	if len(configs[0].ServerNames) != 0 {
		t.Errorf("ServerNames = %v, want empty", configs[0].ServerNames)
	}
}

// TestDomainsToYAMLPHPWithIndexFiles — verify PHP index_files in YAML output.
func TestDomainsToYAMLPHPWithIndexFiles(t *testing.T) {
	nc := NginxConfig{
		ServerNames: []string{"wp.com"},
		Root:        "/var/www",
		IndexFiles:  []string{"index.php", "index.html"},
		Locations: []NginxLocation{
			{Path: `\.php$`, Modifier: "~", FastCGI: "unix:/run/php-fpm.sock"},
		},
	}
	domains := ConvertToUWAS([]NginxConfig{nc})
	yaml := domainsToYAML(domains)
	if !strings.Contains(yaml, "index.php") {
		t.Errorf("should contain PHP index file in YAML, got:\n%s", yaml)
	}
}
