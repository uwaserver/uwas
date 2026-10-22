package phpmanager

import (
	"strings"
	"testing"
)

// F23700: AssignDomain used to ignore PHPInstall.Disabled, so a version the
// operator disabled (EOL / vulnerable) could still be assigned afterwards —
// including by a tenant through POST /api/v1/php/domains.
func TestAssignDomainRejectsDisabledVersion(t *testing.T) {
	m := New(testLogger())
	m.installations = []PHPInstall{
		{Version: "8.1.30", Binary: "/usr/bin/php-cgi8.1", SAPI: "cgi-fcgi"},
		{Version: "8.4.27", Binary: "/usr/bin/php-cgi8.4", SAPI: "cgi-fcgi"},
	}
	if err := m.DisableVersion("8.1"); err != nil {
		t.Fatalf("DisableVersion: %v", err)
	}

	if _, err := m.AssignDomain("ok.test", "8.4"); err != nil {
		t.Fatalf("enabled version must stay assignable: %v", err)
	}
	for _, v := range []string{"8.1", "8.1.30"} {
		if _, err := m.AssignDomain("old.test", v); err == nil || !strings.Contains(err.Error(), "disabled") {
			t.Fatalf("AssignDomain(%q) = %v, want a \"disabled\" error", v, err)
		}
	}
	if _, err := m.AssignDomainWithRoot("old.test", "8.1", "/var/www/old"); err == nil {
		t.Fatal("AssignDomainWithRoot must reject a disabled version too")
	}
	if got := m.GetDomainInstances(); len(got) != 1 || got[0].Domain != "ok.test" {
		t.Fatalf("a rejected assignment must leave no state behind, got %+v", got)
	}
	if _, err := m.AssignDomain("none.test", "7.4"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown version must still report not found, got %v", err)
	}

	m.EnableVersion("8.1")
	if _, err := m.AssignDomain("old.test", "8.1"); err != nil {
		t.Fatalf("re-enabled version must be assignable again: %v", err)
	}
}
