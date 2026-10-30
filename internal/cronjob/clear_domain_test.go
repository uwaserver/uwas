package cronjob

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// F53603: ClearDomain drops every job of one domain, nothing else, and
// rewrites the persisted history.
func TestClearDomainDropsOnlyThatDomain(t *testing.T) {
	dir := t.TempDir()
	m := NewMonitor(dir)
	m.RecordExecution(ExecutionRecord{Domain: "a.example", Command: "one", Success: true, Output: "A-OUT"})
	m.RecordExecution(ExecutionRecord{Domain: "a.example", Command: "two", Success: true})
	m.RecordExecution(ExecutionRecord{Domain: "ab.example", Command: "one", Success: true})

	m.ClearDomain("a.example")

	if m.GetStatus("a.example", "one") != nil || m.GetStatus("a.example", "two") != nil {
		t.Fatal("history of the cleared domain survived")
	}
	if m.GetStatus("ab.example", "one") == nil {
		t.Fatal("control: a domain sharing a prefix must keep its history")
	}
	data, err := os.ReadFile(filepath.Join(dir, "cron_history.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" || strings.Contains(string(data), "A-OUT") {
		t.Fatalf("persisted history still holds cleared output: %s", data)
	}
	m.ClearDomain("never.example") // no-op must not panic or rewrite
}
