package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func setupInstallReq(body string) *http.Request {
	return withAdminContext(httptest.NewRequest("POST", "/api/v1/setup/install", strings.NewReader(body)))
}

func TestSetupInstallRejectsNonAdmin(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	r := withResellerContext(httptest.NewRequest("POST", "/api/v1/setup/install",
		strings.NewReader(`{"items":[{"type":"package","id":"redis"}]}`)))
	s.mux.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for non-admin", rec.Code)
	}
}

func TestSetupInstallValidation(t *testing.T) {
	s := testServer()
	cases := []struct {
		name string
		body string
		want int
	}{
		{"bad json", `{bad`, http.StatusBadRequest},
		{"empty items", `{"items":[]}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			s.mux.ServeHTTP(rec, setupInstallReq(tc.body))
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

// TestSetupInstallSkipsAndQueues verifies the batch endpoint skips unknown and
// already-handled items and reports a per-item result, deduping repeats.
func TestSetupInstallSkipsAndQueues(t *testing.T) {
	s := testServer()
	rec := httptest.NewRecorder()
	// - an unknown package → skipped "unknown package"
	// - a duplicate of it → deduped (single result)
	// - an invalid PHP version → skipped "invalid PHP version"
	body := `{"items":[
		{"type":"package","id":"definitely-not-a-real-pkg-xyz"},
		{"type":"package","id":"definitely-not-a-real-pkg-xyz"},
		{"type":"php","id":"not-a-version"},
		{"type":"bogus","id":"x"}
	]}`
	s.mux.ServeHTTP(rec, setupInstallReq(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Items []struct {
			Type, ID, Reason string
			Skipped          bool
			TaskID           string `json:"task_id"`
		} `json:"items"`
		Queued int `json:"queued"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	// 3 unique items (the duplicate package is deduped).
	if len(resp.Items) != 3 {
		t.Fatalf("got %d result items, want 3 (deduped): %+v", len(resp.Items), resp.Items)
	}
	byID := map[string]string{} // id -> reason
	for _, it := range resp.Items {
		if !it.Skipped {
			t.Errorf("item %s/%s should be skipped, got task %q", it.Type, it.ID, it.TaskID)
		}
		byID[it.ID] = it.Reason
	}
	if byID["definitely-not-a-real-pkg-xyz"] != "unknown package" {
		t.Errorf("unknown package reason = %q", byID["definitely-not-a-real-pkg-xyz"])
	}
	if byID["not-a-version"] != "invalid PHP version" {
		t.Errorf("invalid php reason = %q", byID["not-a-version"])
	}
	if resp.Queued != 0 {
		t.Errorf("queued = %d, want 0 (all skipped)", resp.Queued)
	}
}
