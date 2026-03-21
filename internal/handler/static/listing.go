package static

import (
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/uwaserver/uwas/internal/router"
)

// ServeDirListing renders a minimal HTML directory listing.
func ServeDirListing(ctx *router.RequestContext, dirPath, urlPath string) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		ctx.Response.Error(http.StatusInternalServerError, "500 Internal Server Error")
		return
	}

	// Sort: directories first, then alphabetical
	sort.Slice(entries, func(i, j int) bool {
		di, dj := entries[i].IsDir(), entries[j].IsDir()
		if di != dj {
			return di
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})

	w := ctx.Response
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	escapedPath := html.EscapeString(urlPath)
	fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Index of %s</title>
<style>
body{font-family:system-ui,sans-serif;margin:2em;color:#1e293b}
h1{font-size:1.25em;border-bottom:1px solid #e2e8f0;padding-bottom:.5em}
table{border-collapse:collapse;width:100%%}
th,td{text-align:left;padding:.35em 1em}
th{border-bottom:2px solid #e2e8f0;font-size:.85em;color:#64748b}
tr:hover{background:#f8fafc}
a{color:#2563eb;text-decoration:none}
a:hover{text-decoration:underline}
.size,.date{color:#64748b;font-size:.9em}
</style></head><body>
<h1>Index of %s</h1>
<table><tr><th>Name</th><th>Size</th><th>Modified</th></tr>
`, escapedPath, escapedPath)

	// Parent directory link
	if urlPath != "/" {
		parent := filepath.Dir(strings.TrimRight(urlPath, "/"))
		if parent == "" {
			parent = "/"
		}
		fmt.Fprintf(w, `<tr><td><a href="%s">../</a></td><td class="size">-</td><td class="date">-</td></tr>`+"\n",
			html.EscapeString(parent))
	}

	for _, entry := range entries {
		name := entry.Name()

		// Skip dotfiles
		if strings.HasPrefix(name, ".") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		displayName := html.EscapeString(name)
		link := html.EscapeString(filepath.ToSlash(filepath.Join(urlPath, name)))
		size := formatSize(info.Size())
		date := info.ModTime().Format("2006-01-02 15:04")

		if entry.IsDir() {
			displayName += "/"
			link += "/"
			size = "-"
		}

		fmt.Fprintf(w, `<tr><td><a href="%s">%s</a></td><td class="size">%s</td><td class="date">%s</td></tr>`+"\n",
			link, displayName, size, date)
	}

	fmt.Fprint(w, `</table></body></html>`)
}

func formatSize(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/(1<<30))
	case bytes >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1<<20))
	case bytes >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(bytes)/(1<<10))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
