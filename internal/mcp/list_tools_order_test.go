package mcp

import (
	"sort"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
)

// F49302: ListTools ranged over the tool map, so every tools/list answered in
// a different order. An MCP client hands that list to a model as part of its
// prompt: a list that reshuffles on each call defeats prompt caching and makes
// two identical sessions differ for no reason. The order is now the tool
// names, ascending.

func toolNames(t *testing.T, s *Server) []string {
	t.Helper()
	tools := s.ListTools()
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}
	return names
}

func TestListToolsOrderIsStable(t *testing.T) {
	s := New(&config.Config{}, logger.New("error", "text"), metrics.New())
	first := toolNames(t, s)
	if len(first) < 5 {
		t.Fatalf("only %d tools registered, the check needs several", len(first))
	}

	differing := 0
	const calls = 200
	for i := 0; i < calls; i++ {
		got := toolNames(t, s)
		for j := range got {
			if got[j] != first[j] {
				differing++
				break
			}
		}
	}
	t.Logf("EXPECTED: all %d calls list the %d tools in one order\nACTUAL: %d of %d calls differ from the first", calls, len(first), differing, calls)
	if differing != 0 {
		t.Fatalf("PROBLEM CONFIRMED: tools/list changed order in %d of %d calls", differing, calls)
	}
	t.Log("PROBLEM NOT REPRODUCED")
}

func TestListToolsIsSortedByName(t *testing.T) {
	s := New(&config.Config{}, logger.New("error", "text"), metrics.New())
	got := toolNames(t, s)
	if !sort.StringsAreSorted(got) {
		t.Fatalf("tools are not sorted by name: %v", got)
	}
}

// Control: the same tools are listed, each exactly once, with their schema.
func TestListToolsKeepsEveryToolOnce(t *testing.T) {
	s := New(&config.Config{}, logger.New("error", "text"), metrics.New())
	seen := map[string]int{}
	for _, tool := range s.ListTools() {
		seen[tool.Name]++
		if len(tool.InputSchema) == 0 {
			t.Errorf("tool %s lost its input schema", tool.Name)
		}
	}
	for name := range s.tools {
		if seen[name] != 1 {
			t.Errorf("tool %s listed %d times, want 1", name, seen[name])
		}
	}
	if len(seen) != len(s.tools) {
		t.Errorf("listed %d tools, registered %d", len(seen), len(s.tools))
	}
}
