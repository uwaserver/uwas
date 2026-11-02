package install

import (
	"testing"
	"time"
)

func TestTaskSnapshotsOwnTimestamps(t *testing.T) {
	for _, name := range []string{"Get", "Active", "ActiveByType", "LatestByType", "List"} {
		t.Run(name, func(t *testing.T) {
			at := time.Unix(123, 0)
			q := &Queue{tasks: map[string]*Task{"one": {ID: "one", Type: "php", Status: StatusRunning, StartedAt: &at}}}
			var first *Task
			switch name {
			case "Get":
				first = q.Get("one")
			case "Active":
				first = q.Active()
			case "ActiveByType":
				first = q.ActiveByType("php")
			case "LatestByType":
				first = q.LatestByType("php")
			case "List":
				items := q.List()
				first = &items[0]
			}
			if first == nil || first.StartedAt == nil {
				t.Fatal("fixture")
			}
			ready, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			go func() { close(ready); <-release; *first.StartedAt = time.Unix(456, 0); close(done) }()
			<-ready
			before := q.Get("one")
			close(release)
			<-done
			if before.StartedAt.Unix() != 123 || q.Get("one").StartedAt.Unix() != 123 {
				t.Fatal("StartedAt aliases")
			}
			if first.EndedAt != nil {
				t.Fatal("nil semantics")
			}
		})
	}
	at, end := time.Unix(123, 0), time.Unix(456, 0)
	q := &Queue{tasks: map[string]*Task{"done": {ID: "done", Type: "php", Status: StatusDone, StartedAt: &at, EndedAt: &end}}}
	first := q.Get("done")
	release, done := make(chan struct{}), make(chan struct{})
	go func() { <-release; *first.EndedAt = time.Unix(789, 0); close(done) }()
	before := q.LatestByType("php")
	close(release)
	<-done
	if before.EndedAt.Unix() != 456 || q.Get("done").EndedAt.Unix() != 456 {
		t.Fatal("EndedAt aliases")
	}
	if q.Get("missing") != nil {
		t.Fatal("missing task")
	}
	stopped := New()
	stopped.Stop()
	task := stopped.Submit("php", "php", "install", func(func(string)) error { t.Error("stopped task ran"); return nil })
	if task.EndedAt == nil {
		t.Fatal("stopped task missing end")
	}
	*task.EndedAt = time.Time{}
	if stopped.Get(task.ID).EndedAt.IsZero() {
		t.Fatal("stopped Submit aliases")
	}
}
