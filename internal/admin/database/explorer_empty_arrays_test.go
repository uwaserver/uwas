package database

// F35301: the DB explorer answered `null` instead of `[]` for a result set
// with no rows (a mysql client in batch mode prints nothing at all for one),
// and for a table without columns. Scripts that walk `rows` break on null.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stubClientOutput(t *testing.T, output string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\ncat >/dev/null\nprintf '" + output + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "mariadb"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func exploreQueryJSON(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	h := New(readonlyTestDeps{})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/database/explore/shopdb/query", strings.NewReader(`{"sql":"SELECT id FROM t WHERE 1=0"}`))
	r.SetPathValue("db", "shopdb")
	w := httptest.NewRecorder()
	h.ExploreQuery(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestExploreQueryNoRowsAreEmptyArrays(t *testing.T) {
	cases := []struct {
		name, output, wantRows, wantCols string
	}{
		{"no output at all", ``, `[]`, `[]`},
		{"header only", `id\n`, `[]`, `["id"]`},
		{"control: one row", `id\n1\n`, `[["1"]]`, `["id"]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stubClientOutput(t, c.output)
			got := exploreQueryJSON(t)
			t.Logf("EXPECTED: rows=%s columns=%s\nACTUAL: rows=%s columns=%s", c.wantRows, c.wantCols, got["rows"], got["columns"])
			if string(got["rows"]) != c.wantRows || string(got["columns"]) != c.wantCols {
				t.Fatalf("PROBLEM CONFIRMED: rows=%s columns=%s, want rows=%s columns=%s", got["rows"], got["columns"], c.wantRows, c.wantCols)
			}
		})
	}
}

func TestExploreColumnsNoneIsEmptyArray(t *testing.T) {
	stubClientOutput(t, ``)
	h := New(readonlyTestDeps{})
	r := httptest.NewRequest(http.MethodGet, "/api/v1/database/explore/shopdb/tables/t", nil)
	r.SetPathValue("db", "shopdb")
	r.SetPathValue("table", "t")
	w := httptest.NewRecorder()
	h.ExploreColumns(w, r)
	body := strings.TrimSpace(w.Body.String())
	t.Logf("EXPECTED: []\nACTUAL: %s", body)
	if body != "[]" {
		t.Fatalf("PROBLEM CONFIRMED: columns = %s, want []", body)
	}
}
