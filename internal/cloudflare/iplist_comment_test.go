package cloudflare

import (
	"reflect"
	"testing"
)

// A '#' that starts a token comments out the rest of the line. Splitting on
// spaces first used to drop only the "#" token, so "# 198.51.100.0/24" (the
// usual way to comment a range out) kept the CIDR trusted as a Cloudflare range.
func TestNormalizeCIDRsCommentRunsToEndOfLine(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"space after hash", []string{"# 198.51.100.0/24"}, []string{}},
		{"no space after hash", []string{"#198.51.100.0/24"}, []string{}},
		{"trailing comment", []string{"203.0.113.0/24 # 198.51.100.0/24"}, []string{"203.0.113.0/24"}},
		{"comment prose with an address", []string{"203.0.113.0/24\n# was 198.51.100.0/24 before migration"}, []string{"203.0.113.0/24"}},
		{"crlf lines", []string{"203.0.113.0/24\r\n# 198.51.100.0/24\r\n192.0.2.0/24"}, []string{"192.0.2.0/24", "203.0.113.0/24"}},
		{"comma list with comment", []string{"203.0.113.0/24,\t# 198.51.100.0/24"}, []string{"203.0.113.0/24"}},
		{"comment only the next line", []string{"# note\n192.0.2.1"}, []string{"192.0.2.1/32"}},
		{"no comments unchanged", []string{"203.0.113.0/24, 192.0.2.0/24;203.0.113.0/24"}, []string{"192.0.2.0/24", "203.0.113.0/24"}},
	}
	for _, c := range cases {
		got, err := NormalizeCIDRs(c.in)
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.name, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	// A '#' inside a token is not a comment: the token stays invalid.
	if _, err := NormalizeCIDRs([]string{"203.0.113.0/24#x"}); err == nil {
		t.Error("mid-token '#' must not be treated as a comment")
	}
}

func TestIPSetIgnoresCommentedRange(t *testing.T) {
	cidrs := []string{"203.0.113.0/24\n# 198.51.100.0/24", "192.0.2.0/24 # 198.51.100.0/24"}
	s := NewIPSet()
	for ip, want := range map[string]bool{
		"203.0.113.9":  true,
		"192.0.2.9":    true,
		"198.51.100.7": false,
	} {
		if got := s.Contains(ip, cidrs); got != want {
			t.Errorf("Contains(%s) = %v, want %v", ip, got, want)
		}
	}
}
