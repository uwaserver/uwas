package backup

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type localUploadFailure struct{ err error }

func (r localUploadFailure) Read([]byte) (int, error) { return 0, r.err }

func TestLocalUploadFailurePreservesArchive(t *testing.T) {
	dir := t.TempDir()
	p := NewLocalProvider(dir)
	ctx := context.Background()
	if err := p.Upload(ctx, "existing.tar.gz", strings.NewReader("complete")); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("source failure")
	for _, name := range []string{"existing.tar.gz", "new.tar.gz"} {
		err := p.Upload(ctx, name, io.MultiReader(strings.NewReader("partial"), localUploadFailure{injected}))
		if !errors.Is(err, injected) {
			t.Fatalf("Upload = %v, want source failure", err)
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, "existing.tar.gz"))
	if err != nil || string(b) != "complete" {
		t.Fatalf("existing archive changed: %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.tar.gz")); !os.IsNotExist(err) {
		t.Fatalf("new partial archive visible: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary upload leaked: %v %v", entries, err)
	}
	if err := p.Upload(ctx, "existing.tar.gz", strings.NewReader("replacement")); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(filepath.Join(dir, "existing.tar.gz"))
	if err != nil || string(b) != "replacement" {
		t.Fatalf("replacement: %q, %v", b, err)
	}
	info, err := os.Stat(filepath.Join(dir, "existing.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
}
