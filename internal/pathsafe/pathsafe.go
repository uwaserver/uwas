package pathsafe

import (
	"os"
	"path/filepath"
	"strings"
)

// IsWithinBase reports whether target is inside base using absolute path checks.
func IsWithinBase(base, target string) bool {
	absBase, err := filepath.Abs(base)
	if err != nil {
		return false
	}
	absTarget, err := filepath.Abs(target)
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
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", false
	}
	absTarget, err := filepath.Abs(target)
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
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	// Resolve the closest existing ancestor, then append missing tail segments.
	cur := absPath
	var missing []string
	for {
		real, err := filepath.EvalSymlinks(cur)
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
