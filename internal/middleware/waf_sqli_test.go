package middleware

import "testing"

// TestWAFCatchesBooleanTautologies covers the SQL injection family the rule
// set missed entirely. The keyword rules look for union/drop/insert; the
// canonical probe `' OR '1'='1` contains none of them, so the single most
// common injection attempt — and the classic authentication bypass — passed
// straight through while XSS and traversal were caught.
func TestWAFCatchesBooleanTautologies(t *testing.T) {
	attacks := []string{
		"/?id=1' OR '1'='1",
		"/?id=1' or '1'='1'--",
		`/?id=1" OR "1"="1`,
		"/?id=1 OR 1=1",
		"/?id=1 or 1=1--",
		"/?user=admin' AND '1'='1",
		"/?id=1' Or '1' = '1",
		"/login?u=x' or 'a'='a",
	}
	for _, a := range attacks {
		t.Run(a, func(t *testing.T) {
			if !matchesAny(wafURLPatterns, a) {
				t.Errorf("WAF did not flag %q", a)
			}
		})
	}
}

// TestWAFTautologyRuleDoesNotFireOnOrdinaryQueries is the other half. A WAF
// that blocks real visitors is worse than one that misses a probe, so the
// rule is anchored on a quote or a numeric equality rather than on the words
// themselves.
func TestWAFTautologyRuleDoesNotFireOnOrdinaryQueries(t *testing.T) {
	benign := []string{
		"/?q=rock and roll",
		"/?search=cats or dogs",
		"/?title=Tom's Diner",
		"/?q=and now for something",
		"/?filter=color&value=red",
		"/?name=O'Brien",
		"/?q=bed and breakfast in Paris",
		"/products?category=shoes&sort=price",
		"/?q=a or b",
		"/search?q=this and that",
	}
	for _, b := range benign {
		t.Run(b, func(t *testing.T) {
			if matchesAny(wafURLPatterns, b) {
				t.Errorf("WAF flagged the ordinary query %q", b)
			}
		})
	}
}

func matchesAny(rules []wafRule, s string) bool {
	for _, r := range rules {
		if r.re.MatchString(s) {
			return true
		}
	}
	return false
}
