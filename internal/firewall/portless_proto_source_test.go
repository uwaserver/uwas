package firewall

import (
	"strings"
	"testing"
)

// F23601: with no port column ufw appends the protocol to the source column
// ("203.0.113.7/tcp", "Anywhere/tcp") — see ufw backend_iptables.get_status.
// The old parse kept "203.0.113.7/tcp" as From, so MoveRule deleted the rule and
// then re-created it with an address ufw rejects ("Bad source address"): the
// rule was lost.
func TestParseUFWRulePortlessProtoSource(t *testing.T) {
	cases := []struct {
		name, line, from, proto, port, to string
	}{
		{"v4 source", "[ 2] Anywhere                   ALLOW IN    203.0.113.7/tcp", "203.0.113.7", "tcp", "", "Anywhere"},
		{"v6 source udp", "[ 3] Anywhere                   DENY IN     2001:db8::7/udp", "2001:db8::7", "udp", "", "Anywhere"},
		{"cidr source tcp", "[ 4] Anywhere                   ALLOW IN    10.0.0.0/8/tcp", "10.0.0.0/8", "tcp", "", "Anywhere"},
		{"blanket proto", "[ 5] Anywhere/tcp               DENY IN     Anywhere/tcp", "Anywhere", "tcp", "", "Anywhere/tcp"},
		// controls: nothing to split
		{"plain source", "[ 6] Anywhere                   ALLOW IN    203.0.113.7", "203.0.113.7", "", "", "Anywhere"},
		{"cidr, no proto", "[ 7] Anywhere                   ALLOW IN    10.0.0.0/8", "10.0.0.0/8", "", "", "Anywhere"},
		{"port rule from ip", "[ 8] 3306/tcp                   ALLOW IN    203.0.113.7", "203.0.113.7", "tcp", "3306", "3306/tcp"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := parseUFWRule(c.line)
			if r.From != c.from || r.Proto != c.proto || r.Port != c.port || r.To != c.to {
				t.Fatalf("got From=%q Proto=%q Port=%q To=%q, want %q %q %q %q",
					r.From, r.Proto, r.Port, r.To, c.from, c.proto, c.port, c.to)
			}
		})
	}
}

// A blanket "deny proto tcp" is not the default deny any/any row.
func TestBlanketProtoDenyIsNotDefaultDeny(t *testing.T) {
	r := parseUFWRule("[ 5] Anywhere/tcp               DENY IN     Anywhere/tcp")
	if isDefaultDeny(r) {
		t.Fatal("deny proto tcp must not count as the default deny any/any")
	}
	if !isDefaultDeny(parseUFWRule("[ 9] Anywhere                   DENY IN     Anywhere")) {
		t.Fatal("control: plain deny any/any must still be the default deny")
	}
}

func TestMoveRulePreservesPortlessProtocol(t *testing.T) {
	rows := func(mid string) []string {
		return []string{
			"22/tcp                     ALLOW IN    Anywhere",
			mid,
			"80/tcp                     ALLOW IN    Anywhere",
			"Anywhere                   DENY IN     Anywhere",
		}
	}
	for _, c := range []struct{ name, mid, want string }{
		{"v4 tcp", "Anywhere                   ALLOW IN    203.0.113.7/tcp", "insert 3 allow from 203.0.113.7 proto tcp"},
		{"v6 udp", "Anywhere                   DENY IN     2001:db8::7/udp", "insert 3 deny from 2001:db8::7 proto udp"},
		{"no proto (control)", "Anywhere                   ALLOW IN    203.0.113.7", "insert 3 allow from 203.0.113.7"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := installScopeFake(t, rows(c.mid)...)
			if err := MoveRule(2, "down"); err != nil {
				t.Fatalf("MoveRule: %v", err)
			}
			got := strings.Join(f.cmds, " | ")
			if !strings.Contains(got, c.want) || strings.Contains(got, "/tcp") || strings.Contains(got, "/udp") {
				t.Fatalf("commands %q, want recreate %q without /proto in the address", got, c.want)
			}
		})
	}
}

// A blanket proto rule has a scoped identity ("Anywhere/tcp"); MoveRule must
// refuse it without deleting anything.
func TestMoveRuleBlanketProtoRefusedWithoutDeleting(t *testing.T) {
	f := installScopeFake(t,
		"22/tcp                     ALLOW IN    Anywhere",
		"Anywhere/tcp               ALLOW IN    Anywhere/tcp",
		"80/tcp                     ALLOW IN    Anywhere",
	)
	if err := MoveRule(2, "down"); err == nil {
		t.Fatal("expected a refusal")
	}
	if len(f.cmds) != 0 {
		t.Fatalf("refused move must not run commands, got %q", f.cmds)
	}
}
