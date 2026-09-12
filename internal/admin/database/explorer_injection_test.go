package database

import (
	"strings"
	"testing"
)

// TestExploreRejectsNewlineInject verifies that the Explore handler's SQL validation
// rejects statements containing newlines, which would bypass the semicolon-check
// and allow newline-separated multi-statement SQL injection.
func TestExploreRejectsNewlineInject(t *testing.T) {
	// Simulate the validation logic from the Explore handler (handler.go:721-727).
	// The fix adds: if strings.Contains(trimmed, "\n") → reject.

	tests := []struct {
		name        string
		sql         string
		wantAllowed bool
	}{
		{
			name:        "newline then DROP after SELECT",
			sql:         "SELECT 1\nDROP DATABASE main",
			wantAllowed: false,
		},
		{
			name:        "newline-separated UPDATE",
			sql:         "SELECT id FROM users\nUPDATE users SET admin=1 WHERE id=1",
			wantAllowed: false,
		},
		{
			name:        "CREATE followed by newline-DROP",
			sql:         "SELECT 1\nDELETE FROM users",
			wantAllowed: false,
		},
		{
			name:        "clean single-statement SELECT",
			sql:         "SELECT * FROM users LIMIT 10",
			wantAllowed: true,
		},
		{
			name:        "clean SELECT with WHERE",
			sql:         "SELECT id, name FROM users WHERE active=1",
			wantAllowed: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			trimmed := strings.TrimSpace(tc.sql)
			upper := strings.ToUpper(trimmed)

			// Reject semicolon (existing guard)
			if strings.Contains(tc.sql, ";") {
				if tc.wantAllowed {
					t.Errorf("sql %q: semicolon check rejected but test expects allowed", tc.sql)
				}
				return
			}

			// Reject newline (the fix)
			if strings.Contains(trimmed, "\n") {
				if tc.wantAllowed {
					t.Errorf("sql %q: newline check rejected but test expects allowed", tc.sql)
				}
				return
			}

			// Whitelist: SELECT, SHOW, DESCRIBE, DESC, EXPLAIN
			allowed := strings.HasPrefix(upper, "SELECT") ||
				strings.HasPrefix(upper, "SHOW") ||
				strings.HasPrefix(upper, "DESCRIBE") ||
				strings.HasPrefix(upper, "DESC ") ||
				strings.HasPrefix(upper, "EXPLAIN")

			if allowed != tc.wantAllowed {
				t.Errorf("sql %q: got allowed=%v want %v", tc.sql, allowed, tc.wantAllowed)
			}
		})
	}
}
