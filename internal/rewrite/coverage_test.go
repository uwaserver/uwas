package rewrite

import (
	"testing"
)

// TestConvertConfigRewritesParseConditionError covers the defensive code path
// in ConvertConfigRewrites where ParseCondition returns an error (line 238-239).
// Currently, the known condition strings ("-f", "!-f", "-d", "!-d", etc.)
// never cause ParseCondition to fail. The "default: continue" case at line
// 234 handles unknown strings by skipping them entirely, so ParseCondition
// is never called with an invalid pattern.
//
// To cover line 238-239, we need a condition string that passes the switch
// but causes ParseCondition to fail. This is currently impossible with the
// existing switch cases. The code is purely defensive.
//
// However, we CAN cover the `is_file` and `is_dir` conditions (without "!")
// to ensure all switch cases are exercised.

func TestConvertConfigRewritesAllConditionVariants(t *testing.T) {
	rewrites := []ConfigRewrite{
		{
			Match:      ".*",
			To:         "/handler",
			Conditions: []string{"!is_file", "!-f", "is_file", "-f", "!is_dir", "!-d", "is_dir", "-d"},
		},
	}
	rules := ConvertConfigRewrites(rewrites)
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(rules))
	}
	// All 8 conditions should parse: !-f, -f, -f, -f, !-d, -d, -d, -d
	if len(rules[0].Conditions) != 8 {
		t.Errorf("conditions = %d, want 8", len(rules[0].Conditions))
	}
}

// TestConvertConfigRewritesStatusOnly covers a rewrite with only a status
// code and no flags (line 245-247).
func TestConvertConfigRewritesStatusOnly(t *testing.T) {
	rewrites := []ConfigRewrite{
		{
			Match:  "^/old$",
			To:     "/new",
			Status: 308,
		},
	}
	rules := ConvertConfigRewrites(rewrites)
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(rules))
	}
	if rules[0].Flags.Redirect != 308 {
		t.Errorf("redirect = %d, want 308", rules[0].Flags.Redirect)
	}
	// With redirect set, Last should NOT be forced
	if rules[0].Flags.Last {
		t.Error("should not have Last flag when redirect is set")
	}
}

// TestConvertConfigRewritesDefaultLast covers the default Last flag
// (line 250-252) when there's no chain and no redirect.
func TestConvertConfigRewritesDefaultLast(t *testing.T) {
	rewrites := []ConfigRewrite{
		{
			Match: "^/test$",
			To:    "/result",
		},
	}
	rules := ConvertConfigRewrites(rewrites)
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(rules))
	}
	if !rules[0].Flags.Last {
		t.Error("should have Last flag by default")
	}
}

// TestEvalConditionsComplexOrAnd covers the OR/AND evaluation logic more
// thoroughly to hit remaining uncovered branches.
func TestEvalConditionsComplexOrAnd(t *testing.T) {
	// Test case: first OR fails, second OR fails, falls through to AND
	cond1, _ := ParseCondition("%{REQUEST_METHOD}", "^DELETE$", "[OR]")
	cond2, _ := ParseCondition("%{REQUEST_METHOD}", "^PATCH$", "[OR]")
	cond3, _ := ParseCondition("%{REQUEST_URI}", "^/api", "")

	rule, _ := ParseRule(".*", "/handler", "L")
	rule.Conditions = []Condition{*cond1, *cond2, *cond3}

	engine := NewEngine([]*Rule{rule})

	// GET /api: both OR conditions fail, cond3 matches
	vars := &Variables{RequestMethod: "GET", RequestURI: "/api/test"}
	result := engine.Process("/api/test", "", vars)
	// OR group both fail -> groupResult false, cond3 AND with false -> still false
	if result.Modified {
		t.Error("GET: should not be modified (OR group both fail)")
	}
}

// TestProcessRedirectWithEmptyQuery covers the redirect path when
// result.Query is empty (line 102-103 condition false).
func TestProcessRedirectWithEmptyQuery(t *testing.T) {
	rule, _ := ParseRule("^/old$", "/new", "R=301")
	engine := NewEngine([]*Rule{rule})
	vars := &Variables{RequestURI: "/old"}

	result := engine.Process("/old", "", vars)
	if !result.Redirect {
		t.Fatal("should be redirect")
	}
	if result.URI != "/new" {
		t.Errorf("URI = %q, want /new (no query appended)", result.URI)
	}
}

// TestConditionSymlink covers the -l test type for symlinks.
func TestConditionSymlink(t *testing.T) {
	cond, _ := ParseCondition("%{REQUEST_FILENAME}", "-l", "")
	vars := &Variables{RequestFilename: "/nonexistent/path"}
	matched, _ := cond.Evaluate(vars)
	if matched {
		t.Error("-l should not match non-existent path")
	}
}

// TestConditionSizeGtZero covers the -s test type.
func TestConditionSizeGtZero(t *testing.T) {
	cond, _ := ParseCondition("%{REQUEST_FILENAME}", "-s", "")
	vars := &Variables{RequestFilename: "/nonexistent/path"}
	matched, _ := cond.Evaluate(vars)
	if matched {
		t.Error("-s should not match non-existent path")
	}
}
