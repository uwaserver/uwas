package backup

import (
	"archive/tar"
	"bytes"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// addDirToTar must skip non-regular files rather than hand them to
// addFileToTar, which calls os.Open. A FIFO with no writer blocks in os.Open
// forever, so a single named pipe in a customer's web root would hang the
// whole scheduled backup for every domain; a socket or device fails with
// ENXIO and the propagated error aborted the walk, dropping every file after
// it. The restore side accepts only tar.TypeReg entries (see RestoreBackup),
// so the backup side skips the same set.
func TestAddDirToTarSkipsNonRegularFiles(t *testing.T) {
	t.Run("fifo does not block the walk", func(t *testing.T) {
		dir := t.TempDir()
		if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o644); err != nil {
			t.Skipf("mkfifo unavailable: %v", err)
		}
		// A regular file after the FIFO proves the entry was skipped, not
		// that the walk stopped there.
		if err := os.WriteFile(filepath.Join(dir, "index.php"), []byte("<?php"), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}

		names := archiveWithTimeout(t, dir)
		if !hasName(names, "sites/example.com/index.php") {
			t.Errorf("regular file after the FIFO was not archived; got %v", names)
		}
		if hasName(names, "sites/example.com/pipe") {
			t.Errorf("the FIFO itself was archived; got %v", names)
		}
	})

	t.Run("unix socket does not abort the archive", func(t *testing.T) {
		dir := t.TempDir()
		ln, err := net.Listen("unix", filepath.Join(dir, "app.sock"))
		if err != nil {
			t.Skipf("unix sockets unavailable: %v", err)
		}
		defer ln.Close()
		if err := os.WriteFile(filepath.Join(dir, "index.php"), []byte("<?php"), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}

		names := archiveWithTimeout(t, dir)
		if !hasName(names, "sites/example.com/index.php") {
			t.Errorf("regular file after the socket was not archived; got %v", names)
		}
		if hasName(names, "sites/example.com/app.sock") {
			t.Errorf("the socket itself was archived; got %v", names)
		}
	})

	t.Run("fifo nested in a subdirectory", func(t *testing.T) {
		dir := t.TempDir()
		sub := filepath.Join(dir, "assets")
		if err := os.Mkdir(sub, 0o755); err != nil {
			t.Fatalf("setup: %v", err)
		}
		if err := syscall.Mkfifo(filepath.Join(sub, "pipe"), 0o644); err != nil {
			t.Skipf("mkfifo unavailable: %v", err)
		}
		if err := os.WriteFile(filepath.Join(sub, "app.css"), []byte("body{}"), 0o644); err != nil {
			t.Fatalf("setup: %v", err)
		}

		names := archiveWithTimeout(t, dir)
		if !hasName(names, "sites/example.com/assets/app.css") {
			t.Errorf("regular file beside the nested FIFO was not archived; got %v", names)
		}
	})
}

// Control: the neighbouring branch the fix must not disturb. Symlinks are
// still skipped, and ordinary regular files and directories are still
// archived with the same names. This passed before the fix too.
func TestAddDirToTarStillSkipsSymlinksAndKeepsRegularFiles(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.php"), []byte("<?php"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	sub := filepath.Join(dir, "assets")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sub, "app.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	names := archiveWithTimeout(t, dir)
	if hasName(names, "sites/example.com/link.txt") {
		t.Errorf("symlink was archived; got %v", names)
	}
	for _, want := range []string{
		"sites/example.com/index.php",
		"sites/example.com/assets/app.css",
		"sites/example.com/assets/",
	} {
		if !hasName(names, want) {
			t.Errorf("missing %q from archive; got %v", want, names)
		}
	}
}

// archiveWithTimeout runs the real addDirToTar and fails the test if the walk
// does not finish promptly, which is the failure mode for a blocking file.
func archiveWithTimeout(t *testing.T, dir string) []string {
	t.Helper()

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	done := make(chan error, 1)
	go func() { done <- addDirToTar(tw, dir, "sites/example.com") }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("addDirToTar returned an error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("addDirToTar did not finish within 5s; os.Open on a non-regular file is blocking")
	}

	var names []string
	tr := tar.NewReader(bytes.NewReader(buf.Bytes()))
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		names = append(names, hdr.Name)
	}
	return names
}

func hasName(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}
