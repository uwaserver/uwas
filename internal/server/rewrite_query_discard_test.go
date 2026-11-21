package server

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
)

// TestInternalRewriteDiscardsQuery: an internal rewrite with [QSD], or whose
// substitution ends in "?", must drop the incoming query string, as the rewrite
// engine computes (F6701). The server applied the rewritten query only when it
// was non-empty, so ?token=secret still reached the target (F68100).
func TestInternalRewriteDiscardsQuery(t *testing.T) {
	cases := []struct {
		name, to, flags, wantQuery string
	}{
		{"QSD", "/new", "QSD", ""},
		{"trailing ?", "/new?", "", ""},
		{"own query", "/new?x=1", "", "x=1"},
		{"plain keeps query", "/new", "", "token=secret"},
	}
	for _, c := range cases {
		for _, via := range []string{"yaml", "htaccess"} {
			t.Run(via+"/"+c.name, func(t *testing.T) {
				root := t.TempDir()
				dom := config.Domain{Host: "q.test", Root: root, Type: "static", SSL: config.SSLConfig{Mode: "off"}}
				flags := []string{"L"}
				if c.flags != "" {
					flags = []string{c.flags, "L"}
				}
				if via == "yaml" {
					dom.Rewrites = []config.RewriteRule{{Match: "^/old$", To: c.to, Flags: flags}}
				} else {
					ht := "RewriteEngine On\nRewriteRule ^old$ " + c.to + " [L"
					if c.flags != "" {
						ht += "," + c.flags
					}
					ht += "]\n"
					if err := os.WriteFile(filepath.Join(root, ".htaccess"), []byte(ht), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				cfg := &config.Config{
					Global:  config.GlobalConfig{WorkerCount: "1", LogLevel: "error", LogFormat: "text"},
					Domains: []config.Domain{dom},
				}
				s := New(cfg, logger.New("error", "text"))
				req := httptest.NewRequest("GET", "/old?token=secret", nil)
				ctx := router.AcquireContext(httptest.NewRecorder(), req)
				defer router.ReleaseContext(ctx)
				if via == "yaml" {
					s.applyRewrites(ctx, &cfg.Domains[0])
				} else {
					s.applyHtaccess(ctx, &cfg.Domains[0], true)
				}
				if got := ctx.Request.URL.Path; got != "/new" {
					t.Fatalf("path = %q, want /new", got)
				}
				if got := ctx.Request.URL.RawQuery; got != c.wantQuery {
					t.Fatalf("query = %q, want %q", got, c.wantQuery)
				}
			})
		}
	}
}
