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
	// Format: [ 2] 22/tcp                     ALLOW IN    Anywhere (v6)
	// Format: [ 3] Anywhere on eth0           DENY IN     192.168.1.100
	r := Rule{}

	// Clean the line - remove extra spaces and normalize
	line = strings.TrimSpace(line)

	// Extract number: [ 1] or [1]
	if !strings.HasPrefix(line, "[") {
		return r
	}

	closeBracket := strings.Index(line, "]")
	if closeBracket <= 0 {
		return r
	}

	numStr := strings.TrimSpace(line[1:closeBracket])
	fmt.Sscanf(numStr, "%d", &r.Number)

	// Get the rest after ]
	rest := strings.TrimSpace(line[closeBracket+1:])

	// Split by multiple spaces to get parts
	parts := strings.Fields(rest)
	if len(parts) < 3 {
		return r
	}

	// First part is usually port/proto or interface info
	firstPart := parts[0]

	// Check if it contains port/proto like "80/tcp"
	if strings.Contains(firstPart, "/") {
		pp := strings.SplitN(firstPart, "/", 2)
		if len(pp) == 2 {
			r.Port = pp[0]
			r.Proto = pp[1]
			r.To = firstPart
		}
	} else if firstPart == "Anywhere" {
		// Anywhere with interface? Like "Anywhere on eth0"
		r.To = firstPart
		if len(parts) >= 3 && strings.ToLower(parts[1]) == "on" {
			r.To = firstPart + " " + parts[1] + " " + parts[2]
		}
	} else {
		r.To = firstPart
	}

	// Find action (ALLOW, DENY, REJECT)
	for _, p := range parts {
		up := strings.ToUpper(p)
		if up == "ALLOW" || up == "DENY" || up == "REJECT" {
			r.Action = up
			break
		}
	}

	// Find From (usually "Anywhere" or an IP)
	for _, p := range parts {
		lowerP := strings.ToLower(p)
		if lowerP == "anywhere" {
			r.From = "Anywhere"
			break
		}
	}

	return r
}

// protectedPorts are ports that cannot be denied (would lock out the server).
var protectedPorts = map[string]bool{
	"80": true, "443": true, "22": true,
}

// SetAdminPort adds the admin port to protected list so it can't be denied.
func SetAdminPort(port string) {
	if port != "" {
		// Extract port number from ":9443" or "0.0.0.0:9443"
		if i := strings.LastIndex(port, ":"); i >= 0 {
			port = port[i+1:]
		}
		protectedPorts[port] = true
	}
}

// validatePort checks that port is a valid number or range, not empty, not "any".
func validatePort(port string) error {
	if port == "" {
		return fmt.Errorf("port is required")
	}
	p := strings.ToLower(strings.TrimSpace(port))
	if p == "any" || p == "all" || p == "*" {
		return fmt.Errorf("cannot use '%s' as port — specify a port number", port)
	}
	// Allow ranges like "8000:8100"
	for _, part := range strings.Split(p, ":") {
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return fmt.Errorf("invalid port: %s", port)
			}
		}
	}
	return nil
}

// AllowPort adds a ufw allow rule.
func validateProto(proto string) error {
	if proto == "" {
		return nil
	}
	valid := map[string]bool{"tcp": true, "udp": true}
	if !valid[strings.ToLower(proto)] {
		return fmt.Errorf("invalid protocol %q — must be tcp or udp", proto)
	}
	return nil
}

func AllowPort(port, proto string) error {
	if _, err := execLookPathFn("ufw"); err != nil {
		return fmt.Errorf("ufw not installed")
	}
	if err := validatePort(port); err != nil {
		return err
	}
	if err := validateProto(proto); err != nil {
		return err
	}
	target := port
	if proto != "" {
		target = port + "/" + proto
	}
	return execCommandFn("ufw", "allow", target).Run()
}

// DenyPort adds a ufw deny rule. Cannot deny protected ports (80, 443, 22, admin).
func DenyPort(port, proto string) error {
	if _, err := execLookPathFn("ufw"); err != nil {
		return fmt.Errorf("ufw not installed")
	}
	if err := validatePort(port); err != nil {
		return err
	}
	if err := validateProto(proto); err != nil {
		return err
	}
	if protectedPorts[port] {
		return fmt.Errorf("cannot deny port %s — it is required for server operation (HTTP/HTTPS/SSH/Admin)", port)
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
