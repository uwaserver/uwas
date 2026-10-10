package firewall

import "testing"

func TestRuleExistsProtocolSpecificDoesNotCoverAllProtocols(t *testing.T) {
	tcp := []Rule{{Number: 1, Action: "DENY", Port: "53", Proto: "tcp"}}
	anyp := []Rule{{Number: 1, Action: "DENY", Port: "53"}}
	udp6 := []Rule{{Number: 1, Action: "ALLOW", Port: "53", Proto: "udp", V6: true}}
	cases := []struct {
		name  string
		rules []Rule
		act   string
		proto string
		want  bool
	}{
		{"tcp rule, all-proto request", tcp, "deny", "", false},
		{"tcp rule, tcp request", tcp, "deny", "tcp", true},
		{"tcp rule, TCP uppercase request", tcp, "deny", "TCP", true},
		{"tcp rule, udp request", tcp, "deny", "udp", false},
		{"any rule, all-proto request", anyp, "deny", "", true},
		{"any rule, tcp request", anyp, "deny", "tcp", true},
		{"udp rule, all-proto allow", udp6, "allow", "", false},
		{"different action", tcp, "allow", "tcp", false},
	}
	for _, c := range cases {
		if got := ruleExists(c.rules, c.act, "53", c.proto, ""); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}
