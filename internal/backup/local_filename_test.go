package backup

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalProviderRejectsDirectoryNames(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "backups")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := NewLocalProvider(dir)

	if err := p.Delete(context.Background(), "."); err == nil || !strings.Contains(err.Error(), "invalid backup filename") {
		t.Fatalf("PROBLEM CONFIRMED: Delete(\".\") error = %v, want invalid backup filename", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("backup directory was removed: %v", err)
	}
	if rc, err := p.Download(context.Background(), ".."); err == nil {
		rc.Close()
		t.Fatal("Download(\"..\") opened the provider parent directory")
	}
}

func TestLocalProviderValidFilenameStillRoundTrips(t *testing.T) {
	p := NewLocalProvider(t.TempDir())
	ctx := context.Background()
	if err := p.Upload(ctx, "valid.tar.gz", strings.NewReader("archive")); err != nil {
		t.Fatal(err)
	}
	rc, err := p.Download(ctx, "valid.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil || string(got) != "archive" {
		t.Fatalf("round trip = %q, %v", got, err)
	}
	if err := p.Delete(ctx, "valid.tar.gz"); err != nil {
		t.Fatal(err)
	}
}
