package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// StorageProvider is the interface every backup storage backend must implement.
type StorageProvider interface {
	Name() string
	Upload(ctx context.Context, filename string, data io.Reader) error
	Download(ctx context.Context, filename string) (io.ReadCloser, error)
	List(ctx context.Context) ([]BackupInfo, error)
	Delete(ctx context.Context, filename string) error
}

// BackupInfo describes a single backup archive.
type BackupInfo struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Created  time.Time `json:"created"`
	Provider string    `json:"provider"`
}

// BackupManager orchestrates creating, restoring, listing and deleting backups
// across one or more StorageProvider implementations.
type BackupManager struct {
	logger    *logger.Logger
	providers map[string]StorageProvider
	cfg       config.BackupConfig

	mu        sync.Mutex
	cancel    context.CancelFunc
	running   bool
	schedule  time.Duration
	keepCount int

	configPath string
	certsDir   string
	webRoot    string   // base dir for all domain web roots
	domainsDir string   // domains.d/ config directory
	domainRoots []string // individual domain web roots to backup

	// onBackup is called after each backup attempt (scheduled or manual).
	onBackup func(info *BackupInfo, err error)
}

// New creates a BackupManager from the given config. It registers the
// providers that are configured (local is always available as a fallback).
func New(cfg config.BackupConfig, log *logger.Logger) *BackupManager {
	m := &BackupManager{
		logger:    log,
		providers: make(map[string]StorageProvider),
		cfg:       cfg,
		keepCount: cfg.Keep,
	}
	if m.keepCount <= 0 {
		m.keepCount = 7
	}
	if cfg.Schedule != "" {
		if d, err := time.ParseDuration(cfg.Schedule); err == nil && d > 0 {
			m.schedule = d
		}
	}

	// Always register local provider.
	localPath := cfg.Local.Path
	if localPath == "" {
		localPath = "/var/lib/uwas/backups"
	}
	m.providers["local"] = NewLocalProvider(localPath)

	// S3 provider.
	if cfg.S3.Bucket != "" {
		m.providers["s3"] = NewS3Provider(
			cfg.S3.Endpoint,
			cfg.S3.Bucket,
			cfg.S3.AccessKey,
			cfg.S3.SecretKey,
			cfg.S3.Region,
		)
	}

	// SFTP provider.
	if cfg.SFTP.Host != "" {
		m.providers["sftp"] = NewSFTPProvider(
			cfg.SFTP.Host,
			cfg.SFTP.Port,
			cfg.SFTP.User,
			cfg.SFTP.KeyFile,
			cfg.SFTP.Password,
			cfg.SFTP.RemotePath,
		)
	}

	return m
}

// SetPaths configures the config file path and certificates directory used
// when creating backups.
func (m *BackupManager) SetPaths(configPath, certsDir string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configPath = configPath
	m.certsDir = certsDir
}

// SetDomainPaths configures domain web roots and domains.d directory for backup.
func (m *BackupManager) SetDomainPaths(webRoot, domainsDir string, domainRoots []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.webRoot = webRoot
	m.domainsDir = domainsDir
	m.domainRoots = domainRoots
}

// SetOnBackup sets a callback that fires after each backup attempt.
func (m *BackupManager) SetOnBackup(fn func(info *BackupInfo, err error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onBackup = fn
}

// Provider returns the named provider, or nil.
func (m *BackupManager) Provider(name string) StorageProvider {
	return m.providers[name]
}

// CreateBackup creates a tar.gz archive containing the UWAS config file and
// the certificates directory, then uploads it via the chosen provider.
func (m *BackupManager) CreateBackup(provider string) (*BackupInfo, error) {
	m.mu.Lock()
	configPath := m.configPath
	certsDir := m.certsDir
	m.mu.Unlock()

	if configPath == "" {
		return nil, fmt.Errorf("config path not set")
	}

	p := m.providers[provider]
	if p == nil {
		return nil, fmt.Errorf("unknown backup provider %q", provider)
	}

	// Build the backup filename.
	ts := time.Now().UTC().Format("20060102-150405")
	filename := fmt.Sprintf("uwas-backup-%s.tar.gz", ts)

	// Create a temporary file for the archive.
	tmpFile, err := os.CreateTemp("", "uwas-backup-*.tar.gz")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	gw := gzip.NewWriter(tmpFile)
	tw := tar.NewWriter(gw)

	// Add the config file.
	if err := addFileToTar(tw, configPath, "config/"+filepath.Base(configPath)); err != nil {
		tw.Close()
		gw.Close()
		tmpFile.Close()
		return nil, fmt.Errorf("add config to archive: %w", err)
	}

	// Add domains.d/ config directory if it exists.
	m.mu.Lock()
	domainsDir := m.domainsDir
	domainRoots := make([]string, len(m.domainRoots))
	copy(domainRoots, m.domainRoots)
	m.mu.Unlock()

	if domainsDir != "" {
		if info, err := os.Stat(domainsDir); err == nil && info.IsDir() {
			if err := addDirToTar(tw, domainsDir, "domains.d"); err != nil {
				m.logger.Warn("backup: failed to add domains.d", "error", err)
			}
		}
	}

	// Add certificates directory if it exists.
	if certsDir != "" {
		if info, err := os.Stat(certsDir); err == nil && info.IsDir() {
			if err := addDirToTar(tw, certsDir, "certs"); err != nil {
				tw.Close()
				gw.Close()
				tmpFile.Close()
				return nil, fmt.Errorf("add certs to archive: %w", err)
			}
		}
	}

	// Add domain web content (each domain's web root).
	for _, root := range domainRoots {
		if root == "" {
			continue
		}
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			// Use the last two path components as archive name: e.g. "sites/example.com"
			dirName := filepath.Base(filepath.Dir(root)) + "/" + filepath.Base(root)
			if err := addDirToTar(tw, root, "sites/"+dirName); err != nil {
				m.logger.Warn("backup: failed to add domain root", "root", root, "error", err)
			}
		}
	}

	// MySQL/MariaDB database dump (if mysqldump available).
	if dbDump, err := dumpAllDatabases(); err == nil && len(dbDump) > 0 {
		hdr := &tar.Header{
			Name:    "databases/all-databases.sql",
			Size:    int64(len(dbDump)),
			Mode:    0644,
			ModTime: time.Now(),
		}
		if err := tw.WriteHeader(hdr); err == nil {
			tw.Write(dbDump)
		}
	}

	if err := tw.Close(); err != nil {
		gw.Close()
		tmpFile.Close()
		return nil, err
	}
	if err := gw.Close(); err != nil {
		tmpFile.Close()
		return nil, err
	}

	stat, _ := tmpFile.Stat()
	size := stat.Size()
	tmpFile.Close()

	// Upload.
	f, err := os.Open(tmpPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := p.Upload(ctx, filename, f); err != nil {
		return nil, fmt.Errorf("upload backup: %w", err)
	}

	m.logger.Info("backup created", "name", filename, "provider", provider, "size", size)

	// Prune old backups.
	m.pruneOld(provider)

	return &BackupInfo{
		Name:     filename,
		Size:     size,
		Created:  time.Now().UTC(),
		Provider: provider,
	}, nil
}

// RestoreBackup downloads a backup archive from the provider and extracts its
// contents: config goes to configPath, certs go to certsDir.
func (m *BackupManager) RestoreBackup(name, provider string) error {
	m.mu.Lock()
	configPath := m.configPath
	certsDir := m.certsDir
	m.mu.Unlock()

	if configPath == "" {
		return fmt.Errorf("config path not set")
	}

	p := m.providers[provider]
	if p == nil {
		return fmt.Errorf("unknown backup provider %q", provider)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	rc, err := p.Download(ctx, name)
	if err != nil {
		return fmt.Errorf("download backup: %w", err)
	}
	defer rc.Close()

	gr, err := gzip.NewReader(rc)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}

		// Determine output path from the archive entry name.
		var outPath string
		switch {
		case strings.HasPrefix(hdr.Name, "config/"):
			outPath = configPath
		case strings.HasPrefix(hdr.Name, "domains.d/"):
			if m.domainsDir == "" {
				continue
			}
			rel := strings.TrimPrefix(hdr.Name, "domains.d/")
			if rel == "" {
				continue
			}
			outPath = filepath.Join(m.domainsDir, rel)
		case strings.HasPrefix(hdr.Name, "certs/"):
			if certsDir == "" {
				continue
			}
			rel := strings.TrimPrefix(hdr.Name, "certs/")
			if rel == "" {
				continue
			}
			outPath = filepath.Join(certsDir, rel)
		case strings.HasPrefix(hdr.Name, "sites/"):
			// Restore domain web content to web root
			if m.webRoot == "" {
				continue
			}
			rel := strings.TrimPrefix(hdr.Name, "sites/")
			if rel == "" {
				continue
			}
			outPath = filepath.Join(m.webRoot, rel)
		case hdr.Name == "databases/all-databases.sql":
			// Import database dump via mysql
			if hdr.Typeflag != tar.TypeDir {
				data, _ := io.ReadAll(tr)
				if len(data) > 0 {
					importDatabaseDump(data, m.logger)
				}
			}
			continue
		default:
			continue
		}

		if hdr.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(outPath, 0755); err != nil {
				return err
			}
			continue
		}

		// Write file.
		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			return err
		}
		f, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
		if err != nil {
			return fmt.Errorf("create %s: %w", outPath, err)
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return fmt.Errorf("write %s: %w", outPath, err)
		}
		f.Close()
	}

	m.logger.Info("backup restored", "name", name, "provider", provider)
	return nil
}

// ListBackups returns backup info from all registered providers.
func (m *BackupManager) ListBackups() []BackupInfo {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var all []BackupInfo
	for _, p := range m.providers {
		items, err := p.List(ctx)
		if err != nil {
			m.logger.Warn("list backups failed", "provider", p.Name(), "error", err)
			continue
		}
		all = append(all, items...)
	}
	sort.Slice(all, func(i, j int) bool {
		return all[i].Created.After(all[j].Created)
	})
	return all
}

// DeleteBackup deletes a backup by name from the specified provider.
func (m *BackupManager) DeleteBackup(name, provider string) error {
	p := m.providers[provider]
	if p == nil {
		return fmt.Errorf("unknown backup provider %q", provider)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return p.Delete(ctx, name)
}

// ScheduleBackup starts the automatic periodic backup goroutine. It can be
// called multiple times; only one goroutine runs at a time.
func (m *BackupManager) ScheduleBackup(interval time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cancel != nil {
		m.cancel()
	}
	m.schedule = interval
	if interval <= 0 {
		m.running = false
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.running = true

	provider := m.cfg.Provider
	if provider == "" {
		provider = "local"
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				info, err := m.CreateBackup(provider)
				if err != nil {
					m.logger.Error("scheduled backup failed", "error", err)
				}
				if m.onBackup != nil {
					m.onBackup(info, err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

// ScheduleStatus returns the current schedule interval and whether it is active.
func (m *BackupManager) ScheduleStatus() (interval time.Duration, active bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.schedule, m.running
}

// Stop stops any running scheduled backup.
func (m *BackupManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.running = false
}

// pruneOld removes the oldest backups from the provider, keeping at most
// m.keepCount entries.
func (m *BackupManager) pruneOld(provider string) {
	p := m.providers[provider]
	if p == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	items, err := p.List(ctx)
	if err != nil {
		return
	}
	if len(items) <= m.keepCount {
		return
	}
	// Sort oldest last.
	sort.Slice(items, func(i, j int) bool {
		return items[i].Created.After(items[j].Created)
	})
	for _, item := range items[m.keepCount:] {
		if err := p.Delete(ctx, item.Name); err != nil {
			m.logger.Warn("prune backup failed", "name", item.Name, "error", err)
		} else {
			m.logger.Info("pruned old backup", "name", item.Name, "provider", provider)
		}
	}
}

// --- tar helpers ---

func addFileToTar(tw *tar.Writer, srcPath, archiveName string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return err
	}

	hdr := &tar.Header{
		Name:    archiveName,
		Size:    info.Size(),
		Mode:    int64(info.Mode()),
		ModTime: info.ModTime(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

// importDatabaseDumpFunc is the function used to import database dumps.
// It can be overridden in tests.
var importDatabaseDumpFunc = importDatabaseDumpReal

// importDatabaseDump imports a SQL dump via the mysql command.
func importDatabaseDump(data []byte, log *logger.Logger) {
	importDatabaseDumpFunc(data, log)
}

func importDatabaseDumpReal(data []byte, log *logger.Logger) {
	mysqlBin, err := exec.LookPath("mysql")
	if err != nil {
		log.Warn("backup restore: mysql not found, skipping DB import")
		return
	}
	cmd := exec.Command(mysqlBin)
	cmd.Stdin = strings.NewReader(string(data))
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Error("backup restore: mysql import failed", "error", err, "output", string(out))
		return
	}
	log.Info("backup restore: database imported", "size", len(data))
}

// CreateDomainBackup creates a backup of a single domain (web root + domain config + DB).
func (m *BackupManager) CreateDomainBackup(domain, webRoot, dbName, provider string) (*BackupInfo, error) {
	p := m.providers[provider]
	if p == nil {
		return nil, fmt.Errorf("unknown backup provider %q", provider)
	}

	ts := time.Now().UTC().Format("20060102-150405")
	filename := fmt.Sprintf("uwas-domain-%s-%s.tar.gz", domain, ts)

	tmpFile, err := os.CreateTemp("", "uwas-domain-backup-*.tar.gz")
	if err != nil {
		return nil, err
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	gw := gzip.NewWriter(tmpFile)
	tw := tar.NewWriter(gw)

	// Domain web root
	if webRoot != "" {
		if info, statErr := os.Stat(webRoot); statErr == nil && info.IsDir() {
			addDirToTar(tw, webRoot, "site")
		}
	}

	// Domain config file (domains.d/domain.yaml)
	m.mu.Lock()
	domainsDir := m.domainsDir
	m.mu.Unlock()
	if domainsDir != "" {
		cfgFile := filepath.Join(domainsDir, domain+".yaml")
		if _, statErr := os.Stat(cfgFile); statErr == nil {
			addFileToTar(tw, cfgFile, "config/"+domain+".yaml")
		}
	}

	// Domain database dump
	if dbName != "" {
		if dump, dumpErr := dumpDatabase(dbName); dumpErr == nil && len(dump) > 0 {
			hdr := &tar.Header{
				Name: "database/" + dbName + ".sql", Size: int64(len(dump)),
				Mode: 0644, ModTime: time.Now(),
			}
			if tw.WriteHeader(hdr) == nil {
				tw.Write(dump)
			}
		}
	}

	tw.Close()
	gw.Close()
	stat, _ := tmpFile.Stat()
	size := stat.Size()
	tmpFile.Close()

	f, err := os.Open(tmpPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := p.Upload(ctx, filename, f); err != nil {
		return nil, fmt.Errorf("upload: %w", err)
	}

	m.logger.Info("domain backup created", "domain", domain, "name", filename, "size", size)
	return &BackupInfo{Name: filename, Size: size, Created: time.Now().UTC(), Provider: provider}, nil
}

// dumpDatabaseFunc is the function used to dump a single database.
// It can be overridden in tests.
var dumpDatabaseFunc = dumpDatabaseReal

// dumpDatabase dumps a single MySQL database.
func dumpDatabase(dbName string) ([]byte, error) {
	return dumpDatabaseFunc(dbName)
}

func dumpDatabaseReal(dbName string) ([]byte, error) {
	mysqldump, err := exec.LookPath("mysqldump")
	if err != nil {
		return nil, err
	}
	return exec.Command(mysqldump, "--single-transaction", "--quick", dbName).Output()
}

// dumpAllDatabasesFunc is the function used to dump all databases.
// It can be overridden in tests.
var dumpAllDatabasesFunc = dumpAllDatabasesReal

// dumpAllDatabases runs mysqldump --all-databases and returns the SQL dump.
// Returns nil if mysqldump is not available or MySQL is not running.
func dumpAllDatabases() ([]byte, error) {
	return dumpAllDatabasesFunc()
}

func dumpAllDatabasesReal() ([]byte, error) {
	mysqldump, err := exec.LookPath("mysqldump")
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(mysqldump, "--all-databases", "--single-transaction", "--quick", "--lock-tables=false")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return out, nil
}

func addDirToTar(tw *tar.Writer, srcDir, archivePrefix string) error {
	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		archiveName := filepath.ToSlash(filepath.Join(archivePrefix, rel))

		if info.IsDir() {
			hdr := &tar.Header{
				Name:     archiveName + "/",
				Typeflag: tar.TypeDir,
				Mode:     int64(info.Mode()),
				ModTime:  info.ModTime(),
			}
			return tw.WriteHeader(hdr)
		}

		return addFileToTar(tw, path, archiveName)
	})
}
