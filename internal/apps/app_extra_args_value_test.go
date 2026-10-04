package apps

import (
	"testing"
)

func TestDockerExtraArgsRequireTrailingValue(t *testing.T) {
	for _, args := range [][]string{{"--memory"}, {"--env"}, {"--label"}, {"--cpus", "1", "--memory"}} {
		if ValidateExtraArgs(args) == nil {
			t.Fatalf("missing-value accepted %v", args)
		}
	}
	for _, args := range [][]string{nil, {}, {"--init", "--read-only", "--tty"}, {"--env", "A=1"}, {"--memory=512m"}, {"--cpus", "1.5", "--memory", "512m"}} {
		if err := ValidateExtraArgs(args); err != nil {
			t.Fatalf("valid args %v: %v", args, err)
		}
	}
}
