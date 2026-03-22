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
	if err := os.MkdirAll(p.dir, 0755); err != nil {
		return fmt.Errorf("create backup dir: %w", err)
	}
	dst := filepath.Join(p.dir, filepath.Base(filename))
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, data)
	return err
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
