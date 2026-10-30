package cli

// F52600: `uwas init` writes a pin_code into the config (the admin API then
// refuses destructive routes such as `domain remove` without it) but the .env
// file next to it held only the API key, so the CLI could never learn the PIN.
// The .env now carries UWAS_ADMIN_PIN, which every CLI request sends.

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func envFileValues(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			out[k] = v
		}
	}
	return out
}

func TestEnsureDefaultConfigEnvCarriesThePin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfgPath, err := ensureDefaultConfig("8080", "9443", "127.0.0.1", filepath.Join(home, "www"), "")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`pin_code:\s*"(\d{6})"`).FindStringSubmatch(string(cfg))
	if m == nil {
		t.Fatalf("control: the generated config has no 6-digit pin_code:\n%s", cfg)
	}
	env := envFileValues(t, filepath.Join(filepath.Dir(cfgPath), ".env"))
	t.Logf("EXPECTED: UWAS_ADMIN_PIN=%s\nACTUAL: %q", m[1], env["UWAS_ADMIN_PIN"])
	if env["UWAS_ADMIN_PIN"] != m[1] {
		t.Fatalf("PROBLEM CONFIRMED: .env UWAS_ADMIN_PIN = %q, want the config's pin_code %q", env["UWAS_ADMIN_PIN"], m[1])
	}
	if env["UWAS_ADMIN_KEY"] == "" {
		t.Fatal("control: .env must still carry UWAS_ADMIN_KEY")
	}
}

// The error text for the two refusals an operator can fix names the fix, and
// keeps the "API error <status>: <body>" form other code and tests match on.
func TestAPIErrorHints(t *testing.T) {
	cases := []struct {
		status    int
		body      string
		wantHint  string
		wantPlain bool
	}{
		{401, `{"error":"unauthorized"}`, "UWAS_ADMIN_KEY", false},
		{403, `{"error":"pin_required"}`, "UWAS_ADMIN_PIN", false},
		{403, `{"error":"invalid_pin"}`, "UWAS_ADMIN_PIN", false},
		{403, `{"error":"csrf: invalid origin"}`, "", true},
		{404, `{"error":"domain not found"}`, "", true},
		{500, `{"error":"boom"}`, "", true},
	}
	for _, c := range cases {
		err := (&apiError{status: c.status, body: c.body})
		msg := err.Error()
		if !strings.HasPrefix(msg, "API error ") || !strings.Contains(msg, c.body) {
			t.Errorf("%d %s: %q lost the \"API error <status>: <body>\" form", c.status, c.body, msg)
		}
		if c.wantPlain && msg != "API error "+strconv.Itoa(c.status)+": "+c.body {
			t.Errorf("%d %s: unexpected hint in %q", c.status, c.body, msg)
		}
		if c.wantHint != "" && !strings.Contains(msg, c.wantHint) {
			t.Errorf("%d %s: %q lacks the hint %q", c.status, c.body, msg, c.wantHint)
		}
	}
}
