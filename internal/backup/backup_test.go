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
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
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
