package wordpress

// Regression test for F58302: UpdateCore (root) copied core files with path
// based writes into the tenant's docroot. Its symlink check looked at the
// directory before the copy, but the tenant's own code can swap that directory
// for a symlink in between, and the root write then followed it: a stock
// WordPress file landed in (and replaced the file of the same name in) any
// directory on the host, e.g. another site's document root.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func coreTarball(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "wordpress/", Mode: 0755, Typeflag: tar.TypeDir})
	tw.WriteHeader(&tar.Header{Name: "wordpress/wp-admin/", Mode: 0755, Typeflag: tar.TypeDir})
	for _, f := range []struct{ name, body string }{
		{"wordpress/wp-admin/index.php", "<?php // stock core"},
		{"wordpress/index.php", "<?php // stock core"},
	} {
		tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0644, Size: int64(len(f.body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(f.body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func withTarball(t *testing.T, tarball []byte) {
	t.Helper()
	oldGet, oldLook, oldRead := httpGetFn, execLookPathFn, osReadFileFn
	t.Cleanup(func() { httpGetFn, execLookPathFn, osReadFileFn = oldGet, oldLook, oldRead })
	execLookPathFn = func(string) (string, error) { return "", exec.ErrNotFound }
	httpGetFn = func(url string) (*http.Response, error) {
		if strings.HasSuffix(url, ".sha1") {
			return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(tarball))}, nil
	}
}

func TestUpdateCoreDirSwappedAfterCheck(t *testing.T) {
	withTarball(t, coreTarball(t))
	base := t.TempDir()
	root := filepath.Join(base, "docroot")
	victim := filepath.Join(base, "other-site")
	os.MkdirAll(filepath.Join(root, "wp-admin"), 0755)
	os.MkdirAll(victim, 0755)
	const mine = "other site's own index.php"
	os.WriteFile(filepath.Join(victim, "index.php"), []byte(mine), 0644)

	swapped := false
	real := osReadFileFn
	osReadFileFn = func(name string) ([]byte, error) {
		// Runs after the walk checked <root>/wp-admin and before it writes
		// into it: the tenant replaces the directory with a link.
		if !swapped && strings.HasSuffix(name, filepath.Join("wp-admin", "index.php")) {
			swapped = true
			os.RemoveAll(filepath.Join(root, "wp-admin"))
			os.Symlink(victim, filepath.Join(root, "wp-admin"))
		}
		return real(name)
	}

	UpdateCore(root)

	if !swapped {
		t.Fatal("test never swapped the directory")
	}
	got, _ := os.ReadFile(filepath.Join(victim, "index.php"))
	if string(got) != mine {
		t.Fatalf("EXPECTED the other site's index.php untouched; ACTUAL %q", got)
	}
}

// Control: with no swap the core files land in the docroot and a
// pre-existing file is replaced.
func TestUpdateCoreCopiesWithoutSwap(t *testing.T) {
	withTarball(t, coreTarball(t))
	root := filepath.Join(t.TempDir(), "docroot")
	os.MkdirAll(filepath.Join(root, "wp-admin"), 0755)
	os.WriteFile(filepath.Join(root, "index.php"), []byte("old"), 0644)

	UpdateCore(root)

	for _, rel := range []string{"index.php", filepath.Join("wp-admin", "index.php")} {
		got, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil || !strings.Contains(string(got), "stock core") {
			t.Fatalf("%s = %q, %v; want the stock core file", rel, got, err)
		}
	}
}
