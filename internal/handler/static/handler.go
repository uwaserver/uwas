package static

import (
	"crypto/sha256"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/pathsafe"
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

	// Security: reject dotfiles in any path component (e.g., .git, .env).
	// Scan only the portion relative to the document root — the docroot itself
	// may legitimately contain a dot component (e.g. /home/x/.www), which must
	// not 403 the whole site. Falls back to the full path when DocumentRoot is
	// unknown. The .well-known directory (ACME, security.txt) is allowed.
	scanPath := path
	if ctx.DocumentRoot != "" {
		if rel, err := filepath.Rel(ctx.DocumentRoot, path); err == nil &&
			rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			scanPath = filepath.ToSlash(rel)
		}
	}
	if hasHiddenComponent(scanPath) {
		w.Error(http.StatusForbidden, "403 Forbidden")
		return
	}

	// Use FileInfo from ResolveRequest if available; otherwise stat.
	info, err := ctx.FileInfo, error(nil)
	if info == nil {
		info, err = os.Stat(path)
		if err != nil {
			w.Error(http.StatusNotFound, "404 Not Found")
			return
		}
	}

	// Set content type with charset for text types
	ct := h.mime.Lookup(path)
	if !strings.Contains(ct, "charset") &&
		(strings.HasPrefix(ct, "text/") || ct == "application/json" ||
			ct == "application/javascript" || ct == "application/xml" ||
			ct == "image/svg+xml") {
		ct += "; charset=utf-8"
	}
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

// hasHiddenComponent reports whether any slash-separated path segment is a
// dotfile (starts with "." but is not "." or ".."). The ".well-known"
// directory is exempt — it must remain reachable for ACME HTTP-01 challenges
// and RFC 9116 security.txt. Zero-allocation scan without strings.Split.
func hasHiddenComponent(path string) bool {
	for i := 0; i < len(path); i++ {
		if path[i] != '.' {
			continue
		}
		if i > 0 && path[i-1] != '/' {
			continue // not at a segment start
		}
		j := i
		for j < len(path) && path[j] != '/' {
			j++
		}
		seg := path[i:j]
		if seg == "." || seg == ".." || seg == ".well-known" {
			continue
		}
		return true
	}
	return false
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
		if !acceptsEncoding(accept, c.encoding) {
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
		w.Header().Add("Vary", "Accept-Encoding")
		// Put the encoding discriminator INSIDE the quotes — appending it after
		// the closing " (W/"<hex>"-gzip) is a malformed entity-tag that
		// conditional-request matching ignores past the quote.
		base := generateETag(origInfo)
		w.Header().Set("ETag", base[:len(base)-1]+"-"+c.encoding+`"`)

		http.ServeContent(w, r, filepath.Base(path), origInfo.ModTime(), f)
		_ = f.Close()
		return true
	}

	return false
}

// acceptsEncoding reports whether the client accepts the given content-coding,
// honoring Accept-Encoding q-values. A coding listed with q=0 is an explicit
// refusal (e.g. "gzip;q=0") and must NOT be served — a plain substring match
// would wrongly send it. Token matching is exact, so "br" won't match inside an
// unrelated token.
func acceptsEncoding(header, coding string) bool {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name := part
		q := 1.0
		if i := strings.IndexByte(part, ';'); i != -1 {
			name = strings.TrimSpace(part[:i])
			for _, p := range strings.Split(part[i+1:], ";") {
				p = strings.TrimSpace(p)
				if v, ok := strings.CutPrefix(p, "q="); ok {
					if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
						q = f
					}
				}
			}
		}
		if strings.EqualFold(name, coding) {
			return q > 0
		}
	}
	return false
}

func generateETag(info fs.FileInfo) string {
	// Build raw string without fmt.Sprintf: mtime-size (max ~40 bytes)
	mtime := info.ModTime().UnixNano()
	size := info.Size()
	var raw [64]byte
	n := 0
	// Write mtime
	if mtime < 0 {
		raw[n] = '-'
		n++
		mtime = -mtime
	}
	mtimeDigits := [20]byte{}
	mtimeLen := 0
	for mtime > 0 {
		mtimeDigits[mtimeLen] = byte('0' + mtime%10)
		mtimeLen++
		mtime /= 10
	}
	if mtimeLen == 0 {
		mtimeDigits[0] = '0'
		mtimeLen = 1
	}
	for i := mtimeLen - 1; i >= 0; i-- {
		raw[n] = mtimeDigits[i]
		n++
	}
	raw[n] = '-'
	n++
	// Write size
	sizeDigits := [20]byte{}
	sizeLen := 0
	if size == 0 {
		sizeDigits[0] = '0'
		sizeLen = 1
	} else {
		for size > 0 {
			sizeDigits[sizeLen] = byte('0' + size%10)
			sizeLen++
			size /= 10
		}
	}
	for i := sizeLen - 1; i >= 0; i-- {
		raw[n] = sizeDigits[i]
		n++
	}

	hash := sha256.Sum256(raw[:n])

	// Manual hex encoding: W/"<8-byte-hash-hex>" = 21 bytes total
	const hex = "0123456789abcdef"
	var etag [21]byte
	etag[0] = 'W'
	etag[1] = '/'
	etag[2] = '"'
	for i := 0; i < 8; i++ {
		etag[3+i*2] = hex[hash[i]>>4]
		etag[4+i*2] = hex[hash[i]&0xF]
	}
	etag[19] = '"'
	return string(etag[:20]) // 21 bytes: W/" + 16 hex + " = 3 + 16 + 1
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
		if domain.Type == "php" {
			indexFiles = []string{"index.php", "index.html", "index.htm"}
		} else {
			indexFiles = []string{"index.html", "index.htm"}
		}
	}
	// For PHP domains, ensure index.php is always checked first.
	// Config may have index_files: [index.html] without index.php,
	// which breaks directory resolution (e.g. /wp-admin/ → index.php).
	if domain.Type == "php" {
		hasIndexPHP := false
		for _, f := range indexFiles {
			if f == "index.php" {
				hasIndexPHP = true
				break
			}
		}
		if !hasIndexPHP {
			indexFiles = append([]string{"index.php"}, indexFiles...)
		}
	}
	// Also merge PHP-specific index files if set
	if len(domain.PHP.IndexFiles) > 0 {
		for _, f := range domain.PHP.IndexFiles {
			found := false
			for _, existing := range indexFiles {
				if existing == f {
					found = true
					break
				}
			}
			if !found {
				indexFiles = append(indexFiles, f)
			}
		}
	}

	// Cached resolved docroot — avoids re-running EvalSymlinks on the base
	// for every request (~60% of static-serve allocations on Windows).
	base, baseErr := pathsafe.CachedBase(docRoot)

	for _, candidate := range candidates {
		resolved := expandTryFileVar(candidate, cleanURI)
		fullPath := filepath.Join(docRoot, filepath.Clean("/"+resolved))

		// filepath.Join + Clean guarantees lexical containment when the
		// resolved part is rooted, so only the symlink-aware check is needed.
		if baseErr != nil || !base.Contains(fullPath) {
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
				if idxInfo, err := os.Stat(idxPath); err == nil {
					ctx.ResolvedPath = idxPath
					ctx.RewrittenURI = filepath.ToSlash(filepath.Join(resolved, idx))
					ctx.DocumentRoot = docRoot
					ctx.FileInfo = idxInfo
					return true
				}
			}
			continue
		}

		ctx.ResolvedPath = fullPath
		ctx.RewrittenURI = resolved
		ctx.DocumentRoot = docRoot
		ctx.FileInfo = stat
		return true
	}

	// Last candidate might be a named route (e.g. /index.php for WordPress).
	// PHP frameworks use this as a front-controller: all unmatched URLs route to
	// index.php which handles routing internally.
	if len(candidates) > 0 {
		last := candidates[len(candidates)-1]
		if !strings.HasPrefix(last, "$") {
			fullPath := filepath.Join(docRoot, filepath.Clean("/"+last))

			if baseErr != nil || !base.Contains(fullPath) {
				return false
			}

			// Stat the front-controller so downstream code doesn't re-stat.
			// If the file doesn't physically exist (some apps are purely virtual),
			// proceed without FileInfo — the downstream handler will 404.
			if fi, err := os.Stat(fullPath); err == nil {
				ctx.FileInfo = fi
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
