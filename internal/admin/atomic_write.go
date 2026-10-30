package admin

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// atomicWriteFile writes data to path in a crash-safe way: it creates a
// temporary file in the same directory, writes the data, fsyncs the file to
// flush kernel buffers to disk, and atomically renames it over the target.
// Either the previous content remains intact or the new content is fully
// visible — no half-written file is ever observed by another process.
//
// On Windows, os.Rename refuses to overwrite an existing target, so the
// existing file is removed first; the brief gap is unavoidable on that
// platform but the window between Remove and Rename is microseconds.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	path = resolveWriteTarget(path)
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		// Chmod on Windows is a no-op for our intents; on Unix this enforces
		// the secret-friendly mode before another process can see the file.
		tmp.Close()
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}

	if runtime.GOOS == "windows" {
		// os.Rename on Windows fails if the destination exists.
		_ = os.Remove(path)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	cleanup = false
	return nil
}

// resolveWriteTarget follows a symlinked path (also a dangling one) to the file
// it names, so the rename below replaces the target instead of swapping the
// link for a regular file and leaving the real file stale (F56300). A chain
// longer than a few hops is written as is.
func resolveWriteTarget(path string) string {
	for i := 0; i < 8; i++ {
		fi, err := os.Lstat(path)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			return path
		}
		dest, err := os.Readlink(path)
		if err != nil {
			return path
		}
		if !filepath.IsAbs(dest) {
			dest = filepath.Join(filepath.Dir(path), dest)
		}
		path = dest
	}
	return path
}
