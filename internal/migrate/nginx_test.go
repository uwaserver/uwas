package migrate

import (
	"strings"
	"testing"
)

func TestParseNginxStaticSite(t *testing.T) {
	input := `
server {
    listen 80;
    server_name example.com;
    root /var/www/html;
    index index.html;
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1 {
		t.Fatalf("expected 1 config, got %d", len(configs))
	}
	c := configs[0]
	if len(c.ServerNames) != 1 || c.ServerNames[0] != "example.com" {
		t.Errorf("ServerNames = %v, want [example.com]", c.ServerNames)
	}
	if c.Root != "/var/www/html" {
		t.Errorf("Root = %q, want /var/www/html", c.Root)
	}
}

func TestConvertStaticSite(t *testing.T) {
	input := `
server {
    listen 80;
    server_name example.com;
    root /var/www/html;
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}

	domains := ConvertToUWAS(configs)
	if len(domains) != 1 {
		t.Fatalf("expected 1 domain, got %d", len(domains))
	}
	d := domains[0]
	if d.Host != "example.com" {
		t.Errorf("Host = %q, want example.com", d.Host)
	}
	if d.Type != "static" {
		t.Errorf("Type = %q, want static", d.Type)
	}
	if d.Root != "/var/www/html" {
		t.Errorf("Root = %q, want /var/www/html", d.Root)
	}
}

func TestConvertPHPSiteWithFastCGI(t *testing.T) {
	input := `
server {
    listen 80;
    server_name wordpress.example.com;
    root /var/www/wordpress;
    index index.php index.html;

    location / {
        try_files $uri $uri/ /index.php?$args;
    }

    location ~ \.php$ {
        fastcgi_pass unix:/run/php-fpm.sock;
    }
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}

	domains := ConvertToUWAS(configs)
	if len(domains) != 1 {
		t.Fatalf("expected 1 domain, got %d", len(domains))
	}
	d := domains[0]
	if d.Type != "php" {
		t.Errorf("Type = %q, want php", d.Type)
	}
	if d.PHP.FPMAddress != "unix:/run/php-fpm.sock" {
		t.Errorf("PHP.FPMAddress = %q, want unix:/run/php-fpm.sock", d.PHP.FPMAddress)
	}
	if len(d.PHP.IndexFiles) == 0 || d.PHP.IndexFiles[0] != "index.php" {
		t.Errorf("PHP.IndexFiles = %v, want [index.php]", d.PHP.IndexFiles)
	}
	// try_files should contain /index.php?$args as last element, triggering SPA mode
	if !d.SPAMode {
		t.Error("SPAMode should be true for try_files ending with /index.php?$args")
	}
}

func TestConvertProxySite(t *testing.T) {
	input := `
server {
    listen 80;
    server_name api.example.com;
    proxy_pass http://backend:8080;
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}

	domains := ConvertToUWAS(configs)
	if len(domains) != 1 {
		t.Fatalf("expected 1 domain, got %d", len(domains))
	}
	d := domains[0]
	if d.Type != "proxy" {
		t.Errorf("Type = %q, want proxy", d.Type)
	}
	if len(d.Proxy.Upstreams) != 1 {
		t.Fatalf("Upstreams len = %d, want 1", len(d.Proxy.Upstreams))
	}
	if d.Proxy.Upstreams[0].Address != "http://backend:8080" {
		t.Errorf("Upstream address = %q, want http://backend:8080", d.Proxy.Upstreams[0].Address)
	}
}

func TestConvertProxySiteLocationLevel(t *testing.T) {
	input := `
server {
    listen 80;
    server_name api.example.com;

    location / {
        proxy_pass http://127.0.0.1:3000;
    }
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}

	domains := ConvertToUWAS(configs)
	d := domains[0]
	if d.Type != "proxy" {
		t.Errorf("Type = %q, want proxy", d.Type)
	}
	if len(d.Proxy.Upstreams) != 1 || d.Proxy.Upstreams[0].Address != "http://127.0.0.1:3000" {
		t.Errorf("Upstreams = %v", d.Proxy.Upstreams)
	}
}

func TestConvertRedirect(t *testing.T) {
	input := `
server {
    listen 80;
    server_name example.com;
    return 301 https://example.com$request_uri;
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}

	domains := ConvertToUWAS(configs)
	if len(domains) != 1 {
		t.Fatalf("expected 1 domain, got %d", len(domains))
	}
	d := domains[0]
	if d.Type != "redirect" {
		t.Errorf("Type = %q, want redirect", d.Type)
	}
	if d.Redirect.Status != 301 {
		t.Errorf("Redirect.Status = %d, want 301", d.Redirect.Status)
	}
	if d.Redirect.Target != "https://example.com" {
		t.Errorf("Redirect.Target = %q, want https://example.com", d.Redirect.Target)
	}
	if !d.Redirect.PreservePath {
		t.Error("Redirect.PreservePath should be true when $request_uri is used")
	}
}

func TestConvertSSLCertMapping(t *testing.T) {
	input := `
server {
    listen 443 ssl;
    server_name secure.example.com;
    root /var/www/secure;
    ssl_certificate /etc/ssl/cert.pem;
    ssl_certificate_key /etc/ssl/key.pem;
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}

	domains := ConvertToUWAS(configs)
	d := domains[0]
	if d.SSL.Mode != "manual" {
		t.Errorf("SSL.Mode = %q, want manual", d.SSL.Mode)
	}
	if d.SSL.Cert != "/etc/ssl/cert.pem" {
		t.Errorf("SSL.Cert = %q, want /etc/ssl/cert.pem", d.SSL.Cert)
	}
	if d.SSL.Key != "/etc/ssl/key.pem" {
		t.Errorf("SSL.Key = %q, want /etc/ssl/key.pem", d.SSL.Key)
	}
}

func TestConvertSSLAutoMode(t *testing.T) {
	input := `
server {
    listen 443 ssl;
    server_name auto.example.com;
    root /var/www/auto;
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}

	domains := ConvertToUWAS(configs)
	d := domains[0]
	if d.SSL.Mode != "auto" {
		t.Errorf("SSL.Mode = %q, want auto", d.SSL.Mode)
	}
}

func TestConvertMultipleServerBlocks(t *testing.T) {
	input := `
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
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 2 {
		t.Fatalf("expected 2 configs, got %d", len(configs))
	}

	domains := ConvertToUWAS(configs)
	if len(domains) != 2 {
		t.Fatalf("expected 2 domains, got %d", len(domains))
	}
	if domains[0].Host != "site1.com" {
		t.Errorf("domains[0].Host = %q, want site1.com", domains[0].Host)
	}
	if domains[1].Host != "site2.com" {
		t.Errorf("domains[1].Host = %q, want site2.com", domains[1].Host)
	}
}

func TestConvertServerNameAliases(t *testing.T) {
	input := `
server {
    listen 80;
    server_name example.com www.example.com cdn.example.com;
    root /var/www/example;
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}

	domains := ConvertToUWAS(configs)
	d := domains[0]
	if d.Host != "example.com" {
		t.Errorf("Host = %q, want example.com", d.Host)
	}
	if len(d.Aliases) != 2 {
		t.Fatalf("Aliases len = %d, want 2", len(d.Aliases))
	}
	if d.Aliases[0] != "www.example.com" || d.Aliases[1] != "cdn.example.com" {
		t.Errorf("Aliases = %v, want [www.example.com cdn.example.com]", d.Aliases)
	}
}

func TestParseNginxEmptyInput(t *testing.T) {
	configs, err := ParseNginx(strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 0 {
		t.Fatalf("expected 0 configs, got %d", len(configs))
	}
}

func TestParseNginxCommentsOnly(t *testing.T) {
	input := `
# Just comments
# No server blocks
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 0 {
		t.Fatalf("expected 0 configs, got %d", len(configs))
	}
}

func TestParseNginxInvalidNoServerBlocks(t *testing.T) {
	input := `
http {
    access_log /var/log/nginx/access.log;
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 0 {
		t.Fatalf("expected 0 configs, got %d", len(configs))
	}
}

func TestNginxToYAML(t *testing.T) {
	input := `
server {
    listen 80;
    server_name example.com;
    root /var/www/html;
}
`
	yaml, err := NginxToYAML(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(yaml, "host: example.com") {
		t.Errorf("YAML should contain host, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "type: static") {
		t.Errorf("YAML should contain type, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "root: /var/www/html") {
		t.Errorf("YAML should contain root, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "# Converted from Nginx") {
		t.Errorf("YAML should have header comment, got:\n%s", yaml)
	}
}

func TestNginxToYAMLEmptyInput(t *testing.T) {
	_, err := NginxToYAML(strings.NewReader(""))
	if err == nil {
		t.Fatal("expected error for empty input")
	}
	if !strings.Contains(err.Error(), "no server blocks") {
		t.Errorf("error = %q, want 'no server blocks'", err.Error())
	}
}

func TestConvertDefaultServerName(t *testing.T) {
	input := `
server {
    listen 80;
    server_name _;
    root /var/www/default;
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	domains := ConvertToUWAS(configs)
	if domains[0].Host != "example.com" {
		t.Errorf("Host = %q, want example.com for _ server_name", domains[0].Host)
	}
}

func TestConvertNoServerName(t *testing.T) {
	input := `
server {
    listen 80;
    root /var/www/default;
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	domains := ConvertToUWAS(configs)
	if domains[0].Host != "example.com" {
		t.Errorf("Host = %q, want example.com when no server_name", domains[0].Host)
	}
}

func TestConvertRedirect302(t *testing.T) {
	input := `
server {
    listen 80;
    server_name old.example.com;
    return 302 https://new.example.com;
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	domains := ConvertToUWAS(configs)
	d := domains[0]
	if d.Type != "redirect" {
		t.Errorf("Type = %q, want redirect", d.Type)
	}
	if d.Redirect.Status != 302 {
		t.Errorf("Redirect.Status = %d, want 302", d.Redirect.Status)
	}
	if d.Redirect.Target != "https://new.example.com" {
		t.Errorf("Redirect.Target = %q", d.Redirect.Target)
	}
}

func TestConvertTryFilesWithoutSPA(t *testing.T) {
	input := `
server {
    listen 80;
    server_name example.com;
    root /var/www/html;

    location / {
        try_files $uri $uri/ =404;
    }
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	domains := ConvertToUWAS(configs)
	d := domains[0]
	if d.SPAMode {
		t.Error("SPAMode should be false for try_files ending with =404")
	}
	if len(d.TryFiles) == 0 {
		t.Error("TryFiles should be populated")
	}
}

func TestConvertFullExample(t *testing.T) {
	// The full example from the task description
	input := `
server {
    listen 80;
    listen 443 ssl;
    server_name example.com www.example.com;
    root /var/www/example;
    index index.php index.html;

    ssl_certificate /etc/ssl/cert.pem;
    ssl_certificate_key /etc/ssl/key.pem;

    location / {
        try_files $uri $uri/ /index.php?$args;
    }

    location ~ \.php$ {
        fastcgi_pass unix:/run/php-fpm.sock;
    }
}
`
	yaml, err := NginxToYAML(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}

	// Should be a PHP site
	if !strings.Contains(yaml, "type: php") {
		t.Errorf("should be type: php, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "host: example.com") {
		t.Errorf("should have host, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "www.example.com") {
		t.Errorf("should have alias, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "mode: manual") {
		t.Errorf("should have SSL mode manual, got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "fpm_address: unix:/run/php-fpm.sock") {
		t.Errorf("should have fpm_address, got:\n%s", yaml)
	}
}

func TestConvertMultipleProxyUpstreams(t *testing.T) {
	input := `
server {
    listen 80;
    server_name lb.example.com;

    location /api {
        proxy_pass http://api-backend:3000;
    }

    location /web {
        proxy_pass http://web-backend:4000;
    }
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	domains := ConvertToUWAS(configs)
	d := domains[0]
	if d.Type != "proxy" {
		t.Errorf("Type = %q, want proxy", d.Type)
	}
	if len(d.Proxy.Upstreams) != 2 {
		t.Fatalf("Upstreams len = %d, want 2", len(d.Proxy.Upstreams))
	}
}

func TestExtractDirective(t *testing.T) {
	tests := []struct {
		line, directive, want string
	}{
		{"server_name example.com;", "server_name", "example.com"},
		{"root /var/www;", "root", "/var/www"},
		{"listen 443 ssl;", "listen", "443 ssl"},
		{"proxy_pass http://localhost:3000;", "proxy_pass", "http://localhost:3000"},
		{"# server_name commented;", "server_name", ""},
		{"other_directive value;", "server_name", ""},
		{"ssl_certificate_key /etc/key.pem;", "ssl_certificate_key", "/etc/key.pem"},
		{"ssl_certificate /etc/cert.pem;", "ssl_certificate", "/etc/cert.pem"},
	}
	for _, tt := range tests {
		got := extractDirective(tt.line, tt.directive)
		if got != tt.want {
			t.Errorf("extractDirective(%q, %q) = %q, want %q", tt.line, tt.directive, got, tt.want)
		}
	}
}

func TestExtractLocationModifierAndPath(t *testing.T) {
	tests := []struct {
		line     string
		wantMod  string
		wantPath string
	}{
		{"location / {", "", "/"},
		{"location /api {", "", "/api"},
		{`location ~ \.php$ {`, "~", `\.php$`},
		{`location ~* \.(jpg|png)$ {`, "~*", `\.(jpg|png)$`},
		{"location = /favicon.ico {", "=", "/favicon.ico"},
	}
	for _, tt := range tests {
		mod, path := extractLocationModifierAndPath(tt.line)
		if mod != tt.wantMod || path != tt.wantPath {
			t.Errorf("extractLocationModifierAndPath(%q) = (%q, %q), want (%q, %q)",
				tt.line, mod, path, tt.wantMod, tt.wantPath)
		}
	}
}

func TestParseReturnDirective(t *testing.T) {
	tests := []struct {
		val      string
		wantCode int
		wantURL  string
	}{
		{"301 https://example.com", 301, "https://example.com"},
		{"302 https://new.example.com$request_uri", 302, "https://new.example.com$request_uri"},
		{"invalid", 0, ""},
		{"", 0, ""},
	}
	for _, tt := range tests {
		code, url := parseReturnDirective(tt.val)
		if code != tt.wantCode || url != tt.wantURL {
			t.Errorf("parseReturnDirective(%q) = (%d, %q), want (%d, %q)",
				tt.val, code, url, tt.wantCode, tt.wantURL)
		}
	}
}

func TestConvertPHPDetectionByIndexOnly(t *testing.T) {
	// PHP should be detected even without fastcgi_pass if index includes .php
	input := `
server {
    listen 80;
    server_name example.com;
    root /var/www/html;
    index index.php;
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	domains := ConvertToUWAS(configs)
	if domains[0].Type != "php" {
		t.Errorf("Type = %q, want php (detected via index files)", domains[0].Type)
	}
}
