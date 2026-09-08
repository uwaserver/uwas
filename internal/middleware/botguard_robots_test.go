package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

// robots.txt exists so crawlers can read the rules; blocking a bot from it is
// self-defeating and locks legitimate search engines (which fetch robots.txt
// first) out of the whole site. The listed SEO crawlers (AhrefsBot, etc.) and
// even an empty UA must still get these bot-intended, public paths.
func TestBotGuardServesBotIntendedPaths(t *testing.T) {
	log := logger.New("error", "text")
	served := false
	h := BotGuard(log, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served = true
		w.WriteHeader(http.StatusOK)
	}))

	exempt := []string{"/robots.txt", "/sitemap.xml", "/sitemap_index.xml", "/sitemap.xml.gz",
		"/.well-known/acme-challenge/xyz", "/.well-known/security.txt", "/favicon.ico"}

	for _, path := range exempt {
		for _, ua := range []string{"AhrefsBot/7.0", "SemrushBot", "", "sqlmap/1.7"} {
			served = false
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", path, nil)
			req.RemoteAddr = "203.0.113.9:5555"
			if ua != "" {
				req.Header.Set("User-Agent", ua)
			}
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK || !served {
				t.Errorf("path %s with UA %q: status %d served=%v, want 200 served — bot-intended paths must not be blocked",
					path, ua, rec.Code, served)
			}
		}
	}
}

// The exemption must be narrow: a listed bot hitting a normal content path is
// still blocked, so this does not become a bot-guard bypass.
func TestBotGuardStillBlocksBotsOnContentPaths(t *testing.T) {
	log := logger.New("error", "text")
	h := BotGuard(log, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, path := range []string{"/", "/index.html", "/wp-login.php", "/robots.txt.bak", "/sitemap"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		req.RemoteAddr = "203.0.113.9:5555"
		req.Header.Set("User-Agent", "AhrefsBot/7.0")
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("path %s with AhrefsBot: status %d, want 403 (content paths still bot-blocked)", path, rec.Code)
		}
	}
}
