package server

import (
	"net"
	"net/http"
	"net/http/fcgi"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// htaccessPHPValueSent serves "/" (index.php through the .htaccess path) and
// returns the PHP_VALUE param a FastCGI backend actually received.
func htaccessPHPValueSent(t *testing.T, htaccess string) (string, bool) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got := make(chan map[string]string, 1)
	go fcgi.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- fcgi.ProcessEnv(r)
	}))

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.php"), []byte("<?php echo 1;"), 0644)
	os.WriteFile(filepath.Join(dir, ".htaccess"), []byte(htaccess), 0644)
	s := newDispatchTestServer(t, []config.Domain{{
		Host: "phpvalue.test", Type: "php", Root: dir, SSL: config.SSLConfig{Mode: "off"},
		Htaccess: config.HtaccessConfig{Mode: "import"}, PHP: config.PHPConfig{FPMAddress: ln.Addr().String()},
	}})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "phpvalue.test"
	s.handleRequest(rec, req)
	select {
	case env := <-got:
		v, ok := env["PHP_VALUE"]
		return v, ok
	default:
		t.Fatalf("FastCGI backend got no request (status %d)", rec.Code)
		return "", false
	}
}

// A tenant-written .htaccess must not hand PHP a directive the per-domain
// php.ini override path blocks (extension, ffi.enable, error_log, ...), nor
// smuggle one in behind a CR that PHP's ini parser treats as a line break.
func TestHtaccessPHPValueHonoursDirectiveBlocklist(t *testing.T) {
	for name, ht := range map[string]string{
		"extension":         "php_value extension /tmp/evil.so\n",
		"zend_extension":    "php_value zend_extension /tmp/evil.so\n",
		"error_log":         "php_value error_log /var/www/other.test/public/x.php\n",
		"session.save_path": "php_value session.save_path /var/www/other.test/sess\n",
		"ffi.enable flag":   "php_flag ffi.enable on\n",
		"CR in value":       "php_value memory_limit \"128M\rextension=/tmp/evil.so\"\n",
		"CR in name":        "php_value \"x\rzend_extension\" /tmp/evil.so\n",
	} {
		if v, ok := htaccessPHPValueSent(t, ht); ok {
			t.Errorf("%s: PHP_VALUE = %q, want no PHP_VALUE", name, v)
		}
	}

	v, _ := htaccessPHPValueSent(t, "php_value memory_limit 256M\nphp_value extension /tmp/evil.so\nphp_flag display_errors on\n")
	lines := strings.Split(v, "\n")
	sort.Strings(lines)
	if got := strings.Join(lines, "|"); got != "display_errors = 1|memory_limit = 256M" {
		t.Errorf("mixed .htaccess: PHP_VALUE lines = %q, want allowed directives only", got)
	}
}
