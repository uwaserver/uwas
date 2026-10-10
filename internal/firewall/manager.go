// Package firewall manages ufw/iptables rules.
package firewall

import (
	"fmt"
	"net/netip"
	"os/exec"
	"runtime"
	"strconv"
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
	V6      bool   `json:"v6,omitempty"` // IPv6 rule (Anywhere (v6))

	// scope is the rule's "To" side and direction when they are more than a
	// plain port/proto or Anywhere on IN — an app profile ("Nginx Full"), an
	// interface ("3306/tcp on eth1"), a destination ("10.0.0.5 3306/tcp") or
	// OUT/FWD. Empty for plain rules. Identity checks include it so distinct
	// rules are never treated as the same one.
	scope string
}

// Status returns firewall status and rules.
type Status struct {
	Active          bool   `json:"active"`
	Backend         string `json:"backend"` // "ufw", "iptables", "none"
	Rules           []Rule `json:"rules"`
	Staged          bool   `json:"staged,omitempty"`           // Rules are staged (ufw inactive); they apply on enable
	RollbackPending bool   `json:"rollback_pending,omitempty"` // an automatic disable is scheduled
	RollbackSeconds int    `json:"rollback_seconds,omitempty"` // seconds until that disable
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

	// `ufw status` lists nothing while inactive, so a fresh install preparing
	// rules before enabling would see an empty table. Fall back to the rules
	// staged via `ufw show added` and mark them as such.
	if !st.Active && len(st.Rules) == 0 {
		if staged := stagedRules(); len(staged) > 0 {
			st.Rules = staged
			st.Staged = true
		}
	}

	st.RollbackPending, st.RollbackSeconds = rollbackStatus()
	return st
}

func parseUFWRule(line string) Rule {
	// Format: [ 1] 80/tcp                     ALLOW IN    Anywhere
	// Format: [ 2] 22/tcp                     ALLOW IN    Anywhere (v6)
	// Format: [ 3] Anywhere                   DENY IN     192.168.1.100
	// Format: [ 4] 3306/tcp                   ALLOW IN    203.0.113.10
	r := Rule{}

	line = strings.TrimSpace(line)

	if !strings.HasPrefix(line, "[") {
		return r
	}

	closeBracket := strings.Index(line, "]")
	if closeBracket <= 0 {
		return r
	}

	numStr := strings.TrimSpace(line[1:closeBracket])
	if _, err := fmt.Sscanf(numStr, "%d", &r.Number); err != nil {
		r.Number = 0
	}

	rest := strings.TrimSpace(line[closeBracket+1:])

	// ufw appends "# <comment>" to commented rules (e.g. autoblock denies);
	// split it off so it is not folded into From.
	if i := strings.Index(rest, "#"); i >= 0 {
		r.Comment = strings.TrimSpace(rest[i+1:])
		rest = strings.TrimSpace(rest[:i])
	}

	// Detect IPv6 rule — UFW appends "(v6)" suffix
	if strings.HasSuffix(rest, "(v6)") {
		r.V6 = true
		rest = strings.TrimSpace(rest[:len(rest)-4])
	}

	parts := strings.Fields(rest)
	if len(parts) < 3 {
		return r
	}

	firstPart := parts[0]

	if strings.Contains(firstPart, "/") {
		pp := strings.SplitN(firstPart, "/", 2)
		if len(pp) == 2 {
			r.Port = pp[0]
			r.Proto = pp[1]
			r.To = firstPart
		}
	} else if firstPart == "Anywhere" {
		r.To = firstPart
		if len(parts) >= 3 && strings.ToLower(parts[1]) == "on" {
			r.To = firstPart + " " + parts[1] + " " + parts[2]
		}
	} else if isNumericPort(firstPart) {
		r.Port = firstPart
		r.To = firstPart
	} else {
		r.To = firstPart
	}

	// Find action (ALLOW, DENY, REJECT) and treat everything after IN/OUT as From.
	// Important: do NOT treat the destination "Anywhere" as From — source-IP
	// denys look like `Anywhere DENY IN 1.2.3.4` and used to be mis-parsed as
	// From=Anywhere (showing up as duplicate "Any DENY" rows).
	for i, p := range parts {
		up := strings.ToUpper(p)
		if up == "ALLOW" || up == "DENY" || up == "REJECT" {
			r.Action = up
			continue
		}
		if (up == "IN" || up == "OUT") && i+1 < len(parts) {
			r.From = strings.Join(parts[i+1:], " ")
			break
		}
	}
	if r.From == "" {
		r.From = "Anywhere"
	}
	r.scope = ufwRuleScope(parts)

	return r
}

// ufwRuleScope returns "" when the tokens before the action are a single plain
// port/proto or "Anywhere" and the direction is IN; otherwise it returns those
// tokens plus the direction, so scoped rules keep a distinct identity.
func ufwRuleScope(parts []string) string {
	act := -1
	for i, p := range parts {
		up := strings.ToUpper(p)
		if up == "ALLOW" || up == "DENY" || up == "REJECT" || up == "LIMIT" {
			act = i
			break
		}
	}
	if act < 0 {
		return ""
	}
	var to []string
	for _, p := range parts[:act] {
		if p != "(v6)" {
			to = append(to, p)
		}
	}
	dir := "IN"
	if act+1 < len(parts) {
		if up := strings.ToUpper(parts[act+1]); up == "IN" || up == "OUT" || up == "FWD" {
			dir = up
		}
	}
	if dir == "IN" && len(to) == 1 && (to[0] == "Anywhere" || isPortToken(to[0])) {
		return ""
	}
	return strings.Join(to, " ") + "|" + dir
}

// isPortToken reports a ufw port column such as "80", "80/tcp", "8000:8100/udp"
// or "80,443/tcp" — not an address like "10.0.0.0/8".
func isPortToken(tok string) bool {
	port, proto, hasProto := strings.Cut(tok, "/")
	if hasProto && proto != "tcp" && proto != "udp" {
		return false
	}
	if port == "" {
		return false
	}
	for _, c := range port {
		if (c < '0' || c > '9') && c != ':' && c != ',' {
			return false
		}
	}
	return true
}

// coversAllSources reports a source prefix that matches every address of its
// family (0.0.0.0/0, ::/0), which a deny treats exactly like "any".
func coversAllSources(from string) bool {
	pfx, err := netip.ParsePrefix(normalizeFrom(from))
	return err == nil && pfx.Bits() == 0
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

// validatePort checks that port is a valid number or range, or empty/"any"
// (meaning all ports).
func validatePort(port string) error {
	if normalizePort(port) == "" {
		return nil
	}
	_, _, err := parsePortSpec(port)
	return err
}

// parsePortSpec parses a single port ("80") or an inclusive range ("8000:8100")
// into [lo, hi]. It rejects empty segments, out-of-range numbers, inverted
// ranges, and "any"/"all"/"*" — callers that want "any port" must use
// normalizePort and skip parsePortSpec.
func parsePortSpec(port string) (int, int, error) {
	if port == "" {
		return 0, 0, fmt.Errorf("port is required")
	}
	p := strings.ToLower(strings.TrimSpace(port))
	if p == "any" || p == "all" || p == "*" {
		return 0, 0, fmt.Errorf("cannot use '%s' as port — specify a port number", port)
	}
	parts := strings.Split(p, ":")
	if len(parts) > 2 {
		return 0, 0, fmt.Errorf("invalid port: %s", port)
	}
	nums := make([]int, len(parts))
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 1 || n > 65535 {
			return 0, 0, fmt.Errorf("invalid port: %s", port)
		}
		nums[i] = n
	}
	lo, hi := nums[0], nums[len(nums)-1]
	if lo > hi {
		return 0, 0, fmt.Errorf("invalid port range: %s", port)
	}
	return lo, hi, nil
}

// deniesProtectedPort reports whether a deny target (single port or range)
// covers any protected port. Without this a range like "20:30" slips past the
// single-key protectedPorts lookup and `ufw deny 20:30/tcp` locks out SSH.
func deniesProtectedPort(port string) bool {
	lo, hi, err := parsePortSpec(port)
	if err != nil {
		return false
	}
	for pp := range protectedPorts {
		if n, err := strconv.Atoi(pp); err == nil && n >= lo && n <= hi {
			return true
		}
	}
	return false
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
	return AllowPortFrom(port, proto, "")
}

// AllowPortFrom adds a ufw allow rule, optionally limited to a source IP/CIDR.
// Allows are inserted above port-deny rules so they stay effective.
func AllowPortFrom(port, proto, from string) error {
	return addPortRule("allow", port, proto, from)
}

// DenyPort adds a ufw deny rule. Cannot deny protected ports (80, 443, 22, admin).
func DenyPort(port, proto string) error {
	return DenyPortFrom(port, proto, "")
}

// DenyPortFrom adds a ufw deny rule, optionally limited to a source IP/CIDR.
// Empty port means any port (deny from X, or deny from any to any).
func DenyPortFrom(port, proto, from string) error {
	port = normalizePort(port)
	if port != "" {
		if err := validatePort(port); err != nil {
			return err
		}
		if deniesProtectedPort(port) && (normalizeFrom(from) == "" || coversAllSources(from)) {
			return fmt.Errorf("cannot deny port %s — it covers a port required for server operation (HTTP/HTTPS/SSH/Admin)", port)
		}
	}
	return addPortRule("deny", port, proto, from)
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
	cancelRollback()
	return execCommandFn("ufw", "disable").Run()
}
