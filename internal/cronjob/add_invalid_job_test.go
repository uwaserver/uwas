package cronjob

import (
	"errors"
	"strings"
	"testing"
)

// F33400: every rejection Add makes before the crontab is touched matches
// ErrInvalidJob, so callers can answer 400 instead of 500. The message is
// unchanged.
func TestAddValidationErrorsMatchErrInvalidJob(t *testing.T) {
	old := runtimeGOOS
	runtimeGOOS = "linux"
	defer func() { runtimeGOOS = old }()

	cases := []struct {
		name string
		job  Job
		want string
	}{
		{"schedule words", Job{Schedule: "daily", Command: "echo hi"}, "invalid cron schedule"},
		{"newline", Job{Schedule: "* * * * *", Command: "echo\nid"}, "newlines"},
		{"substitution", Job{Schedule: "* * * * *", Command: "echo $(id)"}, ""},
		{"escaped percent", Job{Schedule: "* * * * *", Command: `date +\%s`}, `"\%"`},
	}
	for _, c := range cases {
		err := Add(c.job)
		if !errors.Is(err, ErrInvalidJob) {
			t.Fatalf("%s: err = %v, want ErrInvalidJob", c.name, err)
		}
		if c.want != "" && !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s: message %q lost %q", c.name, err, c.want)
		}
	}
}

func TestAddWindowsIsNotInvalidJob(t *testing.T) {
	old := runtimeGOOS
	runtimeGOOS = "windows"
	defer func() { runtimeGOOS = old }()
	if err := Add(Job{Schedule: "* * * * *", Command: "echo hi"}); errors.Is(err, ErrInvalidJob) {
		t.Fatalf("platform refusal must not read as a bad job: %v", err)
	}
}
