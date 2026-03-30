// Package filemanager provides web-based file management for domain web roots.
package filemanager

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Testable hooks for filesystem operations.
var (
	evalSymlinks = filepath.EvalSymlinks
	absFunc      = filepath.Abs
	entryInfo    = func(e os.DirEntry) (os.FileInfo, error) { return e.Info() }
)

// Entry represents a file or directory.
type Entry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	IsDir   bool      `json:"is_dir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
	Mode    string    `json:"mode"`
}

// List returns directory contents. Path is relative to baseDir.
func List(baseDir, relPath string) ([]Entry, error) {
	fullPath := safePath(baseDir, relPath)
	if fullPath == "" {
		return nil, fmt.Errorf("invalid path")
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return nil, err
	}

	result := make([]Entry, 0, len(entries))
	for _, e := range entries {
		info, err := entryInfo(e)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(baseDir, filepath.Join(fullPath, e.Name()))
		result = append(result, Entry{
			Name:    e.Name(),
			Path:    filepath.ToSlash(rel),
			IsDir:   e.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Mode:    info.Mode().String(),
		})
	}
	return result, nil
}

// ReadFile returns file contents. Max 5MB.
func ReadFile(baseDir, relPath string) ([]byte, error) {
	fullPath := safePath(baseDir, relPath)
	if fullPath == "" {
		return nil, fmt.Errorf("invalid path")
	}
	info, err := os.Stat(fullPath)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("cannot read directory")
	}
	if info.Size() > 5<<20 {
		return nil, fmt.Errorf("file too large (max 5MB)")
	}
	return os.ReadFile(fullPath)
}

// WriteFile writes content to a file.
func WriteFile(baseDir, relPath string, content []byte) error {
	fullPath := safePath(baseDir, relPath)
	if fullPath == "" {
		return fmt.Errorf("invalid path")
	}
	os.MkdirAll(filepath.Dir(fullPath), 0755)
	return os.WriteFile(fullPath, content, 0644)
}

// Delete removes a file or empty directory.
func Delete(baseDir, relPath string) error {
	fullPath := safePath(baseDir, relPath)
	if fullPath == "" {
		return fmt.Errorf("invalid path")
	}
	// Prevent deleting the base dir itself
	if fullPath == baseDir {
		return fmt.Errorf("cannot delete web root")
	}
	return os.RemoveAll(fullPath)
}

// CreateDir creates a directory.
func CreateDir(baseDir, relPath string) error {
	fullPath := safePath(baseDir, relPath)
	if fullPath == "" {
		return fmt.Errorf("invalid path")
	}
	return os.MkdirAll(fullPath, 0755)
}

// SaveUpload writes an uploaded file.
func SaveUpload(baseDir, relPath string, src io.Reader) (int64, error) {
	fullPath := safePath(baseDir, relPath)
	if fullPath == "" {
		return 0, fmt.Errorf("invalid path")
	}
	os.MkdirAll(filepath.Dir(fullPath), 0755)
	f, err := os.Create(fullPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.Copy(f, src)
}

// DiskUsage returns total bytes used under a directory.
func DiskUsage(dir string) (int64, error) {
	var total int64
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// safePath resolves a relative path within baseDir, preventing directory traversal.
func safePath(baseDir, relPath string) string {
	// Clean and reject absolute paths or traversal
	relPath = filepath.Clean(relPath)
	if filepath.IsAbs(relPath) || strings.HasPrefix(relPath, "..") {
		return ""
	}
	full := filepath.Join(baseDir, relPath)
	// Ensure result is still under baseDir.
	if !isWithinBase(baseDir, full) {
		return ""
	}
	// Resolve symlinks (including non-existing path tails) to prevent escape via
	// symlinked parent directories such as "uploads -> /etc".
	if !isWithinBaseResolved(baseDir, full) {
		return ""
	}
	absFull, _ := absFunc(full)
	return absFull
}

func isWithinBase(baseDir, fullPath string) bool {
	absBase, err := absFunc(baseDir)
	if err != nil {
		return false
	}
	absFull, err := absFunc(fullPath)
	if err != nil {
		return false
	}
	return isWithin(absBase, absFull)
}

func isWithinBaseResolved(baseDir, fullPath string) bool {
	realBase, err := resolvePath(baseDir)
	if err != nil {
		return false
	}
	realFull, err := resolvePath(fullPath)
	if err != nil {
		return false
	}
	return isWithin(realBase, realFull)
}

func resolvePath(path string) (string, error) {
	absPath, err := absFunc(path)
	if err != nil {
		return "", err
	}
	cur := absPath
	var missing []string
	for {
		real, err := evalSymlinks(cur)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				real = filepath.Join(real, missing[i])
			}
			return filepath.Clean(real), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err
		}
		missing = append(missing, filepath.Base(cur))
		cur = parent
	}
}

func isWithin(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	rel = filepath.Clean(rel)
	if rel == "." {
		return true
	}
	if rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
