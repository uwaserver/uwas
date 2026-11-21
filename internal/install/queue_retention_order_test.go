package install

import (
	"fmt"
	"testing"
)

// Retention must hold even when nothing calls List(): tasks are only added by
// Submit, which now prunes (F64301).
func TestSubmitEnforcesRetentionWithoutList(t *testing.T) {
	q := New()
	defer q.Stop()
	for i := 0; i < 120; i++ {
		q.Submit("php", "PHP", "install", func(func(string)) error { return nil })
	}
	running := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	q.Submit("php", "PHP", "install", func(func(string)) error { close(running); <-release; return nil })
	<-running // serial worker: every earlier task is already finished

	q.mu.Lock()
	n := len(q.tasks)
	q.mu.Unlock()
	if n > q.maxKeep+1 {
		t.Fatalf("retained %d tasks without List, want <= %d", n, q.maxKeep+1)
	}
}

// GET /api/v1/tasks paginates List(); pages must come from a stable order
// (F64300).
func TestListIsInCreationOrder(t *testing.T) {
	q := New()
	gate := make(chan struct{})
	defer func() { close(gate); q.Stop() }()
	var ids []string
	for i := 0; i < 20; i++ {
		ids = append(ids, q.Submit("package", fmt.Sprintf("p%d", i), "install", func(func(string)) error { <-gate; return nil }).ID)
	}
	for iter := 0; iter < 20; iter++ {
		l := q.List()
		if len(l) != len(ids) {
			t.Fatalf("List len = %d, want %d", len(l), len(ids))
		}
		for i := range l {
			if l[i].ID != ids[i] {
				t.Fatalf("iter %d: List[%d] = %s, want %s", iter, i, l[i].ID, ids[i])
			}
		}
	}
}
