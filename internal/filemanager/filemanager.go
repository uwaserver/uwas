// Package filemanager provides web-based file management for domain web roots.
package filemanager

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultMaxUpload is the default maximum upload size (100MB).
const DefaultMaxUpload = 100 << 20

// maxListEntries bounds the entries List will read from one directory.
var maxListEntries = 50000

// Testable hooks for filesystem operations.
var (
	evalSymlinks = filepath.EvalSymlinks
	absFunc      = filepath.Abs
	entryInfo    = func(e os.DirEntry) (os.FileInfo, error) { return e.Info() }
)

// maxDanglingHops bounds the dangling-symlink walk in resolvePath (same limit
// as filepath.EvalSymlinks) so a link cycle cannot spin forever.
const maxDanglingHops = 255

var (
	errTooManyLinks  = errors.New("filemanager: too many dangling symlinks")
	errAmbiguousLink = errors.New("filemanager: dangling symlink target has '..' after a path element")
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
	absBase, err := absFunc(baseDir)
	if err != nil {
		return nil, fmt.Errorf("invalid web root: %w", err)
	}
	baseDir = absBase
	fullPath, realFull := safePathResolved(baseDir, relPath)
	if fullPath == "" {
		return nil, fmt.Errorf("invalid path")
	}

	root, rel, err := openContained(baseDir, realFull)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	dir, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	// Read one entry past the cap: a directory a tenant filled with files must
	// not cost memory and one lstat per entry on every listing request (F14601).
	entries, err := dir.ReadDir(maxListEntries + 1)
	dir.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > maxListEntries {
		return nil, fmt.Errorf("directory has more than %d entries; use SFTP to manage it", maxListEntries)
	}

	result := make([]Entry, 0, len(entries))
	for _, e := range entries {
		info, err := entryInfo(e)
		if err != nil {
			// Silently skip entries that can't be read (permission denied, broken symlink, etc.)
			// This is intentional - users shouldn't see files they can't access.
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
	_, fullPath := safePathResolved(baseDir, relPath)
	if fullPath == "" {
		return nil, fmt.Errorf("invalid path")
	}
	root, rel, err := openContained(baseDir, fullPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	// O_NONBLOCK: a FIFO planted in the web root would otherwise block open(2)
	// (and this request) forever.
	f, err := root.OpenFile(rel, os.O_RDONLY|nonBlockFlag|noFollowFlag, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("cannot read directory")
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	if info.Size() > DefaultMaxUpload {
		return nil, fmt.Errorf("file too large (max %dMB)", DefaultMaxUpload>>20)
	}
	return io.ReadAll(io.LimitReader(f, DefaultMaxUpload+1))
}

// openWritable opens fullPath for writing without blocking on a FIFO and
// refuses anything but a regular file.
func openWritable(root *os.Root, rel string) (*os.File, error) {
	_, statErr := root.Lstat(rel)
	created := os.IsNotExist(statErr)
	f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|nonBlockFlag|noFollowFlag, 0600)
	if err != nil {
		return nil, err
	}
	if created {
		ownNewFile(root, f)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("not a regular file")
	}
	if err := f.Truncate(0); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// WriteFile writes content to a file.
func WriteFile(baseDir, relPath string, content []byte) error {
	_, fullPath := safePathResolved(baseDir, relPath)
	if fullPath == "" {
		return fmt.Errorf("invalid path")
	}
	root, rel, err := openContained(baseDir, fullPath)
	if err != nil {
		return err
	}
	defer root.Close()
	if dir := filepath.Dir(rel); dir != "." {
		mkdirAllOwned(root, dir, 0755)
	}
	f, err := openWritable(root, rel)
	if err != nil {
		return err
	}
	_, err = f.Write(content)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Delete removes a file or empty directory.
func Delete(baseDir, relPath string) error {
	fullPath := safePath(baseDir, relPath)
	if fullPath == "" {
		// A symlink whose target lies outside the base (a tenant's PHP
		// symlink()) fails safePath, yet unlinking touches only the link.
		fullPath = symlinkEntryPath(baseDir, relPath)
	}
	if fullPath == "" {
		return fmt.Errorf("invalid path")
	}
	// Prevent deleting the base dir itself. Both sides must be resolved the
	// same way: safePath returns an absolute, cleaned path, so comparing it
	// against the raw baseDir argument only held when the caller happened to
	// pass an already-absolute, clean string. A baseDir with a trailing slash
	// (an operator writing `root: /var/www/site/public_html/`) or a relative
	// one slipped past the check, and os.RemoveAll then wiped the whole web
	// root instead of rejecting the request.
	absBase, err := absFunc(baseDir)
	if err != nil {
		return fmt.Errorf("invalid web root: %w", err)
	}
	if fullPath == filepath.Clean(absBase) {
		return fmt.Errorf("cannot delete web root")
	}
	// Remove through the web root's handle so an ancestor swapped for a symlink
	// after the check cannot redirect the removal outside it (F7100). The
	// parent is addressed by its resolved path (an os.Root refuses absolute
	// symlink targets); the entry itself is removed by name, never followed.
	cleanRel := filepath.Clean(relPath)
	_, parentReal := safePathResolved(baseDir, filepath.Dir(cleanRel))
	if parentReal == "" {
		return fmt.Errorf("invalid path")
	}
	root, rel, err := openContained(baseDir, filepath.Join(parentReal, filepath.Base(cleanRel)))
	if err != nil {
		return err
	}
	defer root.Close()
	return root.RemoveAll(rel)
}

// symlinkEntryPath returns the path of relPath when it names a symlink whose
// parent directory is inside baseDir, whatever the link points to; "" otherwise.
// Callers may only remove the entry, never follow it.
func symlinkEntryPath(baseDir, relPath string) string {
	rel := filepath.Clean(relPath)
	if rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	parent := safePath(baseDir, filepath.Dir(rel))
	if parent == "" {
		return ""
	}
	full := filepath.Join(parent, filepath.Base(rel))
	if info, err := os.Lstat(full); err != nil || info.Mode()&os.ModeSymlink == 0 {
		return ""
	}
	return full
}

// CreateDir creates a directory.
func CreateDir(baseDir, relPath string) error {
	_, realFull := safePathResolved(baseDir, relPath)
	if realFull == "" {
		return fmt.Errorf("invalid path")
	}
	root, rel, err := openContained(baseDir, realFull)
	if err != nil {
		return err
	}
	defer root.Close()
	if rel == "." {
		return nil
	}
	return mkdirAllOwned(root, rel, 0755)
}

// SaveUpload writes an uploaded file.
func SaveUpload(baseDir, relPath string, src io.Reader) (int64, error) {
	_, fullPath := safePathResolved(baseDir, relPath)
	if fullPath == "" {
		return 0, fmt.Errorf("invalid path")
	}
	root, rel, err := openContained(baseDir, fullPath)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	dir := filepath.Dir(rel)
	if dir != "." {
		if err := mkdirAllOwned(root, dir, 0755); err != nil {
			return 0, fmt.Errorf("create directory for upload: %w", err)
		}
	}
	// Finish reading the upload before truncating an existing destination.
	staged, stagedRel, err := createStaged(root, dir)
	if err != nil {
		return 0, err
	}
	defer root.Remove(stagedRel)
	defer staged.Close()
	n, err := io.Copy(staged, src)
	if err != nil {
		return n, err
	}
	if _, err := staged.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	f, err := openWritable(root, rel)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return io.Copy(f, staged)
}

// createStaged creates a private temporary file in dir (relative to root).
func createStaged(root *os.Root, dir string) (*os.File, string, error) {
	for i := 0; i < 16; i++ {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, "", err
		}
		name := filepath.Join(dir, ".upload-"+hex.EncodeToString(b[:]))
		f, err := root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			return f, name, nil
		}
		if !os.IsExist(err) {
			return nil, "", err
		}
	}
	return nil, "", fmt.Errorf("create staging file: too many collisions")
}

// openContained opens the web root and returns realPath (as returned by
// safePathResolved) relative to it.
func openContained(baseDir, realPath string) (*os.Root, string, error) {
	realBase, err := resolvePath(baseDir)
	if err != nil {
		return nil, "", fmt.Errorf("invalid web root: %w", err)
	}
	rel, err := filepath.Rel(realBase, realPath)
	if err != nil {
		return nil, "", fmt.Errorf("invalid path")
	}
	root, err := os.OpenRoot(realBase)
	if err != nil {
		return nil, "", err
	}
	return root, rel, nil
}

// DiskUsage returns total bytes used under a directory.
// Symlinks are not followed; each directory is visited at most once, preventing
// both infinite symlink loops (symlink → ancestor) and hard-link cycles.
func DiskUsage(dir string) (int64, error) {
	return DiskUsageContext(context.Background(), dir)
}

// DiskUsageContext is DiskUsage that stops as soon as ctx is done, so a request
// whose client has gone away does not keep walking the whole tree (F14901).
func DiskUsageContext(ctx context.Context, dir string) (int64, error) {
	var total int64
	visited := make(map[string]bool)

	// WalkDir does not follow a symlinked root (it reports the link itself),
	// so a web root like /var/www -> /srv/www measured only the link's size.
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			return nil // skip inaccessible entries
		}
		if d.IsDir() {
			// Use the on-disk inode so hard-linked directories are only counted once.
			real, err := resolvePathForInode(path)
			if err != nil {
				return nil // skip dirs whose real path can't be resolved
			}
			if visited[real] {
				return filepath.SkipDir // already counted this directory tree
			}
			visited[real] = true
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil // skip files that can't be stat'd (e.g. permissions, deleted)
		}
		total += info.Size()
		return nil
	})
	return total, err
}

// resolvePathForInode returns the on-disk resolved path of path for use as a
// directory-identity key. It is not used for security boundary checks (use
// safePath for that).  Returns the clean absolute path of the on-disk inode,
// following all symlinks, or an error if resolution fails.
func resolvePathForInode(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(real), nil
}

// safePath resolves a relative path within baseDir, preventing directory traversal.
func safePath(baseDir, relPath string) string {
	abs, _ := safePathResolved(baseDir, relPath)
	return abs
}

// safePathResolved is safePath that also returns the symlink-resolved path it
// checked. A caller that opens the file must open that path with O_NOFOLLOW:
// opening the unresolved one lets the tenant swap the final component for a
// symlink between the check and the open (F6800).
func safePathResolved(baseDir, relPath string) (abs, real string) {
	// Clean and reject absolute paths or traversal
	relPath = filepath.Clean(relPath)
	if filepath.IsAbs(relPath) || relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", ""
	}
	full := filepath.Join(baseDir, relPath)
	// Ensure result is still under baseDir.
	if !isWithinBase(baseDir, full) {
		return "", ""
	}
	// Resolve symlinks (including non-existing path tails) to prevent escape via
	// symlinked parent directories such as "uploads -> /etc".
	realBase, err := resolvePath(baseDir)
	if err != nil {
		return "", ""
	}
	realFull, err := resolvePath(full)
	if err != nil || !isWithin(realBase, realFull) {
		return "", ""
	}
	absFull, _ := absFunc(full)
	return absFull, realFull
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
	hops := 0
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
		if info, statErr := os.Lstat(cur); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			// Bound the walk so a dangling-link cycle cannot spin forever; popping
			// a missing path element is not a link hop.
			if hops++; hops > maxDanglingHops {
				return "", errTooManyLinks
			}
			link, linkErr := os.Readlink(cur)
			if linkErr != nil {
				return "", linkErr
			}
			// A ".." after a named element is resolved by the kernel against
			// that element's real location, which a lexical Join cannot model.
			if hasInnerDotDot(link) {
				return "", errAmbiguousLink
			}
			if !filepath.IsAbs(link) {
				// Relative targets are interpreted from the link's real
				// directory, not from the (possibly symlinked) path used to
				// reach it. Joining lexically here classifies base/a/dl as
				// base/x and admits a path the kernel resolves outside base.
				realParent, perr := evalSymlinks(filepath.Dir(cur))
				if perr != nil {
					return "", perr
				}
				link = filepath.Join(realParent, link)
			}
			cur = link
			continue
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err
		}
		missing = append(missing, filepath.Base(cur))
		cur = parent
	}
}

// hasInnerDotDot reports whether link contains a ".." element after a named
// element (e.g. "a/../b"). Leading ".." elements are unambiguous once the
// link's real parent directory is known.
func hasInnerDotDot(link string) bool {
	seenName := false
	for _, part := range strings.Split(filepath.ToSlash(link), "/") {
		switch part {
		case "", ".":
		case "..":
			if seenName {
				return true
			}
		default:
			seenName = true
		}
	}
	return false
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
