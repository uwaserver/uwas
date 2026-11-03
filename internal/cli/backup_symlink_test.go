package cli

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestAddFileToTarRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "certs")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "outside-secret")
	if err := os.WriteFile(target, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "certificate.pem")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	if err := addFileToTarWithin(tw, link, "certs/certificate.pem", dir); err == nil {
		t.Fatal("PROBLEM CONFIRMED: symlink target was archived")
	}
}

func TestAddFileToTarAcceptsRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "certificate.pem")
	if err := os.WriteFile(path, []byte("certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	if err := addFileToTarWithin(tw, path, "certs/certificate.pem", filepath.Dir(path)); err != nil {
		t.Fatalf("regular file rejected: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
}
