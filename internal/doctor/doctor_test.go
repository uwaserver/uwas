package doctor

import "testing"

func TestExtractPHPVersion(t *testing.T) {
	tests := []struct{ input, want string }{
		{"/run/php/php8.3-fpm.sock", "8.3"},
		{"/run/php/php8.4-fpm.sock", "8.4"},
		{"/run/php/php-fpm.sock", ""},
		{"/var/run/php8.2-fpm.sock", "8.2"},
	}
	for _, tt := range tests {
		got := extractPHPVersion(tt.input)
		if got != tt.want {
			t.Errorf("extractPHPVersion(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestModulesToPackages(t *testing.T) {
	got := modulesToPackages([]string{"mysqli", "curl", "GD"})
	want := "php-mysqli php-curl php-gd"
	if got != want {
		t.Errorf("modulesToPackages = %q, want %q", got, want)
	}
}

func TestCheckOS(t *testing.T) {
	c := checkOS()
	if c.Status == "" {
		t.Error("checkOS returned empty status")
	}
}
