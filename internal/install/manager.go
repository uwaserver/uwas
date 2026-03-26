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
	Type      string     `json:"type"`    // "php", "database", "package"
	Name      string     `json:"name"`    // "PHP 8.5", "MariaDB", "certbot"
	Action    string     `json:"action"`  // "install", "uninstall", "repair"
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

// Manager serializes system package operations and tracks their progress.
type Manager struct {
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

// New creates a new installation manager and starts the queue worker.
func New() *Manager {
	m := &Manager{
		tasks:    make(map[string]*Task),
		queue:    make(chan *queueEntry, 32),
		stopCh:   make(chan struct{}),
		maxKeep:  50,
		keepTime: 30 * time.Minute,
	}
	go m.worker()
	return m
}

// Submit queues a new installation task. Returns the task immediately.
// The task will execute when the queue worker is ready (serialized).
func (m *Manager) Submit(taskType, name, action string, fn TaskFunc) *Task {
	m.mu.Lock()
	m.taskSeq++
	id := fmt.Sprintf("%s-%s-%d", taskType, action, m.taskSeq)
	task := &Task{
		ID:        id,
		Type:      taskType,
		Name:      name,
		Action:    action,
		Status:    StatusQueued,
		CreatedAt: time.Now(),
	}
	m.tasks[id] = task
	m.mu.Unlock()

	m.queue <- &queueEntry{task: task, fn: fn}
	return task
}

// Get returns a task by ID.
func (m *Manager) Get(id string) *Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return nil
	}
	// Return a copy to avoid races
	cp := *t
	return &cp
}

// Active returns the currently running task, if any.
func (m *Manager) Active() *Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tasks {
		if t.Status == StatusRunning || t.Status == StatusQueued {
			cp := *t
			return &cp
		}
	}
	return nil
}

// ActiveByType returns the running/queued task of the given type.
func (m *Manager) ActiveByType(taskType string) *Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tasks {
		if t.Type == taskType && (t.Status == StatusRunning || t.Status == StatusQueued) {
			cp := *t
			return &cp
		}
	}
	return nil
}

// List returns all recent tasks (active + recently completed).
func (m *Manager) List() []Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupLocked()
	result := make([]Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		result = append(result, *t)
	}
	return result
}

// IsRunning returns true if any task is currently running.
func (m *Manager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tasks {
		if t.Status == StatusRunning {
			return true
		}
	}
	return false
}

// QueueLen returns the number of queued + running tasks.
func (m *Manager) QueueLen() int {
	return len(m.queue)
}

// Stop shuts down the queue worker.
func (m *Manager) Stop() {
	close(m.stopCh)
}

func (m *Manager) worker() {
	for {
		select {
		case <-m.stopCh:
			return
		case entry := <-m.queue:
			m.runTask(entry)
		}
	}
}

func (m *Manager) runTask(entry *queueEntry) {
	task := entry.task

	m.mu.Lock()
	now := time.Now()
	task.Status = StatusRunning
	task.StartedAt = &now
	m.mu.Unlock()

	appendOutput := func(line string) {
		m.mu.Lock()
		task.Output += line
		m.mu.Unlock()
	}

	err := entry.fn(appendOutput)

	m.mu.Lock()
	end := time.Now()
	task.EndedAt = &end
	if err != nil {
		task.Status = StatusError
		task.Error = err.Error()
	} else {
		task.Status = StatusDone
	}
	m.mu.Unlock()
}

func (m *Manager) cleanupLocked() {
	cutoff := time.Now().Add(-m.keepTime)
	for id, t := range m.tasks {
		if t.Status != StatusRunning && t.Status != StatusQueued {
			if t.EndedAt != nil && t.EndedAt.Before(cutoff) {
				delete(m.tasks, id)
			}
		}
	}
}
