package firewall

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// normalizeFrom returns "" for "any"/"anywhere"/empty; otherwise a trimmed address.
func normalizeFrom(from string) string {
	from = strings.TrimSpace(from)
	if from == "" {
		return ""
	}
	switch strings.ToLower(from) {
	case "any", "anywhere", "*":
		return ""
	}
	return from
}

// validateFrom accepts empty (anywhere), an IP, or a CIDR prefix.
func validateFrom(from string) error {
	from = normalizeFrom(from)
	if from == "" {
		return nil
	}
	if _, err := netip.ParseAddr(from); err == nil {
		return nil
	}
	if _, err := netip.ParsePrefix(from); err == nil {
		return nil
	}
	return fmt.Errorf("invalid source address %q — use an IP or CIDR (e.g. 203.0.113.10 or 10.0.0.0/8)", from)
}

// normalizePort returns "" for empty/"any"/"all"/"*" (meaning any port).
func normalizePort(port string) string {
	p := strings.TrimSpace(strings.ToLower(port))
	if p == "" || p == "any" || p == "all" || p == "*" {
		return ""
	}
	return strings.TrimSpace(port)
}

// buildPortRuleArgs builds `ufw <action> …` args (without the binary name).
// When insertAt > 0 the command is `insert <n> <action> …`.
// Empty / "any" port means all ports (`from …` or `from any to any`).
func buildPortRuleArgs(action, port, proto, from string, insertAt int) ([]string, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	if action != "allow" && action != "deny" && action != "reject" {
		return nil, fmt.Errorf("invalid action %q", action)
	}
	port = normalizePort(port)
	if port != "" {
		if err := validatePort(port); err != nil {
			return nil, err
		}
	}
	if err := validateProto(proto); err != nil {
		return nil, err
	}
	from = normalizeFrom(from)
	if err := validateFrom(from); err != nil {
		return nil, err
	}

	var args []string
	if insertAt > 0 {
		args = append(args, "insert", fmt.Sprintf("%d", insertAt))
	}
	args = append(args, action)

	if port == "" {
		// Any port: source-scoped or blanket.
		if from != "" {
			args = append(args, "from", from)
			return args, nil
		}
		args = append(args, "from", "any", "to", "any")
		return args, nil
	}

	if from != "" {
		args = append(args, "from", from, "to", "any", "port", port)
		if proto != "" {
			args = append(args, "proto", strings.ToLower(proto))
		}
		return args, nil
	}

	target := port
	if proto != "" {
		target = port + "/" + strings.ToLower(proto)
	}
	args = append(args, target)
	return args, nil
}

// firstPortDenyNumber returns the number of the first DENY/REJECT rule that
// targets a port (not an IP-only autoblock deny). Allows should insert above
// these so they stay reachable; IP-source denys at the top stay put.
func firstPortDenyNumber(rules []Rule) int {
	for _, r := range rules {
		act := strings.ToUpper(r.Action)
		if act != "DENY" && act != "REJECT" {
			continue
		}
		if r.Port == "" {
			// Source-IP deny (To=Anywhere, no port) — leave above port allows.
			continue
		}
		return r.Number
	}
	return 0
}

// ruleExists reports whether an equivalent rule is already present.
func ruleExists(rules []Rule, action, port, proto, from string) bool {
	action = strings.ToUpper(action)
	from = normalizeFrom(from)
	port = normalizePort(port)
	proto = strings.ToLower(proto)
	for _, r := range rules {
		if strings.ToUpper(r.Action) != action {
			continue
		}
		if normalizePort(r.Port) != port {
			continue
		}
		rp := strings.ToLower(r.Proto)
		if proto != "" && rp != "" && rp != proto {
			continue
		}
		if normalizeFrom(r.From) != from {
			continue
		}
		return true
	}
	return false
}

// isDefaultDeny reports a blanket DENY any→any (the numbered default-deny row).
func isDefaultDeny(r Rule) bool {
	return strings.ToUpper(r.Action) == "DENY" && normalizePort(r.Port) == "" && normalizeFrom(r.From) == ""
}

// hasDefaultDeny reports whether a blanket IPv4 deny any/any is present.
// IPv6-only twins do not count: after a bad move the v4 row can vanish while
// the v6 DENY remains, and treating that as "present" blocked re-adding IPv4.
func hasDefaultDeny(rules []Rule) bool {
	for _, r := range rules {
		if !r.V6 && isDefaultDeny(r) {
			return true
		}
	}
	return false
}

// EnsureDefaultDenyAtBottom appends a numbered `deny from any to any` when the
// IPv4 default deny is missing (exported so the admin status handler can heal).
func EnsureDefaultDenyAtBottom() error {
	return ensureDefaultDenyAtBottom()
}

// ensureDefaultDenyAtBottom appends a single numbered `deny from any to any`
// when the IPv4 row is missing. UFW's default policy is invisible in the panel;
// operators want an explicit bottom rule so allow-above-deny is visible.
func ensureDefaultDenyAtBottom() error {
	if _, err := execLookPathFn("ufw"); err != nil {
		return fmt.Errorf("ufw not installed")
	}
	st := getUFWStatus()
	if hasDefaultDeny(st.Rules) {
		return nil
	}
	args, err := buildPortRuleArgs("deny", "", "", "", 0)
	if err != nil {
		return err
	}
	if out, err := execCommandFn("ufw", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("firewall rule failed")
	}
	// Adding deny any/any may recreate a v6 twin that already existed alone.
	DeduplicateRules()
	return nil
}

// addPortRule adds an allow/deny rule. Allows are inserted above the first
// port-deny so "allows above denies" holds; denies are appended (default deny
// stays at the bottom when added last on enable).
func addPortRule(action, port, proto, from string) error {
	if _, err := execLookPathFn("ufw"); err != nil {
		return fmt.Errorf("ufw not installed")
	}
	port = normalizePort(port)
	st := getUFWStatus()
	if ruleExists(st.Rules, action, port, proto, from) {
		return nil
	}

	insertAt := 0
	if strings.EqualFold(action, "allow") {
		insertAt = firstPortDenyNumber(st.Rules)
		// Also don't insert below a default deny — keep allows above it.
		if insertAt == 0 {
			for _, r := range st.Rules {
				if !r.V6 && isDefaultDeny(r) {
					insertAt = r.Number
					break
				}
			}
		}
	}

	args, err := buildPortRuleArgs(action, port, proto, from, insertAt)
	if err != nil {
		return err
	}
	if out, err := execCommandFn("ufw", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("firewall rule failed")
	}
	return nil
}

// ruleToUFWArgs rebuilds the argv needed to recreate a numbered rule (for move).
func ruleToUFWArgs(r Rule) ([]string, error) {
	action := strings.ToLower(r.Action)
	if action != "allow" && action != "deny" && action != "reject" {
		return nil, fmt.Errorf("unsupported action %q", r.Action)
	}
	from := normalizeFrom(r.From)

	if r.Port == "" {
		if from == "" {
			return nil, fmt.Errorf("cannot reorder a blanket any/any rule — delete and recreate it")
		}
		return []string{action, "from", from}, nil
	}
	return buildPortRuleArgs(action, r.Port, r.Proto, from, 0)
}

// ruleFingerprint identifies equivalent rules across v4/v6 twins.
func ruleFingerprint(r Rule) string {
	return strings.ToUpper(r.Action) + "|" + normalizePort(r.Port) + "|" + strings.ToLower(r.Proto) + "|" + normalizeFrom(r.From)
}

// matchingRuleNumbers returns all numbered rules (v4+v6) matching r's content.
func matchingRuleNumbers(rules []Rule, r Rule) []int {
	fp := ruleFingerprint(r)
	var nums []int
	for _, x := range rules {
		if x.Number > 0 && ruleFingerprint(x) == fp {
			nums = append(nums, x.Number)
		}
	}
	return nums
}

// logicalIPv4Rules returns non-v6 rules in status order (matches the panel list).
func logicalIPv4Rules(rules []Rule) []Rule {
	out := make([]Rule, 0, len(rules))
	for _, r := range rules {
		if !r.V6 {
			out = append(out, r)
		}
	}
	return out
}

// MoveRule moves a numbered rule up or down by one logical (IPv4) position.
//
// Important: UFW skips inserting a rule that already exists ("Skipping adding
// existing rule") with exit 0. The old insert-then-delete approach then deleted
// the next rule — often the default DENY. We delete-first, then insert.
func MoveRule(number int, direction string) error {
	if _, err := execLookPathFn("ufw"); err != nil {
		return fmt.Errorf("ufw not installed")
	}
	dir := strings.ToLower(strings.TrimSpace(direction))
	if dir != "up" && dir != "down" {
		return fmt.Errorf("direction must be up or down")
	}
	if number <= 0 {
		return fmt.Errorf("invalid rule number")
	}

	st := getUFWStatus()
	logical := logicalIPv4Rules(st.Rules)

	idx := -1
	for i, r := range logical {
		if r.Number == number {
			idx = i
			break
		}
	}
	// Map a v6 click to its IPv4 sibling when present.
	if idx < 0 {
		var clicked Rule
		found := false
		for _, r := range st.Rules {
			if r.Number == number {
				clicked = r
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("rule #%d not found", number)
		}
		fp := ruleFingerprint(clicked)
		for i, r := range logical {
			if ruleFingerprint(r) == fp {
				idx = i
				number = r.Number
				break
			}
		}
	}
	if idx < 0 {
		return fmt.Errorf("rule #%d not found", number)
	}

	r := logical[idx]
	if isDefaultDeny(r) {
		return fmt.Errorf("cannot move the default deny rule")
	}

	swapIdx := idx - 1
	if dir == "down" {
		swapIdx = idx + 1
	}
	if swapIdx < 0 || swapIdx >= len(logical) {
		return nil
	}
	if isDefaultDeny(logical[swapIdx]) {
		return fmt.Errorf("cannot move a rule past the default deny")
	}

	args, err := ruleToUFWArgs(r)
	if err != nil {
		return err
	}

	neighbor := logical[swapIdx]
	toDelete := matchingRuleNumbers(st.Rules, r)
	sort.Ints(toDelete)
	for i := len(toDelete) - 1; i >= 0; i-- {
		if out, err := execCommandFn("ufw", "--force", "delete", fmt.Sprintf("%d", toDelete[i])).CombinedOutput(); err != nil {
			return fmt.Errorf("firewall rule failed")
		}
	}

	// Re-read and place relative to the surviving neighbor (and its v6 twin).
	st2 := getUFWStatus()
	fp := ruleFingerprint(neighbor)
	insertAt := 1
	if dir == "up" {
		for _, x := range st2.Rules {
			if ruleFingerprint(x) == fp {
				insertAt = x.Number
				break
			}
		}
	} else {
		last := 0
		for _, x := range st2.Rules {
			if ruleFingerprint(x) == fp {
				last = x.Number
			}
		}
		if last == 0 {
			return fmt.Errorf("neighbor rule disappeared during move")
		}
		insertAt = last + 1
	}

	ins := append([]string{"insert", fmt.Sprintf("%d", insertAt)}, args...)
	if out, err := execCommandFn("ufw", ins...).CombinedOutput(); err != nil {
		return fmt.Errorf("firewall rule failed")
	}
	_ = ensureDefaultDenyAtBottom()
	return nil
}

// DeduplicateRules removes duplicate rules for the same action/port/proto/from/v6
// (keeps the lowest number). Used around enable so re-enabling cannot stack copies.
func DeduplicateRules() {
	st := getUFWStatus()
	seen := map[string]int{}
	var toDelete []int
	for _, r := range st.Rules {
		if r.Number <= 0 {
			// Staged rules (ufw inactive) have no numbers — delete after enable.
			continue
		}
		key := strings.ToUpper(r.Action) + "|" + normalizePort(r.Port) + "/" + strings.ToLower(r.Proto) + "|" + normalizeFrom(r.From) + "|" + fmt.Sprint(r.V6)
		if _, ok := seen[key]; ok {
			toDelete = append(toDelete, r.Number)
			continue
		}
		seen[key] = r.Number
	}
	for i := len(toDelete) - 1; i >= 0; i-- {
		_ = DeleteRule(toDelete[i])
	}
}

// DeduplicatePortAllows is kept for callers; it now dedupes all rule kinds.
func DeduplicatePortAllows() { DeduplicateRules() }
