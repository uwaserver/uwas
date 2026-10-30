package wordpress

// F40001: UpdateCore dropped the result of its final `chown -R www-data`, so
// on a host where that fails (UWAS unprivileged, or no www-data account) the
// log still read "WordPress core updated" while the freshly written core files
// kept the extracting user's ownership and WordPress could not update or
// delete them later. FixPermissions and the installer already report failed
// ownership steps (F31200); the core updater must too.

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

func updateCoreWithChown(t *testing.T, chown string) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "wordpress/", Mode: 0755, Typeflag: tar.TypeDir})
	body := "<?php // core"
	tw.WriteHeader(&tar.Header{Name: "wordpress/wp-load.php", Mode: 0644, Size: int64(len(body)), Typeflag: tar.TypeReg})
	tw.Write([]byte(body))
	tw.Close()
	gz.Close()
	tarball := buf.Bytes()

	snap := saveHooks()
	t.Cleanup(func() { restoreHooks(snap) })
	execLookPathFn = func(string) (string, error) { return "", exec.ErrNotFound }
	httpGetFn = func(url string) (*http.Response, error) {
		if strings.HasSuffix(url, ".sha1") {
			return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(tarball))}, nil
	}
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		if name == "chown" {
			return exec.Command(chown)
		}
		return exec.Command(name, args...)
	}
	root := filepath.Join(t.TempDir(), "docroot")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return UpdateCore(root)
}

func TestUpdateCoreReportsFailedChown(t *testing.T) {
	// Control: ownership change succeeds, no warning.
	log, err := updateCoreWithChown(t, "true")
	if err != nil {
		t.Fatalf("control: %v", err)
	}
	if strings.Contains(log, "warning") || !strings.Contains(log, "WordPress core updated") {
		t.Fatalf("control log should be clean and report success:\n%s", log)
	}

	log, err = updateCoreWithChown(t, "false")
	if err != nil {
		t.Fatalf("UpdateCore: %v", err)
	}
	t.Logf("EXPECTED: a warning that the ownership change failed\nACTUAL log:\n%s", log)
	if !strings.Contains(log, "warning: chown -R www-data:www-data failed") {
		t.Fatalf("PROBLEM CONFIRMED: chown failed but the log reports a clean update:\n%s", log)
	}
}
