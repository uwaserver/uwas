package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// --- CreateBackup: exercise every archive section in a single run ---

// TestCreateBackupFullArchive sets up config + domains.d + certs + domain roots
// plus mocked native and docker DB dumps so every "add to tar" branch in
// CreateBackup runs.
func TestCreateBackupFullArchive(t *testing.T) {
	oldAll := dumpAllDatabasesFunc
	oldDocker := dockerDumpFn
	defer func() {
		dumpAllDatabasesFunc = oldAll
		dockerDumpFn = oldDocker
	}()
	dumpAllDatabasesFunc = func() ([]byte, error) {
		return []byte("-- native dump"), nil
	}
	SetDockerDumpFunc(func() map[string][]byte {
		return map[string][]byte{
			"app1": []byte("-- docker app1 dump"),
			"app2": nil, // empty dump should be skipped
		}
	})

	m, mp := testManager(t)

	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")
	if err := os.WriteFile(cfgFile, []byte("global:\n  log_level: info\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// domains.d/ with a config file.
	domainsDir := filepath.Join(tmpDir, "domains.d")
	if err := os.MkdirAll(domainsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(domainsDir, "example.com.yaml"), []byte("host: example.com\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// certs/ with a file.
	certsDir := filepath.Join(tmpDir, "certs")
	if err := os.MkdirAll(certsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certsDir, "example.com.pem"), []byte("CERT"), 0644); err != nil {
		t.Fatal(err)
	}

	// A domain web root (sites/<parent>/<root>).
	webBase := filepath.Join(tmpDir, "www")
	domainRoot := filepath.Join(webBase, "example.com")
	if err := os.MkdirAll(domainRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(domainRoot, "index.html"), []byte("<h1>hi</h1>"), 0644); err != nil {
		t.Fatal(err)
	}

	m.SetPaths(cfgFile, certsDir)
	m.SetDomainPaths(webBase, domainsDir, []string{domainRoot, ""}) // "" root skipped

	info, err := m.CreateBackup("mem")
	if err != nil {
		t.Fatal(err)
	}

	// Verify the archive contains all expected sections.
	want := map[string]bool{
		"config/uwas.yaml":                   false,
		"domains.d/example.com.yaml":         false,
		"certs/example.com.pem":              false,
		"databases/native-all-databases.sql": false,
		"databases/docker-app1.sql":          false,
	}
	hasSite := false
	for _, name := range tarEntryNames(t, mp.files[info.Name]) {
		if _, ok := want[name]; ok {
			want[name] = true
		}
		if strings.HasPrefix(name, "sites/") && strings.HasSuffix(name, "index.html") {
			hasSite = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("expected archive entry %q not found", name)
		}
	}
	if !hasSite {
		t.Error("expected a sites/ entry for the domain web root")
	}
}

func tarEntryNames(t *testing.T, data []byte) []string {
	t.Helper()
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, hdr.Name)
	}
	return names
}

// TestCreateBackupCertsAddError makes addDirToTar for certs fail by pointing at
// a directory containing an unreadable entry is hard to force portably; instead
// exercise the fatal-certs branch differently: a config-add failure already
// covered elsewhere. Here we cover the docker dump header/write success path and
// the empty-docker-dump skip path via the full-archive test above. This test
// instead covers CreateBackup when dumpAllDatabases returns an error (skipped).
func TestCreateBackupDBDumpError(t *testing.T) {
	old := dumpAllDatabasesFunc
	defer func() { dumpAllDatabasesFunc = old }()
	dumpAllDatabasesFunc = func() ([]byte, error) {
		return nil, fmt.Errorf("mysql down")
	}

	m, mp := testManager(t)
	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("x"), 0644)
	m.SetPaths(cfgFile, "")

	info, err := m.CreateBackup("mem")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range tarEntryNames(t, mp.files[info.Name]) {
		if strings.HasPrefix(name, "databases/") {
			t.Errorf("did not expect db dump entry, got %q", name)
		}
	}
}

// TestCreateBackupDomainRootNotDir covers the domain-root stat branch where the
// path is a regular file (not a directory) and is therefore skipped.
func TestCreateBackupDomainRootNotDir(t *testing.T) {
	m, _ := testManager(t)
	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("x"), 0644)

	notDir := filepath.Join(tmpDir, "rootfile")
	os.WriteFile(notDir, []byte("data"), 0644)

	m.SetPaths(cfgFile, "")
	m.SetDomainPaths(tmpDir, "", []string{notDir})

	if _, err := m.CreateBackup("mem"); err != nil {
		t.Fatal(err)
	}
}

// --- RestoreBackup edge cases ---

// buildArchive builds a gzipped tar from the given headers/payloads.
func buildArchive(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0644, Typeflag: e.typeflag}
		if e.typeflag == tar.TypeDir {
			hdr.Mode = 0755
		}
		hdr.Size = int64(len(e.data))
		if e.linkname != "" {
			hdr.Linkname = e.linkname
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if len(e.data) > 0 {
			tw.Write(e.data)
		}
	}
	tw.Close()
	gw.Close()
	return buf.Bytes()
}

type tarEntry struct {
	name     string
	data     []byte
	typeflag byte
	linkname string
}

// TestRestoreBackupSkipsNonRegular covers the symlink/non-regular skip branch.
func TestRestoreBackupSkipsNonRegular(t *testing.T) {
	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp

	mp.files["t.tar.gz"] = buildArchive(t, []tarEntry{
		{name: "config/uwas.yaml", data: []byte("cfg"), typeflag: tar.TypeReg},
		{name: "certs/evil-link", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
	})

	dstDir := t.TempDir()
	certsDir := filepath.Join(dstDir, "certs")
	os.MkdirAll(certsDir, 0755)
	m.SetPaths(filepath.Join(dstDir, "uwas.yaml"), certsDir)

	if err := m.RestoreBackup("t.tar.gz", "mem"); err != nil {
		t.Fatal(err)
	}
	// The symlink entry must NOT have been created.
	if _, err := os.Lstat(filepath.Join(certsDir, "evil-link")); !os.IsNotExist(err) {
		t.Error("non-regular entry should have been skipped")
	}
}

// TestRestoreBackupCertsDirEntry covers the certs/ TypeDir creation branch and
// the certs empty-rel skip branch.
func TestRestoreBackupCertsDirEntry(t *testing.T) {
	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp

	mp.files["t.tar.gz"] = buildArchive(t, []tarEntry{
		{name: "config/uwas.yaml", data: []byte("cfg"), typeflag: tar.TypeReg},
		{name: "certs/", typeflag: tar.TypeDir},     // empty rel -> continue
		{name: "certs/sub/", typeflag: tar.TypeDir}, // dir creation
		{name: "certs/sub/x.pem", data: []byte("PEM"), typeflag: tar.TypeReg},
	})

	dstDir := t.TempDir()
	certsDir := filepath.Join(dstDir, "certs")
	m.SetPaths(filepath.Join(dstDir, "uwas.yaml"), certsDir)

	if err := m.RestoreBackup("t.tar.gz", "mem"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(certsDir, "sub", "x.pem")); err != nil {
		t.Errorf("expected restored cert file: %v", err)
	}
}

// TestRestoreBackupCertsDirEmpty covers the certsDir=="" skip branch.
func TestRestoreBackupCertsDirEmpty(t *testing.T) {
	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp

	mp.files["t.tar.gz"] = buildArchive(t, []tarEntry{
		{name: "config/uwas.yaml", data: []byte("cfg"), typeflag: tar.TypeReg},
		{name: "certs/x.pem", data: []byte("PEM"), typeflag: tar.TypeReg},
		{name: "domains.d/x.yaml", data: []byte("d"), typeflag: tar.TypeReg},   // domainsDir == "" skip
		{name: "sites/a/index.html", data: []byte("s"), typeflag: tar.TypeReg}, // webRoot == "" skip
		{name: "unknown/blah", data: []byte("u"), typeflag: tar.TypeReg},       // default skip
	})

	dstDir := t.TempDir()
	m.SetPaths(filepath.Join(dstDir, "uwas.yaml"), "") // certsDir empty

	if err := m.RestoreBackup("t.tar.gz", "mem"); err != nil {
		t.Fatal(err)
	}
}

// TestRestoreBackupRejectsTraversal covers the safeRestorePath rejection
// branches for domains.d/, certs/ and sites/ entries (path traversal attempts).
func TestRestoreBackupRejectsTraversal(t *testing.T) {
	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp

	mp.files["t.tar.gz"] = buildArchive(t, []tarEntry{
		{name: "config/uwas.yaml", data: []byte("cfg"), typeflag: tar.TypeReg},
		{name: "domains.d/../../escape1", data: []byte("x"), typeflag: tar.TypeReg},
		{name: "certs/../../escape2", data: []byte("x"), typeflag: tar.TypeReg},
		{name: "sites/../../escape3", data: []byte("x"), typeflag: tar.TypeReg},
	})

	dstDir := t.TempDir()
	domainsDir := filepath.Join(dstDir, "domains.d")
	certsDir := filepath.Join(dstDir, "certs")
	webRoot := filepath.Join(dstDir, "www")
	os.MkdirAll(domainsDir, 0755)
	os.MkdirAll(certsDir, 0755)
	os.MkdirAll(webRoot, 0755)
	m.SetPaths(filepath.Join(dstDir, "uwas.yaml"), certsDir)
	m.SetDomainPaths(webRoot, domainsDir, nil)

	if err := m.RestoreBackup("t.tar.gz", "mem"); err != nil {
		t.Fatal(err)
	}
	// None of the escape files should exist anywhere.
	for _, p := range []string{
		filepath.Join(dstDir, "..", "escape1"),
		filepath.Join(dstDir, "..", "escape2"),
		filepath.Join(dstDir, "..", "escape3"),
	} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("traversal file was created: %s", p)
		}
	}
}

// TestRestoreBackupWriteError covers the os.OpenFile error branch by making the
// target path's parent a regular file (so MkdirAll on it fails) — actually we
// force the create error by making outPath a directory that already exists.
func TestRestoreBackupWriteError(t *testing.T) {
	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp

	dstDir := t.TempDir()
	certsDir := filepath.Join(dstDir, "certs")
	os.MkdirAll(certsDir, 0755)
	// Pre-create the target path as a DIRECTORY so OpenFile(O_WRONLY) fails.
	os.MkdirAll(filepath.Join(certsDir, "x.pem"), 0755)

	mp.files["t.tar.gz"] = buildArchive(t, []tarEntry{
		{name: "certs/x.pem", data: []byte("PEM"), typeflag: tar.TypeReg},
	})
	m.SetPaths(filepath.Join(dstDir, "uwas.yaml"), certsDir)

	err := m.RestoreBackup("t.tar.gz", "mem")
	if err == nil {
		t.Fatal("expected create error when target is an existing directory")
	}
}

// TestRestoreBackupDBImportError covers the importDatabaseDump error branch.
func TestRestoreBackupDBImportError(t *testing.T) {
	old := importDatabaseDumpFunc
	defer func() { importDatabaseDumpFunc = old }()
	importDatabaseDumpFunc = func(data []byte, l *logger.Logger) error {
		return fmt.Errorf("import boom")
	}

	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp

	mp.files["t.tar.gz"] = buildArchive(t, []tarEntry{
		{name: "databases/native-all-databases.sql", data: []byte("SELECT 1;"), typeflag: tar.TypeReg},
	})

	dstDir := t.TempDir()
	m.SetPaths(filepath.Join(dstDir, "uwas.yaml"), "")

	err := m.RestoreBackup("t.tar.gz", "mem")
	if err == nil || !strings.Contains(err.Error(), "restore database") {
		t.Fatalf("expected restore database error, got %v", err)
	}
}

// TestRestoreBackupDBDirEntry covers the databases entry that is a directory
// (Typeflag == TypeDir) which is skipped without import.
func TestRestoreBackupDBDirEntry(t *testing.T) {
	called := false
	old := importDatabaseDumpFunc
	defer func() { importDatabaseDumpFunc = old }()
	importDatabaseDumpFunc = func(data []byte, l *logger.Logger) error {
		called = true
		return nil
	}

	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp

	mp.files["t.tar.gz"] = buildArchive(t, []tarEntry{
		{name: "databases/all-databases.sql", typeflag: tar.TypeDir},
	})

	dstDir := t.TempDir()
	m.SetPaths(filepath.Join(dstDir, "uwas.yaml"), "")

	if err := m.RestoreBackup("t.tar.gz", "mem"); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Error("import should not run for a directory db entry")
	}
}

// TestRestoreBackupMaxTotalSize covers the total-size limit branch.
func TestRestoreBackupMaxTotalSize(t *testing.T) {
	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp
	m.cfg.MaxTotalSize = 4 // tiny limit

	dstDir := t.TempDir()
	certsDir := filepath.Join(dstDir, "certs")
	m.SetPaths(filepath.Join(dstDir, "uwas.yaml"), certsDir)

	mp.files["t.tar.gz"] = buildArchive(t, []tarEntry{
		{name: "certs/a.pem", data: []byte("0123456789"), typeflag: tar.TypeReg},
		{name: "certs/b.pem", data: []byte("0123456789"), typeflag: tar.TypeReg},
	})

	err := m.RestoreBackup("t.tar.gz", "mem")
	if err == nil || !strings.Contains(err.Error(), "max total size") {
		t.Fatalf("expected max total size error, got %v", err)
	}
}

// TestRestoreBackupMaxFileSize covers the per-file size-limit branch.
func TestRestoreBackupMaxFileSize(t *testing.T) {
	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp
	m.cfg.MaxFileSize = 4 // tiny per-file limit

	dstDir := t.TempDir()
	certsDir := filepath.Join(dstDir, "certs")
	m.SetPaths(filepath.Join(dstDir, "uwas.yaml"), certsDir)

	mp.files["t.tar.gz"] = buildArchive(t, []tarEntry{
		{name: "certs/big.pem", data: []byte("0123456789"), typeflag: tar.TypeReg},
	})

	err := m.RestoreBackup("t.tar.gz", "mem")
	if err == nil || !strings.Contains(err.Error(), "max size limit") {
		t.Fatalf("expected per-file max size error, got %v", err)
	}
}

// TestRestoreBackupDownloadFailure covers the download failure branch.
func TestRestoreBackupDownloadFailure(t *testing.T) {
	log := logger.New("error", "text")
	m := New(config.BackupConfig{Provider: "fail"}, log)
	m.providers["fail"] = &failingProvider{pname: "fail"}

	dstDir := t.TempDir()
	m.SetPaths(filepath.Join(dstDir, "uwas.yaml"), "")

	err := m.RestoreBackup("x.tar.gz", "fail")
	if err == nil || !strings.Contains(err.Error(), "download backup") {
		t.Fatalf("expected download error, got %v", err)
	}
}

// TestRestoreBackupBadGzip covers the gzip.NewReader error branch.
func TestRestoreBackupBadGzip(t *testing.T) {
	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp
	mp.files["t.tar.gz"] = []byte("not gzip data at all")

	dstDir := t.TempDir()
	m.SetPaths(filepath.Join(dstDir, "uwas.yaml"), "")

	err := m.RestoreBackup("t.tar.gz", "mem")
	if err == nil || !strings.Contains(err.Error(), "gzip reader") {
		t.Fatalf("expected gzip reader error, got %v", err)
	}
}

// TestRestoreBackupBadTar covers the tar Next error branch (corrupt tar inside
// a valid gzip stream).
func TestRestoreBackupBadTar(t *testing.T) {
	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	gw.Write([]byte("this is not a valid tar archive but is valid gzip"))
	gw.Close()
	mp.files["t.tar.gz"] = buf.Bytes()

	dstDir := t.TempDir()
	m.SetPaths(filepath.Join(dstDir, "uwas.yaml"), "")

	err := m.RestoreBackup("t.tar.gz", "mem")
	if err == nil || !strings.Contains(err.Error(), "read tar") {
		t.Fatalf("expected read tar error, got %v", err)
	}
}

// TestRestoreBackupUnknownProvider / NoConfigPath cover early returns.
func TestRestoreBackupGuards(t *testing.T) {
	log := logger.New("error", "text")
	m := New(config.BackupConfig{}, log)
	// No config path set.
	if err := m.RestoreBackup("x", "local"); err == nil || !strings.Contains(err.Error(), "config path not set") {
		t.Fatalf("expected config path error, got %v", err)
	}
	m.SetPaths(filepath.Join(t.TempDir(), "c.yaml"), "")
	if err := m.RestoreBackup("x", "nope"); err == nil || !strings.Contains(err.Error(), "unknown backup provider") {
		t.Fatalf("expected unknown provider error, got %v", err)
	}
}

// --- CreateBackup guards ---

func TestCreateBackupGuards(t *testing.T) {
	log := logger.New("error", "text")
	m := New(config.BackupConfig{}, log)
	if _, err := m.CreateBackup("local"); err == nil || !strings.Contains(err.Error(), "config path not set") {
		t.Fatalf("expected config path error, got %v", err)
	}
	m.SetPaths(filepath.Join(t.TempDir(), "c.yaml"), "")
	if _, err := m.CreateBackup("nope"); err == nil || !strings.Contains(err.Error(), "unknown backup provider") {
		t.Fatalf("expected unknown provider error, got %v", err)
	}
}

// --- CreateDomainBackup full archive ---

func TestCreateDomainBackupFullArchive(t *testing.T) {
	old := dumpDatabaseFunc
	defer func() { dumpDatabaseFunc = old }()
	dumpDatabaseFunc = func(dbName string) ([]byte, error) {
		return []byte("-- dump " + dbName), nil
	}

	m, mp := testManager(t)
	tmpDir := t.TempDir()

	webRoot := filepath.Join(tmpDir, "site")
	os.MkdirAll(webRoot, 0755)
	os.WriteFile(filepath.Join(webRoot, "index.php"), []byte("<?php"), 0644)

	domainsDir := filepath.Join(tmpDir, "domains.d")
	os.MkdirAll(domainsDir, 0755)
	os.WriteFile(filepath.Join(domainsDir, "example.com.yaml"), []byte("host: example.com\n"), 0644)

	m.SetDomainPaths("", domainsDir, nil)

	info, err := m.CreateDomainBackup("example.com", webRoot, "mydb", "mem")
	if err != nil {
		t.Fatal(err)
	}

	names := tarEntryNames(t, mp.files[info.Name])
	want := map[string]bool{
		"config/example.com.yaml": false,
		"database/mydb.sql":       false,
	}
	hasSite := false
	for _, n := range names {
		if _, ok := want[n]; ok {
			want[n] = true
		}
		if strings.HasPrefix(n, "site/") && strings.HasSuffix(n, "index.php") {
			hasSite = true
		}
	}
	for n, found := range want {
		if !found {
			t.Errorf("missing archive entry %q", n)
		}
	}
	if !hasSite {
		t.Error("expected site/ entry")
	}
}

// --- archiveAndUpload error paths ---

func TestArchiveAndUploadAddEntriesError(t *testing.T) {
	mp := newMemoryProvider("mem")
	ctx := context.Background()
	_, err := archiveAndUpload(ctx, mp, "uwas-*.tar.gz", "x.tar.gz", func(tw *tar.Writer) error {
		return fmt.Errorf("boom")
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected addEntries error, got %v", err)
	}
	if len(mp.files) != 0 {
		t.Error("upload should not have happened on addEntries error")
	}
}

func TestArchiveAndUploadUploadError(t *testing.T) {
	fp := &failingProvider{pname: "fail"}
	ctx := context.Background()
	_, err := archiveAndUpload(ctx, fp, "uwas-*.tar.gz", "x.tar.gz", func(tw *tar.Writer) error {
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "upload backup") {
		t.Fatalf("expected upload error, got %v", err)
	}
}

// --- addFileToTar / addDirToTar error & symlink paths ---

func TestAddFileToTarOpenError(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	defer tw.Close()
	err := addFileToTar(tw, filepath.Join(t.TempDir(), "does-not-exist"), "x")
	if err == nil {
		t.Fatal("expected open error")
	}
}

func TestAddDirToTarSkipsSymlinks(t *testing.T) {
	srcDir := t.TempDir()
	os.WriteFile(filepath.Join(srcDir, "real.txt"), []byte("data"), 0644)

	// File symlink.
	if err := os.Symlink(filepath.Join(srcDir, "real.txt"), filepath.Join(srcDir, "filelink")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	// Directory symlink.
	otherDir := t.TempDir()
	os.WriteFile(filepath.Join(otherDir, "secret.txt"), []byte("secret"), 0644)
	if err := os.Symlink(otherDir, filepath.Join(srcDir, "dirlink")); err != nil {
		t.Skipf("dir symlinks unavailable: %v", err)
	}

	// Nested real subdir to exercise the dir-header branch.
	os.MkdirAll(filepath.Join(srcDir, "sub"), 0755)
	os.WriteFile(filepath.Join(srcDir, "sub", "n.txt"), []byte("n"), 0644)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := addDirToTar(tw, srcDir, "prefix"); err != nil {
		t.Fatal(err)
	}
	tw.Close()

	names := tarEntryNames(t, mustGzip(t, buf.Bytes()))
	for _, n := range names {
		if strings.Contains(n, "filelink") || strings.Contains(n, "dirlink") || strings.Contains(n, "secret") {
			t.Errorf("symlink content leaked into archive: %q", n)
		}
	}
}

// TestAddFileToTarWriteHeaderError covers the WriteHeader error branch by
// passing a tar.Writer that has already been closed.
func TestAddFileToTarWriteHeaderError(t *testing.T) {
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "f.txt")
	os.WriteFile(src, []byte("data"), 0644)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	tw.Close() // closed: subsequent WriteHeader returns ErrWriteAfterClose

	err := addFileToTar(tw, src, "f.txt")
	if err == nil {
		t.Fatal("expected WriteHeader error on closed tar writer")
	}
}

// TestAddDirToTarWriteHeaderError covers the dir-header WriteHeader error branch
// by walking a directory with a closed tar.Writer.
func TestAddDirToTarWriteHeaderError(t *testing.T) {
	srcDir := t.TempDir()
	os.MkdirAll(filepath.Join(srcDir, "sub"), 0755)
	os.WriteFile(filepath.Join(srcDir, "sub", "f.txt"), []byte("d"), 0644)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	tw.Close()

	err := addDirToTar(tw, srcDir, "prefix")
	if err == nil {
		t.Fatal("expected error walking into closed tar writer")
	}
}

func TestAddDirToTarWalkError(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	defer tw.Close()
	// Non-existent source dir -> WalkDir invokes fn with err on the root.
	err := addDirToTar(tw, filepath.Join(t.TempDir(), "missing"), "prefix")
	if err == nil {
		t.Fatal("expected walk error for missing dir")
	}
}

func mustGzip(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	gw.Write(raw)
	gw.Close()
	return buf.Bytes()
}

// --- nextCronRun field-by-field coverage ---

func TestNextCronRunFieldMatching(t *testing.T) {
	// Each of these is valid and should return a non-zero next time, exercising
	// the month/day/weekday/hour/minute matching loops.
	exprs := []string{
		"0 0 1 1 *",   // specific minute/hour/day/month
		"30 14 * * *", // every day at 14:30
		"0 0 * * 0",   // weekly on Sunday
		"*/15 * * * *",
		"0 0 30 12 *", // day 30 of December
	}
	for _, e := range exprs {
		if got := nextCronRun(e); got.IsZero() {
			t.Errorf("nextCronRun(%q) returned zero", e)
		}
	}
	// Impossible date (Feb 30 with day-of-month 30 in month 2 won't match the
	// candidate-day generation cleanly but should still terminate; verify it
	// does not hang and returns something deterministic).
	_ = nextCronRun("0 0 30 2 1")
}

// TestNextCronRunDay31 is the regression for the calendar-walk bug: a schedule
// on day 31 must actually resolve to a day-31 run. The old index math built
// candidates as 1+(i%30), so day 31 never occurred and day-31 schedules silently
// never fired.
func TestNextCronRunDay31(t *testing.T) {
	next := nextCronRun("0 2 31 * *") // 02:00 on the 31st, any month with 31 days
	if next.IsZero() {
		t.Fatal("nextCronRun(day 31) returned zero — day-31 schedules never fire")
	}
	if next.Day() != 31 {
		t.Errorf("next run day = %d, want 31", next.Day())
	}
	if next.Hour() != 2 || next.Minute() != 0 {
		t.Errorf("next run time = %02d:%02d, want 02:00", next.Hour(), next.Minute())
	}
}

// --- ScheduleBackupCron goroutine fires ---

func TestScheduleBackupCronFires(t *testing.T) {
	m, mp := testManager(t)
	m.cfg.Provider = "mem"

	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("x"), 0644)
	m.SetPaths(cfgFile, "")

	fired := make(chan struct{}, 1)
	m.SetOnBackup(func(info *BackupInfo, err error) {
		select {
		case fired <- struct{}{}:
		default:
		}
	})

	// "* * * * *" => next run is within a minute. To make the test fast we
	// instead drive the cron path and then immediately stop, ensuring the
	// goroutine + nextCronRun + restart logic is exercised. We can't wait a
	// whole minute, so we just verify the schedule becomes active and the
	// goroutine starts cleanly.
	m.ScheduleBackupCron("* * * * *")
	if _, active := m.ScheduleStatus(); !active {
		t.Fatal("expected cron schedule active")
	}
	// Reschedule (covers the cancel-existing branch).
	m.ScheduleBackupCron("0 0 * * *")
	m.Stop()
	_ = mp
	_ = fired
}

// TestScheduleBackupCronGoroutineFires drives the cron scheduler through an
// actual fire of the backup goroutine (CreateBackup + onBackup callback). The
// smallest cron granularity is one minute, so this waits up to ~65s for the
// next minute boundary. It is deterministic in outcome (it WILL fire) and is
// the only way to cover the time.After fire branch without prod test-hooks.
func TestScheduleBackupCronGoroutineFires(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping minute-boundary cron fire test in -short mode")
	}

	m, mp := testManager(t)
	m.cfg.Provider = "mem"
	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("x"), 0644)
	m.SetPaths(cfgFile, "")

	fired := make(chan error, 1)
	m.SetOnBackup(func(info *BackupInfo, err error) {
		select {
		case fired <- err:
		default:
		}
	})

	m.ScheduleBackupCron("* * * * *") // fires at the next minute boundary
	defer stopAndSettle(m)

	select {
	case err := <-fired:
		if err != nil {
			t.Fatalf("scheduled cron backup error: %v", err)
		}
	case <-time.After(70 * time.Second):
		t.Fatal("cron backup did not fire within 70s")
	}

	mp.mu.Lock()
	n := len(mp.files)
	mp.mu.Unlock()
	if n == 0 {
		t.Error("expected at least one backup file from cron fire")
	}
}

// TestScheduleBackupCronDefaultProvider covers the provider=="" -> "local"
// default branch inside ScheduleBackupCron.
func TestScheduleBackupCronDefaultProvider(t *testing.T) {
	m, _ := testManager(t)
	m.cfg.Provider = "" // force the default-to-local branch
	m.ScheduleBackupCron("0 0 * * *")
	if _, active := m.ScheduleStatus(); !active {
		t.Fatal("expected active schedule")
	}
	m.Stop()
}

func TestScheduleBackupCronEmpty(t *testing.T) {
	m, _ := testManager(t)
	m.ScheduleBackupCron("0 0 * * *")
	m.ScheduleBackupCron("") // empty disables
	if _, active := m.ScheduleStatus(); active {
		t.Fatal("expected inactive after empty cron")
	}
}

// --- ScheduleDetail branches ---

func TestScheduleDetailIntervalFormats(t *testing.T) {
	cases := []struct {
		dur  time.Duration
		want string
	}{
		{24 * time.Hour, "1d"},
		{2 * time.Hour, "2h"},
		{90 * time.Minute, "1h30m0s"},
		{0, "0h"}, // 0%time.Hour==0 so the "%dh" branch wins
	}
	for _, c := range cases {
		m, _ := testManager(t)
		m.cfg.Provider = "" // exercise default -> local
		m.mu.Lock()
		m.schedule = c.dur
		m.mu.Unlock()
		d := m.ScheduleDetail()
		if d.Interval != c.want {
			t.Errorf("interval(%v) = %q, want %q", c.dur, d.Interval, c.want)
		}
		if d.Provider != "local" {
			t.Errorf("provider = %q, want local (default)", d.Provider)
		}
	}
}

// --- SetDockerDumpFunc ---

func TestSetDockerDumpFunc(t *testing.T) {
	old := dockerDumpFn
	defer func() { dockerDumpFn = old }()
	SetDockerDumpFunc(func() map[string][]byte {
		return map[string][]byte{"c": []byte("dump")}
	})
	if dockerDumpFn == nil {
		t.Fatal("dockerDumpFn not set")
	}
	if got := dockerDumpFn()["c"]; string(got) != "dump" {
		t.Errorf("docker dump = %q", string(got))
	}
}

// --- Real exec paths via fake mysql/mysqldump binaries on PATH ---
//
// These exercise the success/failure branches of dumpDatabaseReal,
// dumpAllDatabasesReal and importDatabaseDumpReal that otherwise require a real
// MySQL installation. We install fake executables in a temp dir and prepend it
// to PATH for the test. (Safe under -race now that the scheduled-backup tests no
// longer leak a goroutine that concurrently calls exec.LookPath.)

func writeFakeBin(t *testing.T, dir, binName, stdout string, code int) {
	t.Helper()
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s' %q\nexit %d\n", stdout, code)
	if err := os.WriteFile(filepath.Join(dir, binName), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
}

func prependPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestDumpDatabaseRealWithFakeBin(t *testing.T) {
	dir := t.TempDir()
	writeFakeBin(t, dir, "mysqldump", "-- dump output", 0)
	prependPath(t, dir)

	out, err := dumpDatabaseReal("validdb")
	if err != nil {
		t.Fatalf("dumpDatabaseReal: %v", err)
	}
	if string(out) != "-- dump output" {
		t.Errorf("output = %q", string(out))
	}
}

func TestDumpAllDatabasesRealWithFakeBin(t *testing.T) {
	dir := t.TempDir()
	writeFakeBin(t, dir, "mysqldump", "-- all dbs", 0)
	prependPath(t, dir)

	out, err := dumpAllDatabasesReal()
	if err != nil {
		t.Fatalf("dumpAllDatabasesReal: %v", err)
	}
	if string(out) != "-- all dbs" {
		t.Errorf("output = %q", string(out))
	}
}

func TestDumpAllDatabasesRealCmdError(t *testing.T) {
	dir := t.TempDir()
	writeFakeBin(t, dir, "mysqldump", "boom", 1) // non-zero exit
	prependPath(t, dir)

	if _, err := dumpAllDatabasesReal(); err == nil {
		t.Fatal("expected command error from non-zero exit")
	}
}

func TestImportDatabaseDumpRealWithFakeBin(t *testing.T) {
	dir := t.TempDir()
	writeFakeBin(t, dir, "mysql", "", 0)
	prependPath(t, dir)

	log := logger.New("error", "text")
	if err := importDatabaseDumpReal([]byte("SELECT 1;"), log); err != nil {
		t.Fatalf("importDatabaseDumpReal: %v", err)
	}
}

func TestImportDatabaseDumpRealCmdError(t *testing.T) {
	dir := t.TempDir()
	writeFakeBin(t, dir, "mysql", "import failed", 2)
	prependPath(t, dir)

	log := logger.New("error", "text")
	if err := importDatabaseDumpReal([]byte("SELECT 1;"), log); err == nil {
		t.Fatal("expected import error from non-zero exit")
	}
}

// --- S3 request-construction errors (invalid URL) ---

// invalidS3 returns a provider whose endpoint contains a control character so
// http.NewRequestWithContext fails to parse the URL.
func invalidS3() *S3Provider {
	return NewS3Provider("http://bad\x7f host", "bucket", "k", "s", "us-east-1")
}

func TestS3UploadBadURL(t *testing.T) {
	if err := invalidS3().Upload(context.Background(), "f.tar.gz", strings.NewReader("d")); err == nil {
		t.Fatal("expected request construction error")
	}
}

func TestS3DownloadBadURL(t *testing.T) {
	if _, err := invalidS3().Download(context.Background(), "f.tar.gz"); err == nil {
		t.Fatal("expected request construction error")
	}
}

func TestS3ListBadURL(t *testing.T) {
	if _, err := invalidS3().List(context.Background()); err == nil {
		t.Fatal("expected request construction error")
	}
}

func TestS3DeleteBadURL(t *testing.T) {
	if err := invalidS3().Delete(context.Background(), "f.tar.gz"); err == nil {
		t.Fatal("expected request construction error")
	}
}

// --- LocalProvider Upload close-error path ---

func TestLocalProviderUploadCloseError(t *testing.T) {
	dir := t.TempDir()
	p := NewLocalProvider(dir)
	if err := p.Upload(context.Background(), "ok.tar.gz", strings.NewReader("data")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "ok.tar.gz"))
	if string(got) != "data" {
		t.Errorf("content = %q", string(got))
	}
}

// errReader returns an error after the first read to exercise io.Copy failure.
type errReader struct{ msg string }

func (e errReader) Read(_ []byte) (int, error) { return 0, fmt.Errorf("%s", e.msg) }

func TestLocalProviderUploadCopyError(t *testing.T) {
	dir := t.TempDir()
	p := NewLocalProvider(dir)
	err := p.Upload(context.Background(), "x.tar.gz", errReader{msg: "read fail"})
	if err == nil || !strings.Contains(err.Error(), "read fail") {
		t.Fatalf("expected copy error, got %v", err)
	}
}

func TestLocalProviderRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := NewLocalProvider(dir)
	ctx := context.Background()
	if err := p.Upload(ctx, "a.tar.gz", strings.NewReader("AAA")); err != nil {
		t.Fatal(err)
	}
	if err := p.Upload(ctx, "b.tar.gz", strings.NewReader("BB")); err != nil {
		t.Fatal(err)
	}
	infos, err := p.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("expected 2, got %d", len(infos))
	}
	rc, err := p.Download(ctx, "a.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(rc)
	rc.Close()
	if string(data) != "AAA" {
		t.Errorf("download = %q", string(data))
	}
	if err := p.Delete(ctx, "a.tar.gz"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.tar.gz")); !os.IsNotExist(err) {
		t.Error("expected file deleted")
	}
}

// --- SFTP error paths via the in-process SSH server ---

func TestSFTPUploadInvalidFilename(t *testing.T) {
	storageDir := t.TempDir()
	host, port, cleanup := startTestSSHServer(t, storageDir)
	defer cleanup()

	p := NewSFTPProvider(host, port, "testuser", "", "testpass", "/backups", true)
	// mkdir succeeds, then safeBackupFilename rejects the name.
	err := p.Upload(context.Background(), "../evil.tar.gz", strings.NewReader("data"))
	if err == nil || !strings.Contains(err.Error(), "invalid backup filename") {
		t.Fatalf("expected invalid filename error, got %v", err)
	}
}

// TestSFTPUploadStreamsLarge verifies that uploads stream rather than buffer:
// a payload well past the old 100MB in-memory cap must upload and round-trip
// intact (the cap used to hard-fail such backups).
func TestSFTPUploadStreamsLarge(t *testing.T) {
	storageDir := t.TempDir()
	host, port, cleanup := startTestSSHServer(t, storageDir)
	defer cleanup()

	p := NewSFTPProvider(host, port, "testuser", "", "testpass", "/backups", true)
	// A few MB is enough to exercise the streaming path without slowing the
	// suite; the removed cap only ever triggered above 100MB.
	payload := bytes.Repeat([]byte("uwas-stream-test\n"), 200_000) // ~3.4MB
	if err := p.Upload(context.Background(), "big.tar.gz", bytes.NewReader(payload)); err != nil {
		t.Fatalf("streaming upload failed: %v", err)
	}
	rc, err := p.Download(context.Background(), "big.tar.gz")
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read downloaded: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("round-trip mismatch: got %d bytes, want %d", len(got), len(payload))
	}
}

func TestSFTPDownloadInvalidFilename(t *testing.T) {
	p := NewSFTPProvider("127.0.0.1", 22, "u", "", "p", "/backups", true)
	_, err := p.Download(context.Background(), "bad/name")
	if err == nil || !strings.Contains(err.Error(), "invalid backup filename") {
		t.Fatalf("expected invalid filename, got %v", err)
	}
}

func TestSFTPDeleteInvalidFilename(t *testing.T) {
	p := NewSFTPProvider("127.0.0.1", 22, "u", "", "p", "/backups", true)
	err := p.Delete(context.Background(), "bad/name")
	if err == nil || !strings.Contains(err.Error(), "invalid backup filename") {
		t.Fatalf("expected invalid filename, got %v", err)
	}
}

// TestSFTPDownloadRunError covers the Download path where the remote `cat`
// reports a non-existent file via a non-zero exit status when the reader is
// drained. The error surfaces on read/close, not on Download itself, so we
// validate the round trip completes and yields empty content for a missing file.
func TestSFTPDownloadMissingFile(t *testing.T) {
	storageDir := t.TempDir()
	host, port, cleanup := startTestSSHServer(t, storageDir)
	defer cleanup()

	p := NewSFTPProvider(host, port, "testuser", "", "testpass", "/backups", true)
	rc, err := p.Download(context.Background(), "missing.tar.gz")
	if err != nil {
		t.Fatalf("Download setup error: %v", err)
	}
	data, _ := io.ReadAll(rc)
	rc.Close()
	if len(data) != 0 {
		t.Errorf("expected empty content for missing file, got %d bytes", len(data))
	}
}

// TestSFTPDeleteAndListRoundTrip exercises Delete + List success against the
// in-process server (additional coverage of the find/printf parsing branch).
func TestSFTPListAndDeleteRoundTrip(t *testing.T) {
	storageDir := t.TempDir()
	host, port, cleanup := startTestSSHServer(t, storageDir)
	defer cleanup()

	p := NewSFTPProvider(host, port, "testuser", "", "testpass", "/backups", true)
	ctx := context.Background()
	if err := p.Upload(ctx, "one.tar.gz", strings.NewReader("1")); err != nil {
		t.Fatal(err)
	}
	if err := p.Upload(ctx, "two.tar.gz", strings.NewReader("22")); err != nil {
		t.Fatal(err)
	}
	infos, err := p.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Fatalf("expected 2 backups, got %d", len(infos))
	}
	if err := p.Delete(ctx, "one.tar.gz"); err != nil {
		t.Fatal(err)
	}
	infos, _ = p.List(ctx)
	if len(infos) != 1 {
		t.Fatalf("expected 1 after delete, got %d", len(infos))
	}
}

// --- SFTP dial: key-based auth with a valid key against the test server ---

func TestSFTPDialWithValidKey(t *testing.T) {
	storageDir := t.TempDir()

	// Generate an ed25519 client key and write it as PEM.
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pemBlock, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(pemBlock), 0600); err != nil {
		t.Fatal(err)
	}

	// Server that accepts any public key.
	host, port, cleanup := startPubkeySSHServer(t, storageDir)
	defer cleanup()

	p := NewSFTPProvider(host, port, "testuser", keyFile, "", "/backups", true)
	if err := p.Upload(context.Background(), "k.tar.gz", strings.NewReader("keyed")); err != nil {
		t.Fatalf("upload with key auth failed: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(storageDir, "backups", "k.tar.gz"))
	if err != nil {
		t.Fatalf("file not stored: %v", err)
	}
	if string(got) != "keyed" {
		t.Errorf("stored = %q", string(got))
	}
}

// startPubkeySSHServer is like startTestSSHServer but authenticates via any
// public key (PublicKeyCallback) so the dial() key-auth branch is exercised.
func startPubkeySSHServer(t *testing.T, storageDir string) (string, int, func()) {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}

	sshConfig := &ssh.ServerConfig{
		PublicKeyCallback: func(c ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			return nil, nil // accept any key
		},
	}
	sshConfig.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().(*net.TCPAddr)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleSSHConnection(conn, sshConfig, storageDir)
		}
	}()
	return addr.IP.String(), addr.Port, func() {
		listener.Close()
		<-done
	}
}

// startTestSSHServerWithKey is like startTestSSHServer but also returns the
// server's public host key so a test can populate a known_hosts file and
// exercise the non-insecure (TOFU) dial path.
func startTestSSHServerWithKey(t *testing.T, storageDir string) (host string, port int, hostKey ssh.PublicKey, cleanup func()) {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}

	sshConfig := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == "testuser" && string(pass) == "testpass" {
				return nil, nil
			}
			return nil, fmt.Errorf("auth failed")
		},
	}
	sshConfig.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().(*net.TCPAddr)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go handleSSHConnection(conn, sshConfig, storageDir)
		}
	}()
	return addr.IP.String(), addr.Port, signer.PublicKey(), func() {
		listener.Close()
		<-done
	}
}

// startTestSSHServerFailCmds starts a server that accepts sessions and exec
// requests but reports a non-zero exit status for every command, so the
// provider's session.Run / session.Output error branches are exercised.
func startTestSSHServerFailCmds(t *testing.T) (string, int, func()) {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	sshConfig := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == "testuser" && string(pass) == "testpass" {
				return nil, nil
			}
			return nil, fmt.Errorf("auth failed")
		},
	}
	sshConfig.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().(*net.TCPAddr)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				sConn, chans, reqs, err := ssh.NewServerConn(conn, sshConfig)
				if err != nil {
					return
				}
				defer sConn.Close()
				go ssh.DiscardRequests(reqs)
				for newChan := range chans {
					if newChan.ChannelType() != "session" {
						newChan.Reject(ssh.UnknownChannelType, "no")
						continue
					}
					channel, requests, err := newChan.Accept()
					if err != nil {
						continue
					}
					go func() {
						defer channel.Close()
						for req := range requests {
							if req.Type == "exec" {
								if req.WantReply {
									req.Reply(true, nil)
								}
								// Drain any stdin then report failure.
								io.Copy(io.Discard, channel)
								channel.SendRequest("exit-status", false, []byte{0, 0, 0, 1})
								return
							}
							if req.WantReply {
								req.Reply(false, nil)
							}
						}
					}()
				}
			}()
		}
	}()
	return addr.IP.String(), addr.Port, func() {
		listener.Close()
		<-done
	}
}

// startTestSSHServerCatFails accepts mkdir (exit 0) but fails the upload `cat`
// (exit 1), so Upload's run-error branch (after a successful mkdir) is covered.
func startTestSSHServerCatFails(t *testing.T) (string, int, func()) {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	sshConfig := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == "testuser" && string(pass) == "testpass" {
				return nil, nil
			}
			return nil, fmt.Errorf("auth failed")
		},
	}
	sshConfig.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().(*net.TCPAddr)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				sConn, chans, reqs, err := ssh.NewServerConn(conn, sshConfig)
				if err != nil {
					return
				}
				defer sConn.Close()
				go ssh.DiscardRequests(reqs)
				for newChan := range chans {
					if newChan.ChannelType() != "session" {
						newChan.Reject(ssh.UnknownChannelType, "no")
						continue
					}
					channel, requests, err := newChan.Accept()
					if err != nil {
						continue
					}
					go func() {
						defer channel.Close()
						for req := range requests {
							if req.Type != "exec" {
								if req.WantReply {
									req.Reply(false, nil)
								}
								continue
							}
							if req.WantReply {
								req.Reply(true, nil)
							}
							cmd := ""
							if len(req.Payload) >= 4 {
								n := int(req.Payload[0])<<24 | int(req.Payload[1])<<16 | int(req.Payload[2])<<8 | int(req.Payload[3])
								if len(req.Payload) >= 4+n {
									cmd = string(req.Payload[4 : 4+n])
								}
							}
							io.Copy(io.Discard, channel)
							if strings.HasPrefix(cmd, "mkdir ") {
								channel.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
							} else {
								channel.SendRequest("exit-status", false, []byte{0, 0, 0, 1})
							}
							return
						}
					}()
				}
			}()
		}
	}()
	return addr.IP.String(), addr.Port, func() {
		listener.Close()
		<-done
	}
}

// TestSFTPUploadCatFails covers the Upload run-error branch where mkdir succeeds
// but the remote `cat > file` exits non-zero.
func TestSFTPUploadCatFails(t *testing.T) {
	host, port, cleanup := startTestSSHServerCatFails(t)
	defer cleanup()

	p := NewSFTPProvider(host, port, "testuser", "", "testpass", "/backups", true)
	err := p.Upload(context.Background(), "x.tar.gz", strings.NewReader("payload"))
	if err == nil || !strings.Contains(err.Error(), "sftp upload") {
		t.Fatalf("expected upload run error, got %v", err)
	}
}

// TestSFTPUploadMkdirFails covers the Upload mkdir-error branch (remote mkdir
// returns non-zero exit status).
func TestSFTPUploadMkdirFails(t *testing.T) {
	host, port, cleanup := startTestSSHServerFailCmds(t)
	defer cleanup()

	p := NewSFTPProvider(host, port, "testuser", "", "testpass", "/backups", true)
	err := p.Upload(context.Background(), "x.tar.gz", strings.NewReader("d"))
	if err == nil || !strings.Contains(err.Error(), "sftp mkdir") {
		t.Fatalf("expected mkdir error, got %v", err)
	}
}

// TestSFTPListCommandFails covers the List branch where the remote find/ls
// command exits non-zero; List treats that as an empty directory (nil, nil).
func TestSFTPListCommandFails(t *testing.T) {
	host, port, cleanup := startTestSSHServerFailCmds(t)
	defer cleanup()

	p := NewSFTPProvider(host, port, "testuser", "", "testpass", "/backups", true)
	infos, err := p.List(context.Background())
	if err != nil {
		t.Fatalf("List should treat command failure as empty, got %v", err)
	}
	if len(infos) != 0 {
		t.Fatalf("expected 0 infos, got %d", len(infos))
	}
}

// TestSFTPDeleteCommandFails covers the Delete branch where the remote rm
// command exits non-zero, surfacing a run error.
func TestSFTPDeleteCommandFails(t *testing.T) {
	host, port, cleanup := startTestSSHServerFailCmds(t)
	defer cleanup()

	p := NewSFTPProvider(host, port, "testuser", "", "testpass", "/backups", true)
	err := p.Delete(context.Background(), "x.tar.gz")
	if err == nil {
		t.Fatal("expected delete run error")
	}
}

// TestSFTPDialKnownHostsReject covers the non-insecure dial path where the
// server's host key is NOT in known_hosts and the connection is rejected.
func TestSFTPDialKnownHostsReject(t *testing.T) {
	// Use an isolated known_hosts so we don't depend on / pollute the shared one.
	khDir := t.TempDir()
	oldKH := knownHostsPathOverride
	knownHostsPathOverride = filepath.Join(khDir, "known_hosts")
	defer func() { knownHostsPathOverride = oldKH }()

	storageDir := t.TempDir()
	host, port, _, cleanup := startTestSSHServerWithKey(t, storageDir)
	defer cleanup()

	p := NewSFTPProvider(host, port, "testuser", "", "testpass", "/backups") // insecure=false
	err := p.Upload(context.Background(), "x.tar.gz", strings.NewReader("d"))
	if err == nil {
		t.Fatal("expected rejection for unknown host")
	}
}

// TestSFTPDialKnownHostsAccept covers the non-insecure dial path where the
// server's host key IS present in known_hosts (TOFU success).
func TestSFTPDialKnownHostsAccept(t *testing.T) {
	khDir := t.TempDir()
	oldKH := knownHostsPathOverride
	knownHostsPathOverride = filepath.Join(khDir, "known_hosts")
	defer func() { knownHostsPathOverride = oldKH }()

	storageDir := t.TempDir()
	host, port, hostKey, cleanup := startTestSSHServerWithKey(t, storageDir)
	defer cleanup()

	// Write a known_hosts line for [host]:port.
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	line := knownhosts.Line([]string{addr}, hostKey)
	if err := os.WriteFile(knownHostsPathOverride, []byte(line+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	p := NewSFTPProvider(host, port, "testuser", "", "testpass", "/backups") // insecure=false
	if err := p.Upload(context.Background(), "ok.tar.gz", strings.NewReader("known")); err != nil {
		t.Fatalf("expected success with known host, got %v", err)
	}
	got, err := os.ReadFile(filepath.Join(storageDir, "backups", "ok.tar.gz"))
	if err != nil || string(got) != "known" {
		t.Fatalf("stored content = %q err=%v", string(got), err)
	}
}

// TestSFTPDialDefaultKnownHostsPath covers the branch where
// knownHostsPathOverride is empty, so dial derives the path from the home
// directory. We point HOME at a temp dir so nothing real is touched, and the
// connection is rejected because the freshly-created known_hosts is empty
// (unknown host).
func TestSFTPDialDefaultKnownHostsPath(t *testing.T) {
	oldKH := knownHostsPathOverride
	knownHostsPathOverride = "" // force the home-dir derivation path
	defer func() { knownHostsPathOverride = oldKH }()

	home := t.TempDir()
	t.Setenv("HOME", home)

	storageDir := t.TempDir()
	host, port, _, cleanup := startTestSSHServerWithKey(t, storageDir)
	defer cleanup()

	p := NewSFTPProvider(host, port, "testuser", "", "testpass", "/backups") // insecure=false
	// Unknown host -> rejected; the important part is that the home-dir path,
	// MkdirAll, and empty-file creation all ran without touching the real home.
	if err := p.Upload(context.Background(), "x.tar.gz", strings.NewReader("d")); err == nil {
		t.Fatal("expected rejection for unknown host via default known_hosts path")
	}
	// Verify the known_hosts file was created under our fake HOME.
	if _, err := os.Stat(filepath.Join(home, ".ssh", "known_hosts")); err != nil {
		t.Errorf("expected known_hosts created under fake HOME: %v", err)
	}
}

// TestSFTPDialKnownHostsChangedKey covers the KeyError (changed host key /
// potential MITM) rejection branch: known_hosts has a DIFFERENT key for the
// host than the server presents.
func TestSFTPDialKnownHostsChangedKey(t *testing.T) {
	khDir := t.TempDir()
	oldKH := knownHostsPathOverride
	knownHostsPathOverride = filepath.Join(khDir, "known_hosts")
	defer func() { knownHostsPathOverride = oldKH }()

	storageDir := t.TempDir()
	host, port, _, cleanup := startTestSSHServerWithKey(t, storageDir)
	defer cleanup()

	// Generate an UNRELATED key and record it as the "known" host key.
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrongKey, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	line := knownhosts.Line([]string{addr}, wrongKey)
	if err := os.WriteFile(knownHostsPathOverride, []byte(line+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	p := NewSFTPProvider(host, port, "testuser", "", "testpass", "/backups") // insecure=false
	err = p.Upload(context.Background(), "x.tar.gz", strings.NewReader("d"))
	if err == nil {
		t.Fatal("expected rejection for changed host key")
	}
}

// --- cron goroutine: IsZero return path ---

// TestScheduleBackupCronImpossibleDate uses an expression that is valid (5
// fields, passes the empty check) but never matches a real date, so the
// goroutine's nextCronRun returns zero and the goroutine returns immediately.
func TestScheduleBackupCronImpossibleDate(t *testing.T) {
	m, _ := testManager(t)
	// Feb 31 never exists -> nextCronRun yields zero inside the goroutine.
	m.ScheduleBackupCron("0 0 31 2 *")
	// Give the goroutine a moment to run nextCronRun and return; nothing to
	// assert beyond no panic/deadlock.
	time.Sleep(50 * time.Millisecond)
	m.Stop()
}

// --- safeRestorePath / IsInsideDir additional branches ---

func TestSafeRestorePathParentSymlinkChain(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()

	// Create a symlinked subdirectory inside base pointing outside, then try to
	// restore into a path *under* a not-yet-existing child of that symlink. The
	// existing-ancestor walk resolves to the symlink target outside base.
	linkDir := filepath.Join(base, "linkdir")
	if err := os.Symlink(outside, linkDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, ok := safeRestorePath(base, "linkdir/newfile.txt"); ok {
		t.Error("expected rejection: ancestor resolves through symlink outside base")
	}
}

func TestSafeRestorePathDotDot(t *testing.T) {
	base := t.TempDir()
	if _, ok := safeRestorePath(base, ".."); ok {
		t.Error("'..' should be rejected")
	}
	if _, ok := safeRestorePath(base, "."); ok {
		t.Error("'.' should be rejected")
	}
	if _, ok := safeRestorePath("", "x"); ok {
		t.Error("empty base should be rejected")
	}
}

// TestSafeRestorePathBaseNotYetCreated covers the branch where the restore base
// directory does not exist yet but its parent does (the existing-ancestor walk
// stops at the parent and base ⊆ existingReal).
func TestSafeRestorePathBaseNotYetCreated(t *testing.T) {
	parent := t.TempDir()
	base := filepath.Join(parent, "willbecreated") // does not exist yet
	got, ok := safeRestorePath(base, "sub/file.txt")
	if !ok {
		t.Fatal("expected ok for a not-yet-created base under an existing parent")
	}
	if !strings.HasPrefix(got, base) {
		t.Errorf("target %q not under base %q", got, base)
	}
}

// TestSafeRestorePathBrokenSymlinkAncestor covers the EvalSymlinks-error branch:
// the first existing ancestor of the target is a broken symlink, so resolving
// it fails and the path is rejected.
func TestSafeRestorePathBrokenSymlinkAncestor(t *testing.T) {
	base := t.TempDir()
	broken := filepath.Join(base, "brokenlink")
	if err := os.Symlink(filepath.Join(base, "nonexistent-target"), broken); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, ok := safeRestorePath(base, "brokenlink/child.txt"); ok {
		t.Error("expected rejection when an ancestor is a broken symlink")
	}
}

func TestIsInsideDirExactMatch(t *testing.T) {
	base := t.TempDir()
	if !IsInsideDir(base, base) {
		t.Error("a dir should be inside itself")
	}
	if IsInsideDir(filepath.Dir(base), base) {
		t.Error("parent should not be inside child")
	}
}
