//go:build unix

package backup

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// F46000: addDirToTar checks each entry's type from the directory listing and
// then opens it by path. With UWAS running as root and the tree owned by a
// tenant, the tenant can swap an entry between those two steps: a symlink made
// root copy a host file into the archive (which a later restore writes back
// into the tenant's tree), a FIFO hung the open forever, and a directory
// replaced by a symlink redirected every file below it.
//
// The swap is made deterministic with the destination writer: the hook fires
// while the previous entry is being archived, which is after WalkDir listed the
// directory (so the victim entry is still typed "regular") and before the
// victim is opened.

// swapWriter runs hook once, on the first write after the archive already
// contains trigger.
type swapWriter struct {
	buf     bytes.Buffer
	trigger string
	hook    func()
	fired   bool
}

func (w *swapWriter) Write(p []byte) (int, error) {
	n, err := w.buf.Write(p)
	if !w.fired && bytes.Contains(w.buf.Bytes(), []byte(w.trigger)) {
		w.fired = true
		w.hook()
	}
	return n, err
}

func tarContents(t *testing.T, b []byte) map[string]string {
	t.Helper()
	out := map[string]string{}
	tr := tar.NewReader(bytes.NewReader(b))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("read archive: %v", err)
		}
		data, _ := io.ReadAll(tr)
		out[h.Name] = string(data)
	}
}

func archiveDir(t *testing.T, root string, w io.Writer) error {
	t.Helper()
	tw := tar.NewWriter(w)
	err := addDirToTar(tw, root, "pfx")
	_ = tw.Close()
	return err
}

func TestAddDirToTarSwappedLeafSymlinkDoesNotLeak(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "site")
	outside := filepath.Join(base, "host-secret")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("A"), 0o644)
	os.WriteFile(filepath.Join(root, "b.txt"), []byte("B"), 0o644)
	os.WriteFile(outside, []byte("HOST-SECRET"), 0o600)

	w := &swapWriter{trigger: "pfx/a.txt", hook: func() {
		os.Remove(filepath.Join(root, "b.txt"))
		os.Symlink(outside, filepath.Join(root, "b.txt"))
	}}
	if err := archiveDir(t, root, w); err != nil {
		t.Fatalf("archive: %v", err)
	}
	got := tarContents(t, w.buf.Bytes())
	t.Logf("EXPECTED: host file not in archive\nACTUAL: pfx/b.txt = %q", got["pfx/b.txt"])
	if got["pfx/b.txt"] == "HOST-SECRET" {
		t.Fatalf("PROBLEM CONFIRMED: a file swapped for a symlink copied %q into the archive", got["pfx/b.txt"])
	}
	if got["pfx/a.txt"] != "A" {
		t.Errorf("control: pfx/a.txt = %q, want A", got["pfx/a.txt"])
	}
}

func TestAddDirToTarSwappedDirSymlinkDoesNotLeak(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "site")
	outside := filepath.Join(base, "host-dir")
	os.MkdirAll(filepath.Join(root, "sub"), 0o755)
	os.MkdirAll(outside, 0o755)
	os.WriteFile(filepath.Join(root, "sub", "a.txt"), []byte("A"), 0o644)
	os.WriteFile(filepath.Join(root, "sub", "b.txt"), []byte("B"), 0o644)
	os.WriteFile(filepath.Join(outside, "b.txt"), []byte("HOST-SECRET"), 0o600)

	w := &swapWriter{trigger: "pfx/sub/a.txt", hook: func() {
		os.Rename(filepath.Join(root, "sub"), filepath.Join(root, "sub.moved"))
		os.Symlink(outside, filepath.Join(root, "sub"))
	}}
	if err := archiveDir(t, root, w); err != nil {
		t.Fatalf("archive: %v", err)
	}
	got := tarContents(t, w.buf.Bytes())
	t.Logf("EXPECTED: host file not in archive\nACTUAL: pfx/sub/b.txt = %q", got["pfx/sub/b.txt"])
	if got["pfx/sub/b.txt"] == "HOST-SECRET" {
		t.Fatalf("PROBLEM CONFIRMED: a directory swapped for a symlink copied %q into the archive", got["pfx/sub/b.txt"])
	}
	if got["pfx/sub/a.txt"] != "A" {
		t.Errorf("control: pfx/sub/a.txt = %q, want A", got["pfx/sub/a.txt"])
	}
}

func TestAddDirToTarSwappedFIFODoesNotHang(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "site")
	os.MkdirAll(root, 0o755)
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("A"), 0o644)
	os.WriteFile(filepath.Join(root, "b.txt"), []byte("B"), 0o644)
	os.WriteFile(filepath.Join(root, "c.txt"), []byte("C"), 0o644)
	fifo := filepath.Join(root, "b.txt")

	w := &swapWriter{trigger: "pfx/a.txt", hook: func() {
		os.Remove(fifo)
		if err := syscall.Mkfifo(fifo, 0o644); err != nil {
			t.Errorf("mkfifo: %v", err)
		}
	}}
	done := make(chan error, 1)
	go func() { done <- archiveDir(t, root, w) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("archive: %v", err)
		}
		got := tarContents(t, w.buf.Bytes())
		if got["pfx/a.txt"] != "A" || got["pfx/c.txt"] != "C" {
			t.Errorf("entries around the swapped FIFO missing: %v", got)
		}
	case <-time.After(5 * time.Second):
		// Release the blocked open so the goroutine can finish.
		if f, err := os.OpenFile(fifo, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			f.Close()
		}
		<-done
		t.Fatalf("PROBLEM CONFIRMED: a file swapped for a FIFO blocked the backup (EXPECTED: archive finishes, ACTUAL: still blocked after 5s)")
	}
}

// Control: an untouched tree is archived in full.
func TestAddDirToTarUntouchedTreeArchivedInFull(t *testing.T) {
	root := filepath.Join(t.TempDir(), "site")
	os.MkdirAll(filepath.Join(root, "sub"), 0o755)
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("A"), 0o644)
	os.WriteFile(filepath.Join(root, "sub", "b.txt"), []byte("B"), 0o644)
	var buf bytes.Buffer
	if err := archiveDir(t, root, &buf); err != nil {
		t.Fatal(err)
	}
	got := tarContents(t, buf.Bytes())
	if got["pfx/a.txt"] != "A" || got["pfx/sub/b.txt"] != "B" {
		t.Errorf("control: archive = %v", got)
	}
}
