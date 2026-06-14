// Package install provides a global task manager for system installations.
// It serializes apt/dpkg operations to prevent lock conflicts and tracks
// task progress so the dashboard can resume monitoring after page navigation.
package install

import (
	"fmt"
	"sync"
	"time"
)

// TaskStatus represents the current state of a task.
type TaskStatus string

const (
	StatusQueued  TaskStatus = "queued"
	StatusRunning TaskStatus = "running"
	StatusDone    TaskStatus = "done"
	StatusError   TaskStatus = "error"
)

// Task represents a single installation/uninstallation operation.
type Task struct {
	ID        string     `json:"id"`
	Type      string     `json:"type"`   // "php", "database", "package"
	Name      string     `json:"name"`   // "PHP 8.5", "MariaDB", "certbot"
	Action    string     `json:"action"` // "install", "uninstall", "repair"
	Status    TaskStatus `json:"status"`
	Output    string     `json:"output"`
	Error     string     `json:"error,omitempty"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// TaskFunc is the function that performs the actual installation work.
// It receives an output callback to stream progress.
type TaskFunc func(appendOutput func(string)) error

// Queue serializes system package operations and tracks their progress.
// It is request-scoped state (no goroutine lifetime that survives the
// process), so it is named Queue rather than Manager — refactor.md A15
// reserves "Manager" for daemon-like owners.
type Queue struct {
	mu       sync.Mutex
	tasks    map[string]*Task
	queue    chan *queueEntry
	stopCh   chan struct{}
	taskSeq  int
	maxKeep  int           // max completed tasks to retain
	keepTime time.Duration // how long to keep completed tasks
}

type queueEntry struct {
	task *Task
	fn   TaskFunc
}

// New creates a new install Queue and starts its background worker.
func New() *Queue {
	q := &Queue{
		tasks:    make(map[string]*Task),
		queue:    make(chan *queueEntry, 32),
		stopCh:   make(chan struct{}),
		maxKeep:  50,
		keepTime: 30 * time.Minute,
	}
	go q.worker()
	return q
}

// Submit queues a new installation task. Returns the task immediately.
// The task will execute when the queue worker is ready (serialized).
func (q *Queue) Submit(taskType, name, action string, fn TaskFunc) *Task {
	q.mu.Lock()
	q.taskSeq++
	id := fmt.Sprintf("%s-%s-%d", taskType, action, q.taskSeq)
	task := &Task{
		ID:        id,
		Type:      taskType,
		Name:      name,
		Action:    action,
		Status:    StatusQueued,
		CreatedAt: time.Now(),
	}
	q.tasks[id] = task
	cp := *task
	q.mu.Unlock()

	select {
	case q.queue <- &queueEntry{task: task, fn: fn}:
	case <-q.stopCh:
		q.mu.Lock()
		task.Status = StatusError
		task.Error = "manager stopped"
		cp = *task
		q.mu.Unlock()
	}
	return &cp
}

// Get returns a task by ID.
func (q *Queue) Get(id string) *Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	t, ok := q.tasks[id]
	if !ok {
		return nil
	}
	// Return a copy to avoid races
	cp := *t
	return &cp
}

// Active returns the currently running task, if any.
func (q *Queue) Active() *Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, t := range q.tasks {
		if t.Status == StatusRunning || t.Status == StatusQueued {
			cp := *t
			return &cp
		}
	}
	return nil
}

// ActiveByType returns the running/queued task of the given type.
func (q *Queue) ActiveByType(taskType string) *Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, t := range q.tasks {
		if t.Type == taskType && (t.Status == StatusRunning || t.Status == StatusQueued) {
			cp := *t
			return &cp
		}
	}
	return nil
}

// LatestByType returns the most recently created task of the given type
// (active or completed). Returns nil if no such task is retained.
func (q *Queue) LatestByType(taskType string) *Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	var latest *Task
	for _, t := range q.tasks {
		if t.Type != taskType {
			continue
		}
		if latest == nil || t.CreatedAt.After(latest.CreatedAt) {
			latest = t
		}
	}
	if latest == nil {
		return nil
	}
	cp := *latest
	return &cp
}

// List returns all recent tasks (active + recently completed).
func (q *Queue) List() []Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.cleanupLocked()
	result := make([]Task, 0, len(q.tasks))
	for _, t := range q.tasks {
		result = append(result, *t)
	}
	return result
}

// IsRunning returns true if any task is currently running.
func (q *Queue) IsRunning() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, t := range q.tasks {
		if t.Status == StatusRunning {
			return true
		}
	}
	return false
}

// Stop shuts down the queue worker.
func (q *Queue) Stop() {
	close(q.stopCh)
}

func (q *Queue) worker() {
	for {
		select {
		case <-q.stopCh:
			return
		case entry := <-q.queue:
			q.runTask(entry)
		}
	}
}

func (q *Queue) runTask(entry *queueEntry) {
	task := entry.task

	q.mu.Lock()
	now := time.Now()
	task.Status = StatusRunning
	task.StartedAt = &now
	q.mu.Unlock()

	appendOutput := func(line string) {
		q.mu.Lock()
		task.Output += line
		q.mu.Unlock()
	}

	err := entry.fn(appendOutput)

	q.mu.Lock()
	end := time.Now()
	task.EndedAt = &end
	if err != nil {
		task.Status = StatusError
		task.Error = err.Error()
	} else {
		task.Status = StatusDone
	}
	q.mu.Unlock()
}

func (q *Queue) cleanupLocked() {
	cutoff := time.Now().Add(-q.keepTime)
	for id, t := range q.tasks {
		if t.Status != StatusRunning && t.Status != StatusQueued {
			if t.EndedAt != nil && t.EndedAt.Before(cutoff) {
				delete(q.tasks, id)
			}
		}
	}
}
