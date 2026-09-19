package firewall

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Simulates ufw on disk so enable→disable→enable cycles can be reproduced
// without a real firewall. State is a JSON-ish line file the helper reads via
// env (subprocess cannot share memory with the test).

type simState struct {
	mu     sync.Mutex
	path   string
	active bool
	rules  []simRule
}

type simRule struct {
	Action string
	Port   string
	Proto  string
	From   string
	V6     bool
}

func newSim(t *testing.T) *simState {
	t.Helper()
	dir := t.TempDir()
	s := &simState{path: filepath.Join(dir, "ufw.state")}
	s.persist()
	return s
}

func (s *simState) persist() {
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	if s.active {
		b.WriteString("active\n")
	} else {
		b.WriteString("inactive\n")
	}
	for _, r := range s.rules {
		b.WriteString(fmt.Sprintf("%s|%s|%s|%s|%v\n", r.Action, r.Port, r.Proto, r.From, r.V6))
	}
	_ = os.WriteFile(s.path, []byte(b.String()), 0644)
}

func (s *simState) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	s.active = len(lines) > 0 && lines[0] == "active"
	s.rules = nil
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		p := strings.Split(line, "|")
		if len(p) < 5 {
			continue
		}
		s.rules = append(s.rules, simRule{Action: p[0], Port: p[1], Proto: p[2], From: p[3], V6: p[4] == "true"})
	}
}

func (s *simState) statusNumbered() string {
	s.mu.Lock()
	s.load()
	defer s.mu.Unlock()
	var b strings.Builder
	if s.active {
		b.WriteString("Status: active\n\n")
	} else {
		// Match real ufw: inactive often lists no numbered rules.
		b.WriteString("Status: inactive\n")
		return b.String()
	}
	for i, r := range s.rules {
		to := "Anywhere"
		if r.Port != "" {
			to = r.Port
			if r.Proto != "" {
				to = r.Port + "/" + r.Proto
			}
		}
		from := r.From
		if from == "" {
			from = "Anywhere"
		}
		line := fmt.Sprintf("[%2d] %-26s %-11s %s", i+1, to, r.Action+" IN", from)
		if r.V6 {
			line += " (v6)"
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

func (s *simState) showAdded() string {
	s.mu.Lock()
	s.load()
	defer s.mu.Unlock()
	var b strings.Builder
	b.WriteString("Added user rules:\n")
	for _, r := range s.rules {
		if r.V6 {
			continue
		}
		act := strings.ToLower(r.Action)
		switch {
		case r.Port == "" && r.From == "":
			b.WriteString(fmt.Sprintf("ufw %s from any to any\n", act))
		case r.Port == "" && r.From != "":
			b.WriteString(fmt.Sprintf("ufw %s from %s\n", act, r.From))
		case r.From != "":
			line := fmt.Sprintf("ufw %s from %s to any port %s", act, r.From, r.Port)
			if r.Proto != "" {
				line += " proto " + r.Proto
			}
			b.WriteString(line + "\n")
		default:
			t := r.Port
			if r.Proto != "" {
				t = r.Port + "/" + r.Proto
			}
			b.WriteString(fmt.Sprintf("ufw %s %s\n", act, t))
		}
	}
	return b.String()
}

func (s *simState) add(action, port, proto, from string, skipExisting bool) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	from = normalizeFrom(from)
	if skipExisting {
		for _, r := range s.rules {
			if !r.V6 && strings.EqualFold(r.Action, action) && r.Port == port && r.Proto == proto && normalizeFrom(r.From) == from {
				return "Skipping adding existing rule\n"
			}
		}
	}
	s.insertRulesLocked(len(s.rules), action, port, proto, from)
	return ""
}

// insertAt inserts a rule at 1-based position (like `ufw insert N …`).
// Skips when an equivalent rule already exists (real ufw behavior).
func (s *simState) insertAt(pos int, action, port, proto, from string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	from = normalizeFrom(from)
	for _, r := range s.rules {
		if !r.V6 && strings.EqualFold(r.Action, action) && r.Port == port && r.Proto == proto && normalizeFrom(r.From) == from {
			return "Skipping adding existing rule\n"
		}
	}
	idx := pos - 1
	if idx < 0 {
		idx = 0
	}
	if idx > len(s.rules) {
		idx = len(s.rules)
	}
	s.insertRulesLocked(idx, action, port, proto, from)
	return ""
}

func (s *simState) insertRulesLocked(idx int, action, port, proto, from string) {
	v4 := simRule{Action: strings.ToUpper(action), Port: port, Proto: proto, From: from, V6: false}
	newRules := []simRule{v4}
	// IPv4-literal sources only get a v4 row (matches real ufw).
	if from == "" || looksLikeIPv6(from) {
		newRules = append(newRules, simRule{Action: strings.ToUpper(action), Port: port, Proto: proto, From: from, V6: true})
	} else if _, err := parseIPv4Literal(from); err != nil {
		// CIDR / hostname-ish: add both families like ufw often does for "anywhere".
		newRules = append(newRules, simRule{Action: strings.ToUpper(action), Port: port, Proto: proto, From: from, V6: true})
	}
	tail := append([]simRule{}, s.rules[idx:]...)
	s.rules = append(s.rules[:idx], append(newRules, tail...)...)
	s.persistLocked()
}

func parseIPv4Literal(s string) (string, error) {
	if strings.Contains(s, ":") || strings.Contains(s, "/") {
		return "", fmt.Errorf("not v4 literal")
	}
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return "", fmt.Errorf("not v4 literal")
	}
	return s, nil
}

func looksLikeIPv6(s string) bool {
	return strings.Contains(s, ":")
}

func (s *simState) persistLocked() {
	var b strings.Builder
	if s.active {
		b.WriteString("active\n")
	} else {
		b.WriteString("inactive\n")
	}
	for _, r := range s.rules {
		b.WriteString(fmt.Sprintf("%s|%s|%s|%s|%v\n", r.Action, r.Port, r.Proto, r.From, r.V6))
	}
	_ = os.WriteFile(s.path, []byte(b.String()), 0644)
}

func (s *simState) deleteNum(n int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	idx := n - 1
	if idx < 0 || idx >= len(s.rules) {
		return fmt.Errorf("missing")
	}
	s.rules = append(s.rules[:idx], s.rules[idx+1:]...)
	s.persistLocked()
	return nil
}

func (s *simState) setActive(v bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	s.active = v
	s.persistLocked()
}

func (s *simState) v4Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	n := 0
	for _, r := range s.rules {
		if !r.V6 {
			n++
		}
	}
	return n
}

func (s *simState) total() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	return len(s.rules)
}

func installSim(t *testing.T, s *simState) {
	t.Helper()
	origCmd := execCommandFn
	origLook := execLookPathFn
	origGOOS := runtimeGOOS
	t.Cleanup(func() {
		execCommandFn = origCmd
		execLookPathFn = origLook
		runtimeGOOS = origGOOS
		cancelRollback()
	})
	runtimeGOOS = "linux"
	execLookPathFn = fakeLookPath(true)
	statePath := s.path
	execCommandFn = func(name string, arg ...string) *exec.Cmd {
		out, fail := dispatchSim(statePath, arg)
		cs := []string{"-test.run=TestHelperProcess", "--", name}
		cs = append(cs, arg...)
		cmd := exec.Command(os.Args[0], cs...)
		exit := "0"
		if fail {
			exit = "1"
		}
		cmd.Env = append(os.Environ(),
			"GO_HELPER_PROCESS=1",
			"GO_HELPER_OUTPUT="+out,
			"GO_HELPER_EXIT="+exit,
			"UWAS_UFW_SIM="+statePath,
		)
		return cmd
	}
}

// dispatchSim mutates the on-disk sim state for the given ufw argv.
func dispatchSim(path string, arg []string) (string, bool) {
	s := &simState{path: path}
	s.load()
	if len(arg) == 0 {
		return "", true
	}
	switch arg[0] {
	case "status":
		return s.statusNumbered(), false
	case "show":
		return s.showAdded(), false
	case "default":
		return "", false
	case "disable":
		s.setActive(false)
		return "", false
	case "enable":
		s.setActive(true)
		return "", false
	case "--force":
		if len(arg) >= 2 && arg[1] == "enable" {
			s.setActive(true)
			return "", false
		}
		if len(arg) >= 3 && arg[1] == "delete" {
			var n int
			fmt.Sscanf(arg[2], "%d", &n)
			if err := s.deleteNum(n); err != nil {
				return err.Error(), true
			}
			return "", false
		}
		return "", false
	case "allow", "deny", "reject":
		action, port, proto, from := parseUFWAddArgs(arg)
		msg := s.add(action, port, proto, from, true)
		return msg, false
	case "insert":
		// insert N <allow|deny> …
		if len(arg) < 3 {
			return "bad insert", true
		}
		var pos int
		fmt.Sscanf(arg[1], "%d", &pos)
		action, port, proto, from := parseUFWAddArgs(arg[2:])
		msg := s.insertAt(pos, action, port, proto, from)
		return msg, false
	default:
		return "unknown", true
	}
}

func parseUFWAddArgs(arg []string) (action, port, proto, from string) {
	if len(arg) == 0 {
		return
	}
	action = arg[0]
	rest := arg[1:]
	if len(rest) >= 4 && rest[0] == "from" && rest[2] == "to" && rest[3] == "any" {
		from = rest[1]
		if from == "any" {
			from = ""
		}
		if len(rest) >= 6 && rest[4] == "port" {
			port = rest[5]
			if len(rest) >= 8 && rest[6] == "proto" {
				proto = rest[7]
			}
		}
		return
	}
	if len(rest) >= 2 && rest[0] == "from" {
		from = rest[1]
		if from == "any" {
			from = ""
		}
		return
	}
	if len(rest) == 1 {
		t := rest[0]
		if i := strings.Index(t, "/"); i >= 0 {
			port, proto = t[:i], t[i+1:]
		} else {
			port = t
		}
	}
	return
}

func TestSimulate_EnableDisableDoesNotDuplicateAllows(t *testing.T) {
	s := newSim(t)
	installSim(t, s)

	// Seed like a panel that already allowed SSH/HTTP/HTTPS once.
	s.add("allow", "22", "tcp", "", false)
	s.add("allow", "80", "tcp", "", false)
	s.add("allow", "443", "tcp", "", false)
	s.setActive(false)

	if err := EnableWithRollback(0, []string{"22", "80", "443"}); err != nil {
		t.Fatalf("enable 1: %v", err)
	}
	n1 := s.v4Count()
	if n1 < 3 {
		t.Fatalf("after enable: v4 rules=%d, want >=3", n1)
	}

	if err := Disable(); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := EnableWithRollback(0, []string{"22", "80", "443"}); err != nil {
		t.Fatalf("enable 2: %v", err)
	}
	n2 := s.v4Count()
	if n2 != n1 {
		t.Fatalf("re-enable duplicated rules: before=%d after=%d (total incl v6=%d)", n1, n2, s.total())
	}
}

func TestSimulate_DefaultDenyAtBottom(t *testing.T) {
	s := newSim(t)
	installSim(t, s)
	s.add("allow", "22", "tcp", "", false)
	s.setActive(false)

	if err := EnableWithRollback(0, []string{"22"}); err != nil {
		t.Fatalf("enable: %v", err)
	}

	st := GetStatus()
	if len(st.Rules) == 0 {
		t.Fatal("no rules after enable")
	}
	last := st.Rules[len(st.Rules)-1]
	// Prefer last IPv4 default deny; skip trailing v6 twin if present.
	for i := len(st.Rules) - 1; i >= 0; i-- {
		if !st.Rules[i].V6 {
			last = st.Rules[i]
			break
		}
	}
	if last.Action != "DENY" || last.Port != "" || normalizeFrom(last.From) != "" {
		t.Fatalf("last IPv4 rule should be default deny any/any, got %+v", last)
	}
}

func TestBuildPortRuleArgs_AnyPort(t *testing.T) {
	args, err := buildPortRuleArgs("deny", "", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, " ")
	if got != "deny from any to any" {
		t.Fatalf("got %q", got)
	}
	args, err = buildPortRuleArgs("allow", "any", "tcp", "203.0.113.8", 0)
	if err != nil {
		t.Fatal(err)
	}
	got = strings.Join(args, " ")
	if got != "allow from 203.0.113.8" {
		t.Fatalf("got %q want allow from IP (any port)", got)
	}
}

func TestSimulate_SourceOnlyAllow_EmptyProto(t *testing.T) {
	s := newSim(t)
	installSim(t, s)
	s.add("allow", "22", "tcp", "", false)
	s.setActive(false)

	if err := EnableWithRollback(0, []string{"22"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := AllowPortFrom("", "tcp", "203.0.113.50"); err != nil {
		t.Fatalf("allow source-only: %v", err)
	}
	st := GetStatus()
	var found Rule
	for _, r := range st.Rules {
		if !r.V6 && normalizeFrom(r.From) == "203.0.113.50" {
			found = r
			break
		}
	}
	if found.Number == 0 {
		t.Fatal("source-only allow not found")
	}
	if found.Port != "" {
		t.Fatalf("port=%q want empty (any)", found.Port)
	}
	if found.Proto != "" {
		t.Fatalf("proto=%q want empty (UI shows any)", found.Proto)
	}
	if !hasDefaultDeny(st.Rules) {
		t.Fatal("default deny missing after source-only allow")
	}
}

func TestSimulate_MoveUp_PreservesDefaultDeny(t *testing.T) {
	s := newSim(t)
	installSim(t, s)
	s.add("allow", "22", "tcp", "", false)
	s.add("allow", "80", "tcp", "", false)
	s.setActive(false)

	if err := EnableWithRollback(0, []string{"22", "80"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := AllowPortFrom("", "", "203.0.113.9"); err != nil {
		t.Fatalf("allow source: %v", err)
	}

	st := GetStatus()
	var srcNum int
	for _, r := range st.Rules {
		if !r.V6 && normalizeFrom(r.From) == "203.0.113.9" {
			srcNum = r.Number
			break
		}
	}
	if srcNum == 0 {
		t.Fatal("source rule missing")
	}

	// Move source rule up past the rule above it (may need several steps to
	// climb; one up is enough to exercise the buggy insert-skip path).
	if err := MoveRule(srcNum, "up"); err != nil {
		t.Fatalf("move up: %v", err)
	}
	st = GetStatus()
	if !hasDefaultDeny(st.Rules) {
		t.Fatalf("default deny vanished after move up; rules=%v", dumpRules(st.Rules))
	}

	// Climb until just above default deny — every step must keep deny.
	for step := 0; step < 10; step++ {
		st = GetStatus()
		logical := logicalIPv4Rules(st.Rules)
		var srcIdx int = -1
		for i, r := range logical {
			if normalizeFrom(r.From) == "203.0.113.9" {
				srcIdx = i
				break
			}
		}
		if srcIdx < 0 {
			t.Fatal("source rule lost during moves")
		}
		if srcIdx == 0 {
			break
		}
		if isDefaultDeny(logical[srcIdx-1]) {
			t.Fatal("source somehow below default deny neighbor above")
		}
		// Stop when next up would be past something we shouldn't — move until top.
		if err := MoveRule(logical[srcIdx].Number, "up"); err != nil {
			t.Fatalf("move up step %d: %v", step, err)
		}
		if !hasDefaultDeny(GetStatus().Rules) {
			t.Fatalf("default deny vanished at step %d; rules=%v", step, dumpRules(GetStatus().Rules))
		}
	}
	if !hasDefaultDeny(GetStatus().Rules) {
		t.Fatal("default deny missing at end")
	}
}

func TestSimulate_MoveDown_BlockedByDefaultDeny(t *testing.T) {
	s := newSim(t)
	installSim(t, s)
	s.add("allow", "22", "tcp", "", false)
	s.setActive(false)
	if err := EnableWithRollback(0, []string{"22"}); err != nil {
		t.Fatalf("enable: %v", err)
	}
	st := GetStatus()
	logical := logicalIPv4Rules(st.Rules)
	var allowNum, denyNum int
	for _, r := range logical {
		if r.Port == "22" {
			allowNum = r.Number
		}
		if isDefaultDeny(r) {
			denyNum = r.Number
		}
	}
	if allowNum == 0 || denyNum == 0 {
		t.Fatalf("setup failed: allow=%d deny=%d", allowNum, denyNum)
	}
	if err := MoveRule(allowNum, "down"); err == nil {
		t.Fatal("expected error moving allow past default deny")
	}
	if err := MoveRule(denyNum, "up"); err == nil {
		t.Fatal("expected error moving default deny")
	}
	if !hasDefaultDeny(GetStatus().Rules) {
		t.Fatal("default deny missing after refused moves")
	}
}

func TestSimulate_MoveUp_OldInsertSkipWouldDeleteDeny(t *testing.T) {
	// Reproduces the production bug: insert of an already-present rule is a
	// no-op, then deleteAt=number+1 removed the default deny.
	s := newSim(t)
	installSim(t, s)
	s.add("allow", "22", "tcp", "", false)
	s.setActive(true)
	_ = ensureDefaultDenyAtBottom()
	if err := AllowPortFrom("", "", "198.51.100.7"); err != nil {
		t.Fatalf("allow: %v", err)
	}

	st := GetStatus()
	var src Rule
	for _, r := range st.Rules {
		if !r.V6 && normalizeFrom(r.From) == "198.51.100.7" {
			src = r
			break
		}
	}
	if src.Number == 0 {
		t.Fatal("no source rule")
	}

	// Neighbor above source (last allow before deny, or deny itself in bad layouts).
	logical := logicalIPv4Rules(st.Rules)
	srcIdx := -1
	for i, r := range logical {
		if r.Number == src.Number {
			srcIdx = i
			break
		}
	}
	if srcIdx <= 0 {
		t.Fatalf("srcIdx=%d", srcIdx)
	}

	if err := MoveRule(src.Number, "up"); err != nil {
		t.Fatalf("move: %v", err)
	}
	st = GetStatus()
	if !hasDefaultDeny(st.Rules) {
		t.Fatalf("BUG: default deny deleted by move; rules=%v", dumpRules(st.Rules))
	}
	// Source must still exist exactly once (v4).
	n := 0
	for _, r := range st.Rules {
		if !r.V6 && normalizeFrom(r.From) == "198.51.100.7" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("source rule count=%d want 1; rules=%v", n, dumpRules(st.Rules))
	}
}

func TestSimulate_MoveSwap_TwoAllows(t *testing.T) {
	s := newSim(t)
	installSim(t, s)
	s.add("allow", "22", "tcp", "", false)
	s.add("allow", "80", "tcp", "", false)
	s.setActive(false)
	if err := EnableWithRollback(0, []string{"22", "80"}); err != nil {
		t.Fatalf("enable: %v", err)
	}

	st := GetStatus()
	logical := logicalIPv4Rules(st.Rules)
	var n80 int
	for _, r := range logical {
		if r.Port == "80" {
			n80 = r.Number
			break
		}
	}
	if n80 == 0 {
		t.Fatal("no :80")
	}
	if err := MoveRule(n80, "up"); err != nil {
		t.Fatalf("move: %v", err)
	}
	st = GetStatus()
	logical = logicalIPv4Rules(st.Rules)
	if len(logical) < 2 {
		t.Fatal("too few rules")
	}
	if logical[0].Port != "80" {
		t.Fatalf("after move up, first want 80 got %+v", logical[0])
	}
	if logical[1].Port != "22" {
		t.Fatalf("after move up, second want 22 got %+v", logical[1])
	}
	if !hasDefaultDeny(st.Rules) {
		t.Fatal("default deny missing")
	}

	// Move 80 back down.
	if err := MoveRule(logical[0].Number, "down"); err != nil {
		t.Fatalf("move down: %v", err)
	}
	logical = logicalIPv4Rules(GetStatus().Rules)
	if logical[0].Port != "22" || logical[1].Port != "80" {
		t.Fatalf("after move down want 22 then 80, got %+v %+v", logical[0], logical[1])
	}
	if !hasDefaultDeny(GetStatus().Rules) {
		t.Fatal("default deny missing after down")
	}
}

func TestSimulate_EnsureDefaultDeny_WhenOnlyV6Present(t *testing.T) {
	s := newSim(t)
	installSim(t, s)
	s.add("allow", "22", "tcp", "", false)
	s.setActive(true)

	// Orphan: only the IPv6 default deny remains (production bug after bad move).
	s.mu.Lock()
	s.load()
	var kept []simRule
	for _, r := range s.rules {
		if strings.EqualFold(r.Action, "DENY") && r.Port == "" && r.From == "" {
			continue // drop any existing deny twins from seed
		}
		kept = append(kept, r)
	}
	kept = append(kept, simRule{Action: "DENY", V6: true})
	s.rules = kept
	s.persistLocked()
	s.mu.Unlock()

	st := GetStatus()
	if hasDefaultDeny(st.Rules) {
		t.Fatalf("v6-only deny must not satisfy hasDefaultDeny; rules=%v", dumpRules(st.Rules))
	}
	if err := ensureDefaultDenyAtBottom(); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	st = GetStatus()
	if !hasDefaultDeny(st.Rules) {
		t.Fatalf("IPv4 default deny not restored; rules=%v", dumpRules(st.Rules))
	}
	// Still exactly one IPv4 and one IPv6 default deny after dedupe.
	v4, v6 := 0, 0
	for _, r := range st.Rules {
		if !isDefaultDeny(r) {
			continue
		}
		if r.V6 {
			v6++
		} else {
			v4++
		}
	}
	if v4 != 1 || v6 != 1 {
		t.Fatalf("want 1 v4 + 1 v6 default deny, got v4=%d v6=%d; rules=%v", v4, v6, dumpRules(st.Rules))
	}
}

func dumpRules(rules []Rule) string {
	var b strings.Builder
	for _, r := range rules {
		b.WriteString(fmt.Sprintf("#%d %s port=%q proto=%q from=%q v6=%v; ",
			r.Number, r.Action, r.Port, r.Proto, r.From, r.V6))
	}
	return b.String()
}
