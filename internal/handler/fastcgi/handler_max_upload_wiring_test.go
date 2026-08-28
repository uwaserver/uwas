package fastcgi

import (
	"net"
	"net/http"
	"net/http/fcgi"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
)

// Guards the wiring, not the rendering: BuildEnv can render the directives
// perfectly while ServeWith never passes domain.PHP.MaxUpload to it. Tests
// that call BuildEnv directly stay green through exactly that mistake, which
// is the shape of the bug being fixed here.

// alinanParams runs one request through ServeWith against a real FastCGI
// responder and returns the params it received. fcgi.ProcessEnv surfaces the
// params the stdlib does not map onto the request — PHP_ADMIN_VALUE included.
func alinanParams(t *testing.T, domain *config.Domain) map[string]string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	var (
		mu     sync.Mutex
		params map[string]string
		wg     sync.WaitGroup
	)
	wg.Add(1)

	go func() {
		_ = fcgi.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			if params == nil {
				params = fcgi.ProcessEnv(r)
				wg.Done()
			}
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		}))
	}()

	h := New(logger.New("error", "text"))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/upload.php", strings.NewReader(""))
	ctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(ctx)
	ctx.DocumentRoot = domain.Root
	ctx.ResolvedPath = domain.Root + "/upload.php"

	h.ServeWith(ctx, domain, ln.Addr().String(), domain.PHP.Env)

	bitti := make(chan struct{})
	go func() { wg.Wait(); close(bitti) }()
	select {
	case <-bitti:
	case <-time.After(5 * time.Second):
		t.Fatal("FastCGI yanıtlayıcısı istek almadı")
	}

	mu.Lock()
	defer mu.Unlock()
	return params
}

func TestServeWithPassesMaxUploadToPHP(t *testing.T) {
	got := alinanParams(t, &config.Domain{
		Host: "php.test",
		Root: "/srv/www",
		PHP:  config.PHPConfig{MaxUpload: 32 * config.MB, IndexFiles: []string{"index.php"}},
	})

	admin := got["PHP_ADMIN_VALUE"]
	if !strings.Contains(admin, "upload_max_filesize = 33554432") {
		t.Errorf("PHP_ADMIN_VALUE = %q — php.max_upload ServeWith'ten geçmiyor", admin)
	}
	if !strings.Contains(admin, "post_max_size = 34603008") {
		t.Errorf("post_max_size eksik: %q", admin)
	}
	if !strings.Contains(admin, "open_basedir = ") {
		t.Errorf("open_basedir kayboldu: %q", admin)
	}
}

func TestServeWithUnsetMaxUploadSendsNoLimit(t *testing.T) {
	got := alinanParams(t, &config.Domain{
		Host: "php.test",
		Root: "/srv/www",
		PHP:  config.PHPConfig{IndexFiles: []string{"index.php"}},
	})

	if admin := got["PHP_ADMIN_VALUE"]; strings.Contains(admin, "upload_max_filesize") {
		t.Errorf("ayarsız max_upload gönderildi: %q", admin)
	}
}
