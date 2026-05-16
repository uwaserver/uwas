package admin

import (
	"strings"
	"testing"
)

func TestValidateGitURL(t *testing.T) {
	cases := []struct {
		url string
		ok  bool
	}{
		{"https://github.com/user/repo.git", true},
		{"ssh://git@github.com/user/repo.git", true},
		{"git@github.com:user/repo.git", true},
		// Rejected schemes / injection
		{"", false},
		{"ext::sh -c whoami", false},
		{"file:///etc/passwd", false},
		{"http://github.com/user/repo.git", false}, // http (not https) rejected
		{"https://github.com/user/repo.git --upload-pack=whoami", false},
		{"https://example.com/repo.git\nmalicious", false},
		{"git://github.com/user/repo.git", false}, // git:// is plaintext, reject
	}
	for _, c := range cases {
		err := validateGitURL(c.url)
		if c.ok && err != nil {
			t.Errorf("validateGitURL(%q) unexpected err: %v", c.url, err)
		}
		if !c.ok && err == nil {
			t.Errorf("validateGitURL(%q) should have errored", c.url)
		}
	}
}

func TestValidGitRef(t *testing.T) {
	good := []string{"main", "v1.0.0", "feature/x", "release_2", "a-b-c", "1.2.3-rc1"}
	for _, s := range good {
		if !validGitRef(s) {
			t.Errorf("validGitRef(%q) = false, want true", s)
		}
	}
	bad := []string{
		"",
		"main; rm -rf /",
		"main && evil",
		"main`whoami`",
		"main$(whoami)",
		"main|cat /etc/passwd",
		"main\nnewline",
		strings.Repeat("a", 300),
	}
	for _, s := range bad {
		if validGitRef(s) {
			t.Errorf("validGitRef(%q) = true, want false", s)
		}
	}
}

func TestValidateBuildCommand(t *testing.T) {
	good := []string{
		"npm ci",
		"npm ci && npm run build",
		"pip install -r requirements.txt",
		"go build -o ./main",
		"make",
	}
	for _, s := range good {
		if err := validateBuildCommand(s); err != nil {
			t.Errorf("validateBuildCommand(%q) unexpected err: %v", s, err)
		}
	}
	bad := []string{
		"npm ci; rm -rf /",
		"echo `whoami`",
		"echo $(whoami)",
		"npm ci\nmalicious",
		"npm ci\x00",
	}
	for _, s := range bad {
		if err := validateBuildCommand(s); err == nil {
			t.Errorf("validateBuildCommand(%q) should have errored", s)
		}
	}
}
