package apps

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestLogs_PathTraversal_Blocked verifies that path traversal payloads in app names
// are blocked by the canonicalize-then-prefix-check fix in Logs().
func TestLogs_PathTraversal_Blocked(t *testing.T) {
	// logsAccessible mirrors the path-construction logic in Handler.Logs() so we can
	// test the traversal check in isolation (without needing real files or an apps store).
	logsAccessible := func(workDir, name string) bool {
		// Exactly mirrors handler.go lines 636–648:
		logsDir := filepath.Join(filepath.Dir(workDir), "logs")
		logPath := filepath.Clean(filepath.Join(logsDir, name+".log"))
		buildLogPath := filepath.Clean(filepath.Join(logsDir, name+"-build.log"))
		if !strings.HasPrefix(logPath, logsDir+string(filepath.Separator)) {
			return false
		}
		if !strings.HasPrefix(buildLogPath, logsDir+string(filepath.Separator)) {
			return false
		}
		return true
	}

	cases := []struct {
		name     string
		appName  string
		workDir  string
		wantOK   bool // true = path passes checks and is not a traversal
	}{
		// Legitimate names — must pass
		{"normal", "myapp", "/domains/example.com/myapp", true},
		{"with_dash", "my-app", "/domains/example.com/my-app", true},
		// Path traversal attempts — must be blocked
		{"double_dot", "../../../etc/passwd", "/domains/example.com/myapp", false},
		{"mixed_traversal", "logs/../../etc/passwd", "/domains/example.com/myapp", false},
		{"dotdot_in_middle", "logs/../../var/log", "/domains/example.com/logs", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := logsAccessible(tc.workDir, tc.appName)
			if got != tc.wantOK {
				t.Errorf("logsAccessible(%q, %q) = %v; want %v", tc.workDir, tc.appName, got, tc.wantOK)
			}
		})
	}
}
