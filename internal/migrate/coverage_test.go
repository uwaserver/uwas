package migrate

import (
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// --- buildPHPConfig with no FastCGI locations ---

func TestBuildPHPConfigNoFastCGI(t *testing.T) {
	nc := NginxConfig{
		ServerNames: []string{"example.com"},
		Root:        "/var/www/html",
		IndexFiles:  []string{"index.php", "index.html"},
		Locations: []NginxLocation{
			{Path: "/", TryFiles: []string{"$uri", "$uri/", "/index.php?$args"}},
			// No FastCGI location.
		},
	}

	php := buildPHPConfig(nc)
	// FPMAddress should be empty since there is no fastcgi_pass directive.
	if php.FPMAddress != "" {
		t.Errorf("FPMAddress = %q, want empty", php.FPMAddress)
	}
	// Should still extract PHP index files.
	if len(php.IndexFiles) != 1 || php.IndexFiles[0] != "index.php" {
		t.Errorf("IndexFiles = %v, want [index.php]", php.IndexFiles)
	}
}

func TestBuildPHPConfigNoIndexFiles(t *testing.T) {
	nc := NginxConfig{
		ServerNames: []string{"example.com"},
		Root:        "/var/www/html",
		IndexFiles:  []string{"index.html"}, // No PHP index files.
		Locations: []NginxLocation{
			{Path: "/", FastCGI: "127.0.0.1:9000"},
		},
	}

	php := buildPHPConfig(nc)
	if php.FPMAddress != "127.0.0.1:9000" {
		t.Errorf("FPMAddress = %q, want 127.0.0.1:9000", php.FPMAddress)
	}
	if len(php.IndexFiles) != 0 {
		t.Errorf("IndexFiles = %v, want empty", php.IndexFiles)
	}
}

// --- buildProxyConfig with duplicate upstreams ---

func TestBuildProxyConfigDuplicateUpstreams(t *testing.T) {
	nc := NginxConfig{
		ProxyPass: "http://backend:3000",
		Locations: []NginxLocation{
			{Path: "/api", ProxyPass: "http://backend:3000"},     // Duplicate of server-level.
			{Path: "/web", ProxyPass: "http://web-backend:4000"}, // Different.
		},
	}

	proxy := buildProxyConfig(nc)
	// Should deduplicate the server-level and location-level proxy_pass.
	if len(proxy.Upstreams) != 2 {
		t.Errorf("Upstreams len = %d, want 2 (deduplicated)", len(proxy.Upstreams))
	}

	// Verify addresses.
	addrs := make(map[string]bool)
	for _, u := range proxy.Upstreams {
		addrs[u.Address] = true
	}
	if !addrs["http://backend:3000"] {
		t.Error("expected http://backend:3000 in upstreams")
	}
	if !addrs["http://web-backend:4000"] {
		t.Error("expected http://web-backend:4000 in upstreams")
	}
}

func TestBuildProxyConfigEmpty(t *testing.T) {
	nc := NginxConfig{
		// No proxy_pass anywhere.
	}
	proxy := buildProxyConfig(nc)
	if len(proxy.Upstreams) != 0 {
		t.Errorf("Upstreams len = %d, want 0", len(proxy.Upstreams))
	}
}

// --- buildRedirectConfig with $uri variable ---

func TestBuildRedirectConfigWithURI(t *testing.T) {
	nc := NginxConfig{
		Return: "301 https://example.com$uri",
	}

	redirect := buildRedirectConfig(nc)
	if redirect.Status != 301 {
		t.Errorf("Status = %d, want 301", redirect.Status)
	}
	if redirect.Target != "https://example.com" {
		t.Errorf("Target = %q, want https://example.com", redirect.Target)
	}
	if !redirect.PreservePath {
		t.Error("PreservePath should be true when $uri is used")
	}
}

func TestBuildRedirectConfigWithRequestURI(t *testing.T) {
	nc := NginxConfig{
		Return: "302 https://new.example.com$request_uri",
	}

	redirect := buildRedirectConfig(nc)
	if redirect.Status != 302 {
		t.Errorf("Status = %d, want 302", redirect.Status)
	}
	if redirect.Target != "https://new.example.com" {
		t.Errorf("Target = %q, want https://new.example.com", redirect.Target)
	}
	if !redirect.PreservePath {
		t.Error("PreservePath should be true when $request_uri is used")
	}
}

func TestBuildRedirectConfigFromLocation(t *testing.T) {
	nc := NginxConfig{
		Return: "200 ok", // Not a redirect.
		Locations: []NginxLocation{
			{Path: "/", Return: "301 https://target.com$uri"},
		},
	}

	redirect := buildRedirectConfig(nc)
	if redirect.Status != 301 {
		t.Errorf("Status = %d, want 301", redirect.Status)
	}
	if redirect.Target != "https://target.com" {
		t.Errorf("Target = %q, want https://target.com", redirect.Target)
	}
	if !redirect.PreservePath {
		t.Error("PreservePath should be true")
	}
}

func TestBuildRedirectConfigNoRedirect(t *testing.T) {
	nc := NginxConfig{
		Return: "200 ok", // Not a redirect.
	}

	redirect := buildRedirectConfig(nc)
	if redirect.Status != 0 {
		t.Errorf("Status = %d, want 0", redirect.Status)
	}
	if redirect.Target != "" {
		t.Errorf("Target = %q, want empty", redirect.Target)
	}
}

// --- collectTryFiles from location block (not server level) ---

func TestCollectTryFilesFromLocation(t *testing.T) {
	nc := NginxConfig{
		// No server-level try_files.
		Locations: []NginxLocation{
			{Path: "/", TryFiles: []string{"$uri", "$uri/", "/index.html"}},
			{Path: "/api", TryFiles: []string{"$uri", "/api/index.php"}},
		},
	}

	tryFiles := collectTryFiles(nc)
	// Should use the root location's try_files.
	if len(tryFiles) != 3 {
		t.Fatalf("tryFiles len = %d, want 3", len(tryFiles))
	}
	if tryFiles[2] != "/index.html" {
		t.Errorf("tryFiles[2] = %q, want /index.html", tryFiles[2])
	}
}

func TestCollectTryFilesServerLevel(t *testing.T) {
	nc := NginxConfig{
		TryFiles: []string{"$uri", "$uri/", "=404"},
		Locations: []NginxLocation{
			{Path: "/", TryFiles: []string{"$uri", "/index.html"}},
		},
	}

	tryFiles := collectTryFiles(nc)
	// Should prefer server-level try_files.
	if len(tryFiles) != 3 {
		t.Fatalf("tryFiles len = %d, want 3", len(tryFiles))
	}
	if tryFiles[2] != "=404" {
		t.Errorf("tryFiles[2] = %q, want =404", tryFiles[2])
	}
}

func TestCollectTryFilesNone(t *testing.T) {
	nc := NginxConfig{
		Locations: []NginxLocation{
			{Path: "/api", TryFiles: []string{"$uri"}}, // Not root location.
		},
	}

	tryFiles := collectTryFiles(nc)
	if len(tryFiles) != 0 {
		t.Errorf("tryFiles should be empty for non-root location, got %v", tryFiles)
	}
}

// --- domainsToYAML with all domain types ---

func TestDomainsToYAMLAllTypes(t *testing.T) {
	domains := []config.Domain{
		{
			Host:       "static.com",
			Type:       "static",
			Root:       "/var/www/static",
			Aliases:    []string{"www.static.com"},
			IndexFiles: []string{"index.html"},
		},
		{
			Host: "php.com",
			Type: "php",
			Root: "/var/www/php",
			PHP:  config.PHPConfig{FPMAddress: "127.0.0.1:9000", IndexFiles: []string{"index.php"}},
			SSL:  config.SSLConfig{Mode: "manual", Cert: "/ssl/cert.pem", Key: "/ssl/key.pem"},
		},
		{
			Host:  "proxy.com",
			Type:  "proxy",
			Proxy: config.ProxyConfig{Upstreams: []config.Upstream{{Address: "http://backend:3000"}}},
		},
		{
			Host:     "redirect.com",
			Type:     "redirect",
			Redirect: config.RedirectConfig{Target: "https://target.com", Status: 301, PreservePath: true},
		},
		{
			Host:     "spa.com",
			Type:     "static",
			Root:     "/var/www/spa",
			SPAMode:  true,
			TryFiles: []string{"$uri", "$uri/", "/index.html"},
		},
	}

	yaml := domainsToYAML(domains)

	// Verify header comment.
	if !strings.Contains(yaml, "# Converted from Nginx") {
		t.Error("should have header comment")
	}

	// Verify static site.
	if !strings.Contains(yaml, "host: static.com") {
		t.Error("should contain static.com host")
	}
	if !strings.Contains(yaml, "type: static") {
		t.Error("should contain type: static")
	}
	if !strings.Contains(yaml, "www.static.com") {
		t.Error("should contain alias www.static.com")
	}
	if !strings.Contains(yaml, "index.html") {
		t.Error("should contain index_files: index.html")
	}

	// Verify PHP site.
	if !strings.Contains(yaml, "host: php.com") {
		t.Error("should contain php.com host")
	}
	if !strings.Contains(yaml, "fpm_address: 127.0.0.1:9000") {
		t.Error("should contain fpm_address")
	}
	if !strings.Contains(yaml, "mode: manual") {
		t.Error("should contain SSL mode manual")
	}
	if !strings.Contains(yaml, "cert: /ssl/cert.pem") {
		t.Error("should contain SSL cert path")
	}
	if !strings.Contains(yaml, "key: /ssl/key.pem") {
		t.Error("should contain SSL key path")
	}

	// Verify proxy site.
	if !strings.Contains(yaml, "host: proxy.com") {
		t.Error("should contain proxy.com host")
	}
	if !strings.Contains(yaml, "address: http://backend:3000") {
		t.Error("should contain upstream address")
	}

	// Verify redirect site.
	if !strings.Contains(yaml, "host: redirect.com") {
		t.Error("should contain redirect.com host")
	}
	if !strings.Contains(yaml, "target: https://target.com") {
		t.Error("should contain redirect target")
	}
	if !strings.Contains(yaml, "status: 301") {
		t.Error("should contain redirect status")
	}
	if !strings.Contains(yaml, "preserve_path: true") {
		t.Error("should contain preserve_path")
	}

	// Verify SPA site.
	if !strings.Contains(yaml, "host: spa.com") {
		t.Error("should contain spa.com host")
	}
	if !strings.Contains(yaml, "spa_mode: true") {
		t.Error("should contain spa_mode: true")
	}
	if !strings.Contains(yaml, "try_files:") {
		t.Error("should contain try_files")
	}
}

// --- Nginx location with return directive in location block ---

func TestParseNginxLocationReturn(t *testing.T) {
	input := `
server {
    listen 80;
    server_name example.com;
    root /var/www/html;

    location / {
        return 301 https://example.com$request_uri;
    }
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
	if len(c.Locations) != 1 {
		t.Fatalf("expected 1 location, got %d", len(c.Locations))
	}
	if c.Locations[0].Return != "301 https://example.com$request_uri" {
		t.Errorf("Location Return = %q", c.Locations[0].Return)
	}
}

// --- Nginx try_files at location level (not server) ---

func TestParseNginxTryFilesInLocation(t *testing.T) {
	input := `
server {
    listen 80;
    server_name example.com;
    root /var/www/html;

    location / {
        try_files $uri $uri/ /index.html;
    }
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}

	c := configs[0]
	if len(c.Locations) != 1 {
		t.Fatalf("expected 1 location, got %d", len(c.Locations))
	}
	if len(c.Locations[0].TryFiles) != 3 {
		t.Errorf("TryFiles = %v, want 3 elements", c.Locations[0].TryFiles)
	}
}

// --- determineDomainType with location-level redirect ---

func TestDetermineDomainTypeLocationRedirect(t *testing.T) {
	nc := NginxConfig{
		Locations: []NginxLocation{
			{Path: "/", Return: "301 https://target.com"},
		},
	}

	dtype := determineDomainType(nc)
	if dtype != "redirect" {
		t.Errorf("Type = %q, want redirect", dtype)
	}
}

func TestDetermineDomainTypeStatic(t *testing.T) {
	nc := NginxConfig{
		Root: "/var/www/html",
	}

	dtype := determineDomainType(nc)
	if dtype != "static" {
		t.Errorf("Type = %q, want static", dtype)
	}
}

func TestDetermineDomainTypePHPFromLocation(t *testing.T) {
	nc := NginxConfig{
		Root: "/var/www/html",
		Locations: []NginxLocation{
			{Path: `\.php$`, Modifier: "~", FastCGI: "127.0.0.1:9000"},
		},
	}

	dtype := determineDomainType(nc)
	if dtype != "php" {
		t.Errorf("Type = %q, want php", dtype)
	}
}

// --- NginxToYAML with SSL config ---

func TestNginxToYAMLWithSSL(t *testing.T) {
	input := `
server {
    listen 443 ssl;
    server_name secure.com;
    root /var/www/secure;
    ssl_certificate /etc/ssl/cert.pem;
    ssl_certificate_key /etc/ssl/key.pem;
}
`
	yaml, err := NginxToYAML(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(yaml, "host: secure.com") {
		t.Error("should contain host")
	}
	if !strings.Contains(yaml, "mode: manual") {
		t.Error("should contain SSL mode manual")
	}
	if !strings.Contains(yaml, "cert: /etc/ssl/cert.pem") {
		t.Error("should contain cert path")
	}
	if !strings.Contains(yaml, "key: /etc/ssl/key.pem") {
		t.Error("should contain key path")
	}
}

// --- Multiple locations with mixed directives ---

func TestParseNginxMixedLocations(t *testing.T) {
	input := `
server {
    listen 80;
    server_name mixed.com;
    root /var/www/html;

    location / {
        try_files $uri $uri/ /index.php?$args;
    }

    location ~ \.php$ {
        fastcgi_pass unix:/run/php-fpm.sock;
    }

    location /api {
        proxy_pass http://api-backend:3000;
    }
}
`
	configs, err := ParseNginx(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}

	c := configs[0]
	if len(c.Locations) != 3 {
		t.Fatalf("expected 3 locations, got %d", len(c.Locations))
	}

	domains := ConvertToUWAS(configs)
	d := domains[0]
	// PHP should take priority since fastcgi_pass is present.
	if d.Type != "php" {
		t.Errorf("Type = %q, want php", d.Type)
	}
}

// --- domainsToYAML with auto SSL mode ---

func TestDomainsToYAMLAutoSSL(t *testing.T) {
	domains := []config.Domain{
		{
			Host: "auto-ssl.com",
			Type: "static",
			Root: "/var/www/auto",
			SSL:  config.SSLConfig{Mode: "auto"},
		},
	}

	yaml := domainsToYAML(domains)
	if !strings.Contains(yaml, "mode: auto") {
		t.Error("should contain SSL mode auto")
	}
	// Should NOT contain cert/key lines for auto mode.
	if strings.Contains(yaml, "cert:") {
		t.Error("should not contain cert line for auto mode")
	}
	if strings.Contains(yaml, "key:") {
		t.Error("should not contain key line for auto mode")
	}
}

// --- domainsToYAML with redirect status 0 (no status line) ---

func TestDomainsToYAMLRedirectNoStatus(t *testing.T) {
	domains := []config.Domain{
		{
			Host:     "redir.com",
			Type:     "redirect",
			Redirect: config.RedirectConfig{Target: "https://target.com", Status: 0},
		},
	}

	yaml := domainsToYAML(domains)
	if !strings.Contains(yaml, "target: https://target.com") {
		t.Error("should contain target")
	}
	// Status 0 should not appear.
	if strings.Contains(yaml, "status: 0") {
		t.Error("should not output status: 0")
	}
}
