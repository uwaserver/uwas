package server

// F49000/F49001: files that are routinely left in a docroot and must never be
// answered with their content. docs/SPECIFICATION.md §16.2 blocks composer.*,
// package.json, node_modules/ and vendor/ by default; the static handler
// refuses dotfiles and PHP copies on its own.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func secretProbeTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"index.html":                         "home",
		"robots.txt":                         "User-agent: *",
		".well-known/security.txt":           "Contact: mailto:a@b.test",
		".well-known/acme-challenge/tok":     "token",
		"composer.json":                      `{"require":{}}`,
		"composer.lock":                      `{}`,
		"package.json":                       `{"name":"x"}`,
		"package-lock.json":                  `{}`,
		"yarn.lock":                          "x",
		"pnpm-lock.yaml":                     "x",
		"vendor/composer/installed.json":     `{}`,
		"vendor/autoload.php":                "<?php // secret",
		"node_modules/left-pad/package.json": `{}`,
		"node_modules/left-pad/index.js":     "module.exports=1",
		"dump.sql":                           "INSERT INTO users VALUES ('admin','hash');",
		"db_backup.sql":                      "x",
		"backup.zip":                         "PK",
		"site.tar.gz":                        "x",
		"error_log":                          "PHP Fatal error: /home/u/public_html/x.php",
		"debug.log":                          "x",
		"php.ini":                            "display_errors=On",
		"web.config":                         "<configuration/>",
		"Dockerfile":                         "FROM x",
		"docker-compose.yml":                 "services: {}",
		".env":                               "DB_PASSWORD=hunter2",
		".git/config":                        "[core]",
		".idea/workspace.xml":                "x",
		// Front-end assets that live under a directory called vendor and must
		// keep working: Bootstrap-style templates put it at the root.
		"vendor/bootstrap/css/bootstrap.css": "body{}",
		"vendor/jquery/jquery.min.js":        "/*jq*/",
		"assets/vendor/aos/aos.js":           "/*aos*/",
	}
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func secretProbeStatus(s *Server, target string) int {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Host = "d.test"
	req.RemoteAddr = "198.51.100.1:1"
	req.Header.Set("User-Agent", "uwas-test")
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	return rec.Code
}

func TestSecretFilesAreNotServed(t *testing.T) {
	s := newDeepPathServer(t, secretProbeTree(t))
	blocked := []string{
		"/composer.json", "/composer.lock", "/package.json", "/package-lock.json",
		"/vendor/composer/installed.json", "/vendor/autoload.php",
		"/node_modules/left-pad/package.json", "/node_modules/left-pad/index.js",
		"/.env", "/.git/config", "/.idea/workspace.xml",
		// Encoding and normalisation variants of a blocked name.
		"/%63omposer.json", "/./composer.json", "//composer.json",
		"/x/../composer.json", "/composer.json/.", "/composer.json/",
		"/%2e%2e/composer.json", "/%2eenv", "/.%65nv", "/.git/../.git/config",
		"/vendor/%2e%2e/vendor/autoload.php", "/Vendor/autoload.php",
		"/node_modules/../node_modules/left-pad/package.json",
		"/.well-known/../.env", "/.well-known/%2e%2e/.git/config", "/%2e/.env",
		"/index.html/../.env", "/vendor/x/../composer/installed.json",
		"/yarn.lock", "/pnpm-lock.yaml", "/php.ini", "/error_log",
	}
	for _, p := range blocked {
		if c := secretProbeStatus(s, p); c == http.StatusOK {
			t.Errorf("PROBLEM CONFIRMED: GET %s = 200, want 403/404", p)
		}
	}
	// Controls: public files, ACME and security.txt, and front-end assets under
	// a directory named vendor. Downloads that only the WAF's file_probe family
	// refuses (dumps, archives, logs) stay outside this always-on guard.
	for _, p := range []string{
		"/", "/index.html", "/robots.txt", "/.well-known/security.txt", "/.well-known/acme-challenge/tok",
		"/vendor/bootstrap/css/bootstrap.css", "/vendor/jquery/jquery.min.js", "/assets/vendor/aos/aos.js",
		"/dump.sql", "/backup.zip", "/debug.log",
	} {
		if c := secretProbeStatus(s, p); c != http.StatusOK {
			t.Errorf("control: GET %s = %d, want 200", p, c)
		}
	}
}
