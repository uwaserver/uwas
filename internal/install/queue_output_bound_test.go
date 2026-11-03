package install

import (
	"strings"
	"testing"
	"time"
)

func TestTaskOutputIsBoundedAndKeepsLatestProgress(t *testing.T) {
	q := New()
	defer q.Stop()

	task := q.Submit("package", "verbose", "install", func(out func(string)) error {
		out(strings.Repeat("x", 3<<20))
		out("latest progress\n")
		return nil
	})

	deadline := time.Now().Add(10 * time.Second)
	for {
		got := q.Get(task.ID)
		if got != nil && got.Status == StatusDone {
			if len(got.Output) > (2<<20)+128 {
				t.Fatalf("retained task output = %d bytes, want about 2 MiB maximum", len(got.Output))
			}
			if !strings.Contains(got.Output, "truncated") || !strings.HasSuffix(got.Output, "latest progress\n") {
				t.Fatalf("bounded output must mark truncation and retain latest progress")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("task did not finish")
		}
		time.Sleep(time.Millisecond)
	}
}
