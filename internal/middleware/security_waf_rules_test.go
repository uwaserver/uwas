package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

// security.waf.rules was dead configuration: SPECIFICATION.md documents
// `sql_injection | xss | path_traversal` and nothing read it, so every
// WAF-enabled domain got every family whatever it listed.

// wafRequest runs one request through the guard and reports whether it passed.
func wafRequest(t *testing.T, rules []string, target string) bool {
	t.Helper()

	guard := DomainWAFGuard(logger.New("error", "text"), nil, rules, nil)
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.RemoteAddr = "203.0.113.1:5000"
	return guard(httptest.NewRecorder(), req)
}

const (
	sqlAttack       = "/?q=union+select+password+from+users"
	xssAttack       = "/?q=%3Cscript%3Ealert(1)%3C/script%3E"
	traversalAttack = "/../../etc/shadow"
	shellAttack     = "/?x=;cat+/etc/passwd"
)

// No list means every family, which is what has always happened.
func TestWAFEmptyRulesEnforcesEverything(t *testing.T) {
	for _, saldiri := range []string{sqlAttack, xssAttack, traversalAttack, shellAttack} {
		if wafRequest(t, nil, saldiri) {
			t.Errorf("%q passed with no rule list", saldiri)
		}
		if wafRequest(t, []string{}, saldiri) {
			t.Errorf("%q passed with an empty rule list", saldiri)
		}
	}
}

// A list selects: the named family blocks, the others no longer run.
func TestWAFRulesSelectFamilies(t *testing.T) {
	rules := []string{WAFSQLInjection}

	if wafRequest(t, rules, sqlAttack) {
		t.Error("a SQL attack passed with sql_injection listed")
	}
	if !wafRequest(t, rules, xssAttack) {
		t.Error("XSS was blocked too with only sql_injection listed — rules is not selecting")
	}
	if !wafRequest(t, rules, traversalAttack) {
		t.Error("path traversal was blocked too with only sql_injection listed")
	}
}

func TestWAFRulesMultipleFamilies(t *testing.T) {
	rules := []string{WAFXSS, WAFPathTraversal}

	if wafRequest(t, rules, xssAttack) {
		t.Error("XSS passed with xss listed")
	}
	if wafRequest(t, rules, traversalAttack) {
		t.Error("path traversal passed with path_traversal listed")
	}
	if !wafRequest(t, rules, sqlAttack) {
		t.Error("the unlisted sql_injection family still applies")
	}
}

// Case and surrounding space must not change which families run.
func TestWAFRulesNormalised(t *testing.T) {
	if wafRequest(t, []string{" SQL_Injection "}, sqlAttack) {
		t.Error("whitespace or case broke the rule name")
	}
}

// A list of only unknown names must not silently disable the WAF. Without
// filtering, the family set is non-nil and matches nothing, so every request
// passes — a typo in the rule list would turn the WAF off.
func TestWAFUnknownRulesDoNotDisableEverything(t *testing.T) {
	for _, saldiri := range []string{sqlAttack, xssAttack, traversalAttack} {
		if wafRequest(t, []string{"nonsense"}, saldiri) {
			t.Errorf("an unrecognised rule turned the WAF off: %q passed", saldiri)
		}
	}
}

// An unknown name mixed with a real one must be dropped, not widen the set.
func TestWAFUnknownRuleDroppedFromMixedList(t *testing.T) {
	rules := []string{WAFSQLInjection, "nonsense"}

	if wafRequest(t, rules, sqlAttack) {
		t.Error("the listed sql_injection family was not applied")
	}
	if !wafRequest(t, rules, xssAttack) {
		t.Error("an unrecognised entry widened the list — xss applied too")
	}
}

func TestKnownWAFRule(t *testing.T) {
	for _, ok := range []string{"sql_injection", "XSS", " path_traversal ", "shell_injection", "php"} {
		if !KnownWAFRule(ok) {
			t.Errorf("%q was not recognised", ok)
		}
	}
	for _, kotu := range []string{"", "sqli", "nonsense"} {
		if KnownWAFRule(kotu) {
			t.Errorf("%q was recognised", kotu)
		}
	}
}

// The body path must select families too.
func TestWAFRulesApplyToBody(t *testing.T) {
	guard := DomainWAFGuard(logger.New("error", "text"), nil, []string{WAFXSS}, nil)

	body := func(s string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/submit", strings.NewReader(s))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.RemoteAddr = "203.0.113.1:5000"
		return r
	}

	if guard(httptest.NewRecorder(), body("a=javascript:alert(1)")) {
		t.Error("XSS in the body passed with xss listed")
	}
	if !guard(httptest.NewRecorder(), body("a=union+select+x+from+y")) {
		t.Error("the unlisted sql_injection family still applies to the body")
	}
}
