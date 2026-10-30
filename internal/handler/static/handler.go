package static

import (
	"bytes"
	"crypto/sha256"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/pathsafe"
	"github.com/uwaserver/uwas/internal/router"
)

type Handler struct {
	mime  *MIMERegistry
	files *fileCache
}

// Default file cache budget. Small enough that an idle server does not hold
// meaningful memory, large enough to cover the pages, stylesheets and scripts
// a busy site actually serves over and over.
const (
	defaultFileCacheMaxFile    = 512 << 10 // 512KB
	defaultFileCacheMaxBytes   = 64 << 20  // 64MB
	defaultFileCacheRevalidate = time.Second
)

func New() *Handler {
	return &Handler{
		mime:  NewMIMERegistry(nil),
		files: newFileCache(defaultFileCacheMaxFile, defaultFileCacheMaxBytes, defaultFileCacheRevalidate),
	}
}

// NewWithFileCache builds a handler whose file cache follows operator config.
// A disabled cache leaves files nil, and every fileCache method is a no-op on
// nil, so the serve path needs no extra branch.
func NewWithFileCache(cfg config.StaticFileCache) *Handler {
	h := &Handler{mime: NewMIMERegistry(nil)}
	if !cfg.StaticFileCacheEnabled() {
		return h
	}
	maxFile := int64(cfg.MaxFileSize)
	if maxFile <= 0 {
		maxFile = defaultFileCacheMaxFile
	}
	maxBytes := int64(cfg.MaxBytes)
	if maxBytes <= 0 {
		maxBytes = defaultFileCacheMaxBytes
	}
	revalidate := cfg.Revalidate.Duration
	if revalidate <= 0 {
		revalidate = defaultFileCacheRevalidate
	}
	h.files = newFileCache(maxFile, maxBytes, revalidate)
	return h
}

// Serve handles the request by serving the resolved static file.
func (h *Handler) Serve(ctx *router.RequestContext) {
	if !ServesMethod(ctx) {
		return
	}
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
	if hasHiddenComponent(scanPath) || IsPHPSourceLeftover(filepath.Base(path)) {
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
	// Only regular files are served. Opening a FIFO a tenant placed in the
	// docroot would park this request in open(2) indefinitely; devices and
	// sockets are never legitimate static content either.
	if !info.Mode().IsRegular() {
		w.Error(http.StatusNotFound, "404 Not Found")
		return
	}

	// Set content type with charset for text types
	w.Header().Set("Content-Type", h.contentType(path))

	// Try pre-compressed version
	if h.servePreCompressed(w, r, path, ctx.DocumentRoot, info, ctx.NoFollowSymlinks) {
		return
	}

	// Generate weak ETag: W/"mtime-size"
	etag := generateETag(info)
	w.Header().Set("ETag", etag)

	// Serve from the file cache when the bytes are already in memory. The
	// entry is revalidated against the FileInfo we already hold, so a file
	// edited on disk is never served stale here.
	if e := h.files.get(path, info); e != nil {
		http.ServeContent(w, r, filepath.Base(path), e.modTime, bytes.NewReader(e.body))
		return
	}

	// Let http.ServeContent handle If-None-Match, If-Modified-Since, Range requests
	f, err := os.Open(path)
	if err != nil {
		w.Error(http.StatusInternalServerError, "500 Internal Server Error")
		return
	}
	defer f.Close()
	if !openedWithinRoot(ctx.DocumentRoot, path, f) {
		w.Error(http.StatusNotFound, "404 Not Found")
		return
	}

	// Read small files whole so the next request can skip the filesystem
	// entirely. Anything larger streams straight from the file handle: the
	// point of the cache is repeated small assets, not one big download.
	if h.files != nil && info.Size() <= h.files.maxFileSize {
		if body, readErr := io.ReadAll(f); readErr == nil && int64(len(body)) == info.Size() {
			h.files.put(&fileEntry{
				path:    path,
				body:    body,
				size:    info.Size(),
				modTime: info.ModTime(),
			})
			http.ServeContent(w, r, filepath.Base(path), info.ModTime(), bytes.NewReader(body))
			return
		}
		// A short or failed read falls through to streaming from the handle;
		// rewind first, since ReadAll consumed it.
		if _, seekErr := f.Seek(0, io.SeekStart); seekErr != nil {
			w.Error(http.StatusInternalServerError, "500 Internal Server Error")
			return
		}
	}

	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), f)
}

// contentType is the Content-Type of the file at path, with the UTF-8 charset
// added to a text type whose MIME entry names none. The pre-compressed variants
// use it too, so a URL answers with one type whatever Accept-Encoding says
// (F55301).
func (h *Handler) contentType(path string) string {
	ct := h.mime.Lookup(path)
	if !strings.Contains(ct, "charset") &&
		(strings.HasPrefix(ct, "text/") || ct == "application/json" ||
			ct == "application/javascript" || ct == "application/xml" ||
			ct == "image/svg+xml") {
		ct += "; charset=utf-8"
	}
	return ct
}

// openedWithinRoot confirms that the file just opened is the doc root's own
// file. ResolveRequest's containment verdict rests on a resolution that
// pathsafe caches for a few seconds, and Serve opens the path again later: a
// tenant who swaps a served file for a symlink to somewhere outside the root
// gets that file served until the cache entry expires (F6900). The check runs
// after the open, with a fresh resolution, and requires the open handle to be
// the file the path resolves to now, so a swap in either direction is caught.
// Without a known doc root there is nothing to contain against.
func openedWithinRoot(docRoot, path string, f *os.File) bool {
	if docRoot == "" {
		return true
	}
	return pathsafe.OpenedWithin(docRoot, path, f)
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

// phpVariantExts are PHP script extensions other than ".php" that web servers
// commonly map to the PHP handler.
var phpVariantExts = []string{".phtml", ".pht", ".php3", ".php4", ".php5", ".php7", ".php8"}

// IsPHPSourceLeftover reports whether name is a backup or editor copy of a PHP
// script (wp-config.php.bak, index.php~, config.php.old, source.phps). Only a
// name ending in ".php" is handed to FastCGI, so these are not executed and a
// static response would be the script's source; docs/SPECIFICATION.md §16.3
// blocks them (F18600). The other extensions PHP is routinely mapped to
// (.phtml, .pht, .php3 to .php8) are treated the same way: Apache and cPanel
// run them as PHP, UWAS runs nothing, so a static answer would again be the
// script's source (F48600).
func IsPHPSourceLeftover(name string) bool {
	n := strings.ToLower(name)
	if strings.HasSuffix(n, ".phps") {
		return true
	}
	for _, ext := range phpVariantExts {
		if strings.HasSuffix(n, ext) {
			return true
		}
	}
	if strings.HasSuffix(n, "~") && strings.Contains(n, ".php") {
		return true
	}
	for _, ext := range []string{".bak", ".old", ".orig", ".save", ".swp", ".swo", ".tmp", ".backup", ".copy"} {
		if strings.HasSuffix(n, ".php"+ext) {
			return true
		}
	}
	return false
}

// servePreCompressed checks for .br or .gz pre-compressed files.
func (h *Handler) servePreCompressed(w *router.ResponseWriter, r *http.Request, path, docRoot string, origInfo fs.FileInfo, noFollow bool) bool {
	// Identity responses also depend on Accept-Encoding, including cache hits
	// and conditional responses served after this method returns.
	w.Header().Add("Vary", "Accept-Encoding")
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
		// The variant is a different file from the one ResolveRequest
		// checked, so it needs its own containment check: a symlinked
		// app.js.gz must not serve bytes from outside the doc root. With
		// no known doc root, refuse symlinked variants outright.
		if !preCompressedContained(docRoot, compPath) {
			continue
		}
		// "Options -FollowSymLinks" applies to the variant as to any file.
		if noFollow {
			if li, err := os.Lstat(compPath); err == nil && li.Mode()&fs.ModeSymlink != 0 {
				continue
			}
		}
		compInfo, err := os.Stat(compPath)
		if err != nil || !compInfo.Mode().IsRegular() {
			continue
		}
		// A stale compressed artifact must not override an updated source file.
		if compInfo.ModTime().Before(origInfo.ModTime()) {
			continue
		}

		f, err := os.Open(compPath)
		if err != nil {
			continue
		}
		if !openedWithinRoot(docRoot, compPath, f) {
			_ = f.Close()
			continue
		}

		w.Header().Set("Content-Encoding", c.encoding)
		w.Header().Set("Content-Type", h.contentType(path)) // original file's type
		// Put the encoding discriminator INSIDE the quotes — appending it after
		// the closing " (W/"<hex>"-gzip) is a malformed entity-tag that
		// conditional-request matching ignores past the quote.
		base := generateETag(origInfo)
		w.Header().Set("ETag", base[:len(base)-1]+"-"+c.encoding+`"`)

		// http.ServeContent leaves Content-Length unset for a full response
		// once Content-Encoding is set, so a gzip/br variant went out chunked
		// and a HEAD carried no length at all (F36300). State the size of the
		// open variant. Ranges, 304 and the error paths set or clear the
		// header themselves; a failed If-Match / If-Unmodified-Since answers
		// 412 with the header left as we set it, which would declare a body
		// that is never sent, so those requests are left to ServeContent.
		if r.Header.Get("If-Match") == "" && r.Header.Get("If-Unmodified-Since") == "" {
			if st, err := f.Stat(); err == nil {
				w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
			}
		}
		http.ServeContent(w, r, filepath.Base(path), origInfo.ModTime(), f)
		_ = f.Close()
		return true
	}

	return false
}

// preCompressedContained reports whether a pre-compressed variant may be
// served: inside the symlink-resolved doc root when one is known, otherwise
// only when the variant itself is not a symlink. A missing variant passes;
// the caller's Stat then skips it.
func preCompressedContained(docRoot, compPath string) bool {
	if docRoot == "" {
		li, err := os.Lstat(compPath)
		return err != nil || li.Mode()&fs.ModeSymlink == 0
	}
	base, err := pathsafe.CachedBase(docRoot)
	if err != nil {
		return false
	}
	return base.Contains(compPath)
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
				// The parameter name is case-insensitive (RFC 9110 §12.4.2).
				if len(p) >= 2 && (p[0] == 'q' || p[0] == 'Q') && p[1] == '=' {
					v := p[2:]
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

	// Index order precedence: .htaccess DirectoryIndex (per-request ctx
	// override, honored as-given — Apache appends no built-in fallbacks)
	// → domain.IndexFiles from YAML → built-in defaults.
	indexFiles := ctx.IndexFiles
	// .htaccess DirectoryIndex lines accumulate (200 lines of ~700 names made
	// a 113k-name list: 3.9 s for one GET /), and a stat is spent on each name
	// of the list per directory request. Apache would honour them all; no real
	// site lists more than a handful (F14402).
	if len(indexFiles) > config.MaxIndexFiles {
		indexFiles = indexFiles[:config.MaxIndexFiles]
	}
	if len(indexFiles) == 0 {
		indexFiles = domain.IndexFiles
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
				if !base.Contains(idxPath) {
					continue
				}
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
