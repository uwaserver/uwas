package htaccess

import "testing"

// F27500: a backslash before the quote character or another backslash inside a
// quoted argument is an escape, as in Apache. The escaped quote used to end the
// argument, so JSON header values (Report-To, NEL) came out with stray
// backslashes and the quote state was inverted for the rest of the line.
func TestSplitArgsQuotedEscapes(t *testing.T) {
	cases := []struct {
		name string
		line string
		want []string
	}{
		{"plain", `Header set X "a b"`, []string{"Header", "set", "X", "a b"}},
		{"other backslash kept", `Header set X "a\.b"`, []string{"Header", "set", "X", `a\.b`}},
		{"escaped quote", `Header set X "{\"g\":\"d\"}"`, []string{"Header", "set", "X", `{"g":"d"}`}},
		{"escaped backslash", `Header set X "a\\b"`, []string{"Header", "set", "X", `a\b`}},
		{"escaped quote keeps spaces", `Header set X "say \"hi\" now" Y`, []string{"Header", "set", "X", `say "hi" now`, "Y"}},
		{"single quote escape", `Header set X 'it\'s'`, []string{"Header", "set", "X", "it's"}},
		{"single quote inside double stays", `Header set X "it\'s"`, []string{"Header", "set", "X", `it\'s`}},
		{"trailing backslash in quote", `Header set X "a\`, []string{"Header", "set", "X", `a\`}},
		{"escaped backslash closes at quote", `Header set X "a\\" Y`, []string{"Header", "set", "X", `a\`, "Y"}},
	}
	for _, c := range cases {
		got := splitArgs(c.line)
		if len(got) != len(c.want) {
			t.Errorf("%s: %q -> %q, want %q", c.name, c.line, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: arg %d = %q, want %q", c.name, i, got[i], c.want[i])
			}
		}
	}
}
