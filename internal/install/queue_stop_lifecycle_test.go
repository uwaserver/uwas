package install

import (
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// queueWorkers counts live Queue worker goroutines.
func queueWorkers() int {
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	return strings.Count(string(buf[:n]), "install.(*Queue).worker")
}

// waitWorkersAtMost polls goroutine state until at most want workers remain.
// The deadline is a failure bound, not an ordering mechanism.
func waitWorkersAtMost(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for queueWorkers() > want {
		if time.Now().After(deadline) {
			t.Fatalf("queue worker did not exit (live=%d, want<=%d)", queueWorkers(), want)
		}
		runtime.Gosched()
	}
}

// F181: a task still queued when Stop is called must never start, and Submit
// after Stop must fail instead of stranding a forever-"queued" task.
func TestStopNeverStartsQueuedTasks(t *testing.T) {
	base := queueWorkers()
	for i := 0; i < 40; i++ {
		q := New()
		gate := make(chan struct{})
		running := make(chan struct{})
		var bRan atomic.Bool
		a := q.Submit("package", "A", "install", func(func(string)) error { close(running); <-gate; return nil })
		b := q.Submit("package", "B", "install", func(func(string)) error { bRan.Store(true); return nil })
		<-running
		q.Stop()
		close(gate)
		waitWorkersAtMost(t, base)

		if bRan.Load() {
			t.Fatalf("trial %d: queued task started after Stop", i)
		}
		if got := q.Get(a.ID); got.Status != StatusDone {
			t.Errorf("trial %d: running task status = %s, want done", i, got.Status)
		}
		if got := q.Get(b.ID); got.Status != StatusError || got.Error != "manager stopped" {
			t.Errorf("trial %d: queued task = %s/%q, want error/manager stopped", i, got.Status, got.Error)
		}
		if q.Active() != nil {
			t.Errorf("trial %d: Active() still reports a task after Stop", i)
		}
	}

	q := New()
	q.Stop()
	q.Stop() // must be a no-op, not a close-of-closed-channel panic
	waitWorkersAtMost(t, base)
	for i := 0; i < 100; i++ {
		task := q.Submit("package", "p", "install", func(func(string)) error {
			t.Error("task function ran on a stopped queue")
			return nil
		})
		if task.Status != StatusError {
			t.Fatalf("submit %d after Stop: status = %s, want error", i, task.Status)
		}
	}
	if q.Active() != nil {
		t.Error("Active() reports a task on a stopped queue")
	}
}

// F182: a panicking task fails that task instead of crashing the process.
func TestPanickingTaskFailsOnlyThatTask(t *testing.T) {
	q := New()
	defer q.Stop()
	p := q.Submit("package", "boom", "install", func(out func(string)) error {
		out("step1\n")
		panic("task bug")
	})
	next := q.Submit("package", "after", "install", func(func(string)) error { return nil })

	deadline := time.Now().Add(10 * time.Second)
	for {
		if got := q.Get(next.ID); got.Status == StatusDone || got.Status == StatusError {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("queue stopped serving after a panicking task")
		}
		runtime.Gosched()
	}
	got := q.Get(p.ID)
	if got.Status != StatusError || got.Error != "task panicked: task bug" || got.Output != "step1\n" {
		t.Errorf("panicking task = %s/%q output=%q", got.Status, got.Error, got.Output)
	}
	if n := q.Get(next.ID); n.Status != StatusDone {
		t.Errorf("next task status = %s, want done", n.Status)
	}
}
