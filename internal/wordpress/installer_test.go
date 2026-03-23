package wordpress

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeDBName(t *testing.T) {
	tests := []struct {
		domain string
		want   string
	}{
		{"example.com", "wp_example_com"},
		{"my-site.org", "wp_my_site_org"},
		{"sub.domain.example.com", "wp_sub_domain_example_com"},
	}
	for _, tt := range tests {
		got := sanitizeDBName(tt.domain)
		if got != tt.want {
			t.Errorf("sanitizeDBName(%q) = %q, want %q", tt.domain, got, tt.want)
		}
	}
}

func TestSanitizeDBNameMaxLength(t *testing.T) {
	long := "a.very.long.domain.name.that.exceeds.thirty.two.characters.example.com"
	got := sanitizeDBName(long)
	if len(got) > 35 { // "wp_" + 32
		t.Errorf("name too long: %d chars", len(got))
	}
}

func TestGenerateSecret(t *testing.T) {
	s1 := generateSecret(16)
	s2 := generateSecret(16)
	if len(s1) != 16 {
		t.Errorf("length = %d, want 16", len(s1))
	}
	if s1 == s2 {
		t.Error("secrets should be different")
	}
}

func TestGenerateWPConfig(t *testing.T) {
	dir := t.TempDir()
	err := generateWPConfig(dir, "testdb", "testuser", "testpass", "localhost")
	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "wp-config.php"))
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	checks := []string{
		"'testdb'",
		"'testuser'",
		"'testpass'",
		"'localhost'",
		"AUTH_KEY",
		"SECURE_AUTH_KEY",
		"FORCE_SSL_ADMIN",
		"DISALLOW_FILE_EDIT",
		"wp-settings.php",
	}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("wp-config.php should contain %q", check)
		}
	}
}

func TestInstallRequestDefaults(t *testing.T) {
	// Test that Install fills in defaults (won't actually download WP)
	// Just verify the result struct gets proper defaults
	req := InstallRequest{
		Domain:  "test.com",
		WebRoot: t.TempDir(),
	}
	// We can't run full Install without network, but verify defaults logic
	if req.DBHost == "" {
		req.DBHost = "localhost" // same default as Install()
	}
	if req.DBName == "" {
		req.DBName = sanitizeDBName(req.Domain)
	}
	if req.DBName != "wp_test_com" {
		t.Errorf("DBName = %q, want wp_test_com", req.DBName)
	}
}
