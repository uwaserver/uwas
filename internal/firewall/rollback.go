package firewall

import (
	"strings"
	"sync"
	"time"
)

// Enabling ufw over a remote connection is the classic way to lock yourself
// out: one missing allow rule for SSH and the session dies with no way back.
// Two guards live here. EnableWithRollback allows the ports UWAS needs before
// it enables, and — like a network switch's "commit confirmed" — schedules an
// automatic disable unless the operator confirms they still have access. And
// getUFWStatus learns to show rules that were staged while ufw is inactive, so
// the panel does not look empty right when the operator is trying to prepare.

var (
	rbMu       sync.Mutex
	rbTimer    *time.Timer
	rbDeadline time.Time
)

// EnableWithRollback allows allowPorts (best effort — an already-present or
// invalid rule is not fatal), enables the firewall, then schedules an automatic
// Disable after `within` unless ConfirmEnable cancels it first. A `within` of 0
// enables with no rollback.
//
// The allow-first order matters: `ufw enable` starts denying immediately, so a
// rule added afterwards would race the dropped connection.
func EnableWithRollback(within time.Duration, allowPorts []string) error {
	// Clean duplicates left by earlier enable cycles while we can still see
	// staged/numbered rules, then ensure UWAS ports, then an explicit bottom
	// deny-any row (visible in the panel — not only the invisible UFW policy).
	DeduplicateRules()

	for _, p := range allowPorts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		_ = AllowPort(p, "tcp")
	}

	_ = execCommandFn("ufw", "default", "deny", "incoming").Run()
	_ = execCommandFn("ufw", "default", "allow", "outgoing").Run()
	_ = ensureDefaultDenyAtBottom()

	if err := Enable(); err != nil {
		return err
	}

	DeduplicateRules()

	if within > 0 {
		rbMu.Lock()
		if rbTimer != nil {
			rbTimer.Stop()
		}
		rbDeadline = time.Now().Add(within)
		rbTimer = time.AfterFunc(within, func() {
			_ = Disable()
			rbMu.Lock()
			rbTimer = nil
			rbDeadline = time.Time{}
			rbMu.Unlock()
		})
		rbMu.Unlock()
	}
	return nil
}

// ConfirmEnable cancels a pending rollback. Returns true if one was pending.
func ConfirmEnable() bool {
	rbMu.Lock()
	defer rbMu.Unlock()
	if rbTimer == nil {
		return false
	}
	rbTimer.Stop()
	rbTimer = nil
	rbDeadline = time.Time{}
	return true
}

// cancelRollback clears any pending rollback without disabling. Called from a
// manual Disable so a timer does not later fire against a firewall the operator
// already turned off (and maybe re-enabled by hand).
func cancelRollback() {
	rbMu.Lock()
	if rbTimer != nil {
		rbTimer.Stop()
		rbTimer = nil
	}
	rbDeadline = time.Time{}
	rbMu.Unlock()
}

// rollbackStatus reports whether an automatic disable is pending and how many
// seconds remain.
func rollbackStatus() (bool, int) {
	rbMu.Lock()
	defer rbMu.Unlock()
	if rbTimer == nil || rbDeadline.IsZero() {
		return false, 0
	}
	left := int(time.Until(rbDeadline).Round(time.Second).Seconds())
	if left < 0 {
		left = 0
	}
	return true, left
}

// stagedRules parses `ufw show added`, which lists rules even while ufw is
// inactive — unlike `ufw status`, which shows nothing until enabled. This is
// what lets the panel display rules the operator is preparing before they flip
// the firewall on.
func stagedRules() []Rule {
	out, err := execCommandFn("ufw", "show", "added").Output()
	if err != nil {
		return nil
	}
	var rules []Rule
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		// Lines look like: "ufw allow 22/tcp" or "ufw deny from 1.2.3.4".
		rest, ok := strings.CutPrefix(line, "ufw ")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		r := Rule{}
		switch strings.ToLower(fields[0]) {
		case "allow":
			r.Action = "ALLOW"
		case "deny":
			r.Action = "DENY"
		case "reject":
			r.Action = "REJECT"
		default:
			continue
		}
		// Find a port/proto token ("22/tcp" or "443") or a from-address.
		// Also handles: "from 1.2.3.4 to any port 3306 proto tcp" and
		// "from any to any".
		for i := 1; i < len(fields); i++ {
			f := fields[i]
			switch f {
			case "from":
				if i+1 < len(fields) {
					r.From = fields[i+1]
					if r.From == "any" {
						r.From = ""
					}
					i++
				}
			case "to", "in", "out", "on":
				continue
			case "port":
				if i+1 < len(fields) {
					r.Port = fields[i+1]
					r.To = r.Port
					i++
				}
			case "proto":
				if i+1 < len(fields) {
					r.Proto = fields[i+1]
					i++
				}
			default:
				if strings.Contains(f, "/") {
					pp := strings.SplitN(f, "/", 2)
					r.Port, r.Proto, r.To = pp[0], pp[1], f
				} else if strings.ContainsAny(f, ".:") && r.From == "" && f != "any" {
					r.From = f
				} else if r.Port == "" && isNumericPort(f) {
					r.Port = f
					r.To = f
				}
			}
		}
		rules = append(rules, r)
	}
	return rules
}

func isNumericPort(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
