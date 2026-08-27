package deploy

import (
	"encoding/json"
	"testing"

	"github.com/uwaserver/uwas/internal/apps"
)

// The dashboard reads `{ok, checks}` off this endpoint. A sub-package
// extraction once replaced the wrapper with the bare check slice, so the Apps
// page read `.checks` off an array and crashed with
// "undefined is not an object (evaluating 'R?.checks.length')".
//
// The assertion is made on the serialised JSON, because the contract that
// matters is the wire shape the browser receives.
func TestAppDeployPreflightResponseShape(t *testing.T) {
	body, err := json.Marshal(AppDeployPreflightResponse{
		OK: true,
		Checks: []AppPreflightCheck{
			{Name: "node", Label: "Node.js", OK: true},
			{Name: "npm", Label: "npm", OK: false, Required: true},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("payload is not a JSON object — the dashboard reads .checks off it: %v (%s)", err, body)
	}
	for _, field := range []string{"ok", "checks"} {
		if _, present := decoded[field]; !present {
			t.Errorf("payload has no %q field: %s", field, body)
		}
	}

	var checks []AppPreflightCheck
	if err := json.Unmarshal(decoded["checks"], &checks); err != nil {
		t.Fatalf("checks is not an array: %v", err)
	}
	if len(checks) != 2 {
		t.Errorf("checks length = %d, want 2", len(checks))
	}
}

// The stored git token is a credential; the preflight payload goes to the
// browser, so it must be cleared from the copy that ships.
func TestAppForPreflightResponseStripsGitToken(t *testing.T) {
	if got := appForPreflightResponse(nil); got != nil {
		t.Fatalf("nil app returned %+v, want nil", got)
	}

	stored := &apps.App{Name: "dgn-git"}
	stored.Deploy.GitToken = "ghp_secret_value"

	out := appForPreflightResponse(stored)
	if out.Deploy.GitToken != "" {
		t.Errorf("git token survived into the response payload: %q", out.Deploy.GitToken)
	}
	if stored.Deploy.GitToken != "ghp_secret_value" {
		t.Errorf("the stored definition was mutated; token is now %q", stored.Deploy.GitToken)
	}
	if out.Name != "dgn-git" {
		t.Errorf("name = %q, want dgn-git", out.Name)
	}
}
