package firewall

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// F53300: a host without ufw (or with ufw inactive and nothing staged) must
// report "rules": [] rather than null.
func TestGetStatusRulesNeverNull(t *testing.T) {
	old := execLookPathFn
	defer func() { execLookPathFn = old }()
	execLookPathFn = func(string) (string, error) { return "", errors.New("no ufw") }
	b, _ := json.Marshal(GetStatus())
	if !strings.Contains(string(b), `"rules":[]`) {
		t.Fatalf("status = %s, want rules:[]", b)
	}
}
