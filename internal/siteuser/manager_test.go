package siteuser

import (
	"testing"
)

func TestDomainToUsername(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"example.com", "uwas-example--com"},
		{"www.example.com", "uwas-www--example--com"},
		{"a.b.c", "uwas-a--b--c"},
		{"EXAMPLE.COM", "uwas-example--com"},
		{"short", "uwas-short"},
	}

	for _, tt := range tests {
		got := domainToUsername(tt.input)
		if got != tt.want {
			t.Errorf("domainToUsername(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestDomainToUsernameTruncation(t *testing.T) {
	// A very long domain should be truncated to 32 chars
	long := "very-long-subdomain.example.com"
	got := domainToUsername(long)
	if len(got) > 32 {
		t.Errorf("domainToUsername(%q) length = %d, want <= 32", long, len(got))
	}
	if got[:5] != "uwas-" {
		t.Errorf("domainToUsername result should start with 'uwas-', got %q", got)
	}
}

func TestGeneratePasswordUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		p := generatePassword()
		if len(p) == 0 {
			t.Fatal("generatePassword returned empty string")
		}
		if seen[p] {
			t.Fatalf("generatePassword produced duplicate: %q", p)
		}
		seen[p] = true
	}
}

func TestGeneratePasswordLength(t *testing.T) {
	p := generatePassword()
	// 12 random bytes -> 24 hex chars
	if len(p) != 24 {
		t.Errorf("expected password length 24, got %d", len(p))
	}
}

func TestUserStruct(t *testing.T) {
	u := User{
		Username: "uwas-example--com",
		Domain:   "example.com",
		HomeDir:  "/var/www/example.com",
		WebDir:   "/var/www/example.com/public_html",
	}

	if u.Username != "uwas-example--com" {
		t.Errorf("expected Username 'uwas-example--com', got %q", u.Username)
	}
	if u.Domain != "example.com" {
		t.Errorf("expected Domain 'example.com', got %q", u.Domain)
	}
	if u.HomeDir != "/var/www/example.com" {
		t.Errorf("expected HomeDir '/var/www/example.com', got %q", u.HomeDir)
	}
}
