package firewall

import (
	"fmt"
	"net/netip"
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

// buildPortRuleArgs builds `ufw <action> …` args (without the binary name).
// When insertAt > 0 the command is `insert <n> <action> …`.
func buildPortRuleArgs(action, port, proto, from string, insertAt int) ([]string, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	if action != "allow" && action != "deny" && action != "reject" {
		return nil, fmt.Errorf("invalid action %q", action)
	}
	if err := validatePort(port); err != nil {
		return nil, err
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

// ruleExists reports whether an equivalent port rule is already present.
func ruleExists(rules []Rule, action, port, proto, from string) bool {
	action = strings.ToUpper(action)
	from = normalizeFrom(from)
	proto = strings.ToLower(proto)
	for _, r := range rules {
		if strings.ToUpper(r.Action) != action {
			continue
		}
		if r.Port != port {
			continue
		}
		if proto != "" && r.Proto != "" && strings.ToLower(r.Proto) != proto {
			continue
		}
		rf := normalizeFrom(r.From)
		if rf != from {
			continue
		}
		return true
	}
	return false
}

// addPortRule adds an allow/deny rule. Allows are inserted above the first
// port-deny so "allows above denies" holds; denies are appended.
func addPortRule(action, port, proto, from string) error {
	if _, err := execLookPathFn("ufw"); err != nil {
		return fmt.Errorf("ufw not installed")
	}
	st := getUFWStatus()
	if ruleExists(st.Rules, action, port, proto, from) {
		return nil // already present — avoid the duplicate rows operators see on re-enable
	}

	insertAt := 0
	if strings.EqualFold(action, "allow") {
		insertAt = firstPortDenyNumber(st.Rules)
	}

	args, err := buildPortRuleArgs(action, port, proto, from, insertAt)
	if err != nil {
		return err
	}
	if out, err := execCommandFn("ufw", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("ufw %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
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

// MoveRule moves a numbered rule up or down by one position ("up" / "down").
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
	idx := -1
	for i, r := range st.Rules {
		if r.Number == number {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("rule #%d not found", number)
	}
	if dir == "up" && idx == 0 {
		return nil
	}
	if dir == "down" && idx >= len(st.Rules)-1 {
		return nil
	}

	r := st.Rules[idx]
	args, err := ruleToUFWArgs(r)
	if err != nil {
		return err
	}

	var insertAt, deleteAt int
	if dir == "up" {
		insertAt = st.Rules[idx-1].Number
		// After insert, the original rule shifts down by one.
		deleteAt = number + 1
	} else {
		insertAt = st.Rules[idx+1].Number + 1
		// Insert below the next rule: original stays at `number`, delete it there.
		deleteAt = number
		// When inserting at insertAt, everything at/after shifts up, so the
		// original at `number` stays put only if insertAt > number. For moving
		// down one slot: insert at (next.Number+1), then delete the still-
		// original number.
	}

	ins := append([]string{"insert", fmt.Sprintf("%d", insertAt)}, args...)
	if out, err := execCommandFn("ufw", ins...).CombinedOutput(); err != nil {
		return fmt.Errorf("ufw insert: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := execCommandFn("ufw", "--force", "delete", fmt.Sprintf("%d", deleteAt)).CombinedOutput(); err != nil {
		return fmt.Errorf("ufw delete after move: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// DeduplicatePortAllows removes duplicate allow rules for the same port/proto/from
// (keeps the lowest number). Used after enable so re-enabling cannot stack copies.
func DeduplicatePortAllows() {
	st := getUFWStatus()
	seen := map[string]int{}
	// Delete from highest number to lowest so indices stay stable.
	var toDelete []int
	for _, r := range st.Rules {
		if strings.ToUpper(r.Action) != "ALLOW" || r.Port == "" {
			continue
		}
		key := r.Port + "/" + strings.ToLower(r.Proto) + "|" + normalizeFrom(r.From) + "|" + fmt.Sprint(r.V6)
		if prev, ok := seen[key]; ok {
			_ = prev
			toDelete = append(toDelete, r.Number)
			continue
		}
		seen[key] = r.Number
	}
	for i := len(toDelete) - 1; i >= 0; i-- {
		_ = DeleteRule(toDelete[i])
	}
}
