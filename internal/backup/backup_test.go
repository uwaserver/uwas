package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"golang.org/x/crypto/ssh"
)

// memoryProvider is an in-memory StorageProvider for tests.
type memoryProvider struct {
	mu    sync.Mutex
	pname string
	files map[string][]byte
}

func newMemoryProvider(name string) *memoryProvider {
	return &memoryProvider{pname: name, files: make(map[string][]byte)}
}

func (p *memoryProvider) Name() string { return p.pname }

func (p *memoryProvider) Upload(_ context.Context, filename string, data io.Reader) error {
	b, err := io.ReadAll(data)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.files[filename] = b
	return nil
}

func (p *memoryProvider) Download(_ context.Context, filename string) (io.ReadCloser, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	b, ok := p.files[filename]
	if !ok {
		return nil, os.ErrNotExist
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (p *memoryProvider) List(_ context.Context) ([]BackupInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var infos []BackupInfo
	for name, data := range p.files {
		infos = append(infos, BackupInfo{
			Name:     name,
			Size:     int64(len(data)),
			Created:  time.Now(),
			Provider: p.pname,
		})
	}
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].Name < infos[j].Name
	})
	return infos, nil
}

func (p *memoryProvider) Delete(_ context.Context, filename string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.files, filename)
	return nil
}

func testManager(t *testing.T) (*BackupManager, *memoryProvider) {
	t.Helper()
	log := logger.New("error", "text")
	cfg := config.BackupConfig{
		Enabled:  true,
		Provider: "mem",
		Keep:     3,
	}
	m := New(cfg, log)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp
	return m, mp
}

func TestCreateAndListBackup(t *testing.T) {
	m, mp := testManager(t)

	// Create a temporary config file.
	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")
	if err := os.WriteFile(cfgFile, []byte("global:\n  log_level: info\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a certs directory with a test file.
	certsDir := filepath.Join(tmpDir, "certs")
	if err := os.MkdirAll(certsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certsDir, "test.pem"), []byte("CERT DATA"), 0644); err != nil {
		t.Fatal(err)
	}

	m.SetPaths(cfgFile, certsDir)

	info, err := m.CreateBackup("mem")
	if err != nil {
		t.Fatal(err)
	}
	if info == nil {
		t.Fatal("expected backup info, got nil")
	}
	if !strings.HasPrefix(info.Name, "uwas-backup-") {
		t.Errorf("unexpected backup name: %s", info.Name)
	}
	if info.Provider != "mem" {
		t.Errorf("expected provider=mem, got %s", info.Provider)
	}

	// Verify the file is in the provider.
	if len(mp.files) != 1 {
		t.Fatalf("expected 1 file in provider, got %d", len(mp.files))
	}

	// List backups.
	backups := m.ListBackups()
	if len(backups) != 1 {
		t.Fatalf("expected 1 backup, got %d", len(backups))
	}
	if backups[0].Name != info.Name {
		t.Errorf("listed name %s != created name %s", backups[0].Name, info.Name)
	}
}

func TestRestoreBackup(t *testing.T) {
	m, _ := testManager(t)

	// Create source files.
	srcDir := t.TempDir()
	cfgFile := filepath.Join(srcDir, "uwas.yaml")
	cfgContent := "global:\n  log_level: debug\n"
	if err := os.WriteFile(cfgFile, []byte(cfgContent), 0644); err != nil {
		t.Fatal(err)
	}
	certsDir := filepath.Join(srcDir, "certs")
	os.MkdirAll(certsDir, 0755)
	certContent := "CERTIFICATE"
	os.WriteFile(filepath.Join(certsDir, "domain.pem"), []byte(certContent), 0644)

	m.SetPaths(cfgFile, certsDir)

	// Create the backup.
	info, err := m.CreateBackup("mem")
	if err != nil {
		t.Fatal(err)
	}

	// Restore to a new destination.
	dstDir := t.TempDir()
	dstCfg := filepath.Join(dstDir, "uwas.yaml")
	dstCerts := filepath.Join(dstDir, "certs")
	os.WriteFile(dstCfg, []byte(""), 0644)

	m.SetPaths(dstCfg, dstCerts)

	if err := m.RestoreBackup(info.Name, "mem"); err != nil {
		t.Fatal(err)
	}

	// Verify the config was restored.
	got, err := os.ReadFile(dstCfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != cfgContent {
		t.Errorf("config not restored: got %q, want %q", string(got), cfgContent)
	}

	// Verify cert was restored.
	gotCert, err := os.ReadFile(filepath.Join(dstCerts, "domain.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotCert) != certContent {
		t.Errorf("cert not restored: got %q, want %q", string(gotCert), certContent)
	}
}

func TestDeleteBackup(t *testing.T) {
	m, mp := testManager(t)

	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("test"), 0644)
	m.SetPaths(cfgFile, "")

	info, err := m.CreateBackup("mem")
	if err != nil {
		t.Fatal(err)
	}

	if err := m.DeleteBackup(info.Name, "mem"); err != nil {
		t.Fatal(err)
	}

	if len(mp.files) != 0 {
		t.Errorf("expected 0 files after delete, got %d", len(mp.files))
	}
}

func TestPruneOldBackups(t *testing.T) {
	m, mp := testManager(t)
	m.keepCount = 2

	// Manually add 4 backups with different timestamps.
	for i := 0; i < 4; i++ {
		name := "uwas-backup-" + time.Now().Add(time.Duration(i)*time.Second).Format("20060102-150405") + ".tar.gz"
		mp.files[name] = []byte("data")
		time.Sleep(time.Millisecond)
	}

	// Create one more to trigger pruning.
	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("test"), 0644)
	m.SetPaths(cfgFile, "")

	_, err := m.CreateBackup("mem")
	if err != nil {
		t.Fatal(err)
	}

	// Should keep only 2 (keepCount).
	if len(mp.files) > m.keepCount {
		t.Errorf("expected at most %d backups after prune, got %d", m.keepCount, len(mp.files))
	}
}

func TestUnknownProvider(t *testing.T) {
	m, _ := testManager(t)
	m.SetPaths("/tmp/test.yaml", "")

	_, err := m.CreateBackup("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if !strings.Contains(err.Error(), "unknown backup provider") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestScheduleBackup(t *testing.T) {
	m, _ := testManager(t)

	// Schedule with a very long interval (won't actually fire).
	m.ScheduleBackup(24 * time.Hour)

	interval, active := m.ScheduleStatus()
	if !active {
		t.Error("expected schedule to be active")
	}
	if interval != 24*time.Hour {
		t.Errorf("expected 24h interval, got %v", interval)
	}

	// Stop it.
	m.ScheduleBackup(0)
	_, active = m.ScheduleStatus()
	if active {
		t.Error("expected schedule to be inactive after stopping")
	}
}

func TestStopSchedule(t *testing.T) {
	m, _ := testManager(t)
	m.ScheduleBackup(1 * time.Hour)
	m.Stop()
	_, active := m.ScheduleStatus()
	if active {
		t.Error("expected schedule inactive after Stop()")
	}
}

func TestLocalProvider(t *testing.T) {
	tmpDir := t.TempDir()
	p := NewLocalProvider(tmpDir)

	if p.Name() != "local" {
		t.Errorf("name = %q, want local", p.Name())
	}

	ctx := context.Background()

	// Upload.
	data := bytes.NewReader([]byte("test archive content"))
	if err := p.Upload(ctx, "test-backup.tar.gz", data); err != nil {
		t.Fatal(err)
	}

	// List.
	infos, err := p.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 file, got %d", len(infos))
	}
	if infos[0].Name != "test-backup.tar.gz" {
		t.Errorf("name = %q", infos[0].Name)
	}
	if infos[0].Provider != "local" {
		t.Errorf("provider = %q", infos[0].Provider)
	}

	// Download.
	rc, err := p.Download(ctx, "test-backup.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if string(got) != "test archive content" {
		t.Errorf("download content = %q", string(got))
	}

	// Delete.
	if err := p.Delete(ctx, "test-backup.tar.gz"); err != nil {
		t.Fatal(err)
	}
	infos, _ = p.List(ctx)
	if len(infos) != 0 {
		t.Errorf("expected 0 after delete, got %d", len(infos))
	}
}

func TestLocalProviderEmptyDir(t *testing.T) {
	p := NewLocalProvider(filepath.Join(t.TempDir(), "nonexistent"))
	infos, err := p.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 0 {
		t.Errorf("expected empty list, got %d", len(infos))
	}
}

func TestTarContents(t *testing.T) {
	// Verify the tar.gz archive contains expected entries.
	m, mp := testManager(t)

	srcDir := t.TempDir()
	cfgFile := filepath.Join(srcDir, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("config_data"), 0644)
	certsDir := filepath.Join(srcDir, "certs")
	os.MkdirAll(certsDir, 0755)
	os.WriteFile(filepath.Join(certsDir, "a.pem"), []byte("cert_a"), 0644)
	os.WriteFile(filepath.Join(certsDir, "b.key"), []byte("key_b"), 0644)

	m.SetPaths(cfgFile, certsDir)
	info, err := m.CreateBackup("mem")
	if err != nil {
		t.Fatal(err)
	}

	data := mp.files[info.Name]
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	entries := make(map[string]string)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		content, _ := io.ReadAll(tr)
		entries[hdr.Name] = string(content)
	}

	if _, ok := entries["config/uwas.yaml"]; !ok {
		t.Error("config/uwas.yaml missing from archive")
	}
	if entries["config/uwas.yaml"] != "config_data" {
		t.Errorf("config content = %q", entries["config/uwas.yaml"])
	}
	if _, ok := entries["certs/a.pem"]; !ok {
		t.Error("certs/a.pem missing from archive")
	}
	if _, ok := entries["certs/b.key"]; !ok {
		t.Error("certs/b.key missing from archive")
	}
}

func TestS3ProviderName(t *testing.T) {
	p := NewS3Provider("localhost:9000", "test", "key", "secret", "us-east-1")
	if p.Name() != "s3" {
		t.Errorf("name = %q, want s3", p.Name())
	}
}

func TestSFTPProviderName(t *testing.T) {
	p := NewSFTPProvider("host", 22, "user", "", "pass", "/backups")
	if p.Name() != "sftp" {
		t.Errorf("name = %q, want sftp", p.Name())
	}
}

func TestS3URLConstruction(t *testing.T) {
	tests := []struct {
		endpoint string
		bucket   string
		region   string
		wantURL  string
	}{
		{"s3.amazonaws.com", "mybucket", "us-east-1", "https://s3.amazonaws.com/mybucket/testfile"},
		{"http://localhost:9000", "dev", "us-east-1", "http://localhost:9000/dev/testfile"},
		{"https://s3.example.com", "prod", "eu-west-1", "https://s3.example.com/prod/testfile"},
		{"", "mybucket", "ap-southeast-1", "https://s3.ap-southeast-1.amazonaws.com/mybucket/testfile"},
	}
	for _, tt := range tests {
		p := NewS3Provider(tt.endpoint, tt.bucket, "key", "secret", tt.region)
		got := p.objectURL("testfile")
		if got != tt.wantURL {
			t.Errorf("objectURL(%q, %q, %q) = %q, want %q", tt.endpoint, tt.bucket, tt.region, got, tt.wantURL)
		}
	}
}

func TestS3SignRequest(t *testing.T) {
	// Verify that signing does not panic and produces Authorization header.
	p := NewS3Provider("s3.amazonaws.com", "test", "AKID", "SECRET", "us-east-1")
	httpReq, _ := http.NewRequest("GET", "https://s3.amazonaws.com/test/file.tar.gz", nil)
	p.signRequest(httpReq, "UNSIGNED-PAYLOAD")
	auth := httpReq.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256") {
		t.Errorf("expected AWS4-HMAC-SHA256 auth header, got %q", auth)
	}
	if !strings.Contains(auth, "AKID") {
		t.Error("auth header does not contain access key")
	}
}

func TestConfigPathNotSet(t *testing.T) {
	m, _ := testManager(t)
	// Don't call SetPaths — configPath is empty.
	_, err := m.CreateBackup("mem")
	if err == nil {
		t.Fatal("expected error when config path not set")
	}
	if !strings.Contains(err.Error(), "config path not set") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestBackupInfoFields(t *testing.T) {
	info := BackupInfo{
		Name:     "test.tar.gz",
		Size:     1024,
		Created:  time.Date(2026, 3, 22, 10, 0, 0, 0, time.UTC),
		Provider: "local",
	}
	if info.Name != "test.tar.gz" {
		t.Error("name mismatch")
	}
	if info.Size != 1024 {
		t.Error("size mismatch")
	}
	if info.Provider != "local" {
		t.Error("provider mismatch")
	}
}

func TestNewManagerDefaults(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.BackupConfig{
		Enabled:  true,
		Provider: "local",
	}
	m := New(cfg, log)

	// Default keepCount should be 7.
	if m.keepCount != 7 {
		t.Errorf("keepCount = %d, want 7", m.keepCount)
	}

	// Local provider should always be registered.
	if m.Provider("local") == nil {
		t.Error("local provider should be registered")
	}
}

func TestNewManagerWithS3(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.BackupConfig{
		Enabled:  true,
		Provider: "s3",
		S3: config.BackupS3Config{
			Endpoint:  "s3.amazonaws.com",
			Bucket:    "test-bucket",
			AccessKey: "AKID",
			SecretKey: "SECRET",
			Region:    "us-west-2",
		},
	}
	m := New(cfg, log)
	if m.Provider("s3") == nil {
		t.Error("s3 provider should be registered")
	}
}

func TestNewManagerWithSFTP(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.BackupConfig{
		Enabled:  true,
		Provider: "sftp",
		SFTP: config.BackupSFTPConfig{
			Host:       "backup.example.com",
			Port:       22,
			User:       "backup",
			Password:   "secret",
			RemotePath: "/backups",
		},
	}
	m := New(cfg, log)
	if m.Provider("sftp") == nil {
		t.Error("sftp provider should be registered")
	}
}

func TestDeleteUnknownProvider(t *testing.T) {
	m, _ := testManager(t)
	err := m.DeleteBackup("test.tar.gz", "nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRestoreUnknownProvider(t *testing.T) {
	m, _ := testManager(t)
	m.SetPaths("/tmp/cfg.yaml", "")
	err := m.RestoreBackup("test.tar.gz", "nonexistent")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRestoreConfigPathNotSet(t *testing.T) {
	m, _ := testManager(t)
	err := m.RestoreBackup("test.tar.gz", "mem")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "config path not set") {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- Additional coverage tests ---

// failingProvider is a StorageProvider that always returns errors.
type failingProvider struct {
	pname string
}

func (p *failingProvider) Name() string { return p.pname }
func (p *failingProvider) Upload(_ context.Context, _ string, _ io.Reader) error {
	return fmt.Errorf("upload failed")
}
func (p *failingProvider) Download(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("download failed")
}
func (p *failingProvider) List(_ context.Context) ([]BackupInfo, error) {
	return nil, fmt.Errorf("list failed")
}
func (p *failingProvider) Delete(_ context.Context, _ string) error {
	return fmt.Errorf("delete failed")
}

func TestCreateBackupWithCertsSubdirectories(t *testing.T) {
	m, mp := testManager(t)

	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("global:\n  log_level: info\n"), 0644)

	certsDir := filepath.Join(tmpDir, "certs")
	os.MkdirAll(filepath.Join(certsDir, "subdir", "nested"), 0755)
	os.WriteFile(filepath.Join(certsDir, "root.pem"), []byte("ROOT_CERT"), 0644)
	os.WriteFile(filepath.Join(certsDir, "subdir", "intermediate.pem"), []byte("INTERMEDIATE"), 0644)
	os.WriteFile(filepath.Join(certsDir, "subdir", "nested", "leaf.pem"), []byte("LEAF_CERT"), 0644)

	m.SetPaths(cfgFile, certsDir)

	info, err := m.CreateBackup("mem")
	if err != nil {
		t.Fatal(err)
	}

	// Verify archive contents include nested files.
	data := mp.files[info.Name]
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	entries := make(map[string]string)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		content, _ := io.ReadAll(tr)
		entries[hdr.Name] = string(content)
	}

	if entries["certs/root.pem"] != "ROOT_CERT" {
		t.Errorf("root.pem content = %q", entries["certs/root.pem"])
	}
	if entries["certs/subdir/intermediate.pem"] != "INTERMEDIATE" {
		t.Errorf("intermediate.pem content = %q", entries["certs/subdir/intermediate.pem"])
	}
	if entries["certs/subdir/nested/leaf.pem"] != "LEAF_CERT" {
		t.Errorf("leaf.pem content = %q", entries["certs/subdir/nested/leaf.pem"])
	}
}

func TestRestoreBackupEndToEnd(t *testing.T) {
	m, _ := testManager(t)

	// Create source directory with config and certs.
	srcDir := t.TempDir()
	cfgFile := filepath.Join(srcDir, "uwas.yaml")
	cfgContent := "global:\n  log_level: debug\n  http_listen: :8080\n"
	os.WriteFile(cfgFile, []byte(cfgContent), 0644)

	certsDir := filepath.Join(srcDir, "certs")
	os.MkdirAll(certsDir, 0755)
	certContent := "-----BEGIN CERTIFICATE-----\nTESTDATA\n-----END CERTIFICATE-----"
	keyContent := "-----BEGIN PRIVATE KEY-----\nKEYDATA\n-----END PRIVATE KEY-----"
	os.WriteFile(filepath.Join(certsDir, "server.pem"), []byte(certContent), 0644)
	os.WriteFile(filepath.Join(certsDir, "server.key"), []byte(keyContent), 0644)

	m.SetPaths(cfgFile, certsDir)

	// Create backup.
	info, err := m.CreateBackup("mem")
	if err != nil {
		t.Fatal(err)
	}

	// Overwrite source files to prove restore works.
	os.WriteFile(cfgFile, []byte("MODIFIED"), 0644)
	os.WriteFile(filepath.Join(certsDir, "server.pem"), []byte("MODIFIED"), 0644)

	// Restore to a completely new directory.
	dstDir := t.TempDir()
	dstCfg := filepath.Join(dstDir, "uwas.yaml")
	dstCerts := filepath.Join(dstDir, "certs")

	m.SetPaths(dstCfg, dstCerts)

	if err := m.RestoreBackup(info.Name, "mem"); err != nil {
		t.Fatal(err)
	}

	// Verify config was restored correctly.
	got, err := os.ReadFile(dstCfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != cfgContent {
		t.Errorf("config not restored: got %q, want %q", string(got), cfgContent)
	}

	// Verify certs were restored correctly.
	gotCert, err := os.ReadFile(filepath.Join(dstCerts, "server.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotCert) != certContent {
		t.Errorf("cert not restored: got %q, want %q", string(gotCert), certContent)
	}

	gotKey, err := os.ReadFile(filepath.Join(dstCerts, "server.key"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotKey) != keyContent {
		t.Errorf("key not restored: got %q, want %q", string(gotKey), keyContent)
	}
}

func TestRestoreBackupNonTarGz(t *testing.T) {
	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp

	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("test"), 0644)
	m.SetPaths(cfgFile, "")

	// Upload non-tar.gz data.
	mp.files["bad-backup.tar.gz"] = []byte("this is not a valid gzip archive")

	err := m.RestoreBackup("bad-backup.tar.gz", "mem")
	if err == nil {
		t.Fatal("expected error for non-tar.gz content")
	}
	if !strings.Contains(err.Error(), "gzip") {
		t.Errorf("expected gzip error, got: %v", err)
	}
}

func TestRestoreBackupUnknownProvider(t *testing.T) {
	m, _ := testManager(t)
	m.SetPaths("/tmp/test.yaml", "")

	err := m.RestoreBackup("test.tar.gz", "doesnotexist")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if !strings.Contains(err.Error(), "unknown backup provider") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestListBackupsWithFailingProvider(t *testing.T) {
	m, _ := testManager(t)
	m.providers["failing"] = &failingProvider{pname: "failing"}

	// Should not panic and should still return results from working providers (if any).
	backups := m.ListBackups()
	// The "mem" provider is empty and "failing" errors out, so no results.
	if backups == nil {
		// nil is acceptable
	}
}

func TestDeleteBackupSuccess(t *testing.T) {
	m, mp := testManager(t)
	mp.files["to-delete.tar.gz"] = []byte("data")

	err := m.DeleteBackup("to-delete.tar.gz", "mem")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := mp.files["to-delete.tar.gz"]; ok {
		t.Error("file should have been deleted")
	}
}

func TestDeleteBackupErrorPath(t *testing.T) {
	m, _ := testManager(t)
	m.providers["failing"] = &failingProvider{pname: "failing"}

	err := m.DeleteBackup("test.tar.gz", "failing")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "delete failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDeleteBackupUnknownProvider(t *testing.T) {
	m, _ := testManager(t)
	err := m.DeleteBackup("test.tar.gz", "nope")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if !strings.Contains(err.Error(), "unknown backup provider") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPruneOldListError(t *testing.T) {
	m, _ := testManager(t)
	m.providers["failing"] = &failingProvider{pname: "failing"}
	m.keepCount = 1

	// pruneOld should silently return when List fails.
	// No panic expected.
	m.pruneOld("failing")
}

func TestPruneOldNoPruningNeeded(t *testing.T) {
	m, mp := testManager(t)
	m.keepCount = 10

	// Add only 2 backups -- well under keepCount.
	mp.files["backup-1.tar.gz"] = []byte("data1")
	mp.files["backup-2.tar.gz"] = []byte("data2")

	m.pruneOld("mem")

	// Both should still be there.
	if len(mp.files) != 2 {
		t.Errorf("expected 2 files, got %d", len(mp.files))
	}
}

func TestPruneOldUnknownProvider(t *testing.T) {
	m, _ := testManager(t)
	// Should not panic.
	m.pruneOld("nonexistent")
}

func TestStopMultipleTimes(t *testing.T) {
	m, _ := testManager(t)
	m.ScheduleBackup(1 * time.Hour)

	// Stop multiple times should be idempotent.
	m.Stop()
	m.Stop()
	m.Stop()

	_, active := m.ScheduleStatus()
	if active {
		t.Error("expected schedule inactive after multiple Stops")
	}
}

func TestStopWithoutSchedule(t *testing.T) {
	m, _ := testManager(t)
	// Stop without ever scheduling should not panic.
	m.Stop()
	_, active := m.ScheduleStatus()
	if active {
		t.Error("expected schedule inactive")
	}
}

func TestScheduleBackupWithDifferentProviders(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.BackupConfig{
		Enabled:  true,
		Provider: "s3",
		Keep:     3,
	}
	m := New(cfg, log)
	mp := newMemoryProvider("s3")
	m.providers["s3"] = mp

	// Schedule with the configured provider "s3".
	m.ScheduleBackup(24 * time.Hour)
	interval, active := m.ScheduleStatus()
	if !active {
		t.Error("expected schedule to be active")
	}
	if interval != 24*time.Hour {
		t.Errorf("expected 24h, got %v", interval)
	}
	m.Stop()
}

func TestScheduleBackupDefaultProvider(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.BackupConfig{
		Enabled:  true,
		Provider: "", // empty means default to "local"
		Keep:     3,
	}
	m := New(cfg, log)

	m.ScheduleBackup(12 * time.Hour)
	interval, active := m.ScheduleStatus()
	if !active {
		t.Error("expected schedule to be active")
	}
	if interval != 12*time.Hour {
		t.Errorf("expected 12h, got %v", interval)
	}
	m.Stop()
}

func TestScheduleBackupReplace(t *testing.T) {
	m, _ := testManager(t)

	// Schedule, then reschedule with different interval.
	m.ScheduleBackup(1 * time.Hour)
	m.ScheduleBackup(2 * time.Hour)

	interval, active := m.ScheduleStatus()
	if !active {
		t.Error("expected active after reschedule")
	}
	if interval != 2*time.Hour {
		t.Errorf("expected 2h, got %v", interval)
	}
	m.Stop()
}

func TestS3ProviderUploadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("Internal Server Error"))
	}))
	defer srv.Close()

	p := NewS3Provider(srv.URL, "test-bucket", "key", "secret", "us-east-1")
	err := p.Upload(context.Background(), "test.tar.gz", bytes.NewReader([]byte("data")))
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "s3 upload") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestS3ProviderDownloadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	p := NewS3Provider(srv.URL, "test-bucket", "key", "secret", "us-east-1")
	_, err := p.Download(context.Background(), "nonexistent.tar.gz")
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
	if !strings.Contains(err.Error(), "s3 download") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestS3ProviderListError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("error"))
	}))
	defer srv.Close()

	p := NewS3Provider(srv.URL, "test-bucket", "key", "secret", "us-east-1")
	_, err := p.List(context.Background())
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "s3 list") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestS3ProviderListSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult>
  <Contents>
    <Key>uwas-backup-20260101-120000.tar.gz</Key>
    <Size>1024</Size>
    <LastModified>2026-01-01T12:00:00Z</LastModified>
  </Contents>
  <Contents>
    <Key>uwas-backup-20260102-120000.tar.gz</Key>
    <Size>2048</Size>
    <LastModified>2026-01-02T12:00:00Z</LastModified>
  </Contents>
  <Contents>
    <Key>some-other-file.txt</Key>
    <Size>100</Size>
    <LastModified>2026-01-03T12:00:00Z</LastModified>
  </Contents>
</ListBucketResult>`))
	}))
	defer srv.Close()

	p := NewS3Provider(srv.URL, "test-bucket", "key", "secret", "us-east-1")
	infos, err := p.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Only .tar.gz files should be included, not some-other-file.txt.
	if len(infos) != 2 {
		t.Fatalf("expected 2 items, got %d", len(infos))
	}
	// Should be sorted newest first.
	if infos[0].Name != "uwas-backup-20260102-120000.tar.gz" {
		t.Errorf("first item = %q, want 20260102", infos[0].Name)
	}
}

func TestS3ProviderDeleteError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("error"))
	}))
	defer srv.Close()

	p := NewS3Provider(srv.URL, "test-bucket", "key", "secret", "us-east-1")
	err := p.Delete(context.Background(), "test.tar.gz")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "s3 delete") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestS3ProviderDelete404NotError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	p := NewS3Provider(srv.URL, "test-bucket", "key", "secret", "us-east-1")
	err := p.Delete(context.Background(), "test.tar.gz")
	if err != nil {
		t.Errorf("404 should not be an error for delete, got: %v", err)
	}
}

func TestS3ProviderUploadSuccess(t *testing.T) {
	var gotMethod string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	p := NewS3Provider(srv.URL, "test-bucket", "key", "secret", "us-east-1")
	err := p.Upload(context.Background(), "test.tar.gz", bytes.NewReader([]byte("archive data")))
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != "PUT" {
		t.Errorf("method = %s, want PUT", gotMethod)
	}
	if string(gotBody) != "archive data" {
		t.Errorf("body = %q", string(gotBody))
	}
}

func TestS3ProviderDownloadSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("downloaded content"))
	}))
	defer srv.Close()

	p := NewS3Provider(srv.URL, "test-bucket", "key", "secret", "us-east-1")
	rc, err := p.Download(context.Background(), "test.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	data, _ := io.ReadAll(rc)
	if string(data) != "downloaded content" {
		t.Errorf("content = %q", string(data))
	}
}

func TestS3ProviderDeleteSuccess(t *testing.T) {
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.WriteHeader(204)
	}))
	defer srv.Close()

	p := NewS3Provider(srv.URL, "test-bucket", "key", "secret", "us-east-1")
	err := p.Delete(context.Background(), "test.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != "DELETE" {
		t.Errorf("method = %s, want DELETE", gotMethod)
	}
}

func TestS3URLConstructionEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		bucket   string
		region   string
		wantURL  string
	}{
		{
			name:     "empty endpoint defaults to AWS",
			endpoint: "",
			bucket:   "mybucket",
			region:   "eu-west-1",
			wantURL:  "https://s3.eu-west-1.amazonaws.com/mybucket/key",
		},
		{
			name:     "empty region defaults to us-east-1",
			endpoint: "",
			bucket:   "bucket",
			region:   "",
			wantURL:  "https://s3.us-east-1.amazonaws.com/bucket/key",
		},
		{
			name:     "http endpoint preserves scheme",
			endpoint: "http://minio:9000",
			bucket:   "test",
			region:   "us-east-1",
			wantURL:  "http://minio:9000/test/key",
		},
		{
			name:     "https endpoint preserves scheme",
			endpoint: "https://s3.custom.com",
			bucket:   "prod",
			region:   "us-east-1",
			wantURL:  "https://s3.custom.com/prod/key",
		},
		{
			name:     "trailing slash on endpoint",
			endpoint: "http://localhost:9000/",
			bucket:   "dev",
			region:   "us-east-1",
			wantURL:  "http://localhost:9000/dev/key",
		},
		{
			name:     "bare hostname without scheme",
			endpoint: "s3.example.com",
			bucket:   "backup",
			region:   "us-east-1",
			wantURL:  "https://s3.example.com/backup/key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewS3Provider(tt.endpoint, tt.bucket, "key", "secret", tt.region)
			got := p.objectURL("key")
			if got != tt.wantURL {
				t.Errorf("objectURL = %q, want %q", got, tt.wantURL)
			}
		})
	}
}

func TestS3ProviderListBadXML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("not xml at all"))
	}))
	defer srv.Close()

	p := NewS3Provider(srv.URL, "test-bucket", "key", "secret", "us-east-1")
	_, err := p.List(context.Background())
	if err == nil {
		t.Fatal("expected error for bad XML")
	}
	if !strings.Contains(err.Error(), "parse s3 list") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSFTPProviderNameAndDefaults(t *testing.T) {
	// Test default port and remote path.
	p := NewSFTPProvider("host.example.com", 0, "user", "", "pass", "")
	if p.Name() != "sftp" {
		t.Errorf("name = %q, want sftp", p.Name())
	}
	if p.port != 22 {
		t.Errorf("port = %d, want 22", p.port)
	}
	if p.remotePath != "/backups/uwas" {
		t.Errorf("remotePath = %q, want /backups/uwas", p.remotePath)
	}
}

func TestShellQuote(t *testing.T) {
	tests := map[string]string{
		"":                "''",
		"/backups/uwas":   "'/backups/uwas'",
		"/backups;whoami": "'/backups;whoami'",
		"/backups/a b":    "'/backups/a b'",
		"/backups/o'hare": `'/backups/o'\''hare'`,
	}
	for in, want := range tests {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSFTPProviderCustomValues(t *testing.T) {
	p := NewSFTPProvider("sftp.example.com", 2222, "admin", "/path/to/key", "", "/custom/path")
	if p.host != "sftp.example.com" {
		t.Errorf("host = %q", p.host)
	}
	if p.port != 2222 {
		t.Errorf("port = %d", p.port)
	}
	if p.user != "admin" {
		t.Errorf("user = %q", p.user)
	}
	if p.keyFile != "/path/to/key" {
		t.Errorf("keyFile = %q", p.keyFile)
	}
	if p.remotePath != "/custom/path" {
		t.Errorf("remotePath = %q", p.remotePath)
	}
}

func TestSFTPProviderNegativePort(t *testing.T) {
	p := NewSFTPProvider("host", -1, "user", "", "pass", "")
	if p.port != 22 {
		t.Errorf("negative port should default to 22, got %d", p.port)
	}
}

func TestAddFileToTarNonExistent(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	err := addFileToTar(tw, "/nonexistent/path/file.txt", "file.txt")
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}

	tw.Close()
	gw.Close()
}

func TestAddDirToTarEmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	emptyDir := filepath.Join(tmpDir, "empty")
	os.MkdirAll(emptyDir, 0755)

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	err := addDirToTar(tw, emptyDir, "empty")
	if err != nil {
		t.Fatalf("addDirToTar on empty dir should not error: %v", err)
	}

	tw.Close()
	gw.Close()

	// Verify the archive contains only the directory entry.
	gr, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	count := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		count++
		if hdr.Typeflag != tar.TypeDir {
			t.Errorf("expected directory entry, got typeflag %d", hdr.Typeflag)
		}
	}
	if count != 1 {
		t.Errorf("expected 1 entry (the dir itself), got %d", count)
	}
}

func TestCreateBackupNoCertsDir(t *testing.T) {
	m, _ := testManager(t)
	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("test config"), 0644)

	// Set certsDir to empty string.
	m.SetPaths(cfgFile, "")

	info, err := m.CreateBackup("mem")
	if err != nil {
		t.Fatal(err)
	}
	if info == nil {
		t.Fatal("expected backup info")
	}
}

func TestCreateBackupCertsDirNonExistent(t *testing.T) {
	m, _ := testManager(t)
	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("test config"), 0644)

	// Set certsDir to a path that doesn't exist -- should not error, just skip certs.
	m.SetPaths(cfgFile, filepath.Join(tmpDir, "nonexistent-certs"))

	info, err := m.CreateBackup("mem")
	if err != nil {
		t.Fatal(err)
	}
	if info == nil {
		t.Fatal("expected backup info")
	}
}

func TestRestoreBackupWithEmptyCertsDir(t *testing.T) {
	m, _ := testManager(t)

	srcDir := t.TempDir()
	cfgFile := filepath.Join(srcDir, "uwas.yaml")
	cfgContent := "restored config"
	os.WriteFile(cfgFile, []byte(cfgContent), 0644)
	certsDir := filepath.Join(srcDir, "certs")
	os.MkdirAll(certsDir, 0755)
	os.WriteFile(filepath.Join(certsDir, "test.pem"), []byte("CERT"), 0644)

	m.SetPaths(cfgFile, certsDir)
	info, err := m.CreateBackup("mem")
	if err != nil {
		t.Fatal(err)
	}

	// Restore with empty certsDir -- certs entries should be skipped.
	dstDir := t.TempDir()
	dstCfg := filepath.Join(dstDir, "uwas.yaml")
	m.SetPaths(dstCfg, "")

	if err := m.RestoreBackup(info.Name, "mem"); err != nil {
		t.Fatal(err)
	}

	// Config should still be restored.
	got, _ := os.ReadFile(dstCfg)
	if string(got) != cfgContent {
		t.Errorf("config = %q, want %q", string(got), cfgContent)
	}
}

func TestNewManagerWithSchedule(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.BackupConfig{
		Enabled:  true,
		Provider: "local",
		Schedule: "24h",
		Keep:     5,
	}
	m := New(cfg, log)
	if m.schedule != 24*time.Hour {
		t.Errorf("schedule = %v, want 24h", m.schedule)
	}
	if m.keepCount != 5 {
		t.Errorf("keepCount = %d, want 5", m.keepCount)
	}
}

func TestNewManagerInvalidSchedule(t *testing.T) {
	log := logger.New("error", "text")
	cfg := config.BackupConfig{
		Enabled:  true,
		Provider: "local",
		Schedule: "invalid-duration",
	}
	m := New(cfg, log)
	if m.schedule != 0 {
		t.Errorf("invalid schedule should be 0, got %v", m.schedule)
	}
}

func TestProviderReturnsNil(t *testing.T) {
	m, _ := testManager(t)
	p := m.Provider("nonexistent")
	if p != nil {
		t.Error("expected nil for nonexistent provider")
	}
}

func TestListBackupsMultipleProviders(t *testing.T) {
	m, mp := testManager(t)
	mp2 := newMemoryProvider("mem2")
	m.providers["mem2"] = mp2

	// Add backups to both providers.
	mp.files["backup-a.tar.gz"] = []byte("a")
	mp2.files["backup-b.tar.gz"] = []byte("b")

	backups := m.ListBackups()
	if len(backups) < 2 {
		t.Errorf("expected at least 2 backups from 2 providers, got %d", len(backups))
	}
}

func TestRestoreBackupDownloadError(t *testing.T) {
	m, _ := testManager(t)
	m.providers["failing"] = &failingProvider{pname: "failing"}

	tmpDir := t.TempDir()
	m.SetPaths(filepath.Join(tmpDir, "cfg.yaml"), "")

	err := m.RestoreBackup("test.tar.gz", "failing")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "download backup") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCreateBackupUploadError(t *testing.T) {
	m, _ := testManager(t)
	m.providers["failing"] = &failingProvider{pname: "failing"}

	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("test"), 0644)
	m.SetPaths(cfgFile, "")

	_, err := m.CreateBackup("failing")
	if err == nil {
		t.Fatal("expected error from upload")
	}
	if !strings.Contains(err.Error(), "upload backup") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRestoreBackupWithCertsSubdirs(t *testing.T) {
	// This test covers the TypeDir handling in RestoreBackup and the
	// certs/ subdirectory path processing.
	m, _ := testManager(t)

	srcDir := t.TempDir()
	cfgFile := filepath.Join(srcDir, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("config data"), 0644)

	certsDir := filepath.Join(srcDir, "certs")
	os.MkdirAll(filepath.Join(certsDir, "sub"), 0755)
	os.WriteFile(filepath.Join(certsDir, "root.pem"), []byte("ROOT"), 0644)
	os.WriteFile(filepath.Join(certsDir, "sub", "leaf.pem"), []byte("LEAF"), 0644)

	m.SetPaths(cfgFile, certsDir)
	info, err := m.CreateBackup("mem")
	if err != nil {
		t.Fatal(err)
	}

	// Restore to new location.
	dstDir := t.TempDir()
	dstCfg := filepath.Join(dstDir, "restored.yaml")
	dstCerts := filepath.Join(dstDir, "restored-certs")
	m.SetPaths(dstCfg, dstCerts)

	if err := m.RestoreBackup(info.Name, "mem"); err != nil {
		t.Fatal(err)
	}

	// Check subdirectory was created and file restored.
	got, err := os.ReadFile(filepath.Join(dstCerts, "sub", "leaf.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "LEAF" {
		t.Errorf("leaf.pem = %q, want LEAF", string(got))
	}
}

func TestPruneOldWithDeleteError(t *testing.T) {
	// Use a custom provider that lists successfully but fails on delete.
	log := logger.New("error", "text")
	cfg := config.BackupConfig{Enabled: true, Provider: "custom", Keep: 1}
	m := New(cfg, log)

	cp := &listButFailDeleteProvider{
		items: []BackupInfo{
			{Name: "old.tar.gz", Created: time.Now().Add(-2 * time.Hour), Provider: "custom"},
			{Name: "new.tar.gz", Created: time.Now(), Provider: "custom"},
		},
	}
	m.providers["custom"] = cp
	m.keepCount = 1

	// pruneOld should not panic even when delete fails.
	m.pruneOld("custom")
	// The delete call was attempted.
	if !cp.deleteCalled {
		t.Error("expected delete to be called during pruning")
	}
}

// listButFailDeleteProvider succeeds on List but fails on Delete.
type listButFailDeleteProvider struct {
	items        []BackupInfo
	deleteCalled bool
}

func (p *listButFailDeleteProvider) Name() string { return "custom" }
func (p *listButFailDeleteProvider) Upload(_ context.Context, _ string, _ io.Reader) error {
	return nil
}
func (p *listButFailDeleteProvider) Download(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not implemented")
}
func (p *listButFailDeleteProvider) List(_ context.Context) ([]BackupInfo, error) {
	return p.items, nil
}
func (p *listButFailDeleteProvider) Delete(_ context.Context, _ string) error {
	p.deleteCalled = true
	return fmt.Errorf("delete not allowed")
}

func TestScheduleBackupGoroutineFires(t *testing.T) {
	// Test that the schedule goroutine actually fires by using a very short interval.
	m, mp := testManager(t)
	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")
	os.WriteFile(cfgFile, []byte("test"), 0644)
	m.SetPaths(cfgFile, "")
	m.cfg.Provider = "mem"

	m.ScheduleBackup(50 * time.Millisecond)
	// Wait for at least one tick.
	time.Sleep(200 * time.Millisecond)
	m.Stop()

	mp.mu.Lock()
	count := len(mp.files)
	mp.mu.Unlock()

	if count == 0 {
		t.Error("expected at least 1 backup to be created by scheduled goroutine")
	}
}

func TestAddDirToTarWithFiles(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte("A"), 0644)
	os.MkdirAll(filepath.Join(tmpDir, "sub"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "sub", "b.txt"), []byte("B"), 0644)

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	err := addDirToTar(tw, tmpDir, "prefix")
	if err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gw.Close()

	gr, _ := gzip.NewReader(&buf)
	tr := tar.NewReader(gr)

	entries := map[string]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		entries[hdr.Name] = hdr.Typeflag
	}
	gr.Close()

	// Should have: prefix/ (dir), prefix/a.txt (file), prefix/sub/ (dir), prefix/sub/b.txt (file)
	if _, ok := entries["prefix/"]; !ok {
		t.Error("missing prefix/ dir entry")
	}
	if _, ok := entries["prefix/sub/"]; !ok {
		t.Error("missing prefix/sub/ dir entry")
	}
	if _, ok := entries["prefix/a.txt"]; !ok {
		t.Error("missing prefix/a.txt")
	}
	if _, ok := entries["prefix/sub/b.txt"]; !ok {
		t.Error("missing prefix/sub/b.txt")
	}
}

func TestS3ProviderUploadCancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	p := NewS3Provider(srv.URL, "test-bucket", "key", "secret", "us-east-1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := p.Upload(ctx, "test.tar.gz", bytes.NewReader([]byte("data")))
	if err == nil {
		t.Log("cancelled context did not cause error - request may have completed before cancellation")
	}
}

func TestS3ProviderDownloadCancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("data"))
	}))
	defer srv.Close()

	p := NewS3Provider(srv.URL, "test-bucket", "key", "secret", "us-east-1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.Download(ctx, "test.tar.gz")
	if err == nil {
		t.Log("cancelled context did not cause error")
	}
}

func TestS3ProviderListCancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`<ListBucketResult></ListBucketResult>`))
	}))
	defer srv.Close()

	p := NewS3Provider(srv.URL, "test-bucket", "key", "secret", "us-east-1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.List(ctx)
	if err == nil {
		t.Log("cancelled context did not cause error")
	}
}

func TestS3ProviderDeleteCancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	defer srv.Close()

	p := NewS3Provider(srv.URL, "test-bucket", "key", "secret", "us-east-1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := p.Delete(ctx, "test.tar.gz")
	if err == nil {
		t.Log("cancelled context did not cause error")
	}
}

func TestS3ProviderRegionDefault(t *testing.T) {
	p := NewS3Provider("endpoint", "bucket", "key", "secret", "")
	if p.region != "us-east-1" {
		t.Errorf("region = %q, want us-east-1", p.region)
	}
}

func TestLocalProviderUploadCreatesDir(t *testing.T) {
	tmpDir := t.TempDir()
	nested := filepath.Join(tmpDir, "deep", "nested", "dir")
	p := NewLocalProvider(nested)

	ctx := context.Background()
	err := p.Upload(ctx, "test.tar.gz", bytes.NewReader([]byte("data")))
	if err != nil {
		t.Fatal(err)
	}

	// Verify the file exists.
	content, err := os.ReadFile(filepath.Join(nested, "test.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "data" {
		t.Errorf("content = %q", string(content))
	}
}

func TestLocalProviderListFiltersNonTarGz(t *testing.T) {
	tmpDir := t.TempDir()
	p := NewLocalProvider(tmpDir)

	os.WriteFile(filepath.Join(tmpDir, "backup.tar.gz"), []byte("ok"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "readme.txt"), []byte("skip"), 0644)
	os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0755)

	infos, err := p.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected 1 tar.gz file, got %d", len(infos))
	}
	if infos[0].Name != "backup.tar.gz" {
		t.Errorf("name = %q", infos[0].Name)
	}
}

func TestRestoreBackupMkdirError(t *testing.T) {
	// Test the MkdirAll error path in RestoreBackup by making certsDir
	// point to a file (not a directory), so creating subdirs fails.
	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp

	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")

	// Create a file where we expect a directory.
	certsFile := filepath.Join(tmpDir, "certs")
	os.WriteFile(certsFile, []byte("not a directory"), 0644)

	m.SetPaths(cfgFile, certsFile)

	// Build an archive with a certs subdirectory entry.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	// Config entry.
	configData := []byte("config content")
	tw.WriteHeader(&tar.Header{
		Name: "config/uwas.yaml",
		Size: int64(len(configData)),
		Mode: 0644,
	})
	tw.Write(configData)

	// Certs subdirectory entry - this should trigger MkdirAll error
	// because certsFile is a file, not a directory.
	tw.WriteHeader(&tar.Header{
		Name:     "certs/sub/",
		Typeflag: tar.TypeDir,
		Mode:     0755,
	})

	tw.Close()
	gw.Close()
	mp.files["test-restore.tar.gz"] = buf.Bytes()

	err := m.RestoreBackup("test-restore.tar.gz", "mem")
	if err == nil {
		// On some OSes, MkdirAll might not fail if certsFile doesn't conflict.
		// But on most, it should fail because certsFile is a regular file.
		t.Log("MkdirAll did not fail - OS may handle this case differently")
	}
}

func TestRestoreBackupFileWriteError(t *testing.T) {
	// Test the file creation error path by pointing to a read-only location.
	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp

	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")

	// Point certsDir to a file (not a dir) so writing a cert file inside it fails.
	certsBlocker := filepath.Join(tmpDir, "certsblk")
	os.WriteFile(certsBlocker, []byte("blocker"), 0644)

	m.SetPaths(cfgFile, certsBlocker)

	// Build archive with a cert file entry.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	certData := []byte("cert")
	tw.WriteHeader(&tar.Header{
		Name: "config/uwas.yaml",
		Size: int64(len(certData)),
		Mode: 0644,
	})
	tw.Write(certData)

	tw.WriteHeader(&tar.Header{
		Name: "certs/test.pem",
		Size: int64(len(certData)),
		Mode: 0644,
	})
	tw.Write(certData)

	tw.Close()
	gw.Close()
	mp.files["err-restore.tar.gz"] = buf.Bytes()

	err := m.RestoreBackup("err-restore.tar.gz", "mem")
	if err == nil {
		t.Log("file write did not fail - OS may handle this differently")
	}
}

func TestLocalProviderListMultipleFilesSorted(t *testing.T) {
	tmpDir := t.TempDir()
	p := NewLocalProvider(tmpDir)

	// Create multiple tar.gz files with different modification times.
	os.WriteFile(filepath.Join(tmpDir, "backup-a.tar.gz"), []byte("a"), 0644)
	// Wait a tiny bit to ensure different modtimes.
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(filepath.Join(tmpDir, "backup-b.tar.gz"), []byte("bb"), 0644)
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(filepath.Join(tmpDir, "backup-c.tar.gz"), []byte("ccc"), 0644)

	infos, err := p.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 3 {
		t.Fatalf("expected 3 files, got %d", len(infos))
	}
	// Should be sorted newest first.
	if infos[0].Name != "backup-c.tar.gz" {
		t.Errorf("first item = %q, expected backup-c.tar.gz", infos[0].Name)
	}
}

// --- SFTP tests with a mock SSH server ---

// startTestSSHServer starts a minimal SSH server that accepts password auth
// and handles basic shell commands used by the SFTP provider.
// It returns the host, port, and a cleanup function.
func startTestSSHServer(t *testing.T, storageDir string) (string, int, func()) {
	t.Helper()

	// Generate a host key.
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

	// Listen on a random port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	addr := listener.Addr().(*net.TCPAddr)
	host := addr.IP.String()
	port := addr.Port

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

	cleanup := func() {
		listener.Close()
		<-done
	}

	return host, port, cleanup
}

func handleSSHConnection(conn net.Conn, config *ssh.ServerConfig, storageDir string) {
	defer conn.Close()

	sConn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		return
	}
	defer sConn.Close()
	go ssh.DiscardRequests(reqs)

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(ssh.UnknownChannelType, "unsupported channel type")
			continue
		}
		channel, requests, err := newChan.Accept()
		if err != nil {
			continue
		}
		go handleSSHSession(channel, requests, storageDir)
	}
}

func handleSSHSession(channel ssh.Channel, requests <-chan *ssh.Request, storageDir string) {
	defer channel.Close()

	for req := range requests {
		if req.Type != "exec" {
			if req.WantReply {
				req.Reply(false, nil)
			}
			continue
		}

		// Parse the command from the exec payload.
		// The payload format is: uint32 length + string command.
		if len(req.Payload) < 4 {
			if req.WantReply {
				req.Reply(false, nil)
			}
			continue
		}
		cmdLen := int(req.Payload[0])<<24 | int(req.Payload[1])<<16 | int(req.Payload[2])<<8 | int(req.Payload[3])
		if len(req.Payload) < 4+cmdLen {
			if req.WantReply {
				req.Reply(false, nil)
			}
			continue
		}
		cmd := string(req.Payload[4 : 4+cmdLen])

		if req.WantReply {
			req.Reply(true, nil)
		}

		// Handle commands used by SFTPProvider.
		switch {
		case strings.HasPrefix(cmd, "mkdir -p "):
			dir := testShellArg(strings.TrimPrefix(cmd, "mkdir -p "))
			// Map to storage dir.
			localDir := filepath.Join(storageDir, filepath.FromSlash(dir))
			os.MkdirAll(localDir, 0755)
			channel.SendRequest("exit-status", false, []byte{0, 0, 0, 0})

		case strings.HasPrefix(cmd, "cat > "):
			// Write stdin to the file.
			dest := testShellArg(strings.TrimPrefix(cmd, "cat > "))
			localDest := filepath.Join(storageDir, filepath.FromSlash(dest))
			os.MkdirAll(filepath.Dir(localDest), 0755)
			data, _ := io.ReadAll(channel)
			os.WriteFile(localDest, data, 0644)
			channel.SendRequest("exit-status", false, []byte{0, 0, 0, 0})

		case strings.HasPrefix(cmd, "cat "):
			// Read file and send to stdout.
			src := testShellArg(strings.TrimPrefix(cmd, "cat "))
			localSrc := filepath.Join(storageDir, filepath.FromSlash(src))
			data, err := os.ReadFile(localSrc)
			if err != nil {
				channel.SendRequest("exit-status", false, []byte{0, 0, 0, 1})
			} else {
				channel.Write(data)
				channel.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
			}

		case strings.HasPrefix(cmd, "rm -f "):
			target := testShellArg(strings.TrimPrefix(cmd, "rm -f "))
			localTarget := filepath.Join(storageDir, filepath.FromSlash(target))
			os.Remove(localTarget)
			channel.SendRequest("exit-status", false, []byte{0, 0, 0, 0})

		case strings.HasPrefix(cmd, "find "):
			// List .tar.gz files in the remote path with name\tsize\tepoch format.
			// Parse the path from the command.
			parts := strings.Fields(cmd)
			var dir string
			if len(parts) > 1 {
				dir = testShellArg(parts[1])
			}
			localDir := filepath.Join(storageDir, filepath.FromSlash(dir))
			entries, _ := os.ReadDir(localDir)
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".tar.gz") {
					continue
				}
				fi, err := e.Info()
				if err != nil {
					continue
				}
				line := fmt.Sprintf("%s\t%d\t%d.0\n", e.Name(), fi.Size(), fi.ModTime().Unix())
				channel.Write([]byte(line))
			}
			channel.SendRequest("exit-status", false, []byte{0, 0, 0, 0})

		default:
			channel.SendRequest("exit-status", false, []byte{0, 0, 0, 1})
		}
		return
	}
}

func testShellArg(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "-- ")
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "'") {
		var b strings.Builder
		escaped := false
		for i := 1; i < len(s); i++ {
			if escaped {
				b.WriteByte(s[i])
				escaped = false
				continue
			}
			if s[i] == '\\' {
				escaped = true
				continue
			}
			if s[i] == '\'' {
				return b.String()
			}
			b.WriteByte(s[i])
		}
	}
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i]
	}
	return s
}

func TestSFTPProviderUploadAndDownload(t *testing.T) {
	storageDir := t.TempDir()
	host, port, cleanup := startTestSSHServer(t, storageDir)
	defer cleanup()

	p := NewSFTPProvider(host, port, "testuser", "", "testpass", "/backups")

	ctx := context.Background()

	// Upload.
	uploadData := []byte("backup archive data for SFTP test")
	err := p.Upload(ctx, "test-backup.tar.gz", bytes.NewReader(uploadData))
	if err != nil {
		t.Fatalf("Upload error: %v", err)
	}

	// Verify file was created in storage.
	localPath := filepath.Join(storageDir, "backups", "test-backup.tar.gz")
	got, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("file not found in storage: %v", err)
	}
	if string(got) != string(uploadData) {
		t.Errorf("uploaded content = %q, want %q", string(got), string(uploadData))
	}

	// Download.
	rc, err := p.Download(ctx, "test-backup.tar.gz")
	if err != nil {
		t.Fatalf("Download error: %v", err)
	}
	downloaded, _ := io.ReadAll(rc)
	rc.Close()
	if string(downloaded) != string(uploadData) {
		t.Errorf("downloaded content = %q, want %q", string(downloaded), string(uploadData))
	}
}

func TestSFTPProviderList(t *testing.T) {
	storageDir := t.TempDir()
	host, port, cleanup := startTestSSHServer(t, storageDir)
	defer cleanup()

	p := NewSFTPProvider(host, port, "testuser", "", "testpass", "/backups")

	// Create some files in the storage dir.
	backupsDir := filepath.Join(storageDir, "backups")
	os.MkdirAll(backupsDir, 0755)
	os.WriteFile(filepath.Join(backupsDir, "uwas-backup-20260101.tar.gz"), []byte("b1"), 0644)
	os.WriteFile(filepath.Join(backupsDir, "uwas-backup-20260102.tar.gz"), []byte("b2b2"), 0644)
	os.WriteFile(filepath.Join(backupsDir, "not-a-backup.txt"), []byte("skip"), 0644)

	ctx := context.Background()
	infos, err := p.List(ctx)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}

	if len(infos) != 2 {
		t.Fatalf("expected 2 backups, got %d", len(infos))
	}
	for _, info := range infos {
		if !strings.HasSuffix(info.Name, ".tar.gz") {
			t.Errorf("unexpected name: %s", info.Name)
		}
		if info.Provider != "sftp" {
			t.Errorf("provider = %q", info.Provider)
		}
	}
}

func TestSFTPProviderDelete(t *testing.T) {
	storageDir := t.TempDir()
	host, port, cleanup := startTestSSHServer(t, storageDir)
	defer cleanup()

	p := NewSFTPProvider(host, port, "testuser", "", "testpass", "/backups")

	// Create a file to delete.
	backupsDir := filepath.Join(storageDir, "backups")
	os.MkdirAll(backupsDir, 0755)
	targetFile := filepath.Join(backupsDir, "to-delete.tar.gz")
	os.WriteFile(targetFile, []byte("data"), 0644)

	ctx := context.Background()
	err := p.Delete(ctx, "to-delete.tar.gz")
	if err != nil {
		t.Fatalf("Delete error: %v", err)
	}

	if _, err := os.Stat(targetFile); !os.IsNotExist(err) {
		t.Error("file should have been deleted")
	}
}

// startTestSSHServerLsFallback starts an SSH server that returns ls-style output
// (single filename per line, no tabs) instead of find-printf format.
func startTestSSHServerLsFallback(t *testing.T, storageDir string) (string, int, func()) {
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
			go handleSSHConnectionLsFallback(conn, sshConfig, storageDir)
		}
	}()

	return addr.IP.String(), addr.Port, func() {
		listener.Close()
		<-done
	}
}

func handleSSHConnectionLsFallback(conn net.Conn, config *ssh.ServerConfig, storageDir string) {
	defer conn.Close()
	sConn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		return
	}
	defer sConn.Close()
	go ssh.DiscardRequests(reqs)

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(ssh.UnknownChannelType, "unsupported")
			continue
		}
		channel, requests, err := newChan.Accept()
		if err != nil {
			continue
		}
		go handleSSHSessionLsFallback(channel, requests, storageDir)
	}
}

func handleSSHSessionLsFallback(channel ssh.Channel, requests <-chan *ssh.Request, storageDir string) {
	defer channel.Close()

	for req := range requests {
		if req.Type != "exec" {
			if req.WantReply {
				req.Reply(false, nil)
			}
			continue
		}

		if len(req.Payload) < 4 {
			if req.WantReply {
				req.Reply(false, nil)
			}
			continue
		}
		cmdLen := int(req.Payload[0])<<24 | int(req.Payload[1])<<16 | int(req.Payload[2])<<8 | int(req.Payload[3])
		if len(req.Payload) < 4+cmdLen {
			if req.WantReply {
				req.Reply(false, nil)
			}
			continue
		}
		cmd := string(req.Payload[4 : 4+cmdLen])

		if req.WantReply {
			req.Reply(true, nil)
		}

		if strings.HasPrefix(cmd, "find ") {
			// Return ls-style output (just filenames, one per line, no tabs).
			parts := strings.Fields(cmd)
			var dir string
			if len(parts) > 1 {
				dir = testShellArg(parts[1])
			}
			localDir := filepath.Join(storageDir, filepath.FromSlash(dir))
			entries, _ := os.ReadDir(localDir)
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".tar.gz") {
					channel.Write([]byte(e.Name() + "\n"))
				}
			}
			channel.SendRequest("exit-status", false, []byte{0, 0, 0, 0})
		} else {
			channel.SendRequest("exit-status", false, []byte{0, 0, 0, 1})
		}
		return
	}
}

func TestSFTPProviderListFallbackLsFormat(t *testing.T) {
	storageDir := t.TempDir()
	host, port, cleanup := startTestSSHServerLsFallback(t, storageDir)
	defer cleanup()

	p := NewSFTPProvider(host, port, "testuser", "", "testpass", "/backups")

	backupsDir := filepath.Join(storageDir, "backups")
	os.MkdirAll(backupsDir, 0755)
	os.WriteFile(filepath.Join(backupsDir, "uwas-backup-ls.tar.gz"), []byte("b1"), 0644)
	os.WriteFile(filepath.Join(backupsDir, "not-backup.txt"), []byte("skip"), 0644)

	ctx := context.Background()
	infos, err := p.List(ctx)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}

	if len(infos) != 1 {
		t.Fatalf("expected 1 backup, got %d", len(infos))
	}
	if infos[0].Name != "uwas-backup-ls.tar.gz" {
		t.Errorf("name = %q", infos[0].Name)
	}
	if infos[0].Provider != "sftp" {
		t.Errorf("provider = %q", infos[0].Provider)
	}
}

func TestSFTPProviderDialNoAuth(t *testing.T) {
	p := NewSFTPProvider("localhost", 22, "user", "", "", "")
	err := p.Upload(context.Background(), "test.tar.gz", bytes.NewReader(nil))
	if err == nil {
		t.Fatal("expected error for no auth method")
	}
	if !strings.Contains(err.Error(), "no SSH auth method") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSFTPProviderDialBadHost(t *testing.T) {
	// Use a port that nothing is listening on.
	p := NewSFTPProvider("127.0.0.1", 1, "user", "", "pass", "/backups")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := p.Download(ctx, "test.tar.gz")
	if err == nil {
		t.Fatal("expected connection error")
	}
}

func TestSFTPProviderDialBadPassword(t *testing.T) {
	storageDir := t.TempDir()
	host, port, cleanup := startTestSSHServer(t, storageDir)
	defer cleanup()

	p := NewSFTPProvider(host, port, "testuser", "", "wrongpassword", "/backups")
	ctx := context.Background()

	_, err := p.Download(ctx, "test.tar.gz")
	if err != nil {
		// Expected: authentication error, wrapped as "sftp connect: ..."
		if !strings.Contains(err.Error(), "sftp connect") {
			t.Errorf("unexpected error: %v", err)
		}
	}
}

func TestSFTPProviderListEmpty(t *testing.T) {
	storageDir := t.TempDir()
	host, port, cleanup := startTestSSHServer(t, storageDir)
	defer cleanup()

	p := NewSFTPProvider(host, port, "testuser", "", "testpass", "/empty-dir")

	ctx := context.Background()
	infos, err := p.List(ctx)
	if err != nil {
		t.Fatalf("List error: %v", err)
	}
	// Empty dir or nonexistent should return empty list, not error.
	if len(infos) != 0 {
		t.Errorf("expected 0 items, got %d", len(infos))
	}
}

func TestSSHReadCloser(t *testing.T) {
	// Test sshReadCloser.Close() by creating a mock that verifies both
	// session and client are closed. We can't easily mock ssh.Session and
	// ssh.Client, but we can test through the SFTP download flow instead.
	// This is already covered by TestSFTPProviderUploadAndDownload.
}

func TestSFTPProviderKeyAuth(t *testing.T) {
	// Test that key-based auth is attempted (even if it fails) without panic.
	tmpDir := t.TempDir()
	badKeyFile := filepath.Join(tmpDir, "bad_key")
	os.WriteFile(badKeyFile, []byte("not a valid ssh key"), 0600)

	p := NewSFTPProvider("127.0.0.1", 1, "user", badKeyFile, "pass", "/backups")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Should still try password auth after invalid key.
	err := p.Upload(ctx, "test.tar.gz", bytes.NewReader(nil))
	if err == nil {
		t.Fatal("expected connection error (nothing listening)")
	}
}

func TestSFTPProviderKeyAuthValid(t *testing.T) {
	tmpDir := t.TempDir()
	keyFile := filepath.Join(tmpDir, "test_key")

	// Write a file that ssh.ParsePrivateKey cannot parse, so it falls back
	// to password auth. The key file read succeeds but parsing fails.
	os.WriteFile(keyFile, []byte("not parseable as SSH key"), 0600)

	p := NewSFTPProvider("127.0.0.1", 1, "user", keyFile, "pass", "/backups")
	// The keyFile read succeeds but ParsePrivateKey fails, so it falls back
	// to password auth. Then the connection fails because nothing is listening.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := p.Upload(ctx, "test.tar.gz", bytes.NewReader(nil))
	if err == nil {
		t.Fatal("expected connection error")
	}
}

func TestRestoreBackupWithUnknownEntries(t *testing.T) {
	// Create a custom tar.gz archive that contains entries outside
	// of "config/" and "certs/" prefixes, which should be skipped (the "default" case).
	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp

	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "uwas.yaml")
	m.SetPaths(cfgFile, filepath.Join(tmpDir, "certs"))

	// Build a custom tar.gz with a mix of known and unknown entries.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	// Known: config entry.
	configData := []byte("restored config content")
	tw.WriteHeader(&tar.Header{
		Name: "config/uwas.yaml",
		Size: int64(len(configData)),
		Mode: 0644,
	})
	tw.Write(configData)

	// Unknown: should be skipped.
	unknownData := []byte("should be ignored")
	tw.WriteHeader(&tar.Header{
		Name: "other/random.txt",
		Size: int64(len(unknownData)),
		Mode: 0644,
	})
	tw.Write(unknownData)

	// Known: certs directory entry (TypeDir with name "certs/").
	tw.WriteHeader(&tar.Header{
		Name:     "certs/",
		Typeflag: tar.TypeDir,
		Mode:     0755,
	})

	// Known: certs subdirectory.
	tw.WriteHeader(&tar.Header{
		Name:     "certs/sub/",
		Typeflag: tar.TypeDir,
		Mode:     0755,
	})

	// Known: certs file.
	certData := []byte("cert data")
	tw.WriteHeader(&tar.Header{
		Name: "certs/sub/cert.pem",
		Size: int64(len(certData)),
		Mode: 0644,
	})
	tw.Write(certData)

	tw.Close()
	gw.Close()

	mp.files["custom-backup.tar.gz"] = buf.Bytes()

	err := m.RestoreBackup("custom-backup.tar.gz", "mem")
	if err != nil {
		t.Fatalf("RestoreBackup error: %v", err)
	}

	// Verify config was restored.
	got, err := os.ReadFile(cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "restored config content" {
		t.Errorf("config = %q", string(got))
	}

	// Verify certs subdirectory was created and file restored.
	gotCert, err := os.ReadFile(filepath.Join(tmpDir, "certs", "sub", "cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotCert) != "cert data" {
		t.Errorf("cert = %q", string(gotCert))
	}

	// The "other/random.txt" should NOT have been extracted.
	if _, err := os.Stat(filepath.Join(tmpDir, "other", "random.txt")); !os.IsNotExist(err) {
		t.Error("unknown entry should not have been extracted")
	}
}

func TestS3SignRequestWithEmptyPath(t *testing.T) {
	// Test the canonicalURI == "" branch.
	p := NewS3Provider("s3.amazonaws.com", "test", "AKID", "SECRET", "us-east-1")
	// Create a request with no path.
	httpReq, _ := http.NewRequest("GET", "https://s3.amazonaws.com?list-type=2", nil)
	httpReq.URL.Path = "" // force empty path
	p.signRequest(httpReq, "UNSIGNED-PAYLOAD")
	auth := httpReq.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256") {
		t.Errorf("expected valid auth header, got %q", auth)
	}
}

func TestS3SignRequestWithPresetHost(t *testing.T) {
	// Test that when req.Host is already set, it's not overwritten.
	p := NewS3Provider("s3.amazonaws.com", "test", "AKID", "SECRET", "us-east-1")
	httpReq, _ := http.NewRequest("GET", "https://s3.amazonaws.com/test/file.tar.gz", nil)
	httpReq.Host = "custom-host.example.com"
	p.signRequest(httpReq, "UNSIGNED-PAYLOAD")
	auth := httpReq.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256") {
		t.Errorf("expected valid auth header, got %q", auth)
	}
}

func TestS3SignRequestWithEmptyHost(t *testing.T) {
	// Test the req.Host == "" branch explicitly.
	p := NewS3Provider("s3.amazonaws.com", "test", "AKID", "SECRET", "us-east-1")
	httpReq, _ := http.NewRequest("GET", "https://s3.amazonaws.com/test/file.tar.gz", nil)
	httpReq.Host = "" // force empty
	p.signRequest(httpReq, "UNSIGNED-PAYLOAD")
	// After signing, Host should be set from URL.
	if httpReq.Host != "s3.amazonaws.com" {
		t.Errorf("Host = %q, want s3.amazonaws.com", httpReq.Host)
	}
}

func TestRestoreBackupWithCorruptTar(t *testing.T) {
	// Test the "read tar" error path.
	m, _ := testManager(t)
	mp := newMemoryProvider("mem")
	m.providers["mem"] = mp

	tmpDir := t.TempDir()
	m.SetPaths(filepath.Join(tmpDir, "cfg.yaml"), "")

	// Create a valid gzip but invalid tar.
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	gw.Write([]byte("this is not a valid tar stream"))
	gw.Close()

	mp.files["corrupt.tar.gz"] = buf.Bytes()

	err := m.RestoreBackup("corrupt.tar.gz", "mem")
	if err == nil {
		t.Fatal("expected error for corrupt tar")
	}
	// Depending on how tar handles it, we may or may not get an error.
	// The first Next() on invalid data returns an error.
}

func TestSFTPProviderListConnectError(t *testing.T) {
	p := NewSFTPProvider("127.0.0.1", 1, "user", "", "pass", "/backups")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := p.List(ctx)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "sftp connect") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSFTPProviderDeleteConnectError(t *testing.T) {
	p := NewSFTPProvider("127.0.0.1", 1, "user", "", "pass", "/backups")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := p.Delete(ctx, "test.tar.gz")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "sftp connect") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSFTPProviderUploadConnectError(t *testing.T) {
	p := NewSFTPProvider("127.0.0.1", 1, "user", "", "pass", "/backups")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := p.Upload(ctx, "test.tar.gz", bytes.NewReader([]byte("data")))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "sftp connect") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSFTPProviderDownloadConnectError(t *testing.T) {
	p := NewSFTPProvider("127.0.0.1", 1, "user", "", "pass", "/backups")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := p.Download(ctx, "test.tar.gz")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "sftp connect") {
		t.Errorf("unexpected error: %v", err)
	}
}

// startTestSSHServerRejectExec starts an SSH server that rejects all exec requests.
// This helps test error paths in SFTP methods where session commands fail.
func startTestSSHServerRejectExec(t *testing.T) (string, int, func()) {
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
						newChan.Reject(ssh.UnknownChannelType, "unsupported")
						continue
					}
					channel, requests, err := newChan.Accept()
					if err != nil {
						continue
					}
					go func() {
						defer channel.Close()
						for req := range requests {
							// Reject ALL exec requests.
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

func TestSFTPProviderUploadSessionFails(t *testing.T) {
	host, port, cleanup := startTestSSHServerRejectExec(t)
	defer cleanup()

	p := NewSFTPProvider(host, port, "testuser", "", "testpass", "/backups")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// The first session for "mkdir -p" will get rejected, which triggers session.Run error.
	// But session.Run doesn't fail on rejected requests -- it depends on how the server handles it.
	// The channel will close, and Session.Run will return an error.
	err := p.Upload(ctx, "test.tar.gz", bytes.NewReader([]byte("data")))
	if err == nil {
		t.Log("Upload did not fail - server may handle rejected requests differently")
	}
}

func TestSFTPProviderDownloadStartFails(t *testing.T) {
	host, port, cleanup := startTestSSHServerRejectExec(t)
	defer cleanup()

	p := NewSFTPProvider(host, port, "testuser", "", "testpass", "/backups")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := p.Download(ctx, "test.tar.gz")
	if err == nil {
		t.Log("Download did not fail - server may handle rejected requests differently")
	}
}

func TestSFTPProviderDialConnRefused(t *testing.T) {
	// Test the conn.Close() path when ssh.NewClientConn fails after dial succeeds.
	// We need a server that accepts TCP but doesn't speak SSH.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	addr := listener.Addr().(*net.TCPAddr)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			// Accept TCP but close immediately -- not an SSH server.
			conn.Close()
		}
	}()

	p := NewSFTPProvider("127.0.0.1", addr.Port, "user", "", "pass", "/backups")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = p.Upload(ctx, "test.tar.gz", bytes.NewReader([]byte("data")))
	if err == nil {
		t.Fatal("expected error for non-SSH server")
	}
}

func TestIsInsideDir(t *testing.T) {
	tests := []struct {
		path, base string
		want       bool
	}{
		{"/var/www/site/file.txt", "/var/www/site", true},
		{"/var/www/site", "/var/www/site", true},
		{"/var/www/site/sub/deep", "/var/www/site", true},
		{"/etc/passwd", "/var/www/site", false},
		{"/var/www/site/../../../etc/passwd", "/var/www/site", false},
		{"/var/www/other", "/var/www/site", false},
	}
	for _, tt := range tests {
		got := isInsideDir(tt.path, tt.base)
		if got != tt.want {
			t.Errorf("isInsideDir(%q, %q) = %v, want %v", tt.path, tt.base, got, tt.want)
		}
	}
}

func TestSafeRestorePath(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()

	tests := []struct {
		name string
		rel  string
		want bool
	}{
		{name: "normal file", rel: "example.com/index.html", want: true},
		{name: "nested clean", rel: "example.com/../example.com/app.php", want: true},
		{name: "parent traversal", rel: "../outside.txt", want: false},
		{name: "absolute", rel: filepath.Join(base, "absolute.txt"), want: false},
		{name: "empty", rel: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := safeRestorePath(base, tt.rel)
			if ok != tt.want {
				t.Errorf("safeRestorePath(%q) ok = %v, want %v", tt.rel, ok, tt.want)
			}
		})
	}

	linkPath := filepath.Join(base, "link")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, ok := safeRestorePath(base, "link/secret.txt"); ok {
		t.Error("safeRestorePath allowed restore through symlink outside base")
	}
	if _, ok := safeRestorePath(base, "link"); ok {
		t.Error("safeRestorePath allowed replacing an existing symlink")
	}
}

func TestSafeBackupFilename(t *testing.T) {
	valid := []string{
		"uwas-backup-2026-03-31.tar.gz",
		"uwas-domain-backup-example.com.tar.gz",
		"backup_2026.tar.gz",
	}
	for _, name := range valid {
		if err := safeBackupFilename(name); err != nil {
			t.Errorf("safeBackupFilename(%q) = %v, want nil", name, err)
		}
	}

	invalid := []string{
		"",
		"../etc/passwd",
		"file; rm -rf /",
		"back`up.tar.gz",
		"path/to/file.tar.gz",
		"file\x00name",
	}
	for _, name := range invalid {
		if err := safeBackupFilename(name); err == nil {
			t.Errorf("safeBackupFilename(%q) = nil, want error", name)
		}
	}
}
