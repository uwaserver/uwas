package backup

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type localCancellationGate struct {
	entered, release chan struct{}
	read             bool
}

func (r *localCancellationGate) Read(p []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	close(r.entered)
	<-r.release
	return copy(p, "replacement"), io.EOF
}
func TestLocalUploadCancellationPreservesArchive(t *testing.T) {
	for _, name := range []string{"existing.tar.gz", "new.tar.gz"} {
		dir := t.TempDir()
		p := NewLocalProvider(dir)
		if err := p.Upload(context.Background(), "existing.tar.gz", strings.NewReader("complete")); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		gate := &localCancellationGate{entered: make(chan struct{}), release: make(chan struct{})}
		done := make(chan error, 1)
		go func() { done <- p.Upload(ctx, name, gate) }()
		select {
		case <-gate.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("source gate not reached")
		}
		cancel()
		close(gate.release)
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatal("cancel lost", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("upload did not end")
		}
		body, err := os.ReadFile(filepath.Join(dir, "existing.tar.gz"))
		if err != nil || string(body) != "complete" {
			t.Fatal("original changed", string(body), err)
		}
		if name == "new.tar.gz" {
			if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
				t.Fatal("new canceled archive published", err)
			}
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 1 {
			t.Fatal("staging file leaked", entries, err)
		}
		if err := p.Upload(ctx, name, strings.NewReader("already canceled")); !errors.Is(err, context.Canceled) {
			t.Fatal("already-canceled accepted", err)
		}
		injected := errors.New("source failure")
		if err := p.Upload(context.Background(), name, localUploadFailure{injected}); !errors.Is(err, injected) {
			t.Fatal("source error lost", err)
		}
		if err := p.Upload(context.Background(), "empty.tar.gz", strings.NewReader("")); err != nil {
			t.Fatal("active empty upload", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := filepath.Join(t.TempDir(), "not-created")
	if err := NewLocalProvider(dir).Upload(ctx, "x.tar.gz", strings.NewReader("x")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("canceled call created directory", err)
	}
}
