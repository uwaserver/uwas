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

// htaccessAccessSite lays out a PHP docroot (files maps relative path to
// content, .htaccess files included) behind a recording FastCGI backend.
func htaccessAccessSite(t *testing.T, files map[string]string) (*Server, chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ran := make(chan string, 16)
	go fcgi.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ran <- fcgi.ProcessEnv(r)["SCRIPT_FILENAME"]
		w.Write([]byte("PHP-RAN"))
	}))
	t.Cleanup(func() { ln.Close() })
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := newDispatchTestServer(t, []config.Domain{{
		Host:     "htaccess-access.test",
		Type:     "php",
		Root:     dir,
		SSL:      config.SSLConfig{Mode: "off"},
		Htaccess: config.HtaccessConfig{Mode: "import"},
		PHP:      config.PHPConfig{FPMAddress: ln.Addr().String()},
	}})
	return s, ran
}

// TestHtaccessTopLevelAndSubdirAccessEnforced: a top-level "Require all
// denied" / "Deny from all" and the access control of subdirectory .htaccess
// files (e.g. wp-content/uploads/.htaccess denying *.php) used to be ignored,
// so denied files were served and denied scripts executed.
func TestHtaccessTopLevelAndSubdirAccessEnforced(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		path  string
		want  int
	}{
		{"root Require all denied", map[string]string{".htaccess": "Require all denied\n", "a.txt": "x"}, "/a.txt", http.StatusForbidden},
		{"root Deny from all", map[string]string{".htaccess": "Order deny,allow\nDeny from all\n", "a.txt": "x"}, "/a.txt", http.StatusForbidden},
		{"uploads <Files *.php> deny", map[string]string{"wp-content/uploads/.htaccess": "<Files *.php>\ndeny from all\n</Files>\n", "wp-content/uploads/x.php": "x"}, "/wp-content/uploads/x.php", http.StatusForbidden},
		{"subdir Require all denied", map[string]string{"private/.htaccess": "Require all denied\n", "private/a/b.txt": "x"}, "/private/a/b.txt", http.StatusForbidden},
		{"subdir AuthUserFile", map[string]string{"sub/.htaccess": "AuthUserFile .htpasswd\nRequire valid-user\n", "sub/a.txt": "x"}, "/sub/a.txt", http.StatusForbidden},
		{"child grant overrides parent deny", map[string]string{".htaccess": "Require all denied\n", "pub/.htaccess": "Require all granted\n", "pub/a.txt": "x"}, "/pub/a.txt", http.StatusOK},
		{"sibling of denied dir served", map[string]string{"private/.htaccess": "Require all denied\n", "pub/a.txt": "x"}, "/pub/a.txt", http.StatusOK},
		{"single-IP Deny does not block everyone", map[string]string{".htaccess": "Order allow,deny\nAllow from all\nDeny from 203.0.113.9\n", "a.txt": "x"}, "/a.txt", http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, ran := htaccessAccessSite(t, c.files)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", c.path, nil)
			req.Host = "htaccess-access.test"
			s.handleRequest(rec, req)
			if rec.Code != c.want {
				t.Fatalf("GET %s = %d, want %d", c.path, rec.Code, c.want)
			}
			if c.want == http.StatusForbidden {
				select {
				case script := <-ran:
					t.Fatalf("denied request executed %s", script)
				default:
				}
			}
		})
	}
}
