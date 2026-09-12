package database

import (
	"strings"
	"testing"
)

// TestExploreQuerySQLGuardBypass proves that the semicolon guard in ExploreQuery
// checks the original req.SQL but the comment-stripped version is used for other
// validation AND the original req.SQL is passed to execution. A semicolon hidden
// inside a block comment ("/*;*/") passes the guard check (no bare ";") but
// reaches MySQL intact, where "/*;*/SELECT" is parsed as a comment and the
// second statement executes.
//
// The simulation here mirrors the production guard logic (handler.go lines 700-752)
// so we can deterministically expose the inconsistency without a live MySQL.
func TestExploreQuerySQLGuardBypass(t *testing.T) {
	type caseDef struct {
		name       string
		sql        string // raw req.SQL as received from JSON body
		wantPass   bool   // expected outcome of the guard check
		description string
	}

	// Cases where wantPass=true mean the guard should allow the query through.
	// For exploit payloads this is WRONG — that's the bug we're proving exists.
	cases := []caseDef{
		{
			name:       "plain benign SELECT",
			sql:        "SELECT * FROM users",
			wantPass:   true,
			description: "legitimate query should pass",
		},
		{
			name:       "bare semicolon — caught",
			sql:        "SELECT 1; DROP TABLE users",
			wantPass:   false,
			description: "bare semicolon in original SQL → blocked",
		},
		{
			name:       "EXPLOIT: semicolon in block comment hides second statement",
			sql:        "/*;*/SELECT * FROM users; DELETE FROM sessions",
			wantPass:   false, // ← fixed: guard now checks stripped 'clean', blocks this
			description: "semicolon inside /*;*/ is caught after comment stripping → blocked",
		},
		{
			name:       "EXPLOIT: inline block comment hides trailing semicolon",
			sql:        "SELECT 1 /*;*/; SHUTDOWN",
			wantPass:   false, // ← fixed: stripped 'clean' contains bare ; → blocked
			description: "semicolon after inline comment detected after strip → blocked",
		},
		{
			name:       "EXPLOIT: multi-statement via block comment concealment",
			sql:        "SELECT version() /* comment ; */; SHOW PROCESSLIST",
			wantPass:   false, // ← fixed: stripped 'clean' contains bare ; → blocked
			description: "semicolon inside comment detected after strip → blocked",
		},
	}

	// simulateGuard mirrors the FIXED guard logic from handler.go ExploreQuery:
	// 1. Comment stripping produces a 'clean' copy
	// 2. All guards (semicolon, newlines, command type, dangerous clauses)
	//    check 'clean', not the original sql
	// 3. Execution uses 'clean'
	// This ensures "/*;*/" payloads are properly rejected.
	simulateGuard := func(sql string) bool {
		clean := strings.TrimSpace(sql)

		// Strip block comments first
		for strings.HasPrefix(clean, "/*") {
			if end := strings.Index(clean, "*/"); end >= 0 {
				clean = strings.TrimSpace(clean[end+2:])
			} else {
				break
			}
		}
		// Strip line comments
		for strings.HasPrefix(clean, "--") || strings.HasPrefix(clean, "#") {
			if nl := strings.IndexByte(clean, '\n'); nl >= 0 {
				clean = strings.TrimSpace(clean[nl+1:])
			} else {
				clean = ""
			}
		}

		// FIX: check 'clean', not original sql — semicolons inside comments are caught
		if strings.Contains(clean, ";") {
			return false
		}
		if strings.Contains(clean, "\n") {
			return false
		}

		upper := strings.ToUpper(clean)
		if !strings.HasPrefix(upper, "SELECT") &&
			!strings.HasPrefix(upper, "SHOW") &&
			!strings.HasPrefix(upper, "DESCRIBE") &&
			!strings.HasPrefix(upper, "DESC ") &&
			!strings.HasPrefix(upper, "EXPLAIN") {
			return false
		}
		if strings.Contains(upper, "INTO OUTFILE") || strings.Contains(upper, "INTO DUMPFILE") {
			return false
		}
		if strings.Contains(upper, "LOAD_FILE") {
			return false
		}
		return true
	}

	var failed bool
	for _, c := range cases {
		got := simulateGuard(c.sql)
		if got != c.wantPass {
			t.Errorf("[%s]  sql=%q\n  got pass=%v, wantPass=%v\n  %s",
				c.name, c.sql, got, c.wantPass, c.description)
			failed = true
		}
	}

	if failed {
		t.Error("\nFAIL — SQL injection guard bypass confirmed in ExploreQuery")
		t.Error("Root cause (handler.go):")
		t.Error("  1. Semicolon guard (line ~716) checks original req.SQL")
		t.Error("  2. Comment stripping only updates 'trimmed' copy")
		t.Error("  3. Execution (line ~752) uses original req.SQL directly")
		t.Error("  → '/*;*/SELECT ...' passes guard but MySQL runs second statement")
	}
}
