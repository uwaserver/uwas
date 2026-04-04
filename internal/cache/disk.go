package cache

import (
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
)

// DiskCache is a file-based L2 cache with hash-based directory sharding.
type DiskCache struct {
	baseDir   string
	maxBytes  int64
	usedBytes atomic.Int64
}

// NewDiskCache creates a disk cache at the given directory.
// It scans existing cache files to initialise usedBytes so the accounting
// stays correct across restarts.
func NewDiskCache(baseDir string, maxBytes int64) *DiskCache {
	os.MkdirAll(baseDir, 0755)
	dc := &DiskCache{
		baseDir:  baseDir,
		maxBytes: maxBytes,
	}

	// Walk existing files to seed usedBytes.
	var total int64
	filepath.WalkDir(baseDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
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
	path := dc.path(key)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Deserialize(data)
}

// Set writes a cached response to disk.
func (dc *DiskCache) Set(key string, resp *CachedResponse) error {
	data := resp.Serialize()

	// Check disk limit
	if dc.maxBytes > 0 && dc.usedBytes.Load()+int64(len(data)) > dc.maxBytes {
		return nil // silently skip if over limit
	}

	path := dc.path(key)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return err
	}

	dc.usedBytes.Add(int64(len(data)))
	return nil
}

// Delete removes a cached file from disk.
func (dc *DiskCache) Delete(key string) {
	path := dc.path(key)
	info, err := os.Stat(path)
	if err == nil {
		dc.usedBytes.Add(-info.Size())
	}
	os.Remove(path)
}

// PurgeAll removes all cache files.
func (dc *DiskCache) PurgeAll() error {
	dc.usedBytes.Store(0)
	return os.RemoveAll(dc.baseDir)
}

// PurgeByTag removes all cache entries matching any of the given tags.
func (dc *DiskCache) PurgeByTag(tags ...string) int {
	var count int
	tagSet := make(map[string]bool, len(tags))
	for _, t := range tags {
		tagSet[t] = true
	}

	filepath.WalkDir(dc.baseDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
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
			os.Remove(path)
			return nil
		}

		// Check if any tag matches.
		for _, t := range resp.Tags {
			if tagSet[t] {
				if info, err := d.Info(); err == nil {
					dc.usedBytes.Add(-info.Size())
				}
				os.Remove(path)
				count++
				break
			}
		}
		return nil
	})

	return count
}

func (dc *DiskCache) path(key string) string {
	d1, d2 := KeyPrefix(key)
	return filepath.Join(dc.baseDir, d1, d2, HashKey(key)+".cache")
}
