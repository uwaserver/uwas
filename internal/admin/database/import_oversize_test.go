package database

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An import runs the uploaded SQL, so a dump over the limit must be refused,
// not cut at the limit and executed. The body was wrapped in MaxBytesReader
// (which reports an over-limit body) and then in an io.LimitReader of the same
// size, which stops at the limit with a clean EOF: MaxBytesReader never saw the
// excess, the first N bytes were handed to the SQL client, and a half-statement
// dump was imported as if it were whole.
func TestImportRefusesOversizedDumpInsteadOfTruncating(t *testing.T) {
	old, oldDocker := importMaxBytes, dockerImportMaxBytes
	importMaxBytes, dockerImportMaxBytes = 1024, 1024
	t.Cleanup(func() { importMaxBytes, dockerImportMaxBytes = old, oldDocker })

	over := strings.Repeat("-- filler\n", 300) // 3000 bytes
	exact := strings.Repeat("x", 1024)

	type call func(*Handler, http.ResponseWriter, *http.Request)
	for name, c := range map[string]call{
		"Import": func(h *Handler, w http.ResponseWriter, r *http.Request) {
			r.SetPathValue("name", "shop")
			h.Import(w, r)
		},
		"DockerImport": func(h *Handler, w http.ResponseWriter, r *http.Request) {
			r.SetPathValue("name", "c1")
			r.SetPathValue("db", "shop")
			h.DockerImport(w, r)
		},
	} {
		rec := httptest.NewRecorder()
		c(New(&guardDeps{}), rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(over)))
		t.Logf("EXPECTED: 413\nACTUAL: %s -> %d %s", name, rec.Code, strings.TrimSpace(rec.Body.String()))
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("PROBLEM CONFIRMED: %s: a dump over the limit answered %d, want 413 (it was cut and executed)", name, rec.Code)
		}

		// Control: a body of exactly the limit is not refused for its size.
		rec = httptest.NewRecorder()
		c(New(&guardDeps{}), rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(exact)))
		if rec.Code == http.StatusRequestEntityTooLarge {
			t.Errorf("%s: a body of exactly the limit was refused as too large", name)
		}
	}
}
