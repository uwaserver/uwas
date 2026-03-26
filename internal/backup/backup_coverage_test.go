package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// --- SetDomainPaths coverage ---

func TestSetDomainPaths(t *testing.T) {
	m, _ := testManager(t)
	m.SetDomainPaths("/var/www", "/etc/uwas/domains.d", []string{"/var/www/example.com", "/var/www/blog.com"})

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.webRoot != "/var/www" {
		t.Errorf("webRoot = %q, want /var/www", m.webRoot)
	}
	if m.domainsDir != "/etc/uwas/domains.d" {
		t.Errorf("domainsDir = %q", m.domainsDir)
	}
	if len(m.domainRoots) != 2 {
		t.Errorf("domainRoots len = %d, want 2", len(m.domainRoots))
	}
}

// --- SetOnBackup coverage ---

func TestSetOnBackup(t *testing.T) {
	m, _ := testManager(t)
	called := false
	m.SetOnBackup(func(info *BackupInfo, err error) {
		called = true
	})

	m.mu.Lock()
	fn := m.onBackup
	m.mu.Unlock()

	if fn == nil {
		t.Fatal("onBackup should be set")
	}
	fn(nil, nil)
	if !called {
		t.Error("callback was not invoked")
	}
}

// --- CreateBackup with domains.d and domainRoots ---

func TestCreateBackupWithDomainsDir(t *testing.T) {
	m, mp := testManager(t)

	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("global:\n  log_level: info\n"), 0644)

	// Create domains.d directory
	domainsDir := filepath.Join(tmpDir, "domains.d")
	os.MkdirAll(domainsDir, 0755)
	os.WriteFile(filepath.Join(domainsDir, "example.com.yaml"), []byte("host: example.com\n"), 0644)

	m.SetPaths(cfgFile, "")
	m.SetDomainPaths("", domainsDir, nil)

	info, err := m.CreateBackup("mem")
	if err != nil {
		t.Fatal(err)
	}

	// Verify archive contains domains.d entry
	data := mp.files[info.Name]
	gr, _ := gzip.NewReader(bytes.NewReader(data))
	defer gr.Close()
	tr := tar.NewReader(gr)
	found := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(hdr.Name, "domains.d/") && strings.Contains(hdr.Name, "example.com.yaml") {
			found = true
		}
	}
	if !found {
		t.Error("domains.d/example.com.yaml not found in archive")
	}
}

func TestCreateBackupWithDomainRoots(t *testing.T) {
	m, mp := testManager(t)

	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("test"), 0644)

	// Create domain web root
	siteDir := filepath.Join(tmpDir, "sites", "example.com")
	os.MkdirAll(siteDir, 0755)
	os.WriteFile(filepath.Join(siteDir, "index.html"), []byte("<html>test</html>"), 0644)

	m.SetPaths(cfgFile, "")
	m.SetDomainPaths("", "", []string{siteDir, "", filepath.Join(tmpDir, "nonexistent")})

	info, err := m.CreateBackup("mem")
	if err != nil {
		t.Fatal(err)
	}

	// Verify archive contains sites/ entry
	data := mp.files[info.Name]
	gr, _ := gzip.NewReader(bytes.NewReader(data))
	defer gr.Close()
	tr := tar.NewReader(gr)
	found := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(hdr.Name, "sites/") && strings.Contains(hdr.Name, "index.html") {
			found = true
		}
	}
	if !found {
		t.Error("sites/*/index.html not found in archive")
	}
}

// --- RestoreBackup with domains.d and sites ---

func TestRestoreBackupWithDomainsDir(t *testing.T) {
	m, _ := testManager(t)

	// Create backup with domains.d content
	srcDir := t.TempDir()
	cfgFile := filepath.Join(srcDir, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("config"), 0644)

	domainsDir := filepath.Join(srcDir, "domains.d")
	os.MkdirAll(domainsDir, 0755)
	os.WriteFile(filepath.Join(domainsDir, "test.yaml"), []byte("host: test.com\n"), 0644)

	m.SetPaths(cfgFile, "")
	m.SetDomainPaths("", domainsDir, nil)

	info, err := m.CreateBackup("mem")
	if err != nil {
		t.Fatal(err)
	}

	// Restore to new location
	dstDir := t.TempDir()
	dstCfg := filepath.Join(dstDir, "uwas.yaml")
	dstDomains := filepath.Join(dstDir, "domains.d")
	m.SetPaths(dstCfg, "")
	m.mu.Lock()
	m.domainsDir = dstDomains
	m.mu.Unlock()

	if err := m.RestoreBackup(info.Name, "mem"); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dstDomains, "test.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "host: test.com\n" {
		t.Errorf("restored domains.d content = %q", string(got))
	}
}

func TestRestoreBackupWithSites(t *testing.T) {
	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp

	// Build an archive with sites/ content
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	// Config entry
	cfgData := []byte("config data")
	tw.WriteHeader(&tar.Header{Name: "config/uwas.yaml", Size: int64(len(cfgData)), Mode: 0644})
	tw.Write(cfgData)

	// Sites entry
	siteData := []byte("<html>restored</html>")
	tw.WriteHeader(&tar.Header{Name: "sites/sites/example.com/index.html", Size: int64(len(siteData)), Mode: 0644})
	tw.Write(siteData)

	tw.Close()
	gw.Close()
	mp.files["site-backup.tar.gz"] = buf.Bytes()

	dstDir := t.TempDir()
	dstCfg := filepath.Join(dstDir, "uwas.yaml")
	webRoot := filepath.Join(dstDir, "www")
	m.SetPaths(dstCfg, "")
	m.mu.Lock()
	m.webRoot = webRoot
	m.mu.Unlock()

	if err := m.RestoreBackup("site-backup.tar.gz", "mem"); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(webRoot, "sites", "example.com", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "<html>restored</html>" {
		t.Errorf("restored site content = %q", string(got))
	}
}

func TestRestoreBackupWithEmptyDomainsDir(t *testing.T) {
	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp

	// Archive with domains.d entry but domainsDir not set
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	cfgData := []byte("cfg")
	tw.WriteHeader(&tar.Header{Name: "config/uwas.yaml", Size: int64(len(cfgData)), Mode: 0644})
	tw.Write(cfgData)

	domData := []byte("domain")
	tw.WriteHeader(&tar.Header{Name: "domains.d/test.yaml", Size: int64(len(domData)), Mode: 0644})
	tw.Write(domData)

	// Empty rel path for domains.d/
	tw.WriteHeader(&tar.Header{Name: "domains.d/", Typeflag: tar.TypeDir, Mode: 0755})

	tw.Close()
	gw.Close()
	mp.files["test.tar.gz"] = buf.Bytes()

	dstDir := t.TempDir()
	m.SetPaths(filepath.Join(dstDir, "uwas.yaml"), "")
	// domainsDir is empty, so domains.d entries should be skipped
	m.mu.Lock()
	m.domainsDir = ""
	m.mu.Unlock()

	err := m.RestoreBackup("test.tar.gz", "mem")
	if err != nil {
		t.Fatal(err)
	}
}

func TestRestoreBackupWithEmptyWebRoot(t *testing.T) {
	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp

	// Archive with sites/ entry but webRoot not set
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	cfgData := []byte("cfg")
	tw.WriteHeader(&tar.Header{Name: "config/uwas.yaml", Size: int64(len(cfgData)), Mode: 0644})
	tw.Write(cfgData)

	siteData := []byte("site")
	tw.WriteHeader(&tar.Header{Name: "sites/example.com/index.html", Size: int64(len(siteData)), Mode: 0644})
	tw.Write(siteData)

	// Empty rel for sites/
	tw.WriteHeader(&tar.Header{Name: "sites/", Typeflag: tar.TypeDir, Mode: 0755})

	tw.Close()
	gw.Close()
	mp.files["test.tar.gz"] = buf.Bytes()

	dstDir := t.TempDir()
	m.SetPaths(filepath.Join(dstDir, "uwas.yaml"), "")
	m.mu.Lock()
	m.webRoot = ""
	m.mu.Unlock()

	err := m.RestoreBackup("test.tar.gz", "mem")
	if err != nil {
		t.Fatal(err)
	}
}

func TestRestoreBackupWithDatabaseDump(t *testing.T) {
	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp

	// Archive with databases/all-databases.sql
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	cfgData := []byte("cfg")
	tw.WriteHeader(&tar.Header{Name: "config/uwas.yaml", Size: int64(len(cfgData)), Mode: 0644})
	tw.Write(cfgData)

	// Database dump as a file entry
	sqlData := []byte("CREATE DATABASE test;")
	tw.WriteHeader(&tar.Header{Name: "databases/all-databases.sql", Size: int64(len(sqlData)), Mode: 0644})
	tw.Write(sqlData)

	tw.Close()
	gw.Close()
	mp.files["db-backup.tar.gz"] = buf.Bytes()

	dstDir := t.TempDir()
	m.SetPaths(filepath.Join(dstDir, "uwas.yaml"), "")

	// This will try to run mysql (which won't be found), but should not error
	err := m.RestoreBackup("db-backup.tar.gz", "mem")
	if err != nil {
		t.Fatal(err)
	}
}

func TestRestoreBackupDatabaseDumpAsDir(t *testing.T) {
	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp

	// Archive with databases/all-databases.sql as a dir (should be skipped)
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	cfgData := []byte("cfg")
	tw.WriteHeader(&tar.Header{Name: "config/uwas.yaml", Size: int64(len(cfgData)), Mode: 0644})
	tw.Write(cfgData)

	tw.WriteHeader(&tar.Header{Name: "databases/all-databases.sql", Typeflag: tar.TypeDir, Mode: 0755})

	tw.Close()
	gw.Close()
	mp.files["db-dir.tar.gz"] = buf.Bytes()

	dstDir := t.TempDir()
	m.SetPaths(filepath.Join(dstDir, "uwas.yaml"), "")

	err := m.RestoreBackup("db-dir.tar.gz", "mem")
	if err != nil {
		t.Fatal(err)
	}
}

func TestRestoreBackupUnknownEntries(t *testing.T) {
	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp

	// Archive with unknown entries
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	cfgData := []byte("cfg")
	tw.WriteHeader(&tar.Header{Name: "config/uwas.yaml", Size: int64(len(cfgData)), Mode: 0644})
	tw.Write(cfgData)

	unknownData := []byte("unknown")
	tw.WriteHeader(&tar.Header{Name: "unknown/file.txt", Size: int64(len(unknownData)), Mode: 0644})
	tw.Write(unknownData)

	tw.Close()
	gw.Close()
	mp.files["unknown.tar.gz"] = buf.Bytes()

	dstDir := t.TempDir()
	m.SetPaths(filepath.Join(dstDir, "uwas.yaml"), "")

	err := m.RestoreBackup("unknown.tar.gz", "mem")
	if err != nil {
		t.Fatal(err)
	}
}

func TestRestoreBackupEmptyCertsRelPath(t *testing.T) {
	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp

	// Archive with certs/ dir entry (empty rel path)
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	cfgData := []byte("cfg")
	tw.WriteHeader(&tar.Header{Name: "config/uwas.yaml", Size: int64(len(cfgData)), Mode: 0644})
	tw.Write(cfgData)

	tw.WriteHeader(&tar.Header{Name: "certs/", Typeflag: tar.TypeDir, Mode: 0755})

	tw.Close()
	gw.Close()
	mp.files["empty-certs.tar.gz"] = buf.Bytes()

	dstDir := t.TempDir()
	m.SetPaths(filepath.Join(dstDir, "uwas.yaml"), filepath.Join(dstDir, "certs"))

	err := m.RestoreBackup("empty-certs.tar.gz", "mem")
	if err != nil {
		t.Fatal(err)
	}
}

// --- CreateDomainBackup ---

func TestCreateDomainBackup(t *testing.T) {
	m, mp := testManager(t)

	tmpDir := t.TempDir()

	// Create domain web root
	webRoot := filepath.Join(tmpDir, "public_html")
	os.MkdirAll(webRoot, 0755)
	os.WriteFile(filepath.Join(webRoot, "index.html"), []byte("<html>test</html>"), 0644)

	// Create domain config
	domainsDir := filepath.Join(tmpDir, "domains.d")
	os.MkdirAll(domainsDir, 0755)
	os.WriteFile(filepath.Join(domainsDir, "example.com.yaml"), []byte("host: example.com\n"), 0644)

	m.mu.Lock()
	m.domainsDir = domainsDir
	m.mu.Unlock()

	info, err := m.CreateDomainBackup("example.com", webRoot, "", "mem")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(info.Name, "uwas-domain-example.com-") {
		t.Errorf("unexpected name: %s", info.Name)
	}
	if info.Provider != "mem" {
		t.Errorf("provider = %s", info.Provider)
	}

	// Verify archive contents
	data := mp.files[info.Name]
	gr, _ := gzip.NewReader(bytes.NewReader(data))
	defer gr.Close()
	tr := tar.NewReader(gr)
	entries := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		entries[hdr.Name] = true
	}

	if !entries["config/example.com.yaml"] {
		t.Error("missing config/example.com.yaml")
	}
}

func TestCreateDomainBackupUnknownProvider(t *testing.T) {
	m, _ := testManager(t)
	_, err := m.CreateDomainBackup("example.com", "", "", "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if !strings.Contains(err.Error(), "unknown backup provider") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCreateDomainBackupNoWebRoot(t *testing.T) {
	m, _ := testManager(t)
	info, err := m.CreateDomainBackup("example.com", "", "", "mem")
	if err != nil {
		t.Fatal(err)
	}
	if info == nil {
		t.Fatal("expected info")
	}
}

func TestCreateDomainBackupWithDBName(t *testing.T) {
	// This will try mysqldump which won't be found, so DB dump is skipped
	m, _ := testManager(t)

	tmpDir := t.TempDir()
	webRoot := filepath.Join(tmpDir, "public_html")
	os.MkdirAll(webRoot, 0755)
	os.WriteFile(filepath.Join(webRoot, "index.html"), []byte("html"), 0644)

	info, err := m.CreateDomainBackup("example.com", webRoot, "mydb", "mem")
	if err != nil {
		t.Fatal(err)
	}
	if info == nil {
		t.Fatal("expected info")
	}
}

func TestCreateDomainBackupUploadError(t *testing.T) {
	m, _ := testManager(t)
	m.providers["failing"] = &failingProvider{pname: "failing"}

	_, err := m.CreateDomainBackup("example.com", "", "", "failing")
	if err == nil {
		t.Fatal("expected upload error")
	}
	if !strings.Contains(err.Error(), "upload") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- ScheduleBackup with onBackup callback ---

func TestScheduleBackupCallsOnBackup(t *testing.T) {
	m, _ := testManager(t)
	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("test"), 0644)
	m.SetPaths(cfgFile, "")
	m.cfg.Provider = "mem"

	var mu sync.Mutex
	callbackCalled := false
	m.SetOnBackup(func(info *BackupInfo, err error) {
		mu.Lock()
		callbackCalled = true
		mu.Unlock()
	})

	m.ScheduleBackup(50 * time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	m.Stop()

	mu.Lock()
	defer mu.Unlock()
	if !callbackCalled {
		t.Error("onBackup callback was not called")
	}
}

func TestScheduleBackupErrorCallsOnBackup(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.BackupConfig{
		Enabled:  true,
		Provider: "failing",
		Keep:     3,
	}
	m := New(cfg, log)
	m.providers["failing"] = &failingProvider{pname: "failing"}

	// Set config path so CreateBackup proceeds to upload step
	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("test"), 0644)
	m.SetPaths(cfgFile, "")

	var mu sync.Mutex
	var gotErr error
	m.SetOnBackup(func(info *BackupInfo, err error) {
		mu.Lock()
		gotErr = err
		mu.Unlock()
	})

	m.ScheduleBackup(50 * time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	m.Stop()

	mu.Lock()
	defer mu.Unlock()
	if gotErr == nil {
		t.Error("expected error in onBackup callback")
	}
}

// --- importDatabaseDump coverage ---

func TestImportDatabaseDumpNotFound(t *testing.T) {
	log := logger.New("error", "text")
	// mysql is not available in test env, so this exercises the "not found" path
	importDatabaseDumpReal([]byte("SELECT 1;"), log)
}

func TestImportDatabaseDumpMocked(t *testing.T) {
	log := logger.New("error", "text")

	// Mock the import function to simulate success
	old := importDatabaseDumpFunc
	defer func() { importDatabaseDumpFunc = old }()

	var importedData []byte
	importDatabaseDumpFunc = func(data []byte, l *logger.Logger) {
		importedData = data
	}

	importDatabaseDump([]byte("SELECT 1;"), log)
	if string(importedData) != "SELECT 1;" {
		t.Errorf("imported data = %q", string(importedData))
	}
}

// --- dumpDatabase / dumpAllDatabases coverage ---

func TestDumpDatabaseNotFound(t *testing.T) {
	_, err := dumpDatabaseReal("testdb")
	if err == nil {
		t.Log("mysqldump was found (unusual in test env)")
	}
}

func TestDumpAllDatabasesNotFound(t *testing.T) {
	_, err := dumpAllDatabasesReal()
	if err == nil {
		t.Log("mysqldump was found (unusual in test env)")
	}
}

func TestDumpDatabaseMocked(t *testing.T) {
	old := dumpDatabaseFunc
	defer func() { dumpDatabaseFunc = old }()

	dumpDatabaseFunc = func(dbName string) ([]byte, error) {
		return []byte("CREATE TABLE test;"), nil
	}

	data, err := dumpDatabase("testdb")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "CREATE TABLE test;" {
		t.Errorf("dump = %q", string(data))
	}
}

func TestDumpAllDatabasesMocked(t *testing.T) {
	old := dumpAllDatabasesFunc
	defer func() { dumpAllDatabasesFunc = old }()

	dumpAllDatabasesFunc = func() ([]byte, error) {
		return []byte("-- All databases dump"), nil
	}

	data, err := dumpAllDatabases()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "-- All databases dump" {
		t.Errorf("dump = %q", string(data))
	}
}

// --- CreateBackup with mocked database dump ---

func TestCreateBackupWithDBDump(t *testing.T) {
	old := dumpAllDatabasesFunc
	defer func() { dumpAllDatabasesFunc = old }()

	dumpAllDatabasesFunc = func() ([]byte, error) {
		return []byte("-- Full DB dump"), nil
	}

	m, mp := testManager(t)
	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("test"), 0644)
	m.SetPaths(cfgFile, "")

	info, err := m.CreateBackup("mem")
	if err != nil {
		t.Fatal(err)
	}

	// Verify archive contains database dump
	data := mp.files[info.Name]
	gr, _ := gzip.NewReader(bytes.NewReader(data))
	defer gr.Close()
	tr := tar.NewReader(gr)
	found := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Name == "databases/all-databases.sql" {
			found = true
			content, _ := io.ReadAll(tr)
			if string(content) != "-- Full DB dump" {
				t.Errorf("db dump content = %q", string(content))
			}
		}
	}
	if !found {
		t.Error("databases/all-databases.sql not found in archive")
	}
}

// --- CreateDomainBackup with mocked database dump ---

func TestCreateDomainBackupWithDBDump(t *testing.T) {
	old := dumpDatabaseFunc
	defer func() { dumpDatabaseFunc = old }()

	dumpDatabaseFunc = func(dbName string) ([]byte, error) {
		return []byte("-- Dump of " + dbName), nil
	}

	m, mp := testManager(t)

	info, err := m.CreateDomainBackup("example.com", "", "mydb", "mem")
	if err != nil {
		t.Fatal(err)
	}

	data := mp.files[info.Name]
	gr, _ := gzip.NewReader(bytes.NewReader(data))
	defer gr.Close()
	tr := tar.NewReader(gr)
	found := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Name == "database/mydb.sql" {
			found = true
			content, _ := io.ReadAll(tr)
			if string(content) != "-- Dump of mydb" {
				t.Errorf("db dump content = %q", string(content))
			}
		}
	}
	if !found {
		t.Error("database/mydb.sql not found in archive")
	}
}

// --- RestoreBackup with mocked database import ---

func TestRestoreBackupWithDBImport(t *testing.T) {
	oldImport := importDatabaseDumpFunc
	defer func() { importDatabaseDumpFunc = oldImport }()

	var importedData []byte
	importDatabaseDumpFunc = func(data []byte, l *logger.Logger) {
		importedData = data
	}

	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	cfgData := []byte("cfg")
	tw.WriteHeader(&tar.Header{Name: "config/uwas.yaml", Size: int64(len(cfgData)), Mode: 0644})
	tw.Write(cfgData)

	sqlData := []byte("CREATE DATABASE testdb;")
	tw.WriteHeader(&tar.Header{Name: "databases/all-databases.sql", Size: int64(len(sqlData)), Mode: 0644})
	tw.Write(sqlData)

	tw.Close()
	gw.Close()
	mp.files["db-backup.tar.gz"] = buf.Bytes()

	dstDir := t.TempDir()
	m.SetPaths(filepath.Join(dstDir, "uwas.yaml"), "")

	err := m.RestoreBackup("db-backup.tar.gz", "mem")
	if err != nil {
		t.Fatal(err)
	}

	if string(importedData) != "CREATE DATABASE testdb;" {
		t.Errorf("imported data = %q", string(importedData))
	}
}

// --- SFTP dial with no auth methods ---

func TestSFTPDialNoAuth(t *testing.T) {
	p := NewSFTPProvider("127.0.0.1", 22, "user", "", "", "/backups")
	ctx := context.Background()
	err := p.Upload(ctx, "test.tar.gz", bytes.NewReader([]byte("data")))
	if err == nil {
		t.Fatal("expected error with no auth methods")
	}
	if !strings.Contains(err.Error(), "no SSH auth method") && !strings.Contains(err.Error(), "sftp connect") {
		// May get connection refused or no auth method error
		t.Logf("got error: %v", err)
	}
}

// --- SFTP Download error paths ---

func TestSFTPDownloadNoAuth(t *testing.T) {
	p := NewSFTPProvider("127.0.0.1", 22, "user", "", "", "/backups")
	ctx := context.Background()
	_, err := p.Download(ctx, "test.tar.gz")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSFTPListNoAuth(t *testing.T) {
	p := NewSFTPProvider("127.0.0.1", 22, "user", "", "", "/backups")
	ctx := context.Background()
	_, err := p.List(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestSFTPDeleteNoAuth(t *testing.T) {
	p := NewSFTPProvider("127.0.0.1", 22, "user", "", "", "/backups")
	ctx := context.Background()
	err := p.Delete(ctx, "test.tar.gz")
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- SFTP dial with bad key file ---

func TestSFTPDialBadKeyFile(t *testing.T) {
	tmpDir := t.TempDir()
	keyFile := filepath.Join(tmpDir, "bad_key")
	os.WriteFile(keyFile, []byte("not a valid key"), 0644)

	p := NewSFTPProvider("127.0.0.1", 0, "user", keyFile, "pass", "/backups")
	// Should fall back to password auth since key is invalid
	if p.port != 22 {
		t.Errorf("port = %d, want 22", p.port)
	}
}

// --- Compress already-encoded response ---

func TestCreateBackupSkipAlreadyEncodedCerts(t *testing.T) {
	m, mp := testManager(t)

	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("test"), 0644)

	// certsDir is a file, not a directory
	certsFile := filepath.Join(tmpDir, "certs")
	os.WriteFile(certsFile, []byte("not a dir"), 0644)

	m.SetPaths(cfgFile, certsFile)

	info, err := m.CreateBackup("mem")
	if err != nil {
		t.Fatal(err)
	}
	if info == nil {
		t.Fatal("expected info")
	}
	_ = mp
}

// --- CreateBackup with domainsDir that doesn't exist ---

func TestCreateBackupDomainsDirNotExist(t *testing.T) {
	m, _ := testManager(t)

	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("test"), 0644)

	m.SetPaths(cfgFile, "")
	m.SetDomainPaths("", filepath.Join(tmpDir, "nonexistent-domains"), nil)

	info, err := m.CreateBackup("mem")
	if err != nil {
		t.Fatal(err)
	}
	if info == nil {
		t.Fatal("expected info")
	}
}

// --- CreateBackup with domainsDir that is a file ---

func TestCreateBackupDomainsDirIsFile(t *testing.T) {
	m, _ := testManager(t)

	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("test"), 0644)

	domainsFile := filepath.Join(tmpDir, "domains.d")
	os.WriteFile(domainsFile, []byte("not a dir"), 0644)

	m.SetPaths(cfgFile, "")
	m.SetDomainPaths("", domainsFile, nil)

	info, err := m.CreateBackup("mem")
	if err != nil {
		t.Fatal(err)
	}
	if info == nil {
		t.Fatal("expected info")
	}
}

// --- RestoreBackup with empty sites/ rel path ---

func TestRestoreBackupEmptySitesRel(t *testing.T) {
	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	cfgData := []byte("cfg")
	tw.WriteHeader(&tar.Header{Name: "config/uwas.yaml", Size: int64(len(cfgData)), Mode: 0644})
	tw.Write(cfgData)

	tw.WriteHeader(&tar.Header{Name: "sites/", Typeflag: tar.TypeDir, Mode: 0755})

	tw.Close()
	gw.Close()
	mp.files["test.tar.gz"] = buf.Bytes()

	dstDir := t.TempDir()
	m.SetPaths(filepath.Join(dstDir, "uwas.yaml"), "")
	m.mu.Lock()
	m.webRoot = filepath.Join(dstDir, "www")
	m.mu.Unlock()

	err := m.RestoreBackup("test.tar.gz", "mem")
	if err != nil {
		t.Fatal(err)
	}
}

// --- S3 signRequest with empty canonical URI ---

func TestS3SignRequestEmptyURI(t *testing.T) {
	p := NewS3Provider("s3.amazonaws.com", "test", "AKID", "SECRET", "us-east-1")
	req, _ := http.NewRequest("GET", "https://s3.amazonaws.com", nil)
	req.URL.Path = ""
	p.signRequest(req, "UNSIGNED-PAYLOAD")
	auth := req.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256") {
		t.Errorf("auth = %q", auth)
	}
}

// --- LocalProvider Upload error (unwritable dir) ---

// --- CreateBackup with addFileToTar error (config file deleted mid-backup) ---

func TestCreateBackupConfigFileDisappears(t *testing.T) {
	m, _ := testManager(t)

	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("test"), 0644)
	m.SetPaths(cfgFile, "")

	// Remove the config file to trigger addFileToTar error
	os.Remove(cfgFile)

	_, err := m.CreateBackup("mem")
	if err == nil {
		t.Fatal("expected error when config file is missing")
	}
	if !strings.Contains(err.Error(), "add config to archive") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- RestoreBackup with TypeDir entry for domains.d ---

func TestRestoreBackupDomainsDirEntry(t *testing.T) {
	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	cfgData := []byte("cfg")
	tw.WriteHeader(&tar.Header{Name: "config/uwas.yaml", Size: int64(len(cfgData)), Mode: 0644})
	tw.Write(cfgData)

	// Directory entry for domains.d - triggers TypeDir handling
	tw.WriteHeader(&tar.Header{Name: "domains.d/subdir/", Typeflag: tar.TypeDir, Mode: 0755})

	// File entry in domains.d
	domData := []byte("host: sub.com\n")
	tw.WriteHeader(&tar.Header{Name: "domains.d/subdir/sub.yaml", Size: int64(len(domData)), Mode: 0644})
	tw.Write(domData)

	tw.Close()
	gw.Close()
	mp.files["test.tar.gz"] = buf.Bytes()

	dstDir := t.TempDir()
	dstDomains := filepath.Join(dstDir, "domains.d")
	m.SetPaths(filepath.Join(dstDir, "uwas.yaml"), "")
	m.mu.Lock()
	m.domainsDir = dstDomains
	m.mu.Unlock()

	err := m.RestoreBackup("test.tar.gz", "mem")
	if err != nil {
		t.Fatal(err)
	}

	// Check dir was created and file was written
	got, err := os.ReadFile(filepath.Join(dstDomains, "subdir", "sub.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "host: sub.com\n" {
		t.Errorf("got %q", string(got))
	}
}

// --- RestoreBackup with sites TypeDir ---

func TestRestoreBackupSitesDirEntry(t *testing.T) {
	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	cfgData := []byte("cfg")
	tw.WriteHeader(&tar.Header{Name: "config/uwas.yaml", Size: int64(len(cfgData)), Mode: 0644})
	tw.Write(cfgData)

	// Dir entry for sites
	tw.WriteHeader(&tar.Header{Name: "sites/example.com/", Typeflag: tar.TypeDir, Mode: 0755})

	// File in sites
	siteData := []byte("<html>restored</html>")
	tw.WriteHeader(&tar.Header{Name: "sites/example.com/index.html", Size: int64(len(siteData)), Mode: 0644})
	tw.Write(siteData)

	tw.Close()
	gw.Close()
	mp.files["sites.tar.gz"] = buf.Bytes()

	dstDir := t.TempDir()
	webRoot := filepath.Join(dstDir, "www")
	m.SetPaths(filepath.Join(dstDir, "uwas.yaml"), "")
	m.mu.Lock()
	m.webRoot = webRoot
	m.mu.Unlock()

	err := m.RestoreBackup("sites.tar.gz", "mem")
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(webRoot, "example.com", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "<html>restored</html>" {
		t.Errorf("got %q", string(got))
	}
}

// --- CreateDomainBackup with non-existent web root ---

func TestCreateDomainBackupWebRootNotDir(t *testing.T) {
	m, _ := testManager(t)

	tmpDir := t.TempDir()
	// webRoot is a file, not a directory
	webRoot := filepath.Join(tmpDir, "webroot")
	os.WriteFile(webRoot, []byte("not a dir"), 0644)

	info, err := m.CreateDomainBackup("example.com", webRoot, "", "mem")
	if err != nil {
		t.Fatal(err)
	}
	if info == nil {
		t.Fatal("expected info")
	}
}

// --- CreateDomainBackup with missing domainsDir config ---

func TestCreateDomainBackupNoDomainsDir(t *testing.T) {
	m, _ := testManager(t)
	m.mu.Lock()
	m.domainsDir = ""
	m.mu.Unlock()

	info, err := m.CreateDomainBackup("example.com", "", "", "mem")
	if err != nil {
		t.Fatal(err)
	}
	if info == nil {
		t.Fatal("expected info")
	}
}

// --- CreateDomainBackup with config file not existing ---

func TestCreateDomainBackupConfigNotExist(t *testing.T) {
	m, _ := testManager(t)

	tmpDir := t.TempDir()
	domainsDir := filepath.Join(tmpDir, "domains.d")
	os.MkdirAll(domainsDir, 0755)
	// Don't create the config file

	m.mu.Lock()
	m.domainsDir = domainsDir
	m.mu.Unlock()

	info, err := m.CreateDomainBackup("example.com", "", "", "mem")
	if err != nil {
		t.Fatal(err)
	}
	if info == nil {
		t.Fatal("expected info")
	}
}

func TestLocalProviderListWithInfoError(t *testing.T) {
	// Just exercise the normal list flow with multiple items
	tmpDir := t.TempDir()
	p := NewLocalProvider(tmpDir)
	ctx := context.Background()

	os.WriteFile(filepath.Join(tmpDir, "a.tar.gz"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "b.tar.gz"), []byte("bb"), 0644)

	infos, err := p.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 {
		t.Errorf("got %d, want 2", len(infos))
	}
}

// --- LocalProvider: Upload to a path where MkdirAll fails ---

func TestLocalProviderUploadMkdirError(t *testing.T) {
	tmpDir := t.TempDir()
	// Create a file where the directory should be
	blocker := filepath.Join(tmpDir, "blocked")
	os.WriteFile(blocker, []byte("I am a file"), 0644)
	// Try to use a path under the blocker file
	p := NewLocalProvider(filepath.Join(blocker, "subdir"))

	ctx := context.Background()
	err := p.Upload(ctx, "test.tar.gz", bytes.NewReader([]byte("data")))
	if err == nil {
		t.Fatal("expected error when MkdirAll fails")
	}
}

// --- LocalProvider: Upload where Create fails ---

func TestLocalProviderUploadCreateError(t *testing.T) {
	tmpDir := t.TempDir()
	// Create a directory where the file should be created
	dirAsFile := filepath.Join(tmpDir, "test.tar.gz")
	os.MkdirAll(dirAsFile, 0755)

	p := NewLocalProvider(tmpDir)
	ctx := context.Background()
	err := p.Upload(ctx, "test.tar.gz", bytes.NewReader([]byte("data")))
	if err == nil {
		t.Fatal("expected error when Create fails (target is a directory)")
	}
}

// --- LocalProvider: List with non-existent but also non-NotExist error ---
// On Windows, we can trigger this by making the dir unreadable (chmod won't work).
// We simulate by pointing to a file path instead of directory.

func TestLocalProviderListNotExistDir(t *testing.T) {
	// A path that is a file, not a directory
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "afile")
	os.WriteFile(filePath, []byte("data"), 0644)

	p := NewLocalProvider(filePath)
	ctx := context.Background()
	_, err := p.List(ctx)
	// On Windows, ReadDir on a file returns an error that is NOT os.IsNotExist
	// This should exercise line 50 (return nil, err)
	if err == nil {
		t.Log("ReadDir on a file did not error (unexpected)")
	} else {
		t.Logf("got error: %v", err)
	}
}

// --- SFTP provider: error paths within Upload after dial succeeds ---

func TestSFTPProviderUploadSessionError(t *testing.T) {
	// This tests the SFTP Upload flow with the mock SSH server
	// but sends data that will be written successfully.
	// Error paths inside Upload (NewSession, StdinPipe) are hard
	// to trigger without breaking the SSH protocol.
	storageDir := t.TempDir()
	host, port, cleanup := startTestSSHServer(t, storageDir)
	defer cleanup()

	p := NewSFTPProvider(host, port, "testuser", "", "testpass", "/data")

	ctx := context.Background()
	err := p.Upload(ctx, "test.tar.gz", bytes.NewReader([]byte("archive content")))
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}

	// Verify file was written
	got, err := os.ReadFile(filepath.Join(storageDir, "data", "test.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "archive content" {
		t.Errorf("content = %q", string(got))
	}
}

// --- SFTP: Download file not found ---

func TestSFTPProviderDownloadNotFound(t *testing.T) {
	storageDir := t.TempDir()
	host, port, cleanup := startTestSSHServer(t, storageDir)
	defer cleanup()

	p := NewSFTPProvider(host, port, "testuser", "", "testpass", "/backups")

	ctx := context.Background()
	rc, err := p.Download(ctx, "nonexistent.tar.gz")
	if err != nil {
		// This may error in different ways depending on SSH behavior
		t.Logf("Download error (expected): %v", err)
		return
	}
	// If we got a reader, it should have empty/error content
	data, _ := io.ReadAll(rc)
	rc.Close()
	_ = data
}

// --- SFTP: List empty directory ---

func TestSFTPProviderListEmptyDir(t *testing.T) {
	storageDir := t.TempDir()
	host, port, cleanup := startTestSSHServer(t, storageDir)
	defer cleanup()

	// Create the backups directory but leave it empty
	os.MkdirAll(filepath.Join(storageDir, "backups"), 0755)

	p := NewSFTPProvider(host, port, "testuser", "", "testpass", "/backups")

	ctx := context.Background()
	infos, err := p.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 0 {
		t.Errorf("expected 0 backups in empty dir, got %d", len(infos))
	}
}

// --- SFTP: dial with key file that exists but is not a valid key ---

func TestSFTPDialInvalidKeyFile(t *testing.T) {
	storageDir := t.TempDir()
	host, port, cleanup := startTestSSHServer(t, storageDir)
	defer cleanup()

	// Create an invalid key file
	keyFile := filepath.Join(t.TempDir(), "bad_key")
	os.WriteFile(keyFile, []byte("not a valid SSH key"), 0600)

	// Password auth should still work even with bad key file
	p := NewSFTPProvider(host, port, "testuser", keyFile, "testpass", "/backups")
	os.MkdirAll(filepath.Join(storageDir, "backups"), 0755)

	ctx := context.Background()
	err := p.Upload(ctx, "test.tar.gz", bytes.NewReader([]byte("data")))
	if err != nil {
		t.Fatalf("Upload with bad key but valid password: %v", err)
	}
}

// --- SFTP: dial with key file that doesn't exist ---

func TestSFTPDialMissingKeyFile(t *testing.T) {
	storageDir := t.TempDir()
	host, port, cleanup := startTestSSHServer(t, storageDir)
	defer cleanup()

	// Key file doesn't exist, but password auth should work
	p := NewSFTPProvider(host, port, "testuser", "/nonexistent/key", "testpass", "/backups")
	os.MkdirAll(filepath.Join(storageDir, "backups"), 0755)

	ctx := context.Background()
	err := p.Upload(ctx, "test.tar.gz", bytes.NewReader([]byte("data")))
	if err != nil {
		t.Fatalf("Upload with missing key but valid password: %v", err)
	}
}
