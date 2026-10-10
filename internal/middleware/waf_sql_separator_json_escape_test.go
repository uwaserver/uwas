package middleware

import (
	"bytes"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

// SQL keyword rules must accept what SQL accepts between the words (inline
// comments, ALL/DISTINCT, an opening parenthesis), and an oversized JSON body
// must be checked after JSON unescaping (F730, F731).
func TestDomainWAFSQLSeparatorsAndOversizedJSONEscapes(t *testing.T) {
	bs := string(rune(92))
	g := DomainWAFGuard(logger.New("error", "text"), nil, nil, nil)
	get := func(q string) bool {
		return g(httptest.NewRecorder(), httptest.NewRequest("GET", "/i.php?q="+url.QueryEscape(q), nil))
	}
	post := func(ct, body string) bool {
		r := httptest.NewRequest("POST", "/a.php", bytes.NewReader([]byte(body)))
		r.Header.Set("Content-Type", ct)
		return g(httptest.NewRecorder(), r)
	}
	pad := strings.Repeat("A", maxBodyScan+4096)

	blocked := map[string]bool{
		"union all select":    get("1 union all select 1"),
		"union distinct":      get("1 UNION DISTINCT SELECT 1"),
		"union/**/select":     get("1 union/**/select 1"),
		"union(select":        get("1 union(select 1)"),
		"drop/**/table":       get("x drop/**/table u"),
		"form union all":      post("application/x-www-form-urlencoded", "q="+url.QueryEscape("1 union all select pw")),
		"big json \\u0020":    post("application/json", `{"q":"1 union`+bs+`u0020select 1","pad":"`+pad+`"}`),
		"big json php:\\/\\/": post("application/json", `{"q":"php:`+bs+`/`+bs+`/input","pad":"`+pad+`"}`),
	}
	for name, passed := range blocked {
		if passed {
			t.Errorf("%s: passed, want blocked", name)
		}
	}
	allowed := map[string]bool{
		"union station":          get("union station"),
		"european union (sel..)": get("european union (selected members)"),
		"big benign json":        post("application/json", `{"q":"hello","pad":"`+pad+`"}`),
		"escaped backslash":      post("application/json", `{"q":"union`+bs+bs+`u0020select","pad":"`+pad+`"}`),
	}
	for name, passed := range allowed {
		if !passed {
			t.Errorf("%s: blocked, want passed", name)
		}
	}
}
