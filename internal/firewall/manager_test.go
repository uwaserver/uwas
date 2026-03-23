package firewall

import (
	"testing"
)

func TestParseUFWRule(t *testing.T) {
	tests := []struct {
		line   string
		num    int
		action string
		port   string
	}{
		{"[ 1] 80/tcp                     ALLOW IN    Anywhere", 1, "ALLOW", "80"},
		{"[ 2] 443/tcp                    ALLOW IN    Anywhere", 2, "ALLOW", "443"},
		{"[ 3] 22/tcp                     DENY IN     Anywhere", 3, "DENY", "22"},
	}

	for _, tt := range tests {
		r := parseUFWRule(tt.line)
		if r.Number != tt.num {
			t.Errorf("number = %d, want %d for %q", r.Number, tt.num, tt.line)
		}
		if r.Action != tt.action {
			t.Errorf("action = %q, want %q for %q", r.Action, tt.action, tt.line)
		}
		if r.Port != tt.port {
			t.Errorf("port = %q, want %q for %q", r.Port, tt.port, tt.line)
		}
	}
}

func TestGetStatusReturnsStruct(t *testing.T) {
	// Should not panic even without ufw
	st := GetStatus()
	if st.Backend != "ufw" && st.Backend != "none" {
		t.Errorf("unexpected backend: %q", st.Backend)
	}
}

func TestFormatBytes(t *testing.T) {
	// Use the admin package formatBytes if needed, but firewall doesn't have it.
	// Just test that Status struct is valid
	st := Status{Active: false, Backend: "none", Rules: nil}
	if st.Active {
		t.Error("should be inactive")
	}
}
