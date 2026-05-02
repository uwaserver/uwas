package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/database"
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
	cronExpr  string // cron expression for scheduling (e.g. "0 2 * * *")
	keepCount int

	lastBackup time.Time // time of last successful backup
	startedAt  time.Time // when the scheduler was started

	configPath  string
	certsDir    string
	webRoot     string   // base dir for all domain web roots
	domainsDir  string   // domains.d/ config directory
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

	// MySQL/MariaDB database dump (native, if mysqldump available).
	if dbDump, err := dumpAllDatabases(); err == nil && len(dbDump) > 0 {
		hdr := &tar.Header{
			Name:    "databases/native-all-databases.sql",
			Size:    int64(len(dbDump)),
			Mode:    0644,
			ModTime: time.Now(),
		}
		if err := tw.WriteHeader(hdr); err == nil {
			if _, err := tw.Write(dbDump); err != nil {
				m.logger.Error("backup: failed to write DB dump to tar", "error", err)
			}
		}
	}

	// Docker container database dumps.
	if dockerDumpFn != nil {
		dockerDumps := dockerDumpFn()
		for name, dump := range dockerDumps {
			if len(dump) == 0 {
				continue
			}
			hdr := &tar.Header{
				Name:    "databases/docker-" + name + ".sql",
				Size:    int64(len(dump)),
				Mode:    0644,
				ModTime: time.Now(),
			}
			if err := tw.WriteHeader(hdr); err == nil {
				if _, err := tw.Write(dump); err != nil {
					m.logger.Error("backup: failed to write docker DB dump to tar", "container", name, "error", err)
				}
			}
			m.logger.Info("backup: docker DB dumped", "container", name, "size", len(dump))
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

	stat, statErr := tmpFile.Stat()
	if statErr != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("stat backup file: %w", statErr)
	}
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

	// Track last backup time.
	m.mu.Lock()
	m.lastBackup = time.Now().UTC()
	m.mu.Unlock()

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

	// Set default limits.
	maxFileSize := m.cfg.MaxFileSize
	if maxFileSize <= 0 {
		maxFileSize = 500 * 1 << 20 // 500MB
	}
	maxTotalSize := m.cfg.MaxTotalSize
	if maxTotalSize <= 0 {
		maxTotalSize = 10 << 30 // 10GB
	}

	tr := tar.NewReader(gr)
	var totalRead int64
	for {
		// Check total size limit before reading next header.
		if totalRead >= maxTotalSize {
			return fmt.Errorf("backup exceeds max total size limit (%d bytes)", maxTotalSize)
		}

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
			var ok bool
			outPath, ok = safeRestorePath(m.domainsDir, rel)
			if !ok {
				continue // path traversal or symlink escape attempt
			}
		case strings.HasPrefix(hdr.Name, "certs/"):
			if certsDir == "" {
				continue
			}
			rel := strings.TrimPrefix(hdr.Name, "certs/")
			if rel == "" {
				continue
			}
			var ok bool
			outPath, ok = safeRestorePath(certsDir, rel)
			if !ok {
				continue // path traversal or symlink escape attempt
			}
		case strings.HasPrefix(hdr.Name, "sites/"):
			// Restore domain web content to web root
			if m.webRoot == "" {
				continue
			}
			rel := strings.TrimPrefix(hdr.Name, "sites/")
			if rel == "" {
				continue
			}
			var ok bool
			outPath, ok = safeRestorePath(m.webRoot, rel)
			if !ok {
				continue // path traversal or symlink escape attempt
			}
		case hdr.Name == "databases/all-databases.sql" || hdr.Name == "databases/native-all-databases.sql":
			// Import database dump via mysql
			if hdr.Typeflag != tar.TypeDir {
				const maxDumpSize = 2 << 30 // 2GB
				data, _ := io.ReadAll(io.LimitReader(tr, maxDumpSize))
				totalRead += int64(len(data))
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

		// Write file with size limit.
		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			return err
		}
		// Sanitize file permissions from untrusted archive: strip SUID/SGID, cap at 0755
		mode := os.FileMode(hdr.Mode) & 0o755
		mode &^= os.ModeSetuid | os.ModeSetgid | os.ModeSticky
		f, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
		if err != nil {
			return fmt.Errorf("create %s: %w", outPath, err)
		}
		limited := io.LimitReader(tr, maxFileSize)
		written, err := io.Copy(f, limited)
		totalRead += written
		f.Close()
		if err != nil {
			return fmt.Errorf("write %s: %w", outPath, err)
		}
		// If we hit the per-file limit, the file may be truncated.
		if written >= maxFileSize {
			return fmt.Errorf("file %s exceeds max size limit (%d bytes)", hdr.Name, maxFileSize)
		}
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
				m.mu.Lock()
				cb := m.onBackup
				m.mu.Unlock()
				if cb != nil {
					cb(info, err)
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

// ScheduleBackupCron starts the automatic backup goroutine using a cron expression.
// The expression is a 5-field cron: "minute hour day month weekday"
// Example: "0 2 * * *" = daily at 2:00 AM
// Supports: * (any), numbers, ranges (1-5), steps (*/5), lists (1,3,5)
func (m *BackupManager) ScheduleBackupCron(cronExpr string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cancel != nil {
		m.cancel()
	}
	m.cronExpr = cronExpr
	if cronExpr == "" {
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
		for {
			next := nextCronRun(cronExpr)
			if next.IsZero() {
				return
			}
			wait := time.Until(next)
			if wait <= 0 {
				wait = time.Minute // safety: if we're already past, wait 1 min
			}

			select {
			case <-time.After(wait):
				info, err := m.CreateBackup(provider)
				if err != nil {
					m.logger.Error("scheduled backup failed", "error", err)
				}
				m.mu.Lock()
				cb := m.onBackup
				m.mu.Unlock()
				if cb != nil {
					cb(info, err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

// nextCronRun returns the next time a cron expression should fire.
// Returns zero time if the expression is invalid.
func nextCronRun(expr string) time.Time {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return time.Time{}
	}
	// minute hour day month weekday
	minuteField, hourField, dayField, monthField, weekdayField := fields[0], fields[1], fields[2], fields[3], fields[4]

	loc := time.Local
	now := time.Now().In(loc)
	year := now.Year()

	// Try up to 2 years ahead
	for i := 0; i < 365*2; i++ {
		candidate := time.Date(year+i/12/30, time.Month(1+(i/30)%12), 1+(i%30), 0, 0, 0, 0, loc)

		// Check month
		if !matchCronField(int(candidate.Month()), monthField) {
			continue
		}
		// Check day of month
		if !matchCronField(candidate.Day(), dayField) {
			continue
		}
		// Check weekday
		if !matchCronField(int(candidate.Weekday())%7, weekdayField) {
			continue
		}

		// Find matching hour
		for h := 0; h < 24; h++ {
			if !matchCronField(h, hourField) {
				continue
			}
			// Find matching minute
			for min := 0; min < 60; min++ {
				if !matchCronField(min, minuteField) {
					continue
				}
				next := time.Date(year+i/365, candidate.Month(), candidate.Day(), h, min, 0, 0, loc)
				if next.After(now) {
					return next
				}
			}
		}
	}
	return time.Time{}
}

// matchCronField checks if a value matches a cron field pattern.
func matchCronField(value int, field string) bool {
	if field == "*" {
		return true
	}
	// Handle step values: */5
	if strings.HasPrefix(field, "*/") {
		step := parseInt(strings.TrimPrefix(field, "*/"))
		if step > 0 && value%step == 0 {
			return true
		}
		return false
	}
	// Handle ranges: 1-5
	if strings.Contains(field, "-") {
		parts := strings.Split(field, "-")
		if len(parts) == 2 {
			low := parseInt(parts[0])
			high := parseInt(parts[1])
			return value >= low && value <= high
		}
	}
	// Handle lists: 1,3,5
	if strings.Contains(field, ",") {
		for _, v := range strings.Split(field, ",") {
			if parseInt(v) == value {
				return true
			}
		}
		return false
	}
	// Handle single value
	return parseInt(field) == value
}

func parseInt(s string) int {
	n := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}

// SetKeepCount updates the number of backups to retain.
func (m *BackupManager) SetKeepCount(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n > 0 {
		m.keepCount = n
	}
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
	if dbName != "" && !database.ValidDBIdentifier(dbName) {
		return nil, fmt.Errorf("invalid database name %q", dbName)
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
				if _, wErr := tw.Write(dump); wErr != nil {
					return nil, fmt.Errorf("write database dump to tar: %w", wErr)
				}
			}
		}
	}

	if err := tw.Close(); err != nil {
		gw.Close()
		tmpFile.Close()
		return nil, fmt.Errorf("finalize tar: %w", err)
	}
	if err := gw.Close(); err != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("finalize gzip: %w", err)
	}
	stat, statErr := tmpFile.Stat()
	if statErr != nil {
		tmpFile.Close()
		return nil, fmt.Errorf("stat backup file: %w", statErr)
	}
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
	if !database.ValidDBIdentifier(dbName) {
		return nil, fmt.Errorf("invalid database name %q", dbName)
	}
	mysqldump, err := exec.LookPath("mysqldump")
	if err != nil {
		return nil, err
	}
	return exec.Command(mysqldump, "--single-transaction", "--quick", dbName).Output()
}

// dumpAllDatabasesFunc is the function used to dump all databases.
// It can be overridden in tests.
var dumpAllDatabasesFunc = dumpAllDatabasesReal

// dockerDumpFn dumps all Docker container databases. Set by server.go at init.
// Returns map[containerName]sqlDump.
var dockerDumpFn func() map[string][]byte

// SetDockerDumpFunc sets the function that dumps Docker container databases.
func SetDockerDumpFunc(fn func() map[string][]byte) {
	dockerDumpFn = fn
}

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
	return filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip symlinks (files and directories) to prevent archiving outside the web root.
		if d.Type()&fs.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		archiveName := filepath.ToSlash(filepath.Join(archivePrefix, rel))

		if d.IsDir() {
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

// IsInsideDir checks that resolved path is inside the base directory (zip-slip guard).
func IsInsideDir(path, base string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return false
	}
	return strings.HasPrefix(abs, baseAbs+string(filepath.Separator)) || abs == baseAbs
}

// safeRestorePath joins an archive-relative path to a restore root while
// rejecting traversal and existing symlink escapes.
func safeRestorePath(base, rel string) (string, bool) {
	if base == "" || rel == "" || filepath.IsAbs(rel) {
		return "", false
	}
	rel = filepath.Clean(filepath.FromSlash(rel))
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return "", false
	}

	target := filepath.Join(base, rel)
	if !IsInsideDir(target, base) {
		return "", false
	}

	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return "", false
	}
	baseReal, err := filepath.EvalSymlinks(base)
	if err != nil {
		baseReal = baseAbs
	}

	if info, err := os.Lstat(target); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", false
	}

	existing := target
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", false
		}
		existing = parent
	}

	existingReal, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", false
	}

	if IsInsideDir(existing, baseAbs) {
		if !IsInsideDir(existingReal, baseReal) {
			return "", false
		}
	} else if !IsInsideDir(baseAbs, existingReal) {
		return "", false
	}

	return target, true
}

// ScheduleDetail returns the current backup schedule details for the admin UI.
type ScheduleDetail struct {
	Enabled    bool   `json:"enabled"`
	Interval   string `json:"interval"`
	Keep       int    `json:"keep"`
	LastBackup string `json:"last_backup,omitempty"`
	NextBackup string `json:"next_backup,omitempty"`
	Provider   string `json:"provider"`
}

func (m *BackupManager) ScheduleDetail() ScheduleDetail {
	m.mu.Lock()
	defer m.mu.Unlock()

	provider := m.cfg.Provider
	if provider == "" {
		provider = "local"
	}

	simplifyInterval := func(d time.Duration) string {
		days := int(d.Hours() / 24)
		if days > 0 && d%time.Hour == 0 {
			return fmt.Sprintf("%dd", days)
		}
		if d%time.Hour == 0 {
			return fmt.Sprintf("%dh", int(d/time.Hour))
		}
		return d.String()
	}

	var lastBak, nextBak string
	if m.running && m.schedule > 0 && !m.lastBackup.IsZero() {
		lastBak = m.lastBackup.UTC().Format(time.RFC3339)
		nextBak = m.lastBackup.Add(m.schedule).UTC().Format(time.RFC3339)
	}

	return ScheduleDetail{
		Enabled:    m.running,
		Interval:   simplifyInterval(m.schedule),
		Keep:       m.keepCount,
		Provider:   provider,
		LastBackup: lastBak,
		NextBackup: nextBak,
	}
}
