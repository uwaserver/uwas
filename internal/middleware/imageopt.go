package middleware

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/uwaserver/uwas/internal/pathsafe"
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
			w.Header().Add("Vary", "Accept")

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
				if !pathsafe.IsWithinBase(docRoot, diskPath) || !pathsafe.IsWithinBaseResolved(docRoot, diskPath) {
					continue
				}

				// A variant of a deleted or since-replaced original is stale.
				srcInfo, err := os.Stat(filepath.Join(docRoot, relPath))
				if err != nil || !srcInfo.Mode().IsRegular() {
					continue
				}
				info, ok := variantFresh(srcInfo, diskPath)
				if !ok {
					continue
				}

				// Serve the optimized file.
				f, err := os.Open(diskPath)
				if err != nil {
					continue
				}

				w.Header().Set("Content-Type", c.accept)
				http.ServeContent(w, r, filepath.Base(diskPath), info.ModTime(), f)
				f.Close()
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
				if !pathsafe.IsWithinBase(docRoot, srcPath) || !pathsafe.IsWithinBaseResolved(docRoot, srcPath) ||
					!pathsafe.IsWithinBase(docRoot, dstPath) || !pathsafe.IsWithinBaseResolved(docRoot, dstPath) {
					continue
				}

				if converted := convertImage(srcPath, dstPath, c.format); converted {
					if f, err := os.Open(dstPath); err == nil {
						if info, err := f.Stat(); err == nil {
							w.Header().Set("Content-Type", c.accept)
							http.ServeContent(w, r, filepath.Base(dstPath), info.ModTime(), f)
							f.Close()
							return
						}
						f.Close()
					}
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// convertImageFunc can be overridden in tests.
var convertImageFunc = convertImageReal

// convertLookPathFn and convertExecFn are overridden in tests so
// convertImageReal's own locking and early-exit logic can run against a
// scripted converter instead of a real cwebp/avifenc binary.
var (
	convertLookPathFn = exec.LookPath
	convertExecFn     = exec.Command
)

// convertImage converts src to dst using cwebp or avifenc.
// Returns true if conversion succeeded. Thread-safe via file lock.
var convertMu sync.Mutex

func convertImage(src, dst, format string) bool {
	return convertImageFunc(src, dst, format)
}

// variantFresh reports whether dst is a usable optimized variant of the
// original described by src: a regular file not older than the original. A
// FIFO or device planted at the variant name would park the serving request
// in open(2) (F1571).
func variantFresh(src os.FileInfo, dst string) (os.FileInfo, bool) {
	info, err := os.Stat(dst)
	if err != nil || !info.Mode().IsRegular() || info.ModTime().Before(src.ModTime()) {
		return nil, false
	}
	return info, true
}

func convertImageReal(src, dst, format string) bool {
	// Don't convert if src doesn't exist or dst is already an up-to-date
	// conversion of it; a dst older than src is regenerated.
	srcInfo, err := os.Stat(src)
	if err != nil {
		return false
	}
	// A FIFO original would park the converter on open(2) while it holds
	// convertMu, stalling every tenant's conversions (F1571).
	if !srcInfo.Mode().IsRegular() {
		return false
	}
	if _, ok := variantFresh(srcInfo, dst); ok {
		return true // already converted
	}

	convertMu.Lock()
	defer convertMu.Unlock()

	// Double-check after lock
	if _, ok := variantFresh(srcInfo, dst); ok {
		return true
	}

	// Convert to a sibling temp file and rename: the pre-lock Stat(dst) fast
	// path above must only ever see a fully written destination, never a
	// partially converted one (a concurrent request would serve it).
	// The temp file is created exclusively under an unpredictable name: the
	// converter runs as the server user, and a tenant-planted symlink at a
	// predictable dst+".tmp" would redirect its write onto any file (F1570).
	tf, err := os.CreateTemp(filepath.Dir(dst), ".uwas-img-*.tmp")
	if err != nil {
		return false
	}
	dstTmp := tf.Name()
	tf.Close()
	_ = os.Chmod(dstTmp, 0o644)
	// Every exit before the rename must drop the temp file, including the
	// missing-converter and unknown-format returns: each image request would
	// otherwise leave another empty file in the tenant's directory (F1630).
	published := false
	defer func() {
		if !published {
			os.Remove(dstTmp)
		}
	}()

	var cmd *exec.Cmd
	switch format {
	case "webp":
		bin, err := convertLookPathFn("cwebp")
		if err != nil {
			return false
		}
		cmd = convertExecFn(bin, "-q", "80", "-m", "4", src, "-o", dstTmp)
	case "avif":
		bin, err := convertLookPathFn("avifenc")
		if err != nil {
			return false
		}
		cmd = convertExecFn(bin, "-s", "6", "--min", "20", "--max", "40", src, dstTmp)
	default:
		return false
	}

	if err := cmd.Run(); err != nil {
		return false
	}
	if err := os.Rename(dstTmp, dst); err != nil {
		return false
	}
	published = true
	return true
}
