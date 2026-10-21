package phpmanager

import (
	"fmt"
	"strconv"
	"strings"
)

// minPatchedPHP maps each supported branch to the first release that fixes
// CVE-2026-109618 and CVE-2026-109619 (remote code execution via OOM
// corruption / stale state, released 2026-10-21). Branches older than 8.2 are
// end-of-life and receive no fixes, so they are not listed.
var minPatchedPHP = map[string]int{
	"8.2": 35,
	"8.3": 36,
	"8.4": 27,
	"8.5": 12,
}

// Advisory classifies a PHP version string. It returns supported=false for
// branches that no longer get security fixes (anything below 8.2), and a
// non-empty warning when the version is EOL or is a supported branch older
// than the patched release. Unparseable versions yield supported=true and no
// warning so detection never blocks on a format surprise.
func Advisory(version string) (supported bool, warning string) {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 3 {
		return true, ""
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	patch, err3 := strconv.Atoi(leadingDigits(parts[2]))
	if err1 != nil || err2 != nil || err3 != nil {
		return true, ""
	}
	branch := fmt.Sprintf("%d.%d", major, minor)
	if want, ok := minPatchedPHP[branch]; ok {
		if patch < want {
			return true, fmt.Sprintf("PHP %s is vulnerable to CVE-2026-109618/CVE-2026-109619 (RCE); update to %s.%d or newer", version, branch, want)
		}
		return true, ""
	}
	if major < 8 || (major == 8 && minor < 2) {
		return false, fmt.Sprintf("PHP %s is end-of-life and receives no security fixes; upgrade to 8.2 or newer", branch)
	}
	// Newer than any branch we know about: assume supported.
	return true, ""
}

func leadingDigits(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}

// SupportedBranch reports whether a bare "N.N" (or longer) version belongs to a
// branch that still receives security fixes (8.2 or newer). Unparseable input
// is rejected so install paths fail closed.
func SupportedBranch(version string) bool {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return false
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	return major > 8 || (major == 8 && minor >= 2)
}
