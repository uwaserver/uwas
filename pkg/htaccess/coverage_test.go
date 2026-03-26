package htaccess

import (
	"strings"
	"testing"
)

// TestConvertPHPValueDirective covers the php_value directive path
// in Convert (lines 191-193).
func TestConvertPHPValueDirective(t *testing.T) {
	input := `php_value upload_max_filesize 64M
php_value memory_limit 256M`

	directives, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	rules := Convert(directives)

	if rules.PHPValues["upload_max_filesize"] != "64M" {
		t.Errorf("upload_max_filesize = %q, want 64M", rules.PHPValues["upload_max_filesize"])
	}
	if rules.PHPValues["memory_limit"] != "256M" {
		t.Errorf("memory_limit = %q, want 256M", rules.PHPValues["memory_limit"])
	}
}

// TestConvertPHPFlagDirective covers the php_flag directive path
// (lines 196-203).
func TestConvertPHPFlagDirective(t *testing.T) {
	input := `php_flag display_errors on
php_flag log_errors off
php_flag allow_url_include 1
php_flag register_globals true
php_flag session.use_cookies 0`

	directives, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	rules := Convert(directives)

	if rules.PHPFlags["display_errors"] != "1" {
		t.Errorf("display_errors = %q, want 1", rules.PHPFlags["display_errors"])
	}
	if rules.PHPFlags["log_errors"] != "0" {
		t.Errorf("log_errors = %q, want 0", rules.PHPFlags["log_errors"])
	}
	if rules.PHPFlags["allow_url_include"] != "1" {
		t.Errorf("allow_url_include = %q, want 1", rules.PHPFlags["allow_url_include"])
	}
	if rules.PHPFlags["register_globals"] != "1" {
		t.Errorf("register_globals = %q, want 1", rules.PHPFlags["register_globals"])
	}
	if rules.PHPFlags["session.use_cookies"] != "0" {
		t.Errorf("session.use_cookies = %q, want 0", rules.PHPFlags["session.use_cookies"])
	}
}

// TestConvertFilesMatchWithNoArgs covers the FilesMatch block with
// no args (empty pattern) at line 213-214.
func TestConvertFilesMatchWithNoArgs(t *testing.T) {
	input := `<FilesMatch>
Header set X-Test "value"
</FilesMatch>`

	directives, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	rules := Convert(directives)

	if len(rules.FilesMatch) != 1 {
		t.Fatalf("FilesMatch = %d, want 1", len(rules.FilesMatch))
	}
	if rules.FilesMatch[0].Pattern != "" {
		t.Errorf("pattern = %q, want empty", rules.FilesMatch[0].Pattern)
	}
}

// TestConvertRewriteCondTooFewArgs covers the RewriteCond case when
// fewer than 2 args are provided (lines 90-91 condition false).
func TestConvertRewriteCondTooFewArgs(t *testing.T) {
	input := `RewriteEngine On
RewriteCond %{REQUEST_URI}
RewriteRule ^(.*)$ /index.php [L]`

	directives, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	rules := Convert(directives)

	if !rules.RewriteEnabled {
		t.Error("RewriteEnabled should be true")
	}
	// The RewriteCond with only 1 arg should be skipped
	if len(rules.Rewrites) != 1 {
		t.Fatalf("rewrites = %d, want 1", len(rules.Rewrites))
	}
	if len(rules.Rewrites[0].Conditions) != 0 {
		t.Errorf("conditions = %d, want 0 (incomplete RewriteCond skipped)", len(rules.Rewrites[0].Conditions))
	}
}

// TestConvertRewriteRuleTooFewArgs covers the RewriteRule case when
// fewer than 2 args are provided (lines 102-103 condition false).
func TestConvertRewriteRuleTooFewArgs(t *testing.T) {
	input := `RewriteEngine On
RewriteRule ^onlypattern$`

	directives, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	rules := Convert(directives)

	// The RewriteRule with only 1 arg should be skipped
	if len(rules.Rewrites) != 0 {
		t.Errorf("rewrites = %d, want 0 (incomplete RewriteRule skipped)", len(rules.Rewrites))
	}
}

// TestConvertErrorDocumentInvalidCode covers ErrorDocument with a
// non-numeric first arg (lines 122-125 condition false).
func TestConvertErrorDocumentInvalidCode(t *testing.T) {
	input := `ErrorDocument abc /error.html`

	directives, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	rules := Convert(directives)

	// Invalid code "abc" → code=0, so it should not be stored
	if len(rules.ErrorDocuments) != 0 {
		t.Errorf("ErrorDocuments = %d, want 0 for invalid code", len(rules.ErrorDocuments))
	}
}

// TestConvertErrorDocumentTooFewArgs covers ErrorDocument with only 1 arg.
func TestConvertErrorDocumentTooFewArgs(t *testing.T) {
	input := `ErrorDocument 404`

	directives, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	rules := Convert(directives)

	if len(rules.ErrorDocuments) != 0 {
		t.Errorf("ErrorDocuments = %d, want 0 for single-arg ErrorDocument", len(rules.ErrorDocuments))
	}
}

// TestConvertHeaderTooFewArgs covers Header directive with fewer than 2 args.
func TestConvertHeaderTooFewArgs(t *testing.T) {
	input := `Header set`

	directives, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	rules := Convert(directives)

	// "Header set" has only 1 arg, should not be parsed as header
	// Actually it has 1 arg "set" which is still >= 2? No - "Header" is the name, "set" is args[0].
	// So len(d.Args) = 1, which is < 2, so it should be skipped.
	// Wait: "Header set" -> Name="Header", Args=["set"] -> len(d.Args)=1 < 2 -> skipped
	if len(rules.Headers) != 0 {
		t.Errorf("Headers = %d, want 0 for single-arg Header", len(rules.Headers))
	}
}

// TestConvertExpiresActiveOff covers ExpiresActive Off.
func TestConvertExpiresActiveOff(t *testing.T) {
	input := `ExpiresActive Off`

	directives, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	rules := Convert(directives)

	if rules.ExpiresActive {
		t.Error("ExpiresActive should be false for Off")
	}
}

// TestConvertExpiresByTypeTooFewArgs covers ExpiresByType with only 1 arg.
func TestConvertExpiresByTypeTooFewArgs(t *testing.T) {
	input := `ExpiresByType text/html`

	directives, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	rules := Convert(directives)

	if len(rules.ExpiresByType) != 0 {
		t.Errorf("ExpiresByType = %d, want 0 for single-arg", len(rules.ExpiresByType))
	}
}

// TestConvertRewriteEngineNoArgs covers RewriteEngine with no args.
func TestConvertRewriteEngineNoArgs(t *testing.T) {
	// Manually create a directive with no args
	directives := []Directive{
		{Name: "RewriteEngine", Args: nil},
	}
	rules := Convert(directives)
	if rules.RewriteEnabled {
		t.Error("should not be enabled with no args")
	}
}

// TestConvertExpiresActiveNoArgs covers ExpiresActive with no args.
func TestConvertExpiresActiveNoArgs(t *testing.T) {
	directives := []Directive{
		{Name: "ExpiresActive", Args: nil},
	}
	rules := Convert(directives)
	if rules.ExpiresActive {
		t.Error("should not be active with no args")
	}
}

// TestConvertAuthTypeNoArgs covers AuthType with no args.
func TestConvertAuthTypeNoArgs(t *testing.T) {
	directives := []Directive{
		{Name: "AuthType", Args: nil},
	}
	rules := Convert(directives)
	if rules.AuthType != "" {
		t.Errorf("AuthType = %q, want empty", rules.AuthType)
	}
}

// TestConvertAuthNameNoArgs covers AuthName with no args.
func TestConvertAuthNameNoArgs(t *testing.T) {
	directives := []Directive{
		{Name: "AuthName", Args: nil},
	}
	rules := Convert(directives)
	if rules.AuthName != "" {
		t.Errorf("AuthName = %q, want empty", rules.AuthName)
	}
}

// TestConvertAuthUserFileNoArgs covers AuthUserFile with no args.
func TestConvertAuthUserFileNoArgs(t *testing.T) {
	directives := []Directive{
		{Name: "AuthUserFile", Args: nil},
	}
	rules := Convert(directives)
	if rules.AuthUserFile != "" {
		t.Errorf("AuthUserFile = %q, want empty", rules.AuthUserFile)
	}
}

// TestConvertPHPValueTooFewArgs covers php_value with fewer than 2 args.
func TestConvertPHPValueTooFewArgs(t *testing.T) {
	directives := []Directive{
		{Name: "php_value", Args: []string{"memory_limit"}},
	}
	rules := Convert(directives)
	if len(rules.PHPValues) != 0 {
		t.Errorf("PHPValues = %d, want 0", len(rules.PHPValues))
	}
}

// TestConvertPHPFlagTooFewArgs covers php_flag with fewer than 2 args.
func TestConvertPHPFlagTooFewArgs(t *testing.T) {
	directives := []Directive{
		{Name: "php_flag", Args: []string{"display_errors"}},
	}
	rules := Convert(directives)
	if len(rules.PHPFlags) != 0 {
		t.Errorf("PHPFlags = %d, want 0", len(rules.PHPFlags))
	}
}

// TestConvertUnknownDirectiveWithNoBlock covers the default case in Convert
// when the directive has no block (line 207 condition false).
func TestConvertUnknownDirectiveWithNoBlock(t *testing.T) {
	directives := []Directive{
		{Name: "UnknownDirective", Args: []string{"arg1"}},
	}
	rules := Convert(directives)
	// Should not panic, just be ignored
	_ = rules
}

// TestConvertRedirectWithSingleArg covers Redirect with only 1 arg.
func TestConvertRedirectWithSingleArg(t *testing.T) {
	directives := []Directive{
		{Name: "Redirect", Args: []string{"/old"}},
	}
	rules := Convert(directives)

	// Single arg redirect - parseRedirect handles this in the default case
	if len(rules.Redirects) != 1 {
		t.Fatalf("redirects = %d, want 1", len(rules.Redirects))
	}
	// With only 1 arg, Pattern and Target will be empty (default case)
	if rules.Redirects[0].Status != 302 {
		t.Errorf("status = %d, want 302 (default)", rules.Redirects[0].Status)
	}
}

// TestMergePHPValuesAndFlags covers Merge when other has PHPValues and PHPFlags.
func TestMergePHPValuesAndFlags(t *testing.T) {
	base := NewRuleSet()
	base.PHPValues["existing"] = "value"

	other := NewRuleSet()
	other.PHPValues["upload_max_filesize"] = "64M"
	other.PHPFlags["display_errors"] = "1"

	base.Merge(other)

	if base.PHPValues["upload_max_filesize"] != "64M" {
		t.Errorf("upload_max_filesize = %q, want 64M", base.PHPValues["upload_max_filesize"])
	}
	if base.PHPValues["existing"] != "value" {
		t.Errorf("existing = %q, want value", base.PHPValues["existing"])
	}
	if base.PHPFlags["display_errors"] != "1" {
		t.Errorf("display_errors = %q, want 1", base.PHPFlags["display_errors"])
	}
}

// TestMergeNoOverwriteOnEmpty covers Merge when other has empty/default fields
// that should NOT overwrite base values.
func TestMergeNoOverwriteOnEmpty(t *testing.T) {
	base := NewRuleSet()
	base.RewriteEnabled = true
	base.ExpiresActive = true
	base.AuthType = "Basic"
	base.AuthName = "Secure"
	base.AuthUserFile = "/etc/htpasswd"
	base.Require = "valid-user"
	listing := true
	base.DirectoryListing = &listing

	other := NewRuleSet()
	// All fields at default (zero) values

	base.Merge(other)

	// Base values should remain
	if !base.RewriteEnabled {
		t.Error("RewriteEnabled should still be true")
	}
	if !base.ExpiresActive {
		t.Error("ExpiresActive should still be true")
	}
	if base.AuthType != "Basic" {
		t.Errorf("AuthType = %q, want Basic", base.AuthType)
	}
}

// TestConvertPHPValueMultiWordValue covers php_value with a multi-word value.
func TestConvertPHPValueMultiWordValue(t *testing.T) {
	input := `php_value error_log /var/log/php errors.log`

	directives, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	rules := Convert(directives)

	// Args should be ["error_log", "/var/log/php", "errors.log"]
	// Value should be joined: "/var/log/php errors.log"
	if rules.PHPValues["error_log"] != "/var/log/php errors.log" {
		t.Errorf("error_log = %q, want '/var/log/php errors.log'", rules.PHPValues["error_log"])
	}
}

// TestConvertErrorDocumentMultiWordValue covers ErrorDocument with multi-word value.
func TestConvertErrorDocumentMultiWordValue(t *testing.T) {
	input := `ErrorDocument 503 "Service Temporarily Unavailable"`

	directives, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	rules := Convert(directives)

	if rules.ErrorDocuments[503] != "Service Temporarily Unavailable" {
		t.Errorf("503 = %q", rules.ErrorDocuments[503])
	}
}
