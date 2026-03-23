package cli

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// StopCommand stops the running UWAS server by sending SIGTERM.
type StopCommand struct{}

func (s *StopCommand) Name() string        { return "stop" }
func (s *StopCommand) Description() string { return "Stop the running UWAS server" }

func (s *StopCommand) Help() string {
	return `Flags:
  --pid-file string   Path to PID file (auto-detected from config)

Sends SIGTERM to the running UWAS process and waits for it to exit.`
}

func (s *StopCommand) Run(args []string) error {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	pidFileFlag := fs.String("pid-file", "", "path to PID file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Resolve PID file: flag → config → default
	pidFile := *pidFileFlag
	if pidFile == "" {
		pidFile = pidFileFromConfig()
	}
	if pidFile == "" {
		pidFile = "/var/run/uwas.pid"
	}

	pidData, err := os.ReadFile(pidFile)
	if err != nil {
		return fmt.Errorf("cannot read PID file %s: %w\nIs the server running?", pidFile, err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil {
		return fmt.Errorf("invalid PID in %s: %w", pidFile, err)
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("cannot find process %d: %w", pid, err)
	}

	fmt.Printf("Stopping UWAS (PID %d)...\n", pid)
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to send SIGTERM to process %d: %w", pid, err)
	}

	// Wait for exit (up to 15 seconds)
	for i := 0; i < 30; i++ {
		time.Sleep(500 * time.Millisecond)
		if !isProcessAlive(process) {
			fmt.Println("UWAS stopped.")
			return nil
		}
	}

	fmt.Println("Process still running after 15s — sending SIGKILL.")
	process.Kill()
	fmt.Println("UWAS killed.")
	return nil
}

// pidFileFromConfig reads the UWAS config and returns the pid_file setting.
func pidFileFromConfig() string {
	return quickConfigValue("pid_file")
}

// adminURLFromConfig reads the admin listen address from config and returns an HTTP URL.
func adminURLFromConfig() string {
	listen := quickConfigValue("listen")
	if listen == "" {
		return "http://127.0.0.1:9443"
	}
	// Replace 0.0.0.0 with 127.0.0.1 for local CLI access.
	listen = strings.Replace(listen, "0.0.0.0", "127.0.0.1", 1)
	return "http://" + listen
}

// quickConfigValue does a quick line-scan of the config YAML for a given key.
// Not a full YAML parser — works for simple scalar values.
func quickConfigValue(key string) string {
	cfgFile, found := findConfig("")
	if !found {
		return ""
	}
	data, err := os.ReadFile(cfgFile)
	if err != nil {
		return ""
	}
	prefix := key + ":"
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix) {
			val := strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))
			val = strings.Trim(val, `"'`)
			if val != "" {
				return val
			}
		}
	}
	return ""
}
