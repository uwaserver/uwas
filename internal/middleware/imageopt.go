package middleware

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// imageExtensions maps original image extensions to their lowercase form.
var imageExtensions = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".gif":  true,
}

// formatMIME maps optimized image format names to their MIME types.
var formatMIME = map[string]string{
	"webp": "image/webp",
	"avif": "image/avif",
}

// formatExtension maps optimized image format names to file extensions.
var formatExtension = map[string]string{
	"webp": ".webp",
	"avif": ".avif",
}

// ImageOptConfig controls image optimization behavior.
type ImageOptConfig struct {
	Enabled bool
	Formats []string // e.g. ["webp", "avif"]
}

// ImageOptimization returns middleware that serves pre-converted optimized
// image formats (WebP, AVIF) when the browser supports them and a converted
// file exists on disk. It does not perform on-the-fly conversion.
//
// For a request to /images/photo.jpg with Accept: image/webp, the middleware
// checks whether /images/photo.jpg.webp exists. If so it rewrites the
// request to serve that file with the correct Content-Type.
func ImageOptimization(cfg ImageOptConfig, docRoot string) Middleware {
	if !cfg.Enabled || len(cfg.Formats) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}

	// Build ordered list of candidate formats.
	type candidate struct {
		format    string // "webp", "avif"
		ext       string // ".webp", ".avif"
		accept    string // "image/webp", "image/avif"
	}
	var candidates []candidate
	for _, f := range cfg.Formats {
		f = strings.ToLower(f)
		mime, ok := formatMIME[f]
		if !ok {
			continue
		}
		candidates = append(candidates, candidate{
			format: f,
			ext:    formatExtension[f],
			accept: mime,
		})
	}

	if len(candidates) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Always set Vary: Accept so caches key on the header.
			w.Header().Set("Vary", "Accept")

			// Only process image requests.
			ext := strings.ToLower(filepath.Ext(r.URL.Path))
			if !imageExtensions[ext] {
				next.ServeHTTP(w, r)
				return
			}

			accept := r.Header.Get("Accept")

			// Try each candidate format in priority order.
			for _, c := range candidates {
				if !strings.Contains(accept, c.accept) {
					continue
				}

				// Build the on-disk path for the optimized version.
				// The convention is: original path + format extension
				// e.g. /images/photo.jpg → /images/photo.jpg.webp
				relPath := filepath.FromSlash(r.URL.Path)
				diskPath := filepath.Join(docRoot, relPath) + c.ext

				info, err := os.Stat(diskPath)
				if err != nil || info.IsDir() {
					continue
				}

				// Serve the optimized file.
				f, err := os.Open(diskPath)
				if err != nil {
					continue
				}
				defer f.Close()

				w.Header().Set("Content-Type", c.accept)
				http.ServeContent(w, r, filepath.Base(diskPath), info.ModTime(), f)
				return
			}

			// No optimized version available; serve the original.
			next.ServeHTTP(w, r)
		})
	}
}
