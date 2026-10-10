//go:build unix

package middleware

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func leftoverImgTmp(t *testing.T, dir string) int {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(dir, ".uwas-img-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	return len(m)
}

// Every exit of convertImageReal before the rename must remove its private
// temp file; a missing converter used to leave one empty file per request (F1630).
func TestConvertImageLeavesNoTempFile(t *testing.T) {
	ol, oe := convertLookPathFn, convertExecFn
	defer func() { convertLookPathFn, convertExecFn = ol, oe }()
	okExec := func(name string, args ...string) *exec.Cmd {
		return exec.Command("/bin/sh", "-c", "printf C > \"$0\"", args[len(args)-1])
	}
	found := func(string) (string, error) { return "/bin/sh", nil }

	newSrc := func(t *testing.T) (string, string) {
		dir := t.TempDir()
		src := filepath.Join(dir, "p.png")
		if err := os.WriteFile(src, []byte("png"), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir, src
	}

	t.Run("success publishes dst only", func(t *testing.T) {
		convertLookPathFn, convertExecFn = found, okExec
		dir, src := newSrc(t)
		if !convertImage(src, src+".webp", "webp") {
			t.Fatal("conversion failed")
		}
		if b, _ := os.ReadFile(src + ".webp"); string(b) != "C" {
			t.Fatalf("dst=%q", b)
		}
		if n := leftoverImgTmp(t, dir); n != 0 {
			t.Fatalf("%d temp files left", n)
		}
	})
	t.Run("missing converter", func(t *testing.T) {
		convertLookPathFn = func(string) (string, error) { return "", exec.ErrNotFound }
		dir, src := newSrc(t)
		for i := 0; i < 3; i++ {
			if convertImage(src, src+".webp", "webp") || convertImage(src, src+".avif", "avif") {
				t.Fatal("conversion reported success without a converter")
			}
		}
		if n := leftoverImgTmp(t, dir); n != 0 {
			t.Fatalf("%d temp files left", n)
		}
	})
	t.Run("unknown format", func(t *testing.T) {
		convertLookPathFn, convertExecFn = found, okExec
		dir, src := newSrc(t)
		if convertImage(src, src+".bmp", "bogus") {
			t.Fatal("unknown format reported success")
		}
		if n := leftoverImgTmp(t, dir); n != 0 {
			t.Fatalf("%d temp files left", n)
		}
	})
	t.Run("converter fails", func(t *testing.T) {
		convertLookPathFn = found
		convertExecFn = func(string, ...string) *exec.Cmd { return exec.Command("/bin/sh", "-c", "exit 3") }
		dir, src := newSrc(t)
		if convertImage(src, src+".webp", "webp") {
			t.Fatal("failed converter reported success")
		}
		if n := leftoverImgTmp(t, dir); n != 0 {
			t.Fatalf("%d temp files left", n)
		}
	})
	t.Run("rename fails", func(t *testing.T) {
		convertLookPathFn, convertExecFn = found, okExec
		dir, src := newSrc(t)
		if err := os.MkdirAll(filepath.Join(src+".webp", "x"), 0o755); err != nil { // dst is a non-empty dir
			t.Fatal(err)
		}
		if convertImage(src, src+".webp", "webp") {
			t.Fatal("rename onto a directory reported success")
		}
		if n := leftoverImgTmp(t, dir); n != 0 {
			t.Fatalf("%d temp files left", n)
		}
	})
}
