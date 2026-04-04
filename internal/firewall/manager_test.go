package firewall

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
)

// TestHelperProcess is used by tests to mock exec.Command.
// It is not a real test — it is invoked as a subprocess.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_HELPER_PROCESS") != "1" {
		return
	}
	if os.Getenv("GO_HELPER_EXIT") == "1" {
		os.Exit(1)
	}
	fmt.Fprint(os.Stdout, os.Getenv("GO_HELPER_OUTPUT"))
	os.Exit(0)
}

// fakeExecCommand returns a function that creates a *exec.Cmd which re-invokes
// the test binary as TestHelperProcess, injecting the desired stdout output and
// exit code via environment variables.
func fakeExecCommand(output string, fail bool) func(name string, arg ...string) *exec.Cmd {
	return func(name string, arg ...string) *exec.Cmd {
		cs := []string{"-test.run=TestHelperProcess", "--", name}
		cs = append(cs, arg...)
		cmd := exec.Command(os.Args[0], cs...)
		exitVal := "0"
		if fail {
			exitVal = "1"
		}
		cmd.Env = append(os.Environ(),
			"GO_HELPER_PROCESS=1",
			"GO_HELPER_OUTPUT="+output,
			"GO_HELPER_EXIT="+exitVal,
		)
		return cmd
	}
}

// fakeLookPath returns a function that simulates exec.LookPath.
func fakeLookPath(found bool) func(file string) (string, error) {
	return func(file string) (string, error) {
		if found {
			return "/usr/sbin/" + file, nil
		}
		return "", fmt.Errorf("executable file not found in $PATH")
	}
}

// saveAndRestore saves the current package-level vars and returns a function
// that restores them. Call defer saveAndRestore()() in each test.
func saveAndRestore() func() {
	origGOOS := runtimeGOOS
	origCommand := execCommandFn
	origLookPath := execLookPathFn
	return func() {
		runtimeGOOS = origGOOS
		execCommandFn = origCommand
		execLookPathFn = origLookPath
	}
}

// ---------------------------------------------------------------------------
// GetStatus tests
// ---------------------------------------------------------------------------

func TestGetStatus_Windows(t *testing.T) {
	defer saveAndRestore()()
	runtimeGOOS = "windows"

	st := GetStatus()
	if st.Backend != "none" {
		t.Errorf("Backend = %q, want %q", st.Backend, "none")
	}
	if st.Active {
		t.Error("expected Active=false on windows")
	}
	if len(st.Rules) != 0 {
		t.Error("expected no rules on windows")
	}
}

func TestGetStatus_NoUFW(t *testing.T) {
	defer saveAndRestore()()
	runtimeGOOS = "linux"
	execLookPathFn = fakeLookPath(false)

	st := GetStatus()
	if st.Backend != "none" {
		t.Errorf("Backend = %q, want %q", st.Backend, "none")
	}
}

func TestGetStatus_Active(t *testing.T) {
	defer saveAndRestore()()
	runtimeGOOS = "linux"
	execLookPathFn = fakeLookPath(true)

	ufwOutput := `Status: active

     To                         Action      From
     --                         ------      ----
[ 1] 80/tcp                     ALLOW IN    Anywhere
[ 2] 443/tcp                    ALLOW IN    Anywhere
[ 3] 22/tcp                     DENY IN     Anywhere
`
	execCommandFn = fakeExecCommand(ufwOutput, false)

	st := GetStatus()
	if st.Backend != "ufw" {
		t.Errorf("Backend = %q, want %q", st.Backend, "ufw")
	}
	if !st.Active {
		t.Error("expected Active=true")
	}
	if len(st.Rules) != 3 {
		t.Fatalf("expected 3 rules, got %d", len(st.Rules))
	}
	if st.Rules[0].Port != "80" || st.Rules[0].Proto != "tcp" || st.Rules[0].Action != "ALLOW" {
		t.Errorf("rule 0 mismatch: %+v", st.Rules[0])
	}
	if st.Rules[2].Action != "DENY" {
		t.Errorf("rule 2 action = %q, want DENY", st.Rules[2].Action)
	}
}

func TestGetStatus_Inactive(t *testing.T) {
	defer saveAndRestore()()
	runtimeGOOS = "linux"
	execLookPathFn = fakeLookPath(true)

	ufwOutput := `Status: inactive
`
	execCommandFn = fakeExecCommand(ufwOutput, false)

	st := GetStatus()
	if st.Backend != "ufw" {
		t.Errorf("Backend = %q, want %q", st.Backend, "ufw")
	}
	if st.Active {
		t.Error("expected Active=false for inactive status")
	}
	if len(st.Rules) != 0 {
		t.Errorf("expected 0 rules, got %d", len(st.Rules))
	}
}

func TestGetStatus_EmptyOutput(t *testing.T) {
	defer saveAndRestore()()
	runtimeGOOS = "linux"
	execLookPathFn = fakeLookPath(true)

	execCommandFn = fakeExecCommand("", false)

	st := GetStatus()
	if st.Backend != "ufw" {
		t.Errorf("Backend = %q, want %q", st.Backend, "ufw")
	}
	if st.Active {
		t.Error("expected Active=false for empty output")
	}
	if len(st.Rules) != 0 {
		t.Errorf("expected 0 rules, got %d", len(st.Rules))
	}
}

func TestGetStatus_MalformedRules(t *testing.T) {
	defer saveAndRestore()()
	runtimeGOOS = "linux"
	execLookPathFn = fakeLookPath(true)

	ufwOutput := `Status: active

[ 1] 80/tcp                     ALLOW IN    Anywhere
[garbage line without proper format
[ 2] short
[ 3] 443/tcp                    DENY IN     Anywhere
`
	execCommandFn = fakeExecCommand(ufwOutput, false)

	st := GetStatus()
	if !st.Active {
		t.Error("expected Active=true")
	}
	// Only rule 1 and rule 3 should parse fully (with Action set).
	// "[garbage..." has no ']' at correct position so number extraction may differ,
	// but fields won't be valid. "[ 2] short" has < 3 fields, so Action="".
	if len(st.Rules) != 2 {
		t.Errorf("expected 2 valid rules, got %d", len(st.Rules))
		for i, r := range st.Rules {
			t.Logf("  rule %d: %+v", i, r)
		}
	}
}

func TestGetStatus_CommandFails(t *testing.T) {
	defer saveAndRestore()()
	runtimeGOOS = "linux"
	execLookPathFn = fakeLookPath(true)
	execCommandFn = fakeExecCommand("", true)

	st := GetStatus()
	if st.Backend != "ufw" {
		t.Errorf("Backend = %q, want %q", st.Backend, "ufw")
	}
	if st.Active {
		t.Error("expected Active=false when command fails")
	}
}

// ---------------------------------------------------------------------------
// parseUFWRule tests
// ---------------------------------------------------------------------------

func TestParseUFWRule_Standard(t *testing.T) {
	r := parseUFWRule("[ 1] 80/tcp                     ALLOW IN    Anywhere")
	if r.Number != 1 {
		t.Errorf("Number = %d, want 1", r.Number)
	}
	if r.Port != "80" {
		t.Errorf("Port = %q, want %q", r.Port, "80")
	}
	if r.Proto != "tcp" {
		t.Errorf("Proto = %q, want %q", r.Proto, "tcp")
	}
	if r.Action != "ALLOW" {
		t.Errorf("Action = %q, want %q", r.Action, "ALLOW")
	}
	if r.From != "Anywhere" {
		t.Errorf("From = %q, want %q", r.From, "Anywhere")
	}
	if r.To != "80/tcp" {
		t.Errorf("To = %q, want %q", r.To, "80/tcp")
	}
}

func TestParseUFWRule_IPv6(t *testing.T) {
	r := parseUFWRule("[ 2] 443                        ALLOW IN    Anywhere (v6)")
	if r.Number != 2 {
		t.Errorf("Number = %d, want 2", r.Number)
	}
	if r.Action != "ALLOW" {
		t.Errorf("Action = %q, want %q", r.Action, "ALLOW")
	}
	if r.From != "Anywhere" {
		t.Errorf("From = %q, want %q", r.From, "Anywhere")
	}
	if !r.V6 {
		t.Errorf("V6 = false, want true")
	}
	if r.To != "443" {
		t.Errorf("To = %q, want %q", r.To, "443")
	}
}

func TestParseUFWRule_WithFrom(t *testing.T) {
	// When source is a specific IP, "Anywhere" is not present
	r := parseUFWRule("[ 5] 3306/tcp                   ALLOW IN    192.168.1.100")
	if r.Number != 5 {
		t.Errorf("Number = %d, want 5", r.Number)
	}
	if r.Action != "ALLOW" {
		t.Errorf("Action = %q, want %q", r.Action, "ALLOW")
	}
	if r.Port != "3306" {
		t.Errorf("Port = %q, want %q", r.Port, "3306")
	}
	if r.Proto != "tcp" {
		t.Errorf("Proto = %q, want %q", r.Proto, "tcp")
	}
	// From should contain the specific IP source
	if r.From != "192.168.1.100" {
		t.Errorf("From = %q, want %q", r.From, "192.168.1.100")
	}
}

func TestParseUFWRule_Deny(t *testing.T) {
	r := parseUFWRule("[ 3] 22/tcp                     DENY IN     Anywhere")
	if r.Number != 3 {
		t.Errorf("Number = %d, want 3", r.Number)
	}
	if r.Action != "DENY" {
		t.Errorf("Action = %q, want %q", r.Action, "DENY")
	}
}

func TestParseUFWRule_InvalidFormat(t *testing.T) {
	r := parseUFWRule("this is garbage text with no brackets")
	if r.Action != "" {
		t.Errorf("Action = %q, want empty for garbage input", r.Action)
	}
	if r.Number != 0 {
		t.Errorf("Number = %d, want 0 for garbage input", r.Number)
	}
}

func TestParseUFWRule_NoNumber(t *testing.T) {
	// A line starting with '[' but no ']'
	r := parseUFWRule("[no closing bracket 80/tcp ALLOW IN Anywhere")
	// Index of ']' is -1, so idx <= 0 means number extraction is skipped.
	// The whole line goes to Fields; first token is "[no" which contains no "/".
	if r.Number != 0 {
		t.Errorf("Number = %d, want 0 for malformed bracket", r.Number)
	}
}

func TestParseUFWRule_Reject(t *testing.T) {
	r := parseUFWRule("[ 4] 8080/tcp                   REJECT IN   Anywhere")
	if r.Action != "REJECT" {
		t.Errorf("Action = %q, want %q", r.Action, "REJECT")
	}
	if r.Number != 4 {
		t.Errorf("Number = %d, want 4", r.Number)
	}
}

// ---------------------------------------------------------------------------
// AllowPort tests
// ---------------------------------------------------------------------------

func TestAllowPort_Success(t *testing.T) {
	defer saveAndRestore()()
	runtimeGOOS = "linux"
	execLookPathFn = fakeLookPath(true)
	execCommandFn = fakeExecCommand("", false)

	err := AllowPort("80", "tcp")
	if err != nil {
		t.Errorf("AllowPort() error = %v, want nil", err)
	}
}

func TestAllowPort_NoProto(t *testing.T) {
	defer saveAndRestore()()
	runtimeGOOS = "linux"
	execLookPathFn = fakeLookPath(true)
	execCommandFn = fakeExecCommand("", false)

	err := AllowPort("80", "")
	if err != nil {
		t.Errorf("AllowPort() error = %v, want nil", err)
	}
}

func TestAllowPort_Failure(t *testing.T) {
	defer saveAndRestore()()
	runtimeGOOS = "linux"
	execLookPathFn = fakeLookPath(true)
	execCommandFn = fakeExecCommand("", true)

	err := AllowPort("80", "tcp")
	if err == nil {
		t.Error("AllowPort() expected error, got nil")
	}
}

func TestAllowPort_NoUFW(t *testing.T) {
	defer saveAndRestore()()
	runtimeGOOS = "linux"
	execLookPathFn = fakeLookPath(false)

	err := AllowPort("80", "tcp")
	if err == nil {
		t.Error("AllowPort() expected error for missing ufw, got nil")
	}
	if err.Error() != "ufw not installed" {
		t.Errorf("error = %q, want %q", err.Error(), "ufw not installed")
	}
}

// ---------------------------------------------------------------------------
// DenyPort tests
// ---------------------------------------------------------------------------

func TestDenyPort_Success(t *testing.T) {
	defer saveAndRestore()()
	runtimeGOOS = "linux"
	execLookPathFn = fakeLookPath(true)
	execCommandFn = fakeExecCommand("", false)

	err := DenyPort("3306", "tcp")
	if err != nil {
		t.Errorf("DenyPort() error = %v, want nil", err)
	}
}

func TestDenyPort_NoProto(t *testing.T) {
	defer saveAndRestore()()
	runtimeGOOS = "linux"
	execLookPathFn = fakeLookPath(true)
	execCommandFn = fakeExecCommand("", false)

	err := DenyPort("3306", "")
	if err != nil {
		t.Errorf("DenyPort() error = %v, want nil", err)
	}
}

func TestDenyPort_ProtectedPort(t *testing.T) {
	defer saveAndRestore()()
	runtimeGOOS = "linux"
	execLookPathFn = fakeLookPath(true)
	execCommandFn = fakeExecCommand("", false)

	for _, port := range []string{"80", "443", "22"} {
		if err := DenyPort(port, "tcp"); err == nil {
			t.Errorf("DenyPort(%s) should fail for protected port", port)
		}
	}
}

func TestDenyPort_AnyBlocked(t *testing.T) {
	defer saveAndRestore()()
	runtimeGOOS = "linux"
	execLookPathFn = fakeLookPath(true)

	for _, port := range []string{"any", "all", "*", ""} {
		if err := DenyPort(port, ""); err == nil {
			t.Errorf("DenyPort(%q) should fail", port)
		}
	}
}

func TestDenyPort_Failure(t *testing.T) {
	defer saveAndRestore()()
	runtimeGOOS = "linux"
	execLookPathFn = fakeLookPath(true)
	execCommandFn = fakeExecCommand("", true)

	err := DenyPort("3306", "tcp")
	if err == nil {
		t.Error("DenyPort() expected error, got nil")
	}
}

func TestDenyPort_NoUFW(t *testing.T) {
	defer saveAndRestore()()
	runtimeGOOS = "linux"
	execLookPathFn = fakeLookPath(false)

	err := DenyPort("3306", "tcp")
	if err == nil {
		t.Error("DenyPort() expected error for missing ufw, got nil")
	}
	if err.Error() != "ufw not installed" {
		t.Errorf("error = %q, want %q", err.Error(), "ufw not installed")
	}
}

// ---------------------------------------------------------------------------
// DeleteRule tests
// ---------------------------------------------------------------------------

func TestDeleteRule_Success(t *testing.T) {
	defer saveAndRestore()()
	runtimeGOOS = "linux"
	execLookPathFn = fakeLookPath(true)
	execCommandFn = fakeExecCommand("", false)

	err := DeleteRule(1)
	if err != nil {
		t.Errorf("DeleteRule() error = %v, want nil", err)
	}
}

func TestDeleteRule_Failure(t *testing.T) {
	defer saveAndRestore()()
	runtimeGOOS = "linux"
	execLookPathFn = fakeLookPath(true)
	execCommandFn = fakeExecCommand("", true)

	err := DeleteRule(1)
	if err == nil {
		t.Error("DeleteRule() expected error, got nil")
	}
}

func TestDeleteRule_NoUFW(t *testing.T) {
	defer saveAndRestore()()
	runtimeGOOS = "linux"
	execLookPathFn = fakeLookPath(false)

	err := DeleteRule(1)
	if err == nil {
		t.Error("DeleteRule() expected error for missing ufw, got nil")
	}
	if err.Error() != "ufw not installed" {
		t.Errorf("error = %q, want %q", err.Error(), "ufw not installed")
	}
}

// ---------------------------------------------------------------------------
// Enable tests
// ---------------------------------------------------------------------------

func TestEnable_Success(t *testing.T) {
	defer saveAndRestore()()
	runtimeGOOS = "linux"
	execCommandFn = fakeExecCommand("", false)

	err := Enable()
	if err != nil {
		t.Errorf("Enable() error = %v, want nil", err)
	}
}

func TestEnable_Failure(t *testing.T) {
	defer saveAndRestore()()
	runtimeGOOS = "linux"
	execCommandFn = fakeExecCommand("", true)

	err := Enable()
	if err == nil {
		t.Error("Enable() expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// Disable tests
// ---------------------------------------------------------------------------

func TestDisable_Success(t *testing.T) {
	defer saveAndRestore()()
	runtimeGOOS = "linux"
	execCommandFn = fakeExecCommand("", false)

	err := Disable()
	if err != nil {
		t.Errorf("Disable() error = %v, want nil", err)
	}
}

func TestDisable_Failure(t *testing.T) {
	defer saveAndRestore()()
	runtimeGOOS = "linux"
	execCommandFn = fakeExecCommand("", true)

	err := Disable()
	if err == nil {
		t.Error("Disable() expected error, got nil")
	}
}

// ---------------------------------------------------------------------------
// Struct validation tests
// ---------------------------------------------------------------------------

func TestRuleStruct(t *testing.T) {
	r := Rule{
		Number:  1,
		Action:  "ALLOW",
		From:    "Anywhere",
		To:      "80/tcp",
		Port:    "80",
		Proto:   "tcp",
		Comment: "HTTP traffic",
	}
	if r.Number != 1 {
		t.Errorf("Number = %d, want 1", r.Number)
	}
	if r.Action != "ALLOW" {
		t.Errorf("Action = %q, want %q", r.Action, "ALLOW")
	}
	if r.From != "Anywhere" {
		t.Errorf("From = %q, want %q", r.From, "Anywhere")
	}
	if r.To != "80/tcp" {
		t.Errorf("To = %q, want %q", r.To, "80/tcp")
	}
	if r.Port != "80" {
		t.Errorf("Port = %q, want %q", r.Port, "80")
	}
	if r.Proto != "tcp" {
		t.Errorf("Proto = %q, want %q", r.Proto, "tcp")
	}
	if r.Comment != "HTTP traffic" {
		t.Errorf("Comment = %q, want %q", r.Comment, "HTTP traffic")
	}
}

func TestStatusStruct(t *testing.T) {
	rules := []Rule{
		{Number: 1, Action: "ALLOW", From: "Anywhere", To: "80/tcp", Port: "80", Proto: "tcp"},
		{Number: 2, Action: "DENY", From: "Anywhere", To: "22/tcp", Port: "22", Proto: "tcp"},
	}
	st := Status{
		Active:  true,
		Backend: "ufw",
		Rules:   rules,
	}
	if !st.Active {
		t.Error("expected Active=true")
	}
	if st.Backend != "ufw" {
		t.Errorf("Backend = %q, want %q", st.Backend, "ufw")
	}
	if len(st.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(st.Rules))
	}
	if st.Rules[0].Action != "ALLOW" {
		t.Errorf("Rules[0].Action = %q, want %q", st.Rules[0].Action, "ALLOW")
	}
	if st.Rules[1].Action != "DENY" {
		t.Errorf("Rules[1].Action = %q, want %q", st.Rules[1].Action, "DENY")
	}
}

// ---------------------------------------------------------------------------
// SetAdminPort tests
// ---------------------------------------------------------------------------

func TestSetAdminPort(t *testing.T) {
	// Clear protected ports before test
	protectedPorts = make(map[string]bool)

	SetAdminPort("9443")
	if !protectedPorts["9443"] {
		t.Error("expected port 9443 to be protected")
	}
}

func TestSetAdminPortWithHost(t *testing.T) {
	protectedPorts = make(map[string]bool)

	SetAdminPort("0.0.0.0:9443")
	if !protectedPorts["9443"] {
		t.Error("expected port 9443 to be protected from host:port format")
	}
}

func TestSetAdminPortEmpty(t *testing.T) {
	protectedPorts = make(map[string]bool)

	SetAdminPort("")
	if len(protectedPorts) != 0 {
		t.Error("expected no protected ports for empty input")
	}
}

func TestSetAdminPortColonOnly(t *testing.T) {
	protectedPorts = make(map[string]bool)

	SetAdminPort(":8080")
	if !protectedPorts["8080"] {
		t.Error("expected port 8080 to be protected from :port format")
	}
}

func TestDenyPortProtectedAdminPort(t *testing.T) {
	defer saveAndRestore()()
	runtimeGOOS = "linux"
	execLookPathFn = fakeLookPath(true)
	execCommandFn = fakeExecCommand("", false)

	// Clear and set protected ports
	protectedPorts = make(map[string]bool)
	SetAdminPort("9999")

	// Should be able to allow protected port
	err := AllowPort("9999", "tcp")
	if err != nil {
		t.Errorf("AllowPort() error = %v, want nil", err)
	}

	// Should NOT be able to deny protected port
	err = DenyPort("9999", "tcp")
	if err == nil {
		t.Error("DenyPort() expected error for protected admin port")
	}
}
