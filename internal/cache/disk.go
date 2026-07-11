package cache

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
)

// DiskCache is a file-based L2 cache with hash-based directory sharding.
type DiskCache struct {
	baseDir   string
	maxBytes  int64
	mu        sync.RWMutex
	usedBytes atomic.Int64
}

// NewDiskCache creates a disk cache at the given directory.
// It scans existing cache files to initialise usedBytes so the accounting
// stays correct across restarts.
func NewDiskCache(baseDir string, maxBytes int64) *DiskCache {
	os.MkdirAll(baseDir, 0750)
	_ = os.Chmod(baseDir, 0750)
	dc := &DiskCache{
		baseDir:  baseDir,
		maxBytes: maxBytes,
	}

	// Walk existing files to seed usedBytes.
	var total int64
	filepath.WalkDir(baseDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			_ = os.Chmod(path, 0750)
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 || filepath.Ext(path) != ".cache" {
			return nil
		}
		_ = os.Chmod(path, 0600)
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	dc.usedBytes.Store(total)

	return dc
}

// Get reads a cached response from disk.
func (dc *DiskCache) Get(key string) (*CachedResponse, error) {
	dc.mu.RLock()
	defer dc.mu.RUnlock()

	path := dc.path(key)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Deserialize(data)
}

// Set writes a cached response to disk.
func (dc *DiskCache) Set(key string, resp *CachedResponse) error {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	data := resp.Serialize()

	path := dc.path(key)

	// Account for an existing file at this key (TTL refresh re-caches the same
	// key constantly). Without subtracting the old size, usedBytes inflates on
	// every overwrite until it crosses maxBytes and silently disables the cache.
	var oldSize int64
	if info, err := os.Stat(path); err == nil {
		oldSize = info.Size()
	}

	currentBytes := dc.usedBytes.Load()
	accountedOldSize := min(oldSize, currentBytes)
	projectedBytes := currentBytes - accountedOldSize + int64(len(data))
	// Check disk limit against the projected total after this write.
	if dc.maxBytes > 0 && projectedBytes > dc.maxBytes {
		return nil // silently skip if over limit
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".uwas-cache-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := replaceCacheFile(tmpPath, path); err != nil {
		return err
	}

	dc.usedBytes.Store(projectedBytes)
	return nil
}

func replaceCacheFile(tmpPath, path string) error {
	if err := os.Rename(tmpPath, path); err != nil {
		// Windows does not replace an existing destination with os.Rename.
		// Operations remain serialized by DiskCache.mu during this fallback.
		if runtime.GOOS != "windows" {
			return err
		}
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			return removeErr
		}
		return os.Rename(tmpPath, path)
	}
	return nil
}

// Delete removes a cached file from disk.
func (dc *DiskCache) Delete(key string) {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	path := dc.path(key)
	info, statErr := os.Stat(path)
	if err := os.Remove(path); err == nil && statErr == nil {
		dc.subtractUsedBytes(info.Size())
	}
}

// PurgeAll removes all cache files.
func (dc *DiskCache) PurgeAll() error {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	if err := os.RemoveAll(dc.baseDir); err != nil {
		return err
	}
	dc.usedBytes.Store(0)
	return nil
}

// PurgeByTag removes all cache entries matching any of the given tags.
func (dc *DiskCache) PurgeByTag(tags ...string) int {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	var count int
	tagSet := make(map[string]bool, len(tags))
	for _, t := range tags {
		tagSet[t] = true
	}

	filepath.WalkDir(dc.baseDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if filepath.Ext(path) != ".cache" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		resp, err := Deserialize(data)
		if err != nil {
			// Remove corrupt entries.
			if info, infoErr := d.Info(); infoErr == nil {
				if removeErr := os.Remove(path); removeErr == nil {
					dc.subtractUsedBytes(info.Size())
				}
			}
			return nil
		}

		// Check if any tag matches.
		for _, t := range resp.Tags {
			if tagSet[t] {
				if info, infoErr := d.Info(); infoErr == nil {
					if removeErr := os.Remove(path); removeErr == nil {
						dc.subtractUsedBytes(info.Size())
						count++
					}
				}
				break
			}
		}
		return nil
	})

	return count
}

func (dc *DiskCache) subtractUsedBytes(size int64) {
	current := dc.usedBytes.Load()
	if size >= current {
		dc.usedBytes.Store(0)
		return
	}
	dc.usedBytes.Store(current - size)
}

func (dc *DiskCache) path(key string) string {
	d1, d2 := KeyPrefix(key)
	return filepath.Join(dc.baseDir, d1, d2, HashKey(key)+".cache")
}
