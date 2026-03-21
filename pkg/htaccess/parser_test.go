package htaccess

import (
	"strings"
	"testing"
)

func TestParseWordPress(t *testing.T) {
	htaccess := `# BEGIN WordPress
<IfModule mod_rewrite.c>
RewriteEngine On
RewriteBase /
RewriteRule ^index\.php$ - [L]
RewriteCond %{REQUEST_FILENAME} !-f
RewriteCond %{REQUEST_FILENAME} !-d
RewriteRule . /index.php [L]
</IfModule>
# END WordPress`

	directives, err := Parse(strings.NewReader(htaccess))
	if err != nil {
		t.Fatal(err)
	}

	if len(directives) != 1 {
		t.Fatalf("directives count = %d, want 1 (IfModule block)", len(directives))
	}

	block := directives[0]
	if block.Name != "IfModule" {
		t.Errorf("block name = %q, want IfModule", block.Name)
	}

	// Should have 4 inner directives: RewriteEngine, RewriteBase, 2x RewriteRule
	// (RewriteCond is a separate directive)
	if len(block.Block) < 4 {
		t.Errorf("block inner count = %d, want >= 4", len(block.Block))
	}
}

func TestParseLaravel(t *testing.T) {
	htaccess := `<IfModule mod_rewrite.c>
    <IfModule mod_negotiation.c>
        Options -MultiViews -Indexes
    </IfModule>

    RewriteEngine On

    # Handle Authorization Header
    RewriteCond %{HTTP:Authorization} .
    RewriteRule .* - [E=HTTP_AUTHORIZATION:%{HTTP:Authorization}]

    # Redirect Trailing Slashes If Not A Folder...
    RewriteCond %{REQUEST_FILENAME} !-d
    RewriteCond %{REQUEST_URI} (.+)/$
    RewriteRule ^ %1 [L,R=301]

    # Send Requests To Front Controller...
    RewriteCond %{REQUEST_FILENAME} !-d
    RewriteCond %{REQUEST_FILENAME} !-f
    RewriteRule ^ /index.php [L]
</IfModule>`

	directives, err := Parse(strings.NewReader(htaccess))
	if err != nil {
		t.Fatal(err)
	}

	if len(directives) != 1 {
		t.Fatalf("directives count = %d, want 1", len(directives))
	}

	// The outer IfModule should contain the inner IfModule + RewriteEngine + rules
	block := directives[0]
	if len(block.Block) < 5 {
		t.Errorf("inner directives = %d, want >= 5", len(block.Block))
	}
}

func TestParseSimpleDirectives(t *testing.T) {
	htaccess := `ErrorDocument 404 /errors/404.html
ErrorDocument 500 /errors/500.html
DirectoryIndex index.php index.html
Header set X-Frame-Options SAMEORIGIN
Options -Indexes`

	directives, err := Parse(strings.NewReader(htaccess))
	if err != nil {
		t.Fatal(err)
	}

	if len(directives) != 5 {
		t.Fatalf("count = %d, want 5", len(directives))
	}

	if directives[0].Name != "ErrorDocument" {
		t.Errorf("first directive = %q, want ErrorDocument", directives[0].Name)
	}
	if directives[2].Name != "DirectoryIndex" {
		t.Errorf("third directive = %q, want DirectoryIndex", directives[2].Name)
	}
}

func TestParseQuotedStrings(t *testing.T) {
	htaccess := `AuthName "Restricted Area"
Header set X-Custom "value with spaces"`

	directives, err := Parse(strings.NewReader(htaccess))
	if err != nil {
		t.Fatal(err)
	}

	if len(directives) != 2 {
		t.Fatalf("count = %d, want 2", len(directives))
	}
	if directives[0].Args[0] != "Restricted Area" {
		t.Errorf("AuthName value = %q, want 'Restricted Area'", directives[0].Args[0])
	}
}

func TestParseLineContinuation(t *testing.T) {
	htaccess := `RewriteRule ^very-long-pattern$ \
    /very-long-target [L]`

	directives, err := Parse(strings.NewReader(htaccess))
	if err != nil {
		t.Fatal(err)
	}

	if len(directives) != 1 {
		t.Fatalf("count = %d, want 1", len(directives))
	}
	if directives[0].Name != "RewriteRule" {
		t.Errorf("name = %q, want RewriteRule", directives[0].Name)
	}
}

func TestParseComments(t *testing.T) {
	htaccess := `# This is a comment
RewriteEngine On
# Another comment
RewriteRule ^test$ /target [L]`

	directives, err := Parse(strings.NewReader(htaccess))
	if err != nil {
		t.Fatal(err)
	}

	if len(directives) != 2 {
		t.Fatalf("count = %d, want 2 (comments skipped)", len(directives))
	}
}

func TestConvertWordPress(t *testing.T) {
	htaccess := `<IfModule mod_rewrite.c>
RewriteEngine On
RewriteBase /
RewriteRule ^index\.php$ - [L]
RewriteCond %{REQUEST_FILENAME} !-f
RewriteCond %{REQUEST_FILENAME} !-d
RewriteRule . /index.php [L]
</IfModule>`

	directives, _ := Parse(strings.NewReader(htaccess))
	rules := Convert(directives)

	if !rules.RewriteEnabled {
		t.Error("RewriteEngine should be on")
	}
	if len(rules.Rewrites) != 2 {
		t.Fatalf("rewrites count = %d, want 2", len(rules.Rewrites))
	}

	// Second rule should have 2 conditions
	if len(rules.Rewrites[1].Conditions) != 2 {
		t.Errorf("conditions = %d, want 2", len(rules.Rewrites[1].Conditions))
	}
}

func TestConvertErrorDocuments(t *testing.T) {
	directives, _ := Parse(strings.NewReader(`ErrorDocument 404 /errors/404.html
ErrorDocument 500 /errors/500.html`))
	rules := Convert(directives)

	if rules.ErrorDocuments[404] != "/errors/404.html" {
		t.Errorf("404 = %q", rules.ErrorDocuments[404])
	}
	if rules.ErrorDocuments[500] != "/errors/500.html" {
		t.Errorf("500 = %q", rules.ErrorDocuments[500])
	}
}

func TestConvertRedirect(t *testing.T) {
	directives, _ := Parse(strings.NewReader(`Redirect 301 /old /new
Redirect permanent /legacy /modern`))
	rules := Convert(directives)

	if len(rules.Redirects) != 2 {
		t.Fatalf("redirects = %d, want 2", len(rules.Redirects))
	}
	if rules.Redirects[0].Status != 301 {
		t.Errorf("first redirect status = %d, want 301", rules.Redirects[0].Status)
	}
	if rules.Redirects[1].Status != 301 {
		t.Errorf("second redirect status = %d, want 301 (permanent)", rules.Redirects[1].Status)
	}
}

func TestConvertOptions(t *testing.T) {
	directives, _ := Parse(strings.NewReader(`Options -Indexes -FollowSymLinks`))
	rules := Convert(directives)

	if rules.DirectoryListing == nil || *rules.DirectoryListing != false {
		t.Error("DirectoryListing should be false")
	}
	if rules.FollowSymlinks == nil || *rules.FollowSymlinks != false {
		t.Error("FollowSymlinks should be false")
	}
}

func TestConvertHeaders(t *testing.T) {
	directives, _ := Parse(strings.NewReader(`Header set X-Frame-Options SAMEORIGIN
Header unset X-Powered-By`))
	rules := Convert(directives)

	if len(rules.Headers) != 2 {
		t.Fatalf("headers = %d, want 2", len(rules.Headers))
	}
	if rules.Headers[0].Action != "set" || rules.Headers[0].Name != "X-Frame-Options" {
		t.Errorf("first header = %+v", rules.Headers[0])
	}
	if rules.Headers[1].Action != "unset" || rules.Headers[1].Name != "X-Powered-By" {
		t.Errorf("second header = %+v", rules.Headers[1])
	}
}

func TestConvertAuthDirectives(t *testing.T) {
	input := `AuthType Basic
AuthName "Restricted Area"
AuthUserFile /etc/apache2/.htpasswd
Require valid-user`

	directives, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	rules := Convert(directives)

	if rules.AuthType != "Basic" {
		t.Errorf("AuthType = %q, want Basic", rules.AuthType)
	}
	if rules.AuthName != "Restricted Area" {
		t.Errorf("AuthName = %q, want 'Restricted Area'", rules.AuthName)
	}
	if rules.AuthUserFile != "/etc/apache2/.htpasswd" {
		t.Errorf("AuthUserFile = %q, want /etc/apache2/.htpasswd", rules.AuthUserFile)
	}
	if rules.Require != "valid-user" {
		t.Errorf("Require = %q, want valid-user", rules.Require)
	}
}

func TestConvertExpiresDirectives(t *testing.T) {
	input := `ExpiresActive On
ExpiresByType text/html "access plus 1 month"
ExpiresByType image/jpeg "access plus 1 year"`

	directives, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	rules := Convert(directives)

	if !rules.ExpiresActive {
		t.Error("ExpiresActive should be true")
	}
	if len(rules.ExpiresByType) != 2 {
		t.Fatalf("ExpiresByType count = %d, want 2", len(rules.ExpiresByType))
	}
	if rules.ExpiresByType["text/html"] != "access plus 1 month" {
		t.Errorf("text/html expires = %q", rules.ExpiresByType["text/html"])
	}
	if rules.ExpiresByType["image/jpeg"] != "access plus 1 year" {
		t.Errorf("image/jpeg expires = %q", rules.ExpiresByType["image/jpeg"])
	}
}

func TestConvertFilesMatchBlock(t *testing.T) {
	input := `<FilesMatch "\.(jpg|png|gif)$">
Header set Cache-Control "max-age=31536000"
</FilesMatch>`

	directives, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	rules := Convert(directives)

	if len(rules.FilesMatch) != 1 {
		t.Fatalf("FilesMatch count = %d, want 1", len(rules.FilesMatch))
	}
	fm := rules.FilesMatch[0]
	if fm.Pattern != `\.(jpg|png|gif)$` {
		t.Errorf("FilesMatch pattern = %q", fm.Pattern)
	}
	if len(fm.Directives) != 1 {
		t.Fatalf("FilesMatch directives = %d, want 1", len(fm.Directives))
	}
	if fm.Directives[0].Name != "Header" {
		t.Errorf("inner directive name = %q, want Header", fm.Directives[0].Name)
	}
}

func TestParseEmptyInput(t *testing.T) {
	directives, err := Parse(strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(directives) != 0 {
		t.Errorf("directives = %d, want 0 for empty input", len(directives))
	}
}

func TestParseNestedIfModuleBlocks(t *testing.T) {
	input := `<IfModule mod_rewrite.c>
    RewriteEngine On
    <IfModule mod_negotiation.c>
        Options -MultiViews
    </IfModule>
    RewriteRule ^(.*)$ /index.php [L]
</IfModule>`

	directives, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}

	if len(directives) != 1 {
		t.Fatalf("top-level directives = %d, want 1", len(directives))
	}

	outer := directives[0]
	if outer.Name != "IfModule" {
		t.Errorf("outer block name = %q, want IfModule", outer.Name)
	}
	if len(outer.Args) == 0 || outer.Args[0] != "mod_rewrite.c" {
		t.Errorf("outer block args = %v, want [mod_rewrite.c]", outer.Args)
	}

	// Outer should have: RewriteEngine, nested IfModule, RewriteRule = 3 items
	if len(outer.Block) != 3 {
		t.Fatalf("outer inner count = %d, want 3", len(outer.Block))
	}

	// Check the nested IfModule
	nested := outer.Block[1]
	if nested.Name != "IfModule" {
		t.Errorf("nested block name = %q, want IfModule", nested.Name)
	}
	if len(nested.Args) == 0 || nested.Args[0] != "mod_negotiation.c" {
		t.Errorf("nested block args = %v, want [mod_negotiation.c]", nested.Args)
	}
	if len(nested.Block) != 1 {
		t.Fatalf("nested inner count = %d, want 1", len(nested.Block))
	}
	if nested.Block[0].Name != "Options" {
		t.Errorf("nested inner directive = %q, want Options", nested.Block[0].Name)
	}

	// Now test Convert with nested IfModule: both should be "flattened"
	rules := Convert(directives)
	if !rules.RewriteEnabled {
		t.Error("RewriteEngine should be on after converting nested IfModule")
	}
	if rules.DirectoryListing != nil {
		// Options -MultiViews doesn't affect DirectoryListing
	}
	if len(rules.Rewrites) != 1 {
		t.Errorf("rewrites = %d, want 1", len(rules.Rewrites))
	}
}
