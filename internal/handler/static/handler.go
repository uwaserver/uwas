package static

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/router"
)

type Handler struct {
	mime *MIMERegistry
}

func New() *Handler {
	return &Handler{
		mime: NewMIMERegistry(nil),
	}
}

// Serve handles the request by serving the resolved static file.
func (h *Handler) Serve(ctx *router.RequestContext) {
	path := ctx.ResolvedPath
	w := ctx.Response
	r := ctx.Request

	// Security: reject dotfiles in any path component (e.g., .git, .env)
	for _, component := range strings.Split(filepath.ToSlash(path), "/") {
		if strings.HasPrefix(component, ".") && component != "." && component != ".." {
			w.Error(http.StatusForbidden, "403 Forbidden")
			return
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		w.Error(http.StatusNotFound, "404 Not Found")
		return
	}

	// Set content type
	ct := h.mime.Lookup(path)
	w.Header().Set("Content-Type", ct)

	// Try pre-compressed version
	if h.servePreCompressed(w, r, path, info) {
		return
	}

	// Generate weak ETag: W/"mtime-size"
	etag := generateETag(info)
	w.Header().Set("ETag", etag)

	// Let http.ServeContent handle If-None-Match, If-Modified-Since, Range requests
	f, err := os.Open(path)
	if err != nil {
		w.Error(http.StatusInternalServerError, "500 Internal Server Error")
		return
	}
	defer f.Close()

	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), f)
}

// servePreCompressed checks for .br or .gz pre-compressed files.
func (h *Handler) servePreCompressed(w *router.ResponseWriter, r *http.Request, path string, origInfo fs.FileInfo) bool {
	accept := r.Header.Get("Accept-Encoding")
	if accept == "" {
		return false
	}

	// Brotli has priority over gzip
	type preComp struct {
		ext      string
		encoding string
	}
	candidates := []preComp{
		{".br", "br"},
		{".gz", "gzip"},
	}

	for _, c := range candidates {
		if !strings.Contains(accept, c.encoding) {
			continue
		}
		compPath := path + c.ext
		compInfo, err := os.Stat(compPath)
		if err != nil || compInfo.IsDir() {
			continue
		}

		f, err := os.Open(compPath)
		if err != nil {
			continue
		}

		w.Header().Set("Content-Encoding", c.encoding)
		w.Header().Set("Content-Type", h.mime.Lookup(path)) // original file's MIME
		w.Header().Set("Vary", "Accept-Encoding")
		w.Header().Set("ETag", generateETag(origInfo)+"-"+c.encoding)

		http.ServeContent(w, r, filepath.Base(path), origInfo.ModTime(), f)
		f.Close()
		return true
	}

	return false
}

func generateETag(info fs.FileInfo) string {
	raw := fmt.Sprintf("%d-%d", info.ModTime().UnixNano(), info.Size())
	hash := sha256.Sum256([]byte(raw))
	return fmt.Sprintf(`W/"%x"`, hash[:8])
}

// ResolveRequest handles try_files logic and path resolution for a request.
// Returns true if a file was resolved, false if nothing found.
func ResolveRequest(ctx *router.RequestContext, domain *config.Domain) bool {
	uri := ctx.Request.URL.Path
	docRoot := domain.Root

	// Security: prevent path traversal
	cleanURI := filepath.Clean("/" + uri)

	candidates := domain.TryFiles
	if len(candidates) == 0 {
		switch domain.Type {
		case "php":
			candidates = []string{"$uri", "$uri/", "/index.php"}
		default:
			candidates = []string{"$uri", "$uri/", "$uri/index.html"}
		}
	}

	// SPA mode override
	if domain.SPAMode {
		candidates = []string{"$uri", "$uri/", "/index.html"}
	}

	indexFiles := domain.IndexFiles
	if len(indexFiles) == 0 {
		indexFiles = []string{"index.html", "index.htm"}
	}

	for _, candidate := range candidates {
		resolved := expandTryFileVar(candidate, cleanURI)
		fullPath := filepath.Join(docRoot, filepath.Clean("/"+resolved))

		// Security: path must stay within document root
		absRoot, _ := filepath.Abs(docRoot)
		absPath, _ := filepath.Abs(fullPath)
		if !strings.HasPrefix(absPath, absRoot) {
			continue
		}

		stat, err := os.Stat(fullPath)
		if err != nil {
			continue
		}

		if stat.IsDir() {
			// Try index files within directory
			for _, idx := range indexFiles {
				idxPath := filepath.Join(fullPath, idx)
				if _, err := os.Stat(idxPath); err == nil {
					ctx.ResolvedPath = idxPath
					ctx.RewrittenURI = filepath.ToSlash(filepath.Join(resolved, idx))
					ctx.DocumentRoot = docRoot
					return true
				}
			}
			continue
		}

		ctx.ResolvedPath = fullPath
		ctx.RewrittenURI = resolved
		ctx.DocumentRoot = docRoot
		return true
	}

	// Last candidate might be a named route (e.g. /index.php)
	if len(candidates) > 0 {
		last := candidates[len(candidates)-1]
		if !strings.HasPrefix(last, "$") {
			fullPath := filepath.Join(docRoot, filepath.Clean("/"+last))

			// Security: path must stay within document root
			absRoot, _ := filepath.Abs(docRoot)
			absPath, _ := filepath.Abs(fullPath)
			if !strings.HasPrefix(absPath, absRoot) {
				return false
			}

			ctx.ResolvedPath = fullPath
			ctx.RewrittenURI = last
			ctx.DocumentRoot = docRoot
			return true
		}
	}

	return false
}

func expandTryFileVar(candidate, uri string) string {
	result := candidate
	result = strings.ReplaceAll(result, "$uri", uri)
	return result
}
