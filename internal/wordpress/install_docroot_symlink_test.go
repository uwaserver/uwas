package wordpress

// Regression: Install runs as root and writes wp-config.php and .htaccess into
// the domain user's document root. It used os.WriteFile, which follows a
// final-component symlink, so a symlink the domain user planted there
// redirected the write to any file on the host (a dangling wp-config.php link
// even passes IsWordPress, which Stat()s the target). Separately,
// setWordPressPermissions chmod-ed every file 644 after generateWPConfig wrote
// wp-config.php 0600, leaving the DB password and salts world-readable.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func installFixtureHooks(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlinks and modes")
	}
	for _, bin := range []string{"tar", "find", "chmod"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH", bin)
		}
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range []struct{ name, body string }{
		{"wordpress/", ""}, {"wordpress/index.php", "<?php\n"},
		{"wordpress/wp-content/", ""}, {"wordpress/wp-content/index.php", "<?php\n"},
	} {
		h := &tar.Header{Name: e.name, Mode: 0644, Size: int64(len(e.body)), Typeflag: tar.TypeReg}
		if strings.HasSuffix(e.name, "/") {
			h = &tar.Header{Name: e.name, Mode: 0755, Typeflag: tar.TypeDir}
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		io.WriteString(tw, e.body)
	}
	tw.Close()
	gz.Close()
	tgz := buf.Bytes()

	snap := saveHooks()
	t.Cleanup(func() { restoreHooks(snap) })
	runtimeGOOS = "linux"
	execLookPathFn = func(string) (string, error) { return "", fmt.Errorf("not found") }
	httpGetFn = func(url string) (*http.Response, error) {
		if strings.HasSuffix(url, ".sha1") {
			return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(tgz))}, nil
	}
	execCommandFn = func(name string, args ...string) *exec.Cmd {
		switch name {
		case "tar", "find", "chmod":
			return exec.Command(name, args...)
		case "chown":
			return fakeOutputCmd("", "", 0)
		}
		return exec.Command("__nonexistent_binary_for_test__")
	}
}

func installFixtureSite(t *testing.T) (webRoot, outside string) {
	t.Helper()
	base := t.TempDir()
	webRoot = filepath.Join(base, "site", "public_html")
	outside = filepath.Join(base, "outside")
	for _, d := range []string{webRoot, outside} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	return webRoot, outside
}

func TestInstallDoesNotWriteThroughDocrootSymlinks(t *testing.T) {
	installFixtureHooks(t)

	t.Run("htaccess", func(t *testing.T) {
		webRoot, outside := installFixtureSite(t)
		victim := filepath.Join(outside, "victim.conf")
		if err := os.WriteFile(victim, []byte("ORIGINAL\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(victim, filepath.Join(webRoot, ".htaccess")); err != nil {
			t.Fatal(err)
		}
		Install(InstallRequest{Domain: "a.test", WebRoot: webRoot, DBPass: "pw"})
		if got, _ := os.ReadFile(victim); string(got) != "ORIGINAL\n" {
			t.Fatalf("Install wrote through the planted .htaccess symlink: victim now %q", got)
		}
	})

	t.Run("dangling wp-config", func(t *testing.T) {
		webRoot, outside := installFixtureSite(t)
		created := filepath.Join(outside, "created.php")
		if err := os.Symlink(created, filepath.Join(webRoot, "wp-config.php")); err != nil {
			t.Fatal(err)
		}
		res := Install(InstallRequest{Domain: "a.test", WebRoot: webRoot, DBPass: "pw"})
		if _, err := os.Stat(created); err == nil {
			t.Fatalf("Install created %s through the planted wp-config.php symlink", created)
		}
		if res.Status != "error" {
			t.Fatalf("Install status = %q, want error when wp-config.php cannot be written safely", res.Status)
		}
	})
}

func TestInstallKeepsWPConfigPrivate(t *testing.T) {
	installFixtureHooks(t)
	webRoot, _ := installFixtureSite(t)
	if res := Install(InstallRequest{Domain: "a.test", WebRoot: webRoot, DBPass: "pw"}); res.Status != "done" {
		t.Fatalf("install failed: %s\n%s", res.Error, res.Output)
	}
	fi, err := os.Stat(filepath.Join(webRoot, "wp-config.php"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Fatalf("wp-config.php mode = %#o after Install, want 0600 (it holds the DB password and salts)", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(filepath.Join(webRoot, "index.php")); fi == nil || fi.Mode().Perm() != 0644 {
		t.Fatalf("other files should still be 0644")
	}
}
