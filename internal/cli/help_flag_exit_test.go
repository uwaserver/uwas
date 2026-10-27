package cli

// F32200: printUsage advertises "uwas <command> --help", but the dispatcher
// treated --help as an ordinary argument. Commands with subcommands (domain,
// cache, cert, php, user, migrate) answered `unknown subcommand "--help"` and
// flag-based commands printed their flag table followed by
// `uwas stop: flag: help requested`; both exited 1. Asking for help is not a
// failure: it must print help and exit 0.

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestHelperCLIMain re-executes the test binary as the real CLI so os.Exit in
// CLI.Run can be observed.
func TestHelperCLIMain(t *testing.T) {
	if os.Getenv("UWAS_CLI_HELPER") != "1" {
		return
	}
	app := New()
	app.Register(&VersionCommand{})
	app.Register(&DomainCommand{})
	app.Register(&CacheCommand{})
	app.Register(&CertCommand{})
	app.Register(&PHPCommand{})
	app.Register(&StopCommand{})
	app.Register(&BackupCommand{})
	app.Register(NewHelpCommand(app))
	app.Run(strings.Fields(os.Getenv("UWAS_CLI_ARGS")))
	os.Exit(0)
}

func runCLIHelper(t *testing.T, args string) (code int, out string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperCLIMain$")
	cmd.Env = append(os.Environ(), "UWAS_CLI_HELPER=1", "UWAS_CLI_ARGS="+args, "HOME="+t.TempDir())
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run helper: %v", err)
		}
		code = ee.ExitCode()
	}
	return code, buf.String()
}

func TestCommandHelpFlagExitsZero(t *testing.T) {
	for _, args := range []string{
		"domain --help", "domain -h", "cache --help", "cert --help", "php --help",
		"stop --help", "stop -h", "backup --help", "domain list --help", "cache purge -h",
	} {
		t.Run(args, func(t *testing.T) {
			code, out := runCLIHelper(t, args)
			t.Logf("EXPECTED: exit 0 with help text\nACTUAL: exit %d\n%s", code, out)
			if code != 0 {
				t.Fatalf("PROBLEM CONFIRMED: %q exited %d", args, code)
			}
			for _, bad := range []string{"unknown subcommand", "help requested"} {
				if strings.Contains(out, bad) {
					t.Fatalf("PROBLEM CONFIRMED: %q printed %q", args, bad)
				}
			}
			if strings.TrimSpace(out) == "" {
				t.Fatalf("PROBLEM CONFIRMED: %q printed no help", args)
			}
		})
	}
}

// Controls: genuine errors keep failing, successful commands keep succeeding.
func TestCommandHelpFlagControls(t *testing.T) {
	if code, _ := runCLIHelper(t, "domain bogus"); code != 1 {
		t.Fatalf("unknown subcommand exit = %d, want 1", code)
	}
	if code, _ := runCLIHelper(t, "domain list --no-such-flag"); code != 1 {
		t.Fatalf("bad flag exit = %d, want 1", code)
	}
	if code, _ := runCLIHelper(t, "version"); code != 0 {
		t.Fatalf("version exit = %d, want 0", code)
	}
	if code, _ := runCLIHelper(t, "help domain"); code != 0 {
		t.Fatalf("help domain exit = %d, want 0", code)
	}
}
