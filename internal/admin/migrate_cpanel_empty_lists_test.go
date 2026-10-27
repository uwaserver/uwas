package admin

import (
	"encoding/json"
	"testing"
)

// F34302: the import answer used to carry JSON null for databases, errors and
// domains_added when the backup had none (nil Go slices). The dashboard reads
// .length on them, so a clean import crashed the page. Lists must be arrays.
func TestCPanelImportEmptyListsAreArrays(t *testing.T) {
	s, _ := testServerWithRoot(t)
	rec := cpanelUploadpost(t, s, "backup", cpanelUploadarchive(t))
	if rec.Code != 200 {
		t.Fatalf("invalid proof: status %d %s", rec.Code, rec.Body.String())
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"domains", "databases", "errors", "domains_added"} {
		raw, ok := got[key]
		if !ok || string(raw) == "null" {
			t.Errorf("PROBLEM CONFIRMED: %q = %s, want a JSON array", key, raw)
		}
	}
}
