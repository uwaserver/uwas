package firewall

import (
	"fmt"
	"net/netip"
	"strings"
)

// autoblockComment tags every rule this package creates on behalf of the
// autoblocker, so ListBlockedIPs can tell our rules apart from an operator's
// own deny rules and never delete something a human added by hand.
const autoblockComment = "uwas-autoblock"

// validateBlockIP parses an address and refuses the ones that would take the
// server off the network or wall the operator out of it. The autoblocker
// applies its own whitelist first; this is the backstop that runs even for a
// manual API call.
func validateBlockIP(ip string) (netip.Addr, error) {
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return netip.Addr{}, fmt.Errorf("invalid IP address: %s", ip)
	}
	a = a.Unmap()
	switch {
	case a.IsLoopback():
		return netip.Addr{}, fmt.Errorf("refusing to block loopback address %s", a)
	case a.IsPrivate():
		return netip.Addr{}, fmt.Errorf("refusing to block private address %s", a)
	case a.IsLinkLocalUnicast(), a.IsLinkLocalMulticast():
		return netip.Addr{}, fmt.Errorf("refusing to block link-local address %s", a)
	case a.IsUnspecified():
		return netip.Addr{}, fmt.Errorf("refusing to block unspecified address %s", a)
	case a.IsMulticast():
		return netip.Addr{}, fmt.Errorf("refusing to block multicast address %s", a)
	}
	return a, nil
}

// backend reports which tool is available for source-IP rules.
func backend() string {
	if runtimeGOOS == "windows" {
		return "none"
	}
	if _, err := execLookPathFn("ufw"); err == nil {
		return "ufw"
	}
	if _, err := execLookPathFn("iptables"); err == nil {
		return "iptables"
	}
	return "none"
}

// BlockIP drops all traffic from a source address at the kernel.
//
// The rule is inserted at position 1 rather than appended: ufw evaluates in
// order and stops at the first match, so a deny appended after the existing
// "ALLOW 443/tcp" would never be reached.
func BlockIP(ip, comment string) error {
	a, err := validateBlockIP(ip)
	if err != nil {
		return err
	}
	if comment == "" {
		comment = autoblockComment
	}

	switch backend() {
	case "ufw":
		args := []string{"insert", "1", "deny", "from", a.String(), "comment", comment}
		if out, err := execCommandFn("ufw", args...).CombinedOutput(); err != nil {
			// ufw before 0.35 has no `comment` keyword and rejects the whole
			// command. Retry without it rather than losing the block.
			if _, err2 := execCommandFn("ufw", "insert", "1", "deny", "from", a.String()).CombinedOutput(); err2 != nil {
				return fmt.Errorf("ufw deny %s: %w: %s", a, err, strings.TrimSpace(string(out)))
			}
		}
		return nil
	case "iptables":
		chain := "INPUT"
		if a.Is6() {
			return iptablesRun("ip6tables", "-I", chain, "1", "-s", a.String(), "-j", "DROP")
		}
		return iptablesRun("iptables", "-I", chain, "1", "-s", a.String(), "-j", "DROP")
	default:
		return fmt.Errorf("no firewall backend available (ufw or iptables required)")
	}
}

// UnblockIP removes the deny rule for a source address. A missing rule is not
// an error: expiry runs on a timer and may fire after an operator has already
// cleared the rule by hand.
func UnblockIP(ip string) error {
	a, err := validateBlockIP(ip)
	if err != nil {
		return err
	}

	switch backend() {
	case "ufw":
		out, err := execCommandFn("ufw", "--force", "delete", "deny", "from", a.String()).CombinedOutput()
		if err != nil && !strings.Contains(string(out), "Could not delete") {
			return fmt.Errorf("ufw delete deny %s: %w: %s", a, err, strings.TrimSpace(string(out)))
		}
		return nil
	case "iptables":
		if a.Is6() {
			return iptablesRun("ip6tables", "-D", "INPUT", "-s", a.String(), "-j", "DROP")
		}
		return iptablesRun("iptables", "-D", "INPUT", "-s", a.String(), "-j", "DROP")
	default:
		return fmt.Errorf("no firewall backend available (ufw or iptables required)")
	}
}

func iptablesRun(bin string, args ...string) error {
	if _, err := execLookPathFn(bin); err != nil {
		return fmt.Errorf("%s not installed", bin)
	}
	if out, err := execCommandFn(bin, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%s %s: %w: %s", bin, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ListBlockedIPs returns the source addresses currently denied by ufw rules
// carrying the autoblock comment.
func ListBlockedIPs() []string {
	if backend() != "ufw" {
		return nil
	}
	out, err := execCommandFn("ufw", "status", "numbered").Output()
	if err != nil {
		return nil
	}
	var ips []string
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, autoblockComment) || !strings.Contains(line, "DENY") {
			continue
		}
		for _, f := range strings.Fields(line) {
			if a, err := netip.ParseAddr(strings.Trim(f, "[]")); err == nil {
				ips = append(ips, a.String())
				break
			}
		}
	}
	return ips
}
