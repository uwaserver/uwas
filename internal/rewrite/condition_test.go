package rewrite

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOrConditionChaining tests OR condition evaluation.
// With [OR] flag: cond1[OR], cond2 → means (cond1 OR cond2).
// The engine evaluates: if cond1 matches, skip to end of OR group and overall=true.
// If cond1 doesn't match, cond2 is the last AND in the group.
func TestOrConditionChaining(t *testing.T) {
	// Use a single regex that matches both: HTTP_HOST matches "^(a|b)\\.com$"
	cond1, err := ParseCondition("%{HTTP_HOST}", "^a\\.com$", "[OR]")
	if err != nil {
		t.Fatal(err)
	}
	cond2, err := ParseCondition("%{HTTP_HOST}", "^b\\.com$", "[OR]")
	if err != nil {
		t.Fatal(err)
	}
	// Need a trailing AND condition that always passes to end the OR group
	cond3, err := ParseCondition("%{REQUEST_URI}", ".*", "")
	if err != nil {
		t.Fatal(err)
	}

	rule, _ := ParseRule(".*", "/matched", "L")
	rule.Conditions = []Condition{*cond1, *cond2, *cond3}

	engine := NewEngine([]*Rule{rule})

	// Host is a.com (first OR condition matches)
	vars := &Variables{HTTPHost: "a.com", RequestURI: "/test"}
	result := engine.Process("/test", "", vars)
	if result.URI != "/matched" {
		t.Errorf("a.com: URI = %q, want /matched", result.URI)
	}

	// Host is c.com (neither OR matches, but cond3 is AND and matches)
	vars3 := &Variables{HTTPHost: "c.com", RequestURI: "/test"}
	result3 := engine.Process("/test", "", vars3)
	// Since cond1 and cond2 both fail (both have OR), groupResult becomes false.
	// cond3 then does groupResult(false) && matched(true) = false
	// overallResult was false, so evalConditions returns false
	if result3.Modified {
		t.Errorf("c.com: should NOT be modified")
	}
}

// TestOrConditionFirstMatches tests that the first OR condition matching is sufficient.
func TestOrConditionFirstMatches(t *testing.T) {
	cond1, _ := ParseCondition("%{REQUEST_METHOD}", "^GET$", "[OR]")
	cond2, _ := ParseCondition("%{REQUEST_METHOD}", "^POST$", "")

	rule, _ := ParseRule(".*", "/api-handler", "L")
	rule.Conditions = []Condition{*cond1, *cond2}

	engine := NewEngine([]*Rule{rule})

	// GET matches first OR condition
	vars := &Variables{RequestMethod: "GET", RequestURI: "/api"}
	result := engine.Process("/api", "", vars)
	if result.URI != "/api-handler" {
		t.Errorf("GET: URI = %q, want /api-handler", result.URI)
	}
}

// TestConditionNegationWithRegex tests the ! prefix with regex patterns.
func TestConditionNegationWithRegex(t *testing.T) {
	cond, err := ParseCondition("%{HTTP_USER_AGENT}", "!Googlebot", "")
	if err != nil {
		t.Fatal(err)
	}

	// Non-matching user agent (negated, should return true)
	vars := &Variables{HTTPUserAgent: "Mozilla/5.0"}
	matched, _ := cond.Evaluate(vars)
	if !matched {
		t.Error("negated regex should match non-Googlebot user agent")
	}

	// Matching user agent (negated, should return false)
	vars2 := &Variables{HTTPUserAgent: "Googlebot/2.1"}
	matched2, _ := cond2Eval(cond, vars2)
	if matched2 {
		t.Error("negated regex should NOT match Googlebot user agent")
	}
}

func cond2Eval(c *Condition, v *Variables) (bool, []string) {
	return c.Evaluate(v)
}

// TestParseConditionInvalidRegex tests that invalid regex returns an error.
func TestParseConditionInvalidRegex(t *testing.T) {
	_, err := ParseCondition("%{REQUEST_URI}", "[invalid(", "")
	if err == nil {
		t.Error("expected error for invalid regex pattern")
	}
}

// TestParseConditionORFlag tests the OR flag parsing.
func TestParseConditionORFlag(t *testing.T) {
	cond, err := ParseCondition("%{REQUEST_URI}", "^/api", "[OR]")
	if err != nil {
		t.Fatal(err)
	}
	if !cond.OrNext {
		t.Error("OrNext should be true with [OR] flag")
	}
}

// TestParseConditionNegatedFileTests tests negation with file test types.
func TestParseConditionNegatedFileTests(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "exists.txt")
	os.WriteFile(existing, []byte("data"), 0644)

	// !-f on an existing file should return false (file exists, negated)
	cond, _ := ParseCondition("%{REQUEST_FILENAME}", "!-f", "")
	vars := &Variables{RequestFilename: existing}
	matched, _ := cond.Evaluate(vars)
	if matched {
		t.Error("!-f should return false for existing file")
	}

	// !-f on a non-existing file should return true
	vars2 := &Variables{RequestFilename: filepath.Join(dir, "nope")}
	matched2, _ := cond.Evaluate(vars2)
	if !matched2 {
		t.Error("!-f should return true for non-existing file")
	}
}

// TestConditionSymlinkTest tests the -l file test type.
func TestConditionSymlinkTest(t *testing.T) {
	cond, _ := ParseCondition("%{REQUEST_FILENAME}", "-l", "")

	// Non-existing path should not match
	vars := &Variables{RequestFilename: "/nonexistent/path"}
	matched, _ := cond.Evaluate(vars)
	if matched {
		t.Error("-l should not match non-existing path")
	}
}

// TestConditionSizeTest tests the -s file test type.
func TestConditionSizeTest(t *testing.T) {
	dir := t.TempDir()

	// Non-empty file
	fullFile := filepath.Join(dir, "full.txt")
	os.WriteFile(fullFile, []byte("content"), 0644)

	cond, _ := ParseCondition("%{REQUEST_FILENAME}", "-s", "")
	vars := &Variables{RequestFilename: fullFile}
	matched, _ := cond.Evaluate(vars)
	if !matched {
		t.Error("-s should match file with content")
	}

	// Empty file
	emptyFile := filepath.Join(dir, "empty.txt")
	os.WriteFile(emptyFile, []byte(""), 0644)

	vars2 := &Variables{RequestFilename: emptyFile}
	matched2, _ := cond.Evaluate(vars2)
	if matched2 {
		t.Error("-s should not match empty file")
	}
}

// TestConditionRegexCaptures tests that regex conditions return captures.
func TestConditionRegexCaptures(t *testing.T) {
	cond, _ := ParseCondition("%{HTTP_HOST}", "^(www\\.)?(.+)$", "")

	vars := &Variables{HTTPHost: "www.example.com"}
	matched, captures := cond.Evaluate(vars)
	if !matched {
		t.Fatal("should match")
	}
	if len(captures) < 3 {
		t.Fatalf("captures = %d, want at least 3", len(captures))
	}
	if captures[2] != "example.com" {
		t.Errorf("captures[2] = %q, want example.com", captures[2])
	}
}

// TestEvalConditionsAllFalse tests that all-false conditions return false.
func TestEvalConditionsAllFalse(t *testing.T) {
	cond1, _ := ParseCondition("%{REQUEST_URI}", "^/never$", "")
	cond2, _ := ParseCondition("%{REQUEST_URI}", "^/nope$", "")

	rule, _ := ParseRule(".*", "/target", "L")
	rule.Conditions = []Condition{*cond1, *cond2}

	engine := NewEngine([]*Rule{rule})
	vars := &Variables{RequestURI: "/something"}
	result := engine.Process("/something", "", vars)
	if result.Modified {
		t.Error("should not modify when all conditions fail")
	}
}

// TestConditionBackreferencesInTarget tests condition backreferences (%1, %2) in the target.
func TestConditionBackreferencesInTarget(t *testing.T) {
	cond, _ := ParseCondition("%{HTTP_HOST}", "^(www\\.)?(.+)$", "")
	rule, _ := ParseRule("^/(.+)$", "https://%2/$1", "R=301")
	rule.Conditions = []Condition{*cond}

	engine := NewEngine([]*Rule{rule})
	vars := &Variables{HTTPHost: "www.example.com", RequestURI: "/page"}
	result := engine.Process("/page", "", vars)

	if !result.Redirect {
		t.Fatal("should redirect")
	}
	if result.URI != "https://example.com/page" {
		t.Errorf("URI = %q, want https://example.com/page", result.URI)
	}
}
