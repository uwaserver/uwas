package build

import (
	"strings"
	"testing"
)

func TestInfo(t *testing.T) {
	info := Info()
	if !strings.Contains(info, "uwas") {
		t.Errorf("Info() = %q, should contain 'uwas'", info)
	}
	if !strings.Contains(info, Version) {
		t.Errorf("Info() = %q, should contain version %q", info, Version)
	}
}
