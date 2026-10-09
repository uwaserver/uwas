package backup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type failingUploadReader struct {
	r   io.Reader
	err error
}

func (f *failingUploadReader) Read(p []byte) (int, error) {
	n, err := f.r.Read(p)
	if err == io.EOF {
		return n, f.err
	}
	return n, err
}

// TestSFTPUploadFailureLeavesNoArchive pins that an upload whose local stream
// fails never leaves a truncated *.tar.gz under the final name, where List
// and pruneOld would count it as a real backup and evict good ones.
func TestSFTPUploadFailureLeavesNoArchive(t *testing.T) {
	storage := t.TempDir()
	host, port, cleanup := startTestSSHServer(t, storage)
	defer cleanup()
	sp := NewSFTPProvider(host, port, "testuser", "", "testpass", "/backups", true)

	payload := bytes.Repeat([]byte("A"), 4096)
	r := &failingUploadReader{r: bytes.NewReader(payload[:1000]), err: errors.New("read failed")}
	if err := sp.Upload(context.Background(), "uwas-backup-x.tar.gz", r); err == nil {
		t.Fatal("upload with a failing reader succeeded")
	}
	if entries, _ := os.ReadDir(filepath.Join(storage, "backups")); len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("failed upload left remote files: %v", names)
	}
	if list, _ := sp.List(context.Background()); len(list) != 0 {
		t.Fatalf("failed upload is listed as a backup: %+v", list)
	}
}

// TestFullBackupRestoresSiteRootsInPlace pins that every domain root under
// web_root is restored to its own path, not only the web_root/<x>/<y> layout,
// and that two roots sharing their last two path elements stay separate.
func TestFullBackupRestoresSiteRootsInPlace(t *testing.T) {
	origDump := dumpAllDatabasesFunc
	dumpAllDatabasesFunc = func() ([]byte, error) { return nil, nil }
	defer func() { dumpAllDatabasesFunc = origDump }()

	m, _ := testManager(t)
	base := t.TempDir()
	webRoot := filepath.Join(base, "www")
	roots := map[string]string{
		filepath.Join(webRoot, "b.com", "public_html"):            "B",
		filepath.Join(webRoot, "a.com"):                           "A",
		filepath.Join(webRoot, "clients", "c.com", "public_html"): "C",
		filepath.Join(webRoot, "d.com", "public_html"):            "D",
		filepath.Join(webRoot, "old", "d.com", "public_html"):     "D-OLD",
	}
	var list []string
	for r, c := range roots {
		if err := os.MkdirAll(r, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(r, "index.html"), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
		list = append(list, r)
	}
	cfg := filepath.Join(base, "uwas.yaml")
	if err := os.WriteFile(cfg, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.SetPaths(cfg, "")
	m.SetDomainPaths(webRoot, "", list)

	info, err := m.CreateBackup("mem")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(webRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(webRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.RestoreBackup(info.Name, "mem"); err != nil {
		t.Fatal(err)
	}
	for r, c := range roots {
		got, err := os.ReadFile(filepath.Join(r, "index.html"))
		if err != nil || string(got) != c {
			t.Errorf("%s restored as %q (err=%v), want %q", r, got, err, c)
		}
	}
}
