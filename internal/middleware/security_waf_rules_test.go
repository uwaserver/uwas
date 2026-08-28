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

// wafIstek runs one request through the guard and reports whether it passed.
func wafIstek(t *testing.T, rules []string, target string) bool {
	t.Helper()

	guard := DomainWAFGuard(logger.New("error", "text"), nil, rules, nil)
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.RemoteAddr = "203.0.113.1:5000"
	return guard(httptest.NewRecorder(), req)
}

const (
	sqlSaldiri   = "/?q=union+select+password+from+users"
	xssSaldiri   = "/?q=%3Cscript%3Ealert(1)%3C/script%3E"
	yolSaldiri   = "/../../etc/shadow"
	kabukSaldiri = "/?x=;cat+/etc/passwd"
)

// No list means every family, which is what has always happened.
func TestWAFEmptyRulesEnforcesEverything(t *testing.T) {
	for _, saldiri := range []string{sqlSaldiri, xssSaldiri, yolSaldiri, kabukSaldiri} {
		if wafIstek(t, nil, saldiri) {
			t.Errorf("kural listesi yokken %q geçti", saldiri)
		}
		if wafIstek(t, []string{}, saldiri) {
			t.Errorf("boş kural listesiyle %q geçti", saldiri)
		}
	}
}

// A list selects: the named family blocks, the others no longer run.
func TestWAFRulesSelectFamilies(t *testing.T) {
	rules := []string{WAFSQLInjection}

	if wafIstek(t, rules, sqlSaldiri) {
		t.Error("sql_injection listelenmişken SQL saldırısı geçti")
	}
	if !wafIstek(t, rules, xssSaldiri) {
		t.Error("yalnızca sql_injection listeliyken XSS de engellendi — rules seçim yapmıyor")
	}
	if !wafIstek(t, rules, yolSaldiri) {
		t.Error("yalnızca sql_injection listeliyken yol geçişi de engellendi")
	}
}

func TestWAFRulesMultipleFamilies(t *testing.T) {
	rules := []string{WAFXSS, WAFPathTraversal}

	if wafIstek(t, rules, xssSaldiri) {
		t.Error("xss listelenmişken XSS geçti")
	}
	if wafIstek(t, rules, yolSaldiri) {
		t.Error("path_traversal listelenmişken yol geçişi geçti")
	}
	if !wafIstek(t, rules, sqlSaldiri) {
		t.Error("listelenmemiş sql_injection hâlâ uygulanıyor")
	}
}

// Case and surrounding space must not change which families run.
func TestWAFRulesNormalised(t *testing.T) {
	if wafIstek(t, []string{" SQL_Injection "}, sqlSaldiri) {
		t.Error("boşluk/büyük harf kural adını bozdu")
	}
}

// A list of only unknown names must not silently disable the WAF. Without
// filtering, the family set is non-nil and matches nothing, so every request
// passes — a typo in the rule list would turn the WAF off.
func TestWAFUnknownRulesDoNotDisableEverything(t *testing.T) {
	for _, saldiri := range []string{sqlSaldiri, xssSaldiri, yolSaldiri} {
		if wafIstek(t, []string{"saçmalık"}, saldiri) {
			t.Errorf("tanınmayan kural WAF'ı kapattı: %q geçti", saldiri)
		}
	}
}

// An unknown name mixed with a real one must be dropped, not widen the set.
func TestWAFUnknownRuleDroppedFromMixedList(t *testing.T) {
	rules := []string{WAFSQLInjection, "saçmalık"}

	if wafIstek(t, rules, sqlSaldiri) {
		t.Error("listelenen sql_injection uygulanmadı")
	}
	if !wafIstek(t, rules, xssSaldiri) {
		t.Error("tanınmayan giriş listeyi genişletti — xss de uygulandı")
	}
}

func TestKnownWAFRule(t *testing.T) {
	for _, ok := range []string{"sql_injection", "XSS", " path_traversal ", "shell_injection", "php"} {
		if !KnownWAFRule(ok) {
			t.Errorf("%q tanınmadı", ok)
		}
	}
	for _, kotu := range []string{"", "sqli", "saçmalık"} {
		if KnownWAFRule(kotu) {
			t.Errorf("%q tanındı", kotu)
		}
	}
}

// The body path must select families too.
func TestWAFRulesApplyToBody(t *testing.T) {
	guard := DomainWAFGuard(logger.New("error", "text"), nil, []string{WAFXSS}, nil)

	govde := func(s string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/submit", strings.NewReader(s))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.RemoteAddr = "203.0.113.1:5000"
		return r
	}

	if guard(httptest.NewRecorder(), govde("a=javascript:alert(1)")) {
		t.Error("xss listelenmişken gövdedeki XSS geçti")
	}
	if !guard(httptest.NewRecorder(), govde("a=union+select+x+from+y")) {
		t.Error("listelenmemiş sql_injection gövdede hâlâ uygulanıyor")
	}
}
