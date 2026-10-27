package admin

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/firewall"
)

// F34000: a firewall rule the request got wrong must answer 400, not 500; a
// failure of the firewall backend stays 500.
func TestFirewallRuleRejectionStatus(t *testing.T) {
	origAllow, origDeny, origMove := firewallAllowPort, firewallDenyPort, firewallMoveRule
	defer func() { firewallAllowPort, firewallDenyPort, firewallMoveRule = origAllow, origDeny, origMove }()

	var failWith error
	firewallAllowPort = func(port, proto, from string) error { return failWith }
	firewallDenyPort = func(port, proto, from string) error { return failWith }
	firewallMoveRule = func(n int, dir string) error { return failWith }

	s := testServer()
	invalid := fmt.Errorf("invalid port: 99999: %w", firewall.ErrInvalidRule)
	backend := errors.New("firewall rule failed")

	for _, c := range []struct {
		name string
		err  error
		want int
	}{{"client error", invalid, 400}, {"backend failure (control)", backend, 500}} {
		failWith = c.err
		for _, route := range []string{"allow", "deny", "move"} {
			rec := httptest.NewRecorder()
			switch route {
			case "allow":
				s.handleFirewallAllow(rec, withAdminContext(httptest.NewRequest("POST", "/", strings.NewReader(`{"port":"99999","proto":"tcp"}`))))
			case "deny":
				s.handleFirewallDeny(rec, withAdminContext(httptest.NewRequest("POST", "/", strings.NewReader(`{"port":"99999","proto":"tcp"}`))))
			case "move":
				req := httptest.NewRequest("POST", "/", strings.NewReader(`{"direction":"sideways"}`))
				req.SetPathValue("number", "1")
				s.handleFirewallMove(rec, withAdminContext(req))
			}
			if rec.Code != c.want {
				t.Errorf("%s %s: status = %d, want %d", route, c.name, rec.Code, c.want)
			}
		}
	}
}
