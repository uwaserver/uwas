package middleware

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
		format string // "webp", "avif"
		ext    string // ".webp", ".avif"
		accept string // "image/webp", "image/avif"
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

			// No optimized version available — try on-the-fly conversion.
			for _, c := range candidates {
				if !strings.Contains(accept, c.accept) {
					continue
				}
				relPath := filepath.FromSlash(r.URL.Path)
				srcPath := filepath.Join(docRoot, relPath)
				dstPath := srcPath + c.ext

				if converted := convertImage(srcPath, dstPath, c.format); converted {
					if f, err := os.Open(dstPath); err == nil {
						defer f.Close()
						if info, err := f.Stat(); err == nil {
							w.Header().Set("Content-Type", c.accept)
							http.ServeContent(w, r, filepath.Base(dstPath), info.ModTime(), f)
							return
						}
					}
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// convertImage converts src to dst using cwebp or avifenc.
// Returns true if conversion succeeded. Thread-safe via file lock.
var convertMu sync.Mutex

func convertImage(src, dst, format string) bool {
	// Don't convert if src doesn't exist or dst already exists
	if _, err := os.Stat(src); err != nil {
		return false
	}
	if _, err := os.Stat(dst); err == nil {
		return true // already converted
	}

	convertMu.Lock()
	defer convertMu.Unlock()

	// Double-check after lock
	if _, err := os.Stat(dst); err == nil {
		return true
	}

	var cmd *exec.Cmd
	switch format {
	case "webp":
		bin, err := exec.LookPath("cwebp")
		if err != nil {
			return false
		}
		cmd = exec.Command(bin, "-q", "80", "-m", "4", src, "-o", dst)
	case "avif":
		bin, err := exec.LookPath("avifenc")
		if err != nil {
			return false
		}
		cmd = exec.Command(bin, "-s", "6", "--min", "20", "--max", "40", src, dst)
	default:
		return false
	}

	if err := cmd.Run(); err != nil {
		os.Remove(dst) // cleanup partial file
		return false
	}
	return true
}
