package wordpress

// F31201: SetDebugMode removed existing WP_DEBUG defines with a regex that only
// matched single-quoted names and lowercase true/false. A wp-config.php that
// says define('WP_DEBUG', FALSE); (common in hand-written and host-generated
// configs) kept that line, and the new define was inserted AFTER it. PHP keeps
// the FIRST define and warns about the second, so "enable debug" silently did
// nothing and every request logged "Constant WP_DEBUG already defined".

import (
	"strings"
	"testing"
)

func TestSetDebugModeReplacesUppercaseAndDoubleQuotedDefines(t *testing.T) {
	cases := []struct {
		name, line string
	}{
		{"lowercase single quotes (control)", "define('WP_DEBUG', false);"},
		{"uppercase FALSE", "define('WP_DEBUG', FALSE);"},
		{"double quotes", `define("WP_DEBUG", false);`},
		{"spaced and uppercase", "define( 'WP_DEBUG_LOG', TRUE );"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			snap := saveHooks()
			defer restoreHooks(snap)
			root := seedWPConfig(t, "<?php\n"+c.line+"\n\nrequire_once ABSPATH . 'wp-settings.php';\n")
			if err := SetDebugMode(root, true); err != nil {
				t.Fatalf("SetDebugMode: %v", err)
			}
			got := readWPConfig(t, root)
			n := strings.Count(strings.ToUpper(got), "'WP_DEBUG'") + strings.Count(strings.ToUpper(got), `"WP_DEBUG"`)
			t.Logf("EXPECTED: exactly one WP_DEBUG define\nACTUAL: %d\n%s", n, got)
			if n != 1 {
				t.Fatalf("PROBLEM CONFIRMED: %d WP_DEBUG defines after enabling debug (the stale one wins in PHP)", n)
			}
			if strings.Count(strings.ToUpper(got), "WP_DEBUG_LOG'") > 1 || strings.Count(strings.ToUpper(got), `WP_DEBUG_LOG"`) > 1 {
				t.Fatalf("PROBLEM CONFIRMED: duplicate WP_DEBUG_LOG defines:\n%s", got)
			}
		})
	}
}
