package phpmanager

import (
	"encoding/json"
	"strings"
	"testing"
)

// F46301: GET /api/v1/php/install-info returns GetInstallInfo as JSON. The
// dashboard types `packages` as string[] (like `commands`), but the Fedora,
// Arch and unrecognised-distribution branches never set it, so a nil slice
// serialised as `"packages":null`.

func TestInstallInfoPackagesIsAnArrayOnEveryDistro(t *testing.T) {
	origGOOS, origRead := runtimeGOOSInstall, readOSRelease
	t.Cleanup(func() { runtimeGOOSInstall, readOSRelease = origGOOS, origRead })
	runtimeGOOSInstall = "linux"

	cases := []struct {
		id      string
		release string
	}{
		{"fedora", "ID=fedora\nVERSION_ID=41\nPRETTY_NAME=\"Fedora\"\n"},
		{"arch", "ID=arch\nPRETTY_NAME=\"Arch Linux\"\n"},
		{"unrecognised", "ID=plan9\nPRETTY_NAME=\"Plan 9\"\n"},
		// Control: branches that fill packages already serialise an array.
		{"alpine", "ID=alpine\nVERSION_ID=3.20\nPRETTY_NAME=\"Alpine\"\n"},
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			readOSRelease = func() ([]byte, error) { return []byte(c.release), nil }
			raw, err := json.Marshal(GetInstallInfo("8.4"))
			if err != nil {
				t.Fatal(err)
			}
			body := string(raw)
			t.Logf("EXPECTED: \"packages\":[...] and \"commands\":[...] (never null)\nACTUAL: %s", body)
			if strings.Contains(body, `"packages":null`) {
				t.Errorf("PROBLEM CONFIRMED: packages serialised as null: %s", body)
			}
			if strings.Contains(body, `"commands":null`) {
				t.Errorf("commands serialised as null: %s", body)
			}
		})
	}
}
