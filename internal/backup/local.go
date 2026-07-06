package backup

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LocalProvider stores backups in a local directory.
type LocalProvider struct {
	dir string
}

// NewLocalProvider creates a LocalProvider that saves archives under dir.
func NewLocalProvider(dir string) *LocalProvider {
	return &LocalProvider{dir: dir}
}

func (p *LocalProvider) Name() string { return "local" }

func (p *LocalProvider) Upload(_ context.Context, filename string, data io.Reader) error {
	// Backup archives bundle uwas.yaml (API keys, admin secrets) and the TLS
	// certs directory (private keys), so both the directory and the files must
	// be owner-only — 0755/0644 would expose credentials to every local user.
	if err := os.MkdirAll(p.dir, 0700); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}
	dst := filepath.Join(p.dir, filepath.Base(filename))
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, data); err != nil {
		f.Close()
		return err
	}
	// Surface a flush/close failure (e.g. full disk) instead of reporting a
	// truncated backup as success.
	if err := f.Close(); err != nil {
		return fmt.Errorf("close backup file: %w", err)
	}
	return nil
}

func (p *LocalProvider) Download(_ context.Context, filename string) (io.ReadCloser, error) {
	path := filepath.Join(p.dir, filepath.Base(filename))
	return os.Open(path)
}

func (p *LocalProvider) List(_ context.Context) ([]BackupInfo, error) {
	entries, err := os.ReadDir(p.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var infos []BackupInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tar.gz") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		infos = append(infos, BackupInfo{
			Name:     e.Name(),
			Size:     fi.Size(),
			Created:  fi.ModTime(),
			Provider: "local",
		})
	}
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].Created.After(infos[j].Created)
	})
	return infos, nil
}

func (p *LocalProvider) Delete(_ context.Context, filename string) error {
	path := filepath.Join(p.dir, filepath.Base(filename))
	return os.Remove(path)
}
