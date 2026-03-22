package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// RestartCommand performs a graceful restart of the running UWAS server.
// It sends SIGTERM to the running process (which drains connections), waits
// for it to exit, then the process manager (systemd, etc.) restarts it.
type RestartCommand struct{}

func (r *RestartCommand) Name() string        { return "restart" }
func (r *RestartCommand) Description() string { return "Graceful restart of the running server" }

func (r *RestartCommand) Help() string {
	return `Flags:
  --pid-file string   Path to PID file (default "/var/run/uwas.pid")
  --api-url string    Admin API URL for graceful drain (default "http://127.0.0.1:9443")
  --api-key string    Admin API key (default from UWAS_ADMIN_KEY env)

Process:
  1. Optionally notifies the admin API to start draining
  2. Sends SIGTERM to the running process (reads PID from file)
  3. The process drains all active connections before exiting
  4. The process manager (systemd) restarts the service

Examples:
  uwas restart
  uwas restart --pid-file /var/run/uwas.pid`
}

func (r *RestartCommand) Run(args []string) error {
	fs := flag.NewFlagSet("restart", flag.ContinueOnError)
	pidFile := fs.String("pid-file", "/var/run/uwas.pid", "path to PID file")
	apiURL := fs.String("api-url", "http://127.0.0.1:9443", "admin API URL")
	apiKey := fs.String("api-key", os.Getenv("UWAS_ADMIN_KEY"), "admin API key")

	if err := fs.Parse(args); err != nil {
		return err
	}

	// Try to check health via admin API first
	healthData, err := apiRequest("GET", *apiURL+"/api/v1/health", *apiKey, nil)
	if err == nil {
		var health map[string]any
		json.Unmarshal(healthData, &health)
		fmt.Printf("Current server status: %s (uptime: %s)\n", health["status"], health["uptime"])
	}

	// Read PID from file
	pidData, err := os.ReadFile(*pidFile)
	if err != nil {
		return fmt.Errorf("cannot read PID file %s: %w\nIs the server running?", *pidFile, err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil {
		return fmt.Errorf("invalid PID in %s: %w", *pidFile, err)
	}

	// Find the process
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("cannot find process %d: %w", pid, err)
	}

	// Send SIGTERM for graceful shutdown (which drains connections)
	fmt.Printf("Sending SIGTERM to process %d...\n", pid)
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to send SIGTERM to process %d: %w", pid, err)
	}

	fmt.Println("SIGTERM sent. The server will drain active connections and exit.")
	fmt.Println("Your process manager (systemd) will restart the service automatically.")
	fmt.Println("To manually restart: uwas serve -c /path/to/uwas.yaml")

	return nil
}
