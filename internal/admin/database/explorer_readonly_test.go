package database

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dbpkg "github.com/uwaserver/uwas/internal/database"
)

// The DB explorer is a read-only console: security-report/sc-sqli-results.md
// records it as a "read-only allowlist (SELECT/SHOW/DESCRIBE/EXPLAIN only)",
// and the handler's own 403 message says the same.
//
// EXPLAIN ANALYZE (MySQL 8.0.18+) breaks that. It is the one EXPLAIN form that
// does not merely plan — it *runs* the statement it prefixes, accepting
// SELECT, INSERT, UPDATE, DELETE and TABLE, then discarding the result set. It
// passed the EXPLAIN allowlist prefix test and reached the executor, so
// `EXPLAIN ANALYZE DELETE FROM users` performed a real write. Every
// write-blocking sub-guard in the handler is gated on a SELECT prefix, so none
// of them applied to it either.
type readonlyTestDeps struct{}

func (readonlyTestDeps) RequireAdmin(w http.ResponseWriter, r *http.Request) bool { return true }
func (readonlyTestDeps) RequirePin(w http.ResponseWriter, r *http.Request) bool   { return true }
func (readonlyTestDeps) LogInfo(msg string, args ...any)                       {}
func (readonlyTestDeps) LogDebug(msg string, args ...any)                      {}
func (readonlyTestDeps) LogError(msg string, args ...any)                      {}
func (readonlyTestDeps) RecordAudit(r *http.Request, action, detail string, success bool) {
}
func (readonlyTestDeps) ParsePagination(r *http.Request) (limit, offset int) { return 50, 0 }
func (readonlyTestDeps) TaskActive() *TaskInfo                               { return nil }
func (readonlyTestDeps) TaskSubmit(category, name, action string, fn func(func(string)) error) *TaskInfo {
	return nil
}
func (readonlyTestDeps) StartService() error             { return nil }
func (readonlyTestDeps) StopService() error              { return nil }
func (readonlyTestDeps) RestartService() error           { return nil }
func (readonlyTestDeps) RepairService() (string, error)  { return "", nil }
func (readonlyTestDeps) Uninstall() (string, error)      { return "", nil }
func (readonlyTestDeps) ForceUninstall() (string, error) { return "", nil }
func (readonlyTestDeps) CreateDB(name, user, password, host string) (*dbpkg.CreateResult, error) {
	return nil, nil
}
func (readonlyTestDeps) DropDB(name, user, host string) error { return nil }

// exploreStatus drives the real ExploreQuery handler and returns its HTTP status.
// A guard rejection is 403; a statement that passes validation and reaches
// dbpkg.RunSQL surfaces as 400 in an environment without a mysql binary. That
// difference is what makes the guard decision observable without a live MySQL.
func exploreStatus(t *testing.T, sql string) int {
	t.Helper()
	h := New(readonlyTestDeps{})

	body, _ := json.Marshal(map[string]any{"sql": sql, "limit": 10})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/database/explore/shopdb/query", strings.NewReader(string(body)))
	r.SetPathValue("db", "shopdb")
	w := httptest.NewRecorder()

	h.ExploreQuery(w, r)
	return w.Code
}

func TestExplorerRejectsExplainAnalyzeWrites(t *testing.T) {
	writes := []string{
		"EXPLAIN ANALYZE DELETE FROM users",
		"EXPLAIN ANALYZE UPDATE users SET admin=1 WHERE id=1",
		"EXPLAIN ANALYZE INSERT INTO audit (msg) VALUES ('x')",
		"explain analyze delete from users",
		"ExPlAiN AnAlYzE Truncate TABLE orders",
		// MySQL accepts any run of whitespace between keywords, so a literal
		// prefix check is bypassable; the guard compares on fields.
		"EXPLAIN  ANALYZE DELETE FROM users",
		"EXPLAIN\tANALYZE DROP TABLE users",
		"EXPLAIN   ANALYZE   SELECT * FROM users",
	}
	for _, sql := range writes {
		t.Run(sql, func(t *testing.T) {
			if code := exploreStatus(t, sql); code != http.StatusForbidden {
				t.Errorf("FAIL: %q was admitted by the read-only explorer (HTTP %d, want 403); "+
					"EXPLAIN ANALYZE executes the statement it prefixes", sql, code)
			}
		})
	}
}

// Controls — these pass before and after the fix, so they pin that the guard
// did not become over-broad.
func TestExplorerStillAllowsReadOnlyStatements(t *testing.T) {
	readOnly := []string{
		"SELECT id FROM users",
		"SELECT * FROM users LIMIT 10",
		"SHOW TABLES",
		"DESCRIBE users",
		"DESC users",
		// Plain EXPLAIN only produces a plan; it does not execute.
		"EXPLAIN SELECT * FROM users",
		"EXPLAIN DELETE FROM users",
	}
	for _, sql := range readOnly {
		t.Run(sql, func(t *testing.T) {
			if code := exploreStatus(t, sql); code == http.StatusForbidden {
				t.Errorf("%q was rejected (HTTP 403); it is read-only and must stay allowed", sql)
			}
		})
	}
}

func TestExplorerStillRejectsBareWrites(t *testing.T) {
	for _, sql := range []string{
		"DELETE FROM users",
		"UPDATE users SET admin=1",
		"DROP TABLE users",
		"INSERT INTO audit (msg) VALUES ('x')",
		"TRUNCATE TABLE orders",
	} {
		t.Run(sql, func(t *testing.T) {
			if code := exploreStatus(t, sql); code != http.StatusForbidden {
				t.Errorf("%q returned HTTP %d, want 403", sql, code)
			}
		})
	}
}
