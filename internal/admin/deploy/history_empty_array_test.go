package deploy

// F35302: GET /apps/{name}/deploy-history answered {"items": null} for an app
// that has never been deployed (the append-onto-nil copy stays nil). The list
// must be an empty array.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/apps"
)

func deployHistoryBody(t *testing.T, h *Handler, name string) string {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/apps/"+name+"/deploy-history", nil)
	r.SetPathValue("name", name)
	w := httptest.NewRecorder()
	h.DeployHistory(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	return strings.TrimSpace(w.Body.String())
}

func TestDeployHistoryEmptyIsArray(t *testing.T) {
	d := &branchFilterDeps{mgr: apps.NewManager(apps.NewStore(t.TempDir()), nil)}
	h := New(d)

	body := deployHistoryBody(t, h, "never-deployed")
	t.Logf("EXPECTED: items = []\nACTUAL: %s", body)
	if !strings.Contains(body, `"items":[]`) {
		t.Fatalf("PROBLEM CONFIRMED: %s, want \"items\":[]", body)
	}

	// control: a recorded entry is still listed
	h.deployHistoryMu.Lock()
	h.deployHistory["demo"] = []DeployHistoryEntry{{}}
	h.deployHistoryMu.Unlock()
	if body := deployHistoryBody(t, h, "demo"); strings.Contains(body, `"items":null`) || strings.Contains(body, `"items":[]`) {
		t.Fatalf("control: recorded history must be listed, got %s", body)
	}
}
