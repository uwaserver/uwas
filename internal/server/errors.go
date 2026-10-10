package server

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/pathsafe"
)

// maxErrorPageSize bounds a custom error page read from a tenant docroot.
const maxErrorPageSize = 1 << 20

// readErrorPage reads a custom error page named by tenant-controlled config or
// .htaccess. The server runs as root, so the page must stay inside the docroot
// ("../" and symlinked directories are refused), the final component must not
// be a symlink, and only a bounded regular file is read (a FIFO never blocks).
func readErrorPage(root, page string) ([]byte, error) {
	full := filepath.Join(root, page)
	base, err := pathsafe.CachedBase(root)
	if err != nil || !base.Contains(full) {
		return nil, os.ErrPermission
	}
	f, err := os.OpenFile(full, os.O_RDONLY|noFollowFlag|nonBlockFlag, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxErrorPageSize {
		return nil, os.ErrPermission
	}
	data, err := io.ReadAll(io.LimitReader(f, maxErrorPageSize+1))
	if err != nil || len(data) > maxErrorPageSize {
		return nil, os.ErrPermission
	}
	return data, nil
}

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
					if data, err := readErrorPage(domain.Root, pagePath); err == nil {
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
				if data, err := readErrorPage(domain.Root, pagePath); err == nil {
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
