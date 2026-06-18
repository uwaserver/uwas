package install

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubmitAndGet(t *testing.T) {
	m := New()
	defer m.Stop()

	task := m.Submit("php", "PHP 8.4", "install", func(append func(string)) error {
		append("step 1\n")
		append("step 2\n")
		return nil
	})

	if task.ID == "" {
		t.Fatal("task ID should not be empty")
	}
	if task.Status != StatusQueued {
		t.Errorf("initial status = %s, want queued", task.Status)
	}

	// Wait for completion
	for i := 0; i < 50; i++ {
		time.Sleep(50 * time.Millisecond)
		got := m.Get(task.ID)
		if got != nil && got.Status == StatusDone {
			if got.Output != "step 1\nstep 2\n" {
				t.Errorf("output = %q, want step 1+2", got.Output)
			}
			return
		}
	}
	t.Fatal("task did not complete in time")
}

func TestSubmitError(t *testing.T) {
	m := New()
	defer m.Stop()

	task := m.Submit("database", "MariaDB", "install", func(append func(string)) error {
		append("starting...\n")
		return fmt.Errorf("apt lock conflict")
	})

	for i := 0; i < 50; i++ {
		time.Sleep(50 * time.Millisecond)
		got := m.Get(task.ID)
		if got != nil && got.Status == StatusError {
			if got.Error != "apt lock conflict" {
				t.Errorf("error = %q, want 'apt lock conflict'", got.Error)
			}
			return
		}
	}
	t.Fatal("task did not finish with error")
}

func TestSerialExecution(t *testing.T) {
	m := New()
	defer m.Stop()

	var running int32
	var maxConcurrent int32

	run := func(append func(string)) error {
		cur := atomic.AddInt32(&running, 1)
		for {
			old := atomic.LoadInt32(&maxConcurrent)
			if cur <= old || atomic.CompareAndSwapInt32(&maxConcurrent, old, cur) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&running, -1)
		return nil
	}

	// Submit 3 tasks — they must run one at a time
	t1 := m.Submit("package", "nginx", "install", run)
	t2 := m.Submit("package", "redis", "install", run)
	t3 := m.Submit("php", "PHP 8.5", "install", run)

	// Wait for all to complete
	for i := 0; i < 100; i++ {
		time.Sleep(30 * time.Millisecond)
		g1 := m.Get(t1.ID)
		g2 := m.Get(t2.ID)
		g3 := m.Get(t3.ID)
		if g1 != nil && g2 != nil && g3 != nil &&
			g1.Status == StatusDone && g2.Status == StatusDone && g3.Status == StatusDone {
			break
		}
	}

	if atomic.LoadInt32(&maxConcurrent) > 1 {
		t.Errorf("max concurrent = %d, want 1 (serial execution)", maxConcurrent)
	}
}

func TestActive(t *testing.T) {
	m := New()
	defer m.Stop()

	// No active task initially
	if a := m.Active(); a != nil {
		t.Errorf("expected no active task, got %+v", a)
	}

	done := make(chan struct{})
	m.Submit("php", "PHP 8.3", "install", func(append func(string)) error {
		<-done
		return nil
	})

	time.Sleep(50 * time.Millisecond)

	a := m.Active()
	if a == nil {
		t.Fatal("expected an active task")
	}
	if a.Type != "php" {
		t.Errorf("active type = %s, want php", a.Type)
	}

	close(done)
	time.Sleep(50 * time.Millisecond)

	if a2 := m.Active(); a2 != nil {
		t.Errorf("expected no active task after completion, got %+v", a2)
	}
}

func TestActiveByType(t *testing.T) {
	m := New()
	defer m.Stop()

	done := make(chan struct{})
	m.Submit("database", "MariaDB", "install", func(append func(string)) error {
		<-done
		return nil
	})

	time.Sleep(50 * time.Millisecond)

	if a := m.ActiveByType("php"); a != nil {
		t.Errorf("expected no active php task, got %+v", a)
	}
	if a := m.ActiveByType("database"); a == nil {
		t.Fatal("expected active database task")
	}

	close(done)
}

func TestList(t *testing.T) {
	m := New()
	defer m.Stop()

	m.Submit("php", "PHP 8.3", "install", func(append func(string)) error { return nil })
	m.Submit("database", "MariaDB", "install", func(append func(string)) error { return nil })

	time.Sleep(200 * time.Millisecond)

	list := m.List()
	if len(list) != 2 {
		t.Errorf("list length = %d, want 2", len(list))
	}
}

func TestGetNotFound(t *testing.T) {
	m := New()
	defer m.Stop()

	if got := m.Get("nonexistent"); got != nil {
		t.Errorf("expected nil for nonexistent task, got %+v", got)
	}
}

func TestIsRunning(t *testing.T) {
	m := New()
	defer m.Stop()

	if m.IsRunning() {
		t.Error("should not be running initially")
	}

	done := make(chan struct{})
	m.Submit("php", "PHP 8.3", "install", func(append func(string)) error {
		<-done
		return nil
	})

	time.Sleep(50 * time.Millisecond)

	if !m.IsRunning() {
		t.Error("should be running")
	}

	close(done)
	time.Sleep(50 * time.Millisecond)

	if m.IsRunning() {
		t.Error("should not be running after completion")
	}
}

func TestLatestByType(t *testing.T) {
	m := New()
	defer m.Stop()

	// No tasks of this type yet.
	if l := m.LatestByType("php"); l != nil {
		t.Errorf("expected nil for unknown type, got %+v", l)
	}

	first := m.Submit("php", "PHP 8.3", "install", func(append func(string)) error { return nil })
	// Ensure a distinct, later CreatedAt for the second task.
	time.Sleep(10 * time.Millisecond)
	second := m.Submit("php", "PHP 8.4", "install", func(append func(string)) error { return nil })
	// A different type should not affect the php result.
	m.Submit("database", "MariaDB", "install", func(append func(string)) error { return nil })

	// Wait for everything to settle so timestamps are stable.
	time.Sleep(200 * time.Millisecond)

	latest := m.LatestByType("php")
	if latest == nil {
		t.Fatal("expected a latest php task")
	}
	if latest.ID != second.ID {
		t.Errorf("latest php = %s, want %s (first was %s)", latest.ID, second.ID, first.ID)
	}
	if latest.Name != "PHP 8.4" {
		t.Errorf("latest name = %q, want PHP 8.4", latest.Name)
	}

	// A type with no tasks still returns nil even when other tasks exist.
	if l := m.LatestByType("package"); l != nil {
		t.Errorf("expected nil for type with no tasks, got %+v", l)
	}
}

func TestSubmitOnStoppedQueue(t *testing.T) {
	m := New()
	// Stop the worker so it stops draining the queue and stopCh is closed.
	m.Stop()
	// Give the worker a moment to exit so it no longer consumes from q.queue.
	time.Sleep(20 * time.Millisecond)

	// Submit's select races between sending to the buffered queue channel and
	// the closed stopCh. To deterministically exercise the stop branch we must
	// fill the buffer so the send case is not ready, leaving only stopCh.
	m.mu.Lock()
	for i := 0; i < cap(m.queue); i++ {
		m.queue <- &queueEntry{task: &Task{}, fn: func(func(string)) error { return nil }}
	}
	m.mu.Unlock()

	task := m.Submit("package", "nginx", "install", func(append func(string)) error {
		t.Error("task function should not run on a stopped queue")
		return nil
	})

	if task.Status != StatusError {
		t.Errorf("status = %s, want error", task.Status)
	}
	if task.Error != "manager stopped" {
		t.Errorf("error = %q, want 'manager stopped'", task.Error)
	}

	// The stored task should reflect the error state too.
	got := m.Get(task.ID)
	if got == nil {
		t.Fatal("task not retained")
	}
	if got.Status != StatusError {
		t.Errorf("stored status = %s, want error", got.Status)
	}
}

func TestListCleansUpExpiredTasks(t *testing.T) {
	m := New()
	defer m.Stop()
	// Shrink the retention window so completed tasks expire immediately.
	m.mu.Lock()
	m.keepTime = 0
	m.mu.Unlock()

	m.Submit("php", "PHP 8.3", "install", func(append func(string)) error { return nil })

	// Wait for the task to complete (EndedAt set).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !m.IsRunning() {
			// Confirm it actually ran and has an EndedAt before checking cleanup.
			done := false
			m.mu.Lock()
			for _, tk := range m.tasks {
				if tk.EndedAt != nil {
					done = true
				}
			}
			m.mu.Unlock()
			if done {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	// keepTime is 0 so the cutoff is "now"; an already-ended task is before it
	// and List() should drop it via cleanupLocked.
	if list := m.List(); len(list) != 0 {
		t.Errorf("list length = %d, want 0 after cleanup", len(list))
	}
}

func TestListRetainsActiveTasks(t *testing.T) {
	m := New()
	defer m.Stop()
	// Even with an aggressive cutoff, running/queued tasks must be kept.
	m.mu.Lock()
	m.keepTime = 0
	m.mu.Unlock()

	done := make(chan struct{})
	m.Submit("php", "PHP 8.3", "install", func(append func(string)) error {
		<-done
		return nil
	})

	time.Sleep(50 * time.Millisecond)

	if list := m.List(); len(list) != 1 {
		t.Errorf("list length = %d, want 1 (running task retained)", len(list))
	}

	close(done)
}

func TestTaskTimestamps(t *testing.T) {
	m := New()
	defer m.Stop()

	task := m.Submit("php", "PHP 8.3", "install", func(append func(string)) error {
		return nil
	})

	time.Sleep(100 * time.Millisecond)

	got := m.Get(task.ID)
	if got == nil {
		t.Fatal("task not found")
	}
	if got.StartedAt == nil {
		t.Error("StartedAt should be set")
	}
	if got.EndedAt == nil {
		t.Error("EndedAt should be set")
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
}
