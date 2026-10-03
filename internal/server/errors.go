package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/uwaserver/uwas/internal/config"
)

var defaultErrorTitles = map[int]string{
	400: "Bad Request",
	403: "Forbidden",
	404: "Not Found",
	500: "Internal Server Error",
	502: "Bad Gateway",
	503: "Service Unavailable",
	504: "Gateway Timeout",
}

// renderDomainError serves a custom error page if configured, otherwise the default styled page.
// Precedence: the domain's .htaccess ErrorDocument first (per-directory override, Apache
// semantics — looked up in the htaccess cache entry), then domain.ErrorPages from YAML config,
// then the built-in page. Both maps are immutable at runtime (config reloads atomically swap the
// entire config; htaccess entries are re-parsed when the file changes), so no extra locking is
// needed beyond getHtaccessRuleSet's own. Nil-receiver safe: without a Server (tests) only the
// config and built-in paths apply.
func (s *Server) renderDomainError(w http.ResponseWriter, code int, domain *config.Domain) {
	if domain != nil && domain.Root != "" {
		// .htaccess ErrorDocument — per-directory override.
		if s != nil {
			if entry := s.getHtaccessRuleSet(domain.Root); entry != nil && entry.errorPages != nil {
				if pagePath, ok := entry.errorPages[code]; ok {
					if data, err := os.ReadFile(filepath.Join(domain.Root, pagePath)); err == nil {
						w.Header().Set("Content-Type", "text/html; charset=utf-8")
						w.WriteHeader(code)
						w.Write(data)
						return
					}
				}
			}
		}
		if domain.ErrorPages != nil {
			if pagePath, ok := domain.ErrorPages[code]; ok {
				if data, err := os.ReadFile(filepath.Join(domain.Root, pagePath)); err == nil {
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					w.WriteHeader(code)
					w.Write(data)
					return
				}
			}
		}
	}
	renderErrorPage(w, code)
}

func renderErrorPage(w http.ResponseWriter, code int) {
	title := defaultErrorTitles[code]
	if title == "" {
		title = http.StatusText(code)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	fmt.Fprintf(w, `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>%d %s</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:system-ui,-apple-system,sans-serif;background:#0f172a;color:#e2e8f0;
display:flex;justify-content:center;align-items:center;min-height:100vh}
.container{text-align:center;padding:2rem}
.code{font-size:6rem;font-weight:800;color:#2563eb;line-height:1}
.title{font-size:1.5rem;margin:.5rem 0 1rem;color:#94a3b8}
.line{width:60px;height:3px;background:#2563eb;margin:1rem auto}
.msg{color:#64748b;font-size:.9rem}
</style>
</head>
<body>
<div class="container">
<div class="code">%d</div>
<div class="title">%s</div>
<div class="line"></div>
<p class="msg">UWAS — Unified Web Application Server</p>
</div>
</body>
</html>`, code, title, code, title)
}
