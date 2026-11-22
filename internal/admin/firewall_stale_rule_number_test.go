//go:build linux

package admin

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/firewall"
)

// fakeUFWScript is a minimal stand-in for ufw (status numbered, delete N,
// insert N) over a rule-per-line state file, so the handler runs the real
// firewall package without touching a real firewall.
const fakeUFWScript = `#!/bin/sh
S="$UWAS_FAKE_UFW_STATE"
case "$1" in
status)
  echo "Status: active"; echo
  n=0
  while IFS= read -r l; do n=$((n+1)); echo "[ $n] $l"; done < "$S"
  ;;
--force)
  if [ "$2" = delete ]; then
    case "$3" in ''|*[!0-9]*) exit 1;; esac
    total=$(wc -l < "$S")
    if [ "$3" -lt 1 ] || [ "$3" -gt "$total" ]; then exit 1; fi
    sed -i "$3d" "$S"
  fi
  ;;
insert)
  pos=$2; shift 2
  act=$(echo "$1" | tr a-z A-Z)
  if [ "$2" = from ]; then
    c=""; [ "$4" = comment ] && c=" # $5"
    line="Anywhere   $act IN   $3$c"
  else
    line="$2   $act IN   Anywhere"
  fi
  total=$(wc -l < "$S")
  if [ "$pos" -gt "$total" ]; then echo "$line" >> "$S"; else sed -i "${pos}i $line" "$S"; fi
  ;;
*) exit 0 ;;
esac
`

// TestFirewallDeleteMoveRefuseRenumberedRule: rule numbers are positions and
// the autoblocker's BlockIP inserts at 1, so a number the dashboard read
// before a block named the SSH allow by the time the delete/move arrived
// (F68500, F68501). A request naming the expected rule must get 409 and change
// nothing; the same request without a block in between still acts.
func TestFirewallDeleteMoveRefuseRenumberedRule(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ufw"), []byte(fakeUFWScript), 0o755); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "state")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("UWAS_FAKE_UFW_STATE", state)
	origStatus, origDelete, origMove := firewallGetStatus, firewallDeleteRule, firewallMoveRule
	firewallGetStatus, firewallDeleteRule, firewallMoveRule = firewall.GetStatus, firewall.DeleteRule, firewall.MoveRule
	t.Cleanup(func() { firewallGetStatus, firewallDeleteRule, firewallMoveRule = origStatus, origDelete, origMove })
	seed := func() {
		if err := os.WriteFile(state, []byte("22/tcp   ALLOW IN   Anywhere\n80/tcp   ALLOW IN   Anywhere\n"+
			"443/tcp   ALLOW IN   Anywhere\nAnywhere   DENY IN   Anywhere\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	table := func() string {
		b, _ := os.ReadFile(state)
		return strings.Join(strings.Fields(string(b)), " ")
	}
	s := testServer()
	r80 := func() firewall.Rule {
		for _, r := range firewall.GetStatus().Rules {
			if r.Port == "80" {
				return r
			}
		}
		t.Fatalf("80/tcp not listed: %s", table())
		return firewall.Rule{}
	}
	send := func(move bool, rule firewall.Rule) int {
		rec := httptest.NewRecorder()
		if move {
			body, _ := json.Marshal(map[string]any{"direction": "up", "expect": rule})
			req := httptest.NewRequest("POST", "/", strings.NewReader(string(body)))
			req.SetPathValue("number", fmt.Sprint(rule.Number))
			s.handleFirewallMove(rec, withAdminContext(req))
		} else {
			body, _ := json.Marshal(map[string]any{"expect": rule})
			req := httptest.NewRequest("DELETE", "/", strings.NewReader(string(body)))
			req.SetPathValue("number", fmt.Sprint(rule.Number))
			s.handleFirewallDelete(rec, withAdminContext(req))
		}
		return rec.Code
	}

	for _, move := range []bool{false, true} {
		seed()
		rule := r80()
		if err := firewall.BlockIP("203.0.113.9", ""); err != nil {
			t.Fatal(err)
		}
		before := table()
		if code := send(move, rule); code != 409 || table() != before {
			t.Errorf("move=%v after a block: status=%d table=%q, want 409 and unchanged %q", move, code, table(), before)
		}

		seed()
		rule = r80()
		if code := send(move, rule); code != 200 {
			t.Errorf("move=%v without a block: status=%d table=%q, want 200", move, code, table())
		}
	}
}
