//go:build unix

package middleware

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func stubImageConverter(calls *int32) func() {
	ol, oe := convertLookPathFn, convertExecFn
	convertLookPathFn = func(string) (string, error) { return "/bin/sh", nil }
	convertExecFn = func(name string, args ...string) *exec.Cmd {
		atomic.AddInt32(calls, 1)
		out := args[len(args)-1]
		return exec.Command("/bin/sh", "-c", "printf 'CONVERTED' > \"$0\"", out)
	}
	return func() { convertLookPathFn, convertExecFn = ol, oe }
}

// F1570: the converter writes to dst+".tmp" as the server user (root); a
// tenant-planted symlink at that name redirects the write to any file.
func TestConvertImageTmpSymlinkNotFollowed(t *testing.T) {
	var calls int32
	defer stubImageConverter(&calls)()

	// control: no symlink — conversion publishes dst, nothing else touched.
	cdir := t.TempDir()
	csrc := filepath.Join(cdir, "a.png")
	os.WriteFile(csrc, []byte("png"), 0o644)
	ok := convertImage(csrc, csrc+".webp", "webp")
	b, _ := os.ReadFile(csrc + ".webp")
	if !ok || string(b) != "CONVERTED" {
		t.Fatalf("INVALID PROOF: control failed ok=%v dst=%q", ok, b)
	}

	outside := t.TempDir()
	victim := filepath.Join(outside, "victim")
	os.WriteFile(victim, []byte("ORIGINAL"), 0o600)
	dir := t.TempDir()
	src := filepath.Join(dir, "p.png")
	os.WriteFile(src, []byte("png"), 0o644)
	if err := os.Symlink(victim, src+".webp.tmp"); err != nil {
		t.Fatal(err)
	}
	convertImage(src, src+".webp", "webp")
	got, _ := os.ReadFile(victim)
	if string(got) != "ORIGINAL" {
		t.Errorf("victim overwritten via planted dst.tmp symlink: %q", got)
	}
	// The planted symlink is left alone, the conversion still publishes dst,
	// and no converter temp file is left behind.
	if ok := convertImage(src, src+".webp", "webp"); !ok {
		t.Error("conversion must still succeed beside a planted .tmp symlink")
	}
	if b, _ := os.ReadFile(src + ".webp"); string(b) != "CONVERTED" {
		t.Errorf("dst = %q", b)
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".uwas-img-*"))
	if len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

// F1571: a FIFO as the original makes the converter (holding the global
// convertMu) block on it; a FIFO as the variant blocks the serve path.
func TestImageOptNonRegularFilesIgnored(t *testing.T) {
	var calls int32
	defer stubImageConverter(&calls)()

	// control: regular original is converted (converter invoked once).
	cdir := t.TempDir()
	csrc := filepath.Join(cdir, "a.png")
	os.WriteFile(csrc, []byte("png"), 0o644)
	convertImage(csrc, csrc+".webp", "webp")
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("INVALID PROOF: control calls=%d", calls)
	}

	atomic.StoreInt32(&calls, 0)
	dir := t.TempDir()
	fifoSrc := filepath.Join(dir, "f.png")
	if err := syscall.Mkfifo(fifoSrc, 0o644); err != nil {
		t.Skip(err)
	}
	r1 := convertImage(fifoSrc, fifoSrc+".webp", "webp")
	_ = r1
	bad := calls != 0

	// FIFO variant next to a regular original.
	vdir := t.TempDir()
	os.WriteFile(filepath.Join(vdir, "v.png"), []byte("png"), 0o644)
	syscall.Mkfifo(filepath.Join(vdir, "v.png.webp"), 0o644)
	os.Chtimes(filepath.Join(vdir, "v.png"), time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	h := ImageOptimization(ImageOptConfig{Enabled: true, Formats: []string{"webp"}}, vdir)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
	done := make(chan struct{})
	go func() {
		req := httptest.NewRequest("GET", "/v.png", nil)
		req.Header.Set("Accept", "image/webp")
		h.ServeHTTP(httptest.NewRecorder(), req)
		close(done)
	}()
	returned := false
	select {
	case <-done:
		returned = true
	case <-time.After(1500 * time.Millisecond):
	}
	if bad {
		t.Errorf("converter ran for a FIFO original (calls=%d)", calls)
	}
	if !returned {
		t.Error("request for an image with a FIFO variant did not return")
	}
}
