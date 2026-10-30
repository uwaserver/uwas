package monitor

import (
	"fmt"
	"sort"
	"testing"
)

// F49304: Results ranged over the result map, so GET /api/v1/monitor answered
// the same domains in a different order on every call. Anything that polls it
// and diffs, caches or renders the list in server order saw the rows shuffle
// with no change in the data. The list is now ordered by host.

func hostsOf(rs []HealthResult) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Host
	}
	return out
}

func TestResultsOrderIsStable(t *testing.T) {
	m := New(nil, testLogger())
	m.resultsMu.Lock()
	for i := 0; i < 12; i++ {
		h := fmt.Sprintf("site-%02d.example.com", (i*7)%12)
		m.results[h] = &HealthResult{Host: h, Status: "up"}
	}
	m.resultsMu.Unlock()

	first := hostsOf(m.Results())
	differing := 0
	const calls = 200
	for i := 0; i < calls; i++ {
		got := hostsOf(m.Results())
		for j := range got {
			if got[j] != first[j] {
				differing++
				break
			}
		}
	}
	t.Logf("EXPECTED: all %d calls list the %d hosts in one order\nACTUAL: %d of %d calls differ from the first", calls, len(first), differing, calls)
	if differing != 0 {
		t.Fatalf("PROBLEM CONFIRMED: Results changed order in %d of %d calls", differing, calls)
	}
	t.Log("PROBLEM NOT REPRODUCED")
}

func TestResultsIsSortedByHost(t *testing.T) {
	m := New(nil, testLogger())
	m.resultsMu.Lock()
	for i := 0; i < 12; i++ {
		h := fmt.Sprintf("site-%02d.example.com", (i*7)%12)
		m.results[h] = &HealthResult{Host: h, Status: "up"}
	}
	m.resultsMu.Unlock()
	if got := hostsOf(m.Results()); !sort.StringsAreSorted(got) {
		t.Fatalf("results are not sorted by host: %v", got)
	}
}

// Control: every result is returned once, and the check history is a copy.
func TestResultsKeepsEveryResultAndCopiesChecks(t *testing.T) {
	m := New(nil, testLogger())
	m.resultsMu.Lock()
	m.results["a.test"] = &HealthResult{Host: "a.test", Checks: []Check{{StatusCode: 200}}}
	m.results["b.test"] = &HealthResult{Host: "b.test"}
	m.resultsMu.Unlock()

	got := m.Results()
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	for i := range got {
		if got[i].Host == "a.test" {
			got[i].Checks[0].StatusCode = 500
		}
	}
	m.resultsMu.RLock()
	defer m.resultsMu.RUnlock()
	if m.results["a.test"].Checks[0].StatusCode != 200 {
		t.Error("Results handed out the live check history")
	}
}
