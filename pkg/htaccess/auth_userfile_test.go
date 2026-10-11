package htaccess

import (
	"strings"
	"testing"
)

// TestAuthUserFileRequiresAuth pins the fail-closed contract for an .htaccess
// AuthUserFile block.
//
// Before this helper existed, AuthUserFile was parsed at converter.go:232 —
// behind a traversal guard whose comment says "to prevent reading arbitrary
// files" — and merged at :307, but had zero consumers. A directory an operator
// protected with "AuthUserFile .htpasswd" + "Require valid-user" was served to
// anyone with the URL, silently discarding the protection.
//
// UWAS does not verify htpasswd credentials, so the only safe answer is to deny
// (403 at the dispatch gate). This test pins the decision helper that drives it.
func TestAuthUserFileRequiresAuth(t *testing.T) {
	cases := []struct {
		name  string
		rules *RuleSet
		want  bool
	}{
		{"nil ruleset", nil, false},
		{"empty ruleset", NewRuleSet(), false},
		{
			name:  "authuserfile set means auth is required",
			rules: &RuleSet{AuthUserFile: ".htpasswd", Require: "valid-user"},
			want:  true,
		},
		{
			name:  "authuserfile alone is enough to require auth",
			rules: &RuleSet{AuthUserFile: "users.htpasswd"},
			want:  true,
		},
		{
			name:  "authtype alone requires no auth",
			rules: &RuleSet{AuthType: "Basic", AuthName: "Realm"},
			want:  false,
		},
		{
			name:  "require alone requires no auth",
			rules: &RuleSet{Require: "valid-user"},
			want:  false,
		},
		{
			name:  "unrelated directives require no auth",
			rules: &RuleSet{RewriteEnabled: true, DirectoryListing: boolPtr(false)},
			want:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AuthUserFileRequiresAuth(tc.rules); got != tc.want {
				t.Errorf("AuthUserFileRequiresAuth() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestAuthUserFileConvertedFromDirectives pins that the "authuserfile" directive
// reaches the RuleSet and is therefore caught by the fail-closed gate, and that
// the traversal guard still keeps hostile paths out of it.
func TestAuthUserFileConvertedFromDirectives(t *testing.T) {
	cases := []struct {
		name       string
		directives []Directive
		wantAuth   bool
		wantFile   string
	}{
		{
			name: "ordinary relative htpasswd path is recorded",
			directives: []Directive{
				{Name: "AuthType", Args: []string{"Basic"}},
				{Name: "AuthName", Args: []string{"Private"}},
				{Name: "AuthUserFile", Args: []string{".htpasswd"}},
				{Name: "Require", Args: []string{"valid-user"}},
			},
			wantAuth: true,
			wantFile: ".htpasswd",
		},
		{
			// The path is never kept (so it can never be read), but the
			// directory is still password-protected: gate it (F2800).
			name: "traversal path is dropped by the converter guard but still gates",
			directives: []Directive{
				{Name: "AuthUserFile", Args: []string{"../../../etc/passwd"}},
				{Name: "Require", Args: []string{"valid-user"}},
			},
			wantAuth: true,
			wantFile: "",
		},
		{
			name: "absolute path is dropped by the converter guard but still gates",
			directives: []Directive{
				{Name: "AuthUserFile", Args: []string{"/etc/shadow"}},
			},
			wantAuth: true,
			wantFile: "",
		},
		{
			name:       "no authuserfile directive means no auth",
			directives: []Directive{{Name: "DirectoryIndex", Args: []string{"index.html"}}},
			wantAuth:   false,
			wantFile:   "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rules := Convert(tc.directives)
			if got := rules.AuthUserFile; got != tc.wantFile {
				t.Errorf("AuthUserFile = %q, want %q", got, tc.wantFile)
			}
			if got := AuthUserFileRequiresAuth(rules); got != tc.wantAuth {
				t.Errorf("AuthUserFileRequiresAuth() = %v, want %v (AuthUserFile=%q)",
					got, tc.wantAuth, rules.AuthUserFile)
			}
		})
	}
}

// TestAuthUserFileSurvivesMerge guards the other entry point into a RuleSet: a
// block wrapped in <IfModule> produces a sub-RuleSet that is merged into the
// parent. If Merge dropped AuthUserFile, an auth-protected block would be
// silently unprotected again.
func TestAuthUserFileSurvivesMerge(t *testing.T) {
	parent := NewRuleSet()
	child := Convert([]Directive{
		{Name: "AuthType", Args: []string{"Basic"}},
		{Name: "AuthUserFile", Args: []string{".htpasswd"}},
	})

	parent.Merge(child)

	if got := parent.AuthUserFile; got != ".htpasswd" {
		t.Errorf("merged AuthUserFile = %q, want %q", got, ".htpasswd")
	}
	if !AuthUserFileRequiresAuth(parent) {
		t.Error("merged RuleSet with AuthUserFile must require auth")
	}
}

func boolPtr(b bool) *bool { return &b }

// F2800: an AuthUserFile whose path the converter refuses to keep (absolute or
// traversal) is still an authentication requirement and must still gate.
func TestAuthUserFileRejectedPathStillRequiresAuth(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      bool
	}{
		{"relative", "AuthUserFile .htpasswd", true},
		{"absolute", "AuthUserFile /home/u/.htpasswd", true},
		{"traversal", "AuthUserFile ../../x", true},
		{"none", "AuthType Basic", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds, err := Parse(strings.NewReader(tc.src + "\n"))
			if err != nil {
				t.Fatal(err)
			}
			rs := Convert(ds)
			if got := AuthUserFileRequiresAuth(rs); got != tc.want {
				t.Errorf("AuthUserFileRequiresAuth = %v, want %v", got, tc.want)
			}
			merged := NewRuleSet()
			merged.Merge(rs)
			if got := AuthUserFileRequiresAuth(merged); got != tc.want {
				t.Errorf("after Merge = %v, want %v", got, tc.want)
			}
		})
	}
}

// F2801: authentication-based Require lines that are the only access rule
// cannot be verified here and must deny instead of leaving the access unset.
func TestAuthOnlyRequireDeniesAccess(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      AccessDecision
	}{
		{"valid-user", "Require valid-user", AccessDenied},
		{"user", "Require user bob", AccessDenied},
		{"group", "Require group staff", AccessDenied},
		{"RequireAll", "<RequireAll>\nRequire valid-user\n</RequireAll>", AccessDenied},
		{"ip plus valid-user keeps the ip rule", "Require ip 10.0.0.0/8\nRequire valid-user", AccessDenied}, // client below matches no ip
		{"all granted", "Require all granted", AccessGranted},
		{"nothing", "Options -Indexes", AccessUnset},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds, err := Parse(strings.NewReader(tc.src + "\n"))
			if err != nil {
				t.Fatal(err)
			}
			if got := Convert(ds).AccessFor(nil); got != tc.want {
				t.Errorf("AccessFor = %v, want %v", got, tc.want)
			}
		})
	}
}
