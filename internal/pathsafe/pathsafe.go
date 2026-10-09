package pathsafe

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var (
	absFunc      = filepath.Abs
	evalSymlinks = filepath.EvalSymlinks
)

// maxDanglingHops bounds the dangling-symlink walk in resolvePath (same limit
// as filepath.EvalSymlinks) so a link cycle cannot spin forever.
const maxDanglingHops = 255

var (
	errTooManyLinks  = errors.New("pathsafe: too many dangling symlinks")
	errAmbiguousLink = errors.New("pathsafe: dangling symlink target has '..' after a path element")
)

// IsWithinBase reports whether target is inside base using absolute path checks.
func IsWithinBase(base, target string) bool {
	absBase, err := absFunc(base)
	if err != nil {
		return false
	}
	absTarget, err := absFunc(target)
	if err != nil {
		return false
	}
	return isWithin(absBase, absTarget)
}

// IsWithinBaseResolved reports whether target is inside base after resolving
// symlinks. Non-existing path tails are supported by resolving the nearest
// existing parent first.
func IsWithinBaseResolved(base, target string) bool {
	resolvedBase, err := resolvePath(base)
	if err != nil {
		return false
	}
	resolvedTarget, err := resolvePath(target)
	if err != nil {
		return false
	}
	return isWithin(resolvedBase, resolvedTarget)
}

// RelativeToBase returns target relative to base only if target is within base.
func RelativeToBase(base, target string) (string, bool) {
	absBase, err := absFunc(base)
	if err != nil {
		return "", false
	}
	absTarget, err := absFunc(target)
	if err != nil {
		return "", false
	}
	if !isWithin(absBase, absTarget) {
		return "", false
	}
	rel, err := filepath.Rel(absBase, absTarget)
	if err != nil {
		return "", false
	}
	if rel == "." {
		return "", true
	}
	return rel, true
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

func resolvePath(path string) (string, error) {
	absPath, err := absFunc(path)
	if err != nil {
		return "", err
	}

	// Resolve the closest existing ancestor, then append missing tail segments.
	cur := absPath
	var missing []string
	for hops := 0; ; hops++ {
		if hops > maxDanglingHops {
			return "", errTooManyLinks
		}
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
				// directory, not from the (possibly symlinked) path used to reach it.
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
