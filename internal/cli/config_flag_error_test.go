package cli

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigCommandPropagatesFlagErrors(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fixture.yaml")
	if e := os.WriteFile(p, []byte("domains: []\n"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, sub := range []string{"validate", "test"} {
		cmd := &ConfigCommand{}
		if e := cmd.Run([]string{sub, "-c", p}); e != nil {
			t.Fatal(e)
		}
		for _, args := range [][]string{{sub, "-c", p, "--unknown-audit-option"}, {sub, "-c", p, "-c"}} {
			e := cmd.Run(args)
			if e == nil || (!strings.Contains(e.Error(), "flag") && !strings.Contains(e.Error(), "argument")) {
				t.Fatal(args, e)
			}
		}
		if e := cmd.Run([]string{sub, "-c", filepath.Join(t.TempDir(), "missing"), "-h"}); !errors.Is(e, flag.ErrHelp) {
			t.Fatal("help must stop before config loading", e)
		}
	}
	t.Log("FIX VERIFIED")
}
