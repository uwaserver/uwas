//go:build unix

package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// F1450: custom error pages come from tenant-controlled config/.htaccess and
// are read by a root server process; they must not escape the docroot.
func TestRenderDomainErrorStaysInDocroot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "site")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{filepath.Join(root, "errors"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, c string) {
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(base, "secret.txt"), "TOPSECRET")
	write(filepath.Join(outside, "x.html"), "TOPSECRET")
	write(filepath.Join(root, "errors", "404.html"), "LEGIT404")
	write(filepath.Join(root, "big.html"), "TOPSECRET"+strings.Repeat("a", 1<<20))
	if err := os.Symlink(filepath.Join(base, "secret.txt"), filepath.Join(root, "link.html")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linkdir")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "fifo.html"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	write(filepath.Join(root, ".htaccess"), "ErrorDocument 403 ../secret.txt\nErrorDocument 500 link.html\nErrorDocument 401 linkdir/x.html\n")

	cfg := &config.Config{
		Global: config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
		Domains: []config.Domain{{Host: "f1450.local", Root: root, Type: "static",
			SSL: config.SSLConfig{Mode: "off"}, Htaccess: config.HtaccessConfig{Mode: "import"},
			ErrorPages: map[int]string{
				404: "errors/404.html", 502: "../secret.txt", 503: "link.html",
				504: "linkdir/x.html", 400: "big.html", 410: "fifo.html",
			}}},
	}
	s := New(cfg, logger.New("error", "text"))
	d := &cfg.Domains[0]
	render := func(code int) (string, int) {
		done := make(chan string, 1)
		rec := httptest.NewRecorder()
		go func() { s.renderDomainError(rec, code, d); done <- rec.Body.String() }()
		select {
		case b := <-done:
			return b, rec.Code
		case <-time.After(5 * time.Second):
			t.Fatalf("code %d: renderDomainError blocked", code)
			return "", 0
		}
	}

	if body, _ := render(404); !strings.Contains(body, "LEGIT404") {
		t.Fatalf("control: in-docroot page not served: %q", body)
	}
	for name, code := range map[string]int{
		"config ../": 502, "config symlink": 503, "config symlinked dir": 504,
		"config oversized": 400, "config fifo": 410,
		"htaccess ../": 403, "htaccess symlink": 500, "htaccess symlinked dir": 401,
	} {
		body, status := render(code)
		if strings.Contains(body, "TOPSECRET") {
			t.Errorf("%s: leaked content outside docroot", name)
		}
		if status != code || !strings.Contains(body, "UWAS") {
			t.Errorf("%s: status=%d, want default page with %d", name, status, code)
		}
	}
}
