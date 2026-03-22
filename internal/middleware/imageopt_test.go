package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestImageOptServesWebP(t *testing.T) {
	dir := t.TempDir()

	// Create original image and its .webp counterpart.
	os.WriteFile(filepath.Join(dir, "photo.jpg"), []byte("original-jpg"), 0644)
	os.WriteFile(filepath.Join(dir, "photo.jpg.webp"), []byte("optimized-webp"), 0644)

	handler := ImageOptimization(ImageOptConfig{
		Enabled: true,
		Formats: []string{"webp"},
	}, dir)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fallback"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/photo.jpg", nil)
	req.Header.Set("Accept", "image/webp,image/png,image/*")
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Type") != "image/webp" {
		t.Errorf("Content-Type = %q, want image/webp", rec.Header().Get("Content-Type"))
	}
	if rec.Body.String() != "optimized-webp" {
		t.Errorf("body = %q, want optimized-webp", rec.Body.String())
	}
}

func TestImageOptServesAVIF(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "photo.png"), []byte("original-png"), 0644)
	os.WriteFile(filepath.Join(dir, "photo.png.avif"), []byte("optimized-avif"), 0644)

	handler := ImageOptimization(ImageOptConfig{
		Enabled: true,
		Formats: []string{"avif", "webp"},
	}, dir)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fallback"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/photo.png", nil)
	req.Header.Set("Accept", "image/avif,image/webp,image/*")
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Type") != "image/avif" {
		t.Errorf("Content-Type = %q, want image/avif", rec.Header().Get("Content-Type"))
	}
	if rec.Body.String() != "optimized-avif" {
		t.Errorf("body = %q, want optimized-avif", rec.Body.String())
	}
}

func TestImageOptFallsBackToOriginal(t *testing.T) {
	dir := t.TempDir()

	// Only the original exists; no .webp version.
	os.WriteFile(filepath.Join(dir, "photo.jpg"), []byte("original-jpg"), 0644)

	handler := ImageOptimization(ImageOptConfig{
		Enabled: true,
		Formats: []string{"webp"},
	}, dir)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("original-served"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/photo.jpg", nil)
	req.Header.Set("Accept", "image/webp,image/*")
	handler.ServeHTTP(rec, req)

	if rec.Body.String() != "original-served" {
		t.Errorf("body = %q, want original-served", rec.Body.String())
	}
}

func TestImageOptNoAcceptHeader(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "photo.jpg"), []byte("original-jpg"), 0644)
	os.WriteFile(filepath.Join(dir, "photo.jpg.webp"), []byte("optimized-webp"), 0644)

	handler := ImageOptimization(ImageOptConfig{
		Enabled: true,
		Formats: []string{"webp"},
	}, dir)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("original-served"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/photo.jpg", nil)
	// No Accept header with image/webp
	handler.ServeHTTP(rec, req)

	if rec.Body.String() != "original-served" {
		t.Errorf("body = %q, want original-served (no webp accept)", rec.Body.String())
	}
}

func TestImageOptDisabled(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "photo.jpg"), []byte("original-jpg"), 0644)
	os.WriteFile(filepath.Join(dir, "photo.jpg.webp"), []byte("optimized-webp"), 0644)

	handler := ImageOptimization(ImageOptConfig{
		Enabled: false,
		Formats: []string{"webp"},
	}, dir)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("passthrough"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/photo.jpg", nil)
	req.Header.Set("Accept", "image/webp,image/*")
	handler.ServeHTTP(rec, req)

	if rec.Body.String() != "passthrough" {
		t.Errorf("body = %q, want passthrough (disabled)", rec.Body.String())
	}
}

func TestImageOptNonImagePassthrough(t *testing.T) {
	dir := t.TempDir()

	handler := ImageOptimization(ImageOptConfig{
		Enabled: true,
		Formats: []string{"webp"},
	}, dir)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("html-content"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/index.html", nil)
	req.Header.Set("Accept", "image/webp,text/html")
	handler.ServeHTTP(rec, req)

	if rec.Body.String() != "html-content" {
		t.Errorf("body = %q, want html-content (non-image path)", rec.Body.String())
	}
}

func TestImageOptVaryHeader(t *testing.T) {
	dir := t.TempDir()

	handler := ImageOptimization(ImageOptConfig{
		Enabled: true,
		Formats: []string{"webp"},
	}, dir)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/photo.jpg", nil)
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Vary") != "Accept" {
		t.Errorf("Vary = %q, want Accept", rec.Header().Get("Vary"))
	}
}

func TestImageOptGIFExtension(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "anim.gif"), []byte("original-gif"), 0644)
	os.WriteFile(filepath.Join(dir, "anim.gif.webp"), []byte("optimized-webp"), 0644)

	handler := ImageOptimization(ImageOptConfig{
		Enabled: true,
		Formats: []string{"webp"},
	}, dir)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fallback"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/anim.gif", nil)
	req.Header.Set("Accept", "image/webp,image/*")
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Type") != "image/webp" {
		t.Errorf("Content-Type = %q, want image/webp", rec.Header().Get("Content-Type"))
	}
}

func TestImageOptJPEGExtension(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "photo.jpeg"), []byte("original-jpeg"), 0644)
	os.WriteFile(filepath.Join(dir, "photo.jpeg.webp"), []byte("optimized-webp"), 0644)

	handler := ImageOptimization(ImageOptConfig{
		Enabled: true,
		Formats: []string{"webp"},
	}, dir)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fallback"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/photo.jpeg", nil)
	req.Header.Set("Accept", "image/webp,image/*")
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Type") != "image/webp" {
		t.Errorf("Content-Type = %q, want image/webp", rec.Header().Get("Content-Type"))
	}
}

func TestImageOptPriorityOrder(t *testing.T) {
	dir := t.TempDir()

	// Both avif and webp exist; avif is listed first so should win.
	os.WriteFile(filepath.Join(dir, "photo.jpg"), []byte("original"), 0644)
	os.WriteFile(filepath.Join(dir, "photo.jpg.avif"), []byte("avif-version"), 0644)
	os.WriteFile(filepath.Join(dir, "photo.jpg.webp"), []byte("webp-version"), 0644)

	handler := ImageOptimization(ImageOptConfig{
		Enabled: true,
		Formats: []string{"avif", "webp"},
	}, dir)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fallback"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/photo.jpg", nil)
	req.Header.Set("Accept", "image/avif,image/webp,image/*")
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Type") != "image/avif" {
		t.Errorf("Content-Type = %q, want image/avif (higher priority)", rec.Header().Get("Content-Type"))
	}
	if rec.Body.String() != "avif-version" {
		t.Errorf("body = %q, want avif-version", rec.Body.String())
	}
}

func TestImageOptSubdirectory(t *testing.T) {
	dir := t.TempDir()

	// Create subdirectory structure.
	os.MkdirAll(filepath.Join(dir, "images"), 0755)
	os.WriteFile(filepath.Join(dir, "images", "photo.jpg"), []byte("original"), 0644)
	os.WriteFile(filepath.Join(dir, "images", "photo.jpg.webp"), []byte("webp-sub"), 0644)

	handler := ImageOptimization(ImageOptConfig{
		Enabled: true,
		Formats: []string{"webp"},
	}, dir)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("fallback"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/images/photo.jpg", nil)
	req.Header.Set("Accept", "image/webp,image/*")
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Type") != "image/webp" {
		t.Errorf("Content-Type = %q, want image/webp", rec.Header().Get("Content-Type"))
	}
	if rec.Body.String() != "webp-sub" {
		t.Errorf("body = %q, want webp-sub", rec.Body.String())
	}
}

func TestImageOptEmptyFormats(t *testing.T) {
	dir := t.TempDir()

	handler := ImageOptimization(ImageOptConfig{
		Enabled: true,
		Formats: []string{},
	}, dir)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("passthrough"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/photo.jpg", nil)
	req.Header.Set("Accept", "image/webp,image/*")
	handler.ServeHTTP(rec, req)

	if rec.Body.String() != "passthrough" {
		t.Errorf("body = %q, want passthrough (empty formats)", rec.Body.String())
	}
}

func TestImageOptUnknownFormat(t *testing.T) {
	dir := t.TempDir()

	handler := ImageOptimization(ImageOptConfig{
		Enabled: true,
		Formats: []string{"unknownformat"},
	}, dir)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("passthrough"))
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/photo.jpg", nil)
	req.Header.Set("Accept", "image/webp,image/*")
	handler.ServeHTTP(rec, req)

	if rec.Body.String() != "passthrough" {
		t.Errorf("body = %q, want passthrough (unknown format)", rec.Body.String())
	}
}
