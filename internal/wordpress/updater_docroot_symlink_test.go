package wordpress

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

// FixPermissions runs as root and must not chmod, create or chown anything a
// domain user's symlink points at outside the docroot.
func TestFixPermissionsSkipsDocrootSymlinks(t *testing.T) {
	if _, err := exec.LookPath("find"); err != nil {
		t.Skip("find not available")
	}
	base := t.TempDir()
	root := filepath.Join(base, "docroot")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outside, "content"), 0700); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(outside, "victim.conf")
	if err := os.WriteFile(victim, []byte("system file\n"), 0644); err != nil {
		t.Fatal(err)
	}
	os.Symlink(victim, filepath.Join(root, "wp-config.php"))
	os.Symlink(filepath.Join(outside, "content"), filepath.Join(root, "wp-content"))

	FixPermissions(root)

	if fi, _ := os.Stat(victim); fi.Mode().Perm() != 0644 {
		t.Errorf("file outside docroot chmodded through wp-config.php symlink: %o", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(filepath.Join(outside, "content")); fi.Mode().Perm() != 0700 {
		t.Errorf("dir outside docroot chmodded through wp-content symlink: %o", fi.Mode().Perm())
	}
	if _, err := os.Lstat(filepath.Join(outside, "content", "upgrade")); err == nil {
		t.Errorf("wp-content/upgrade created outside docroot through symlink")
	}
}

// The UpdateCore download fallback must not copy core files through a
// directory symlink inside the docroot.
func TestUpdateCoreRefusesIntermediateDirSymlink(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "wordpress/", Mode: 0755, Typeflag: tar.TypeDir})
	tw.WriteHeader(&tar.Header{Name: "wordpress/wp-admin/", Mode: 0755, Typeflag: tar.TypeDir})
	body := "<?php // core admin"
	tw.WriteHeader(&tar.Header{Name: "wordpress/wp-admin/admin.php", Mode: 0644, Size: int64(len(body)), Typeflag: tar.TypeReg})
	tw.Write([]byte(body))
	tw.Close()
	gz.Close()
	tarball := buf.Bytes()

	oldGet, oldLook := httpGetFn, execLookPathFn
	t.Cleanup(func() { httpGetFn, execLookPathFn = oldGet, oldLook })
	execLookPathFn = func(string) (string, error) { return "", exec.ErrNotFound }
	httpGetFn = func(url string) (*http.Response, error) {
		if strings.HasSuffix(url, ".sha1") {
			return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(tarball))}, nil
	}

	base := t.TempDir()
	root := filepath.Join(base, "docroot")
	outside := filepath.Join(base, "outside")
	os.MkdirAll(root, 0755)
	os.MkdirAll(outside, 0755)
	os.Symlink(outside, filepath.Join(root, "wp-admin"))

	_, err := UpdateCore(root)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("UpdateCore err = %v, want refusal mentioning symlink", err)
	}
	if _, err := os.Lstat(filepath.Join(outside, "admin.php")); err == nil {
		t.Errorf("core file written outside docroot through wp-admin symlink")
	}
}
