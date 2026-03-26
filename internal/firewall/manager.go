// Package firewall manages ufw/iptables rules.
package firewall

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

var (
	runtimeGOOS    = runtime.GOOS
	execCommandFn  = exec.Command
	execLookPathFn = exec.LookPath
)

// Rule represents a firewall rule.
type Rule struct {
	Number  int    `json:"number"`
	Action  string `json:"action"` // ALLOW, DENY
	From    string `json:"from"`
	To      string `json:"to"`
	Port    string `json:"port,omitempty"`
	Proto   string `json:"proto,omitempty"`
	Comment string `json:"comment,omitempty"`
}

// Status returns firewall status and rules.
type Status struct {
	Active  bool   `json:"active"`
	Backend string `json:"backend"` // "ufw", "iptables", "none"
	Rules   []Rule `json:"rules"`
}

// GetStatus returns the current firewall status.
func GetStatus() Status {
	if runtimeGOOS == "windows" {
		return Status{Backend: "none"}
	}

	// Try ufw first
	if _, err := execLookPathFn("ufw"); err == nil {
		return getUFWStatus()
	}

	return Status{Backend: "none"}
}

func getUFWStatus() Status {
	st := Status{Backend: "ufw"}

	out, err := execCommandFn("ufw", "status", "numbered").Output()
	if err != nil {
		return st
	}

	output := string(out)
	st.Active = strings.Contains(output, "Status: active")

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "[") {
			continue
		}
		rule := parseUFWRule(line)
		if rule.Action != "" {
			st.Rules = append(st.Rules, rule)
		}
	}
	return st
}

func parseUFWRule(line string) Rule {
	// Format: [ 1] 80/tcp                     ALLOW IN    Anywhere
	r := Rule{}

	// Extract number
	if idx := strings.Index(line, "]"); idx > 0 {
		numStr := strings.TrimSpace(line[1:idx])
		fmt.Sscanf(numStr, "%d", &r.Number)
		line = strings.TrimSpace(line[idx+1:])
	}

	parts := strings.Fields(line)
	if len(parts) < 3 {
		return r
	}

	r.To = parts[0]
	if strings.Contains(parts[0], "/") {
		pp := strings.SplitN(parts[0], "/", 2)
		r.Port = pp[0]
		r.Proto = pp[1]
	}

	for _, p := range parts {
		up := strings.ToUpper(p)
		if up == "ALLOW" || up == "DENY" || up == "REJECT" {
			r.Action = up
			break
		}
	}

	if idx := strings.Index(line, "Anywhere"); idx >= 0 {
		r.From = "Anywhere"
	}

	return r
}

// AllowPort adds a ufw allow rule.
func AllowPort(port, proto string) error {
	if _, err := execLookPathFn("ufw"); err != nil {
		return fmt.Errorf("ufw not installed")
	}
	target := port
	if proto != "" {
		target = port + "/" + proto
	}
	return execCommandFn("ufw", "allow", target).Run()
}

// DenyPort adds a ufw deny rule.
func DenyPort(port, proto string) error {
	if _, err := execLookPathFn("ufw"); err != nil {
		return fmt.Errorf("ufw not installed")
	}
	target := port
	if proto != "" {
		target = port + "/" + proto
	}
	return execCommandFn("ufw", "deny", target).Run()
}

// DeleteRule removes a rule by number.
func DeleteRule(number int) error {
	if _, err := execLookPathFn("ufw"); err != nil {
		return fmt.Errorf("ufw not installed")
	}
	cmd := execCommandFn("ufw", "--force", "delete", fmt.Sprintf("%d", number))
	return cmd.Run()
}

// Enable enables the firewall.
func Enable() error {
	return execCommandFn("ufw", "--force", "enable").Run()
}

// Disable disables the firewall.
func Disable() error {
	return execCommandFn("ufw", "disable").Run()
}
