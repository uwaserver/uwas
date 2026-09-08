package settings

import (
	"strings"
	"testing"
)

// The raw config editor shows secrets as "********". On save, a line still
// holding the mask must be restored from disk, not written literally — writing
// the mask overwrites the real secret. A line the operator changed keeps its
// new value.
func TestUnmaskYAMLValueRestoresMaskedSecret(t *testing.T) {
	old := "global:\n  admin:\n    api_key: realsecret123\n    pin_code: 4242\n"
	// operator saved with api_key still masked, pin_code changed to a new value
	neu := "global:\n  admin:\n    api_key: \"********\"\n    pin_code: 9999\n"

	out := unmaskYAMLValue(neu, old, "api_key")
	out = unmaskYAMLValue(out, old, "pin_code")

	if !strings.Contains(out, "api_key: realsecret123") {
		t.Errorf("masked api_key was not restored:\n%s", out)
	}
	if strings.Contains(out, `api_key: "********"`) {
		t.Error("the mask must never survive to disk")
	}
	if !strings.Contains(out, "pin_code: 9999") {
		t.Errorf("a changed secret must keep its new value:\n%s", out)
	}
}

// Duplicate keys (password under multiple sections) each restore from their
// corresponding original, by occurrence order.
func TestUnmaskYAMLValueDuplicateKeysByOccurrence(t *testing.T) {
	old := "s3:\n  password: s3pass\nsftp:\n  password: sftppass\n"
	neu := "s3:\n  password: \"********\"\nsftp:\n  password: \"********\"\n"
	out := unmaskYAMLValue(neu, old, "password")
	if !strings.Contains(out, "s3pass") || !strings.Contains(out, "sftppass") {
		t.Errorf("duplicate-key restore failed:\n%s", out)
	}
}

// A value the operator changed away from the mask is left alone even if the
// original differs.
func TestUnmaskYAMLValueKeepsChangedValue(t *testing.T) {
	old := "purge_key: oldkey\n"
	neu := "purge_key: brandnewkey\n"
	out := unmaskYAMLValue(neu, old, "purge_key")
	if !strings.Contains(out, "purge_key: brandnewkey") {
		t.Errorf("changed value must be preserved:\n%s", out)
	}
}
