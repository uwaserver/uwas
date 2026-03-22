package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestMigrateCommandNameDescription(t *testing.T) {
	m := &MigrateCommand{}
	if m.Name() != "migrate" {
		t.Errorf("Name() = %q, want migrate", m.Name())
	}
	if m.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

func TestMigrateCommandHelp(t *testing.T) {
	m := &MigrateCommand{}
	h := m.Help()
	if !strings.Contains(h, "nginx") {
		t.Error("Help should mention nginx")
	}
	if !strings.Contains(h, "apache") {
		t.Error("Help should mention apache")
	}
}

func TestMigrateCommandNoArgs(t *testing.T) {
	m := &MigrateCommand{}

	old := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	err := m.Run(nil)

	w.Close()
	os.Stdout = old

	if err != nil {
		t.Errorf("Run(nil) error: %v", err)
	}
}

func TestMigrateCommandUnknownFormat(t *testing.T) {
	m := &MigrateCommand{}
	err := m.Run([]string{"caddy", "/some/file"})
	if err == nil {
		t.Fatal("expected error for unknown format")
	}
	if !strings.Contains(err.Error(), "unknown format") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestMigrateNginxBasicServer(t *testing.T) {
	content := `
server {
    listen 80;
    server_name example.com;
    root /var/www/html;
}
`
	tmpFile := writeTempFile(t, content, "nginx-*.conf")
	defer os.Remove(tmpFile)

	output := captureStdout(t, func() {
		err := migrateNginx(tmpFile)
		if err != nil {
			t.Fatalf("migrateNginx error: %v", err)
		}
	})

	if !strings.Contains(output, "host: example.com") {
		t.Errorf("output should contain host, got:\n%s", output)
	}
	if !strings.Contains(output, "type: static") {
		t.Errorf("output should contain type: static, got:\n%s", output)
	}
	if !strings.Contains(output, "root: /var/www/html") {
		t.Errorf("output should contain root, got:\n%s", output)
	}
}

func TestMigrateNginxSSL(t *testing.T) {
	content := `
server {
    listen 443 ssl;
    server_name secure.example.com;
    root /var/www/secure;
    ssl_certificate /etc/ssl/certs/example.crt;
    ssl_certificate_key /etc/ssl/private/example.key;
}
`
	tmpFile := writeTempFile(t, content, "nginx-ssl-*.conf")
	defer os.Remove(tmpFile)

	output := captureStdout(t, func() {
		err := migrateNginx(tmpFile)
		if err != nil {
			t.Fatalf("migrateNginx error: %v", err)
		}
	})

	if !strings.Contains(output, "host: secure.example.com") {
		t.Errorf("output should contain host, got:\n%s", output)
	}
	if !strings.Contains(output, "mode: manual") {
		t.Errorf("output should contain ssl mode manual, got:\n%s", output)
	}
	if !strings.Contains(output, "cert: /etc/ssl/certs/example.crt") {
		t.Errorf("output should contain cert path, got:\n%s", output)
	}
	if !strings.Contains(output, "key: /etc/ssl/private/example.key") {
		t.Errorf("output should contain key path, got:\n%s", output)
	}
}

func TestMigrateNginxProxy(t *testing.T) {
	content := `
server {
    listen 80;
    server_name api.example.com;

    location / {
        proxy_pass http://127.0.0.1:3000;
    }
}
`
	tmpFile := writeTempFile(t, content, "nginx-proxy-*.conf")
	defer os.Remove(tmpFile)

	output := captureStdout(t, func() {
		err := migrateNginx(tmpFile)
		if err != nil {
			t.Fatalf("migrateNginx error: %v", err)
		}
	})

	if !strings.Contains(output, "type: proxy") {
		t.Errorf("output should contain type: proxy, got:\n%s", output)
	}
	if !strings.Contains(output, "address: http://127.0.0.1:3000") {
		t.Errorf("output should contain upstream address, got:\n%s", output)
	}
}

func TestMigrateNginxMultipleServers(t *testing.T) {
	content := `
server {
    listen 80;
    server_name site1.com;
    root /var/www/site1;
}

server {
    listen 80;
    server_name site2.com;
    root /var/www/site2;
}
`
	tmpFile := writeTempFile(t, content, "nginx-multi-*.conf")
	defer os.Remove(tmpFile)

	output := captureStdout(t, func() {
		err := migrateNginx(tmpFile)
		if err != nil {
			t.Fatalf("migrateNginx error: %v", err)
		}
	})

	if !strings.Contains(output, "host: site1.com") {
		t.Errorf("output should contain site1.com, got:\n%s", output)
	}
	if !strings.Contains(output, "host: site2.com") {
		t.Errorf("output should contain site2.com, got:\n%s", output)
	}
}

func TestMigrateNginxNoServerBlock(t *testing.T) {
	content := `
# Just a comment, no server blocks
`
	tmpFile := writeTempFile(t, content, "nginx-empty-*.conf")
	defer os.Remove(tmpFile)

	err := migrateNginx(tmpFile)
	if err == nil {
		t.Fatal("expected error for no server blocks")
	}
	if !strings.Contains(err.Error(), "no server blocks") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestMigrateNginxNonexistentFile(t *testing.T) {
	err := migrateNginx("/nonexistent/file.conf")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestMigrateNginxSSLListenAutoMode(t *testing.T) {
	content := `
server {
    listen 443 ssl;
    server_name auto-ssl.example.com;
    root /var/www/auto;
}
`
	tmpFile := writeTempFile(t, content, "nginx-auto-ssl-*.conf")
	defer os.Remove(tmpFile)

	output := captureStdout(t, func() {
		err := migrateNginx(tmpFile)
		if err != nil {
			t.Fatalf("migrateNginx error: %v", err)
		}
	})

	if !strings.Contains(output, "mode: auto") {
		t.Errorf("output should contain ssl mode auto, got:\n%s", output)
	}
}

func TestMigrateNginxDefaultServerName(t *testing.T) {
	content := `
server {
    listen 80;
    server_name _;
    root /var/www/default;
}
`
	tmpFile := writeTempFile(t, content, "nginx-default-*.conf")
	defer os.Remove(tmpFile)

	output := captureStdout(t, func() {
		err := migrateNginx(tmpFile)
		if err != nil {
			t.Fatalf("migrateNginx error: %v", err)
		}
	})

	if !strings.Contains(output, "host: example.com") {
		t.Errorf("_ server_name should become example.com, got:\n%s", output)
	}
}

// --- Apache tests ---

func TestMigrateApacheBasicVHost(t *testing.T) {
	content := `
<VirtualHost *:80>
    ServerName example.com
    DocumentRoot /var/www/html
</VirtualHost>
`
	tmpFile := writeTempFile(t, content, "apache-*.conf")
	defer os.Remove(tmpFile)

	output := captureStdout(t, func() {
		err := migrateApache(tmpFile)
		if err != nil {
			t.Fatalf("migrateApache error: %v", err)
		}
	})

	if !strings.Contains(output, "host: example.com") {
		t.Errorf("output should contain host, got:\n%s", output)
	}
	if !strings.Contains(output, "type: static") {
		t.Errorf("output should contain type: static, got:\n%s", output)
	}
	if !strings.Contains(output, "root: /var/www/html") {
		t.Errorf("output should contain root, got:\n%s", output)
	}
}

func TestMigrateApacheSSL(t *testing.T) {
	content := `
<VirtualHost *:443>
    ServerName secure.example.com
    DocumentRoot /var/www/secure
    SSLEngine on
    SSLCertificateFile /etc/ssl/certs/example.crt
    SSLCertificateKeyFile /etc/ssl/private/example.key
</VirtualHost>
`
	tmpFile := writeTempFile(t, content, "apache-ssl-*.conf")
	defer os.Remove(tmpFile)

	output := captureStdout(t, func() {
		err := migrateApache(tmpFile)
		if err != nil {
			t.Fatalf("migrateApache error: %v", err)
		}
	})

	if !strings.Contains(output, "mode: manual") {
		t.Errorf("output should contain ssl mode manual, got:\n%s", output)
	}
	if !strings.Contains(output, "cert: /etc/ssl/certs/example.crt") {
		t.Errorf("output should contain cert path, got:\n%s", output)
	}
	if !strings.Contains(output, "key: /etc/ssl/private/example.key") {
		t.Errorf("output should contain key path, got:\n%s", output)
	}
}

func TestMigrateApacheProxy(t *testing.T) {
	content := `
<VirtualHost *:80>
    ServerName api.example.com
    ProxyPass / http://127.0.0.1:8080/
</VirtualHost>
`
	tmpFile := writeTempFile(t, content, "apache-proxy-*.conf")
	defer os.Remove(tmpFile)

	output := captureStdout(t, func() {
		err := migrateApache(tmpFile)
		if err != nil {
			t.Fatalf("migrateApache error: %v", err)
		}
	})

	if !strings.Contains(output, "type: proxy") {
		t.Errorf("output should contain type: proxy, got:\n%s", output)
	}
	if !strings.Contains(output, "address: http://127.0.0.1:8080/") {
		t.Errorf("output should contain upstream address, got:\n%s", output)
	}
}

func TestMigrateApacheMultipleVHosts(t *testing.T) {
	content := `
<VirtualHost *:80>
    ServerName site1.com
    DocumentRoot /var/www/site1
</VirtualHost>

<VirtualHost *:80>
    ServerName site2.com
    DocumentRoot /var/www/site2
</VirtualHost>
`
	tmpFile := writeTempFile(t, content, "apache-multi-*.conf")
	defer os.Remove(tmpFile)

	output := captureStdout(t, func() {
		err := migrateApache(tmpFile)
		if err != nil {
			t.Fatalf("migrateApache error: %v", err)
		}
	})

	if !strings.Contains(output, "host: site1.com") {
		t.Errorf("output should contain site1.com, got:\n%s", output)
	}
	if !strings.Contains(output, "host: site2.com") {
		t.Errorf("output should contain site2.com, got:\n%s", output)
	}
}

func TestMigrateApacheNoVHostBlock(t *testing.T) {
	content := `
# Just a comment
`
	tmpFile := writeTempFile(t, content, "apache-empty-*.conf")
	defer os.Remove(tmpFile)

	err := migrateApache(tmpFile)
	if err == nil {
		t.Fatal("expected error for no VirtualHost blocks")
	}
	if !strings.Contains(err.Error(), "no VirtualHost") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestMigrateApacheNonexistentFile(t *testing.T) {
	err := migrateApache("/nonexistent/file.conf")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestMigrateApacheSSLEngineOnly(t *testing.T) {
	content := `
<VirtualHost *:443>
    ServerName ssl-only.example.com
    DocumentRoot /var/www/ssl
    SSLEngine on
</VirtualHost>
`
	tmpFile := writeTempFile(t, content, "apache-ssl-only-*.conf")
	defer os.Remove(tmpFile)

	output := captureStdout(t, func() {
		err := migrateApache(tmpFile)
		if err != nil {
			t.Fatalf("migrateApache error: %v", err)
		}
	})

	if !strings.Contains(output, "mode: auto") {
		t.Errorf("output should contain ssl mode auto for SSLEngine without cert files, got:\n%s", output)
	}
}

func TestMigrateApacheDefaultServerName(t *testing.T) {
	content := `
<VirtualHost *:80>
    DocumentRoot /var/www/default
</VirtualHost>
`
	tmpFile := writeTempFile(t, content, "apache-noname-*.conf")
	defer os.Remove(tmpFile)

	output := captureStdout(t, func() {
		err := migrateApache(tmpFile)
		if err != nil {
			t.Fatalf("migrateApache error: %v", err)
		}
	})

	if !strings.Contains(output, "host: example.com") {
		t.Errorf("missing ServerName should default to example.com, got:\n%s", output)
	}
}

// --- Parser unit tests ---

func TestExtractNginxDirective(t *testing.T) {
	tests := []struct {
		line, directive, want string
	}{
		{"server_name example.com;", "server_name", "example.com"},
		{"root /var/www;", "root", "/var/www"},
		{"listen 443 ssl;", "listen", "443 ssl"},
		{"proxy_pass http://localhost:3000;", "proxy_pass", "http://localhost:3000"},
		{"# server_name commented;", "server_name", ""},
		{"other_directive value;", "server_name", ""},
	}

	for _, tt := range tests {
		got := extractNginxDirective(tt.line, tt.directive)
		if got != tt.want {
			t.Errorf("extractNginxDirective(%q, %q) = %q, want %q", tt.line, tt.directive, got, tt.want)
		}
	}
}

func TestExtractNginxLocationPath(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"location / {", "/"},
		{"location /api {", "/api"},
		{"location ~ \\.php$ {", "\\.php$"},
		{"location ~* \\.(jpg|png)$ {", "\\.(jpg|png)$"},
		{"location = /favicon.ico {", "/favicon.ico"},
	}

	for _, tt := range tests {
		got := extractNginxLocationPath(tt.line)
		if got != tt.want {
			t.Errorf("extractNginxLocationPath(%q) = %q, want %q", tt.line, got, tt.want)
		}
	}
}

func TestExtractApacheValue(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"ServerName example.com", "example.com"},
		{`DocumentRoot "/var/www/html"`, `"/var/www/html"`},
		{"SSLEngine on", "on"},
		{"", ""},
		{"SingleWord", ""},
	}

	for _, tt := range tests {
		got := extractApacheValue(tt.line)
		if got != tt.want {
			t.Errorf("extractApacheValue(%q) = %q, want %q", tt.line, got, tt.want)
		}
	}
}

func TestExtractApacheVHostAddr(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"<VirtualHost *:80>", "*:80"},
		{"<VirtualHost *:443>", "*:443"},
		{"<VirtualHost 192.168.1.1:80>", "192.168.1.1:80"},
	}

	for _, tt := range tests {
		got := extractApacheVHostAddr(tt.line)
		if got != tt.want {
			t.Errorf("extractApacheVHostAddr(%q) = %q, want %q", tt.line, got, tt.want)
		}
	}
}

// --- Helpers ---

func writeTempFile(t *testing.T, content, pattern string) string {
	t.Helper()
	tmpFile, err := os.CreateTemp("", pattern)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()
	return tmpFile.Name()
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.String()
}
