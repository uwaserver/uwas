package server

import (
	"net"
	"net/http"
	"net/http/fcgi"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// Direct *.php requests and /wp-* paths skip .htaccess internal rewrites, but
// its access control must still apply: WordPress hardening denies exactly
// these scripts with [F] rules, and skipping the whole file ran them anyway.
func TestHtaccessDenyAppliesToDirectPHPRequests(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ran := make(chan string, 16)
	go fcgi.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ran <- filepath.Base(fcgi.ProcessEnv(r)["SCRIPT_FILENAME"])
		w.Write([]byte("ok"))
	}))
	t.Cleanup(func() { ln.Close() })

	dir := t.TempDir()
	for _, f := range []string{"index.php", "wp-login.php", "xmlrpc.php", "wp-includes/version.php", "a.php"} {
		p := filepath.Join(dir, filepath.FromSlash(f))
		os.MkdirAll(filepath.Dir(p), 0755)
		os.WriteFile(p, []byte("<?php"), 0644)
	}
	ht := "RewriteEngine On\n" +
		"RewriteRule ^index\\.php$ - [L]\n" +
		"RewriteRule ^xmlrpc\\.php$ - [F,L]\n" +
		"RewriteRule ^wp-includes/[^/]+\\.php$ - [F,L]\n" +
		"RewriteRule ^a\\.php$ /index.php [L]\n" +
		"RewriteCond %{REQUEST_FILENAME} !-f\n" +
		"RewriteRule . /index.php [L]\n"
	os.WriteFile(filepath.Join(dir, ".htaccess"), []byte(ht), 0644)

	s := newDispatchTestServer(t, []config.Domain{{
		Host:     "php-ht.test",
		Type:     "php",
		Root:     dir,
		SSL:      config.SSLConfig{Mode: "off"},
		Htaccess: config.HtaccessConfig{Mode: "import"},
		PHP:      config.PHPConfig{FPMAddress: ln.Addr().String()},
	}})
	get := func(path string) (int, string) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		req.Host = "php-ht.test"
		s.handleRequest(rec, req)
		select {
		case script := <-ran:
			return rec.Code, script
		default:
			return rec.Code, ""
		}
	}

	for _, path := range []string{"/xmlrpc.php", "/wp-includes/version.php"} {
		if code, script := get(path); code != http.StatusForbidden || script != "" {
			t.Errorf("GET %s = %d (ran %q), want 403 and no PHP execution", path, code, script)
		}
	}
	if code, script := get("/wp-login.php"); code != http.StatusOK || script != "wp-login.php" {
		t.Errorf("GET /wp-login.php = %d (ran %q), want 200 running wp-login.php", code, script)
	}
	// Internal rewrites stay off for direct .php requests.
	if code, script := get("/a.php"); code != http.StatusOK || script != "a.php" {
		t.Errorf("GET /a.php = %d (ran %q), want 200 running a.php unrewritten", code, script)
	}
	if code, script := get("/pretty/url"); code != http.StatusOK || script != "index.php" {
		t.Errorf("GET /pretty/url = %d (ran %q), want front controller index.php", code, script)
	}
}
