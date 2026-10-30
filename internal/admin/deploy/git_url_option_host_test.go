package deploy

// F48302: validateGitURL kept out `ext::`, `file://` and `--upload-pack`, but
// accepted an ssh:// or scp-style URL whose host (or user) starts with `-`:
// `ssh://-oProxyCommand=touch${IFS}/tmp/x/repo`. git passes that word to ssh as
// an option, so on a git older than 2.13 (CVE-2017-1000117; CentOS 7 still
// ships 1.8.3) the clone runs the command, as root under a root UWAS. Current
// git refuses such hosts itself; the URL check now does too, so the answer does
// not depend on the git a host happens to have.

import "testing"

func TestValidateGitURLRejectsOptionLikeHost(t *testing.T) {
	bad := []string{
		"ssh://-oProxyCommand=touch${IFS}/tmp/x/repo",
		"ssh://git@-oProxyCommand=touch${IFS}/tmp/x/repo",
		"ssh://-oProxyCommand=x@host.example/repo",
		"ssh://git@-host.example:2222/repo",
		"git@-oProxyCommand=touch${IFS}/tmp/x:repo",
		"git@-host.example:team/repo.git",
		"https://-oProxyCommand=x/repo.git",
	}
	for _, u := range bad {
		err := validateGitURL(u)
		t.Logf("EXPECTED: error\nACTUAL: %v  (%s)", err, u)
		if err == nil {
			t.Errorf("PROBLEM CONFIRMED: %q accepted although its host looks like an ssh option", u)
		}
	}
}

// Control: ordinary hosts, ports, users and dashes inside names still pass.
func TestValidateGitURLAcceptsOrdinaryHosts(t *testing.T) {
	good := []string{
		"https://github.com/uwaserver/uwas.git",
		"https://user@github.com/u/r.git",
		"https://my-git.example.com:8443/team/-weird-repo.git",
		"ssh://git@github.com/u/r.git",
		"ssh://git@git-host.example.com:2222/u/r.git",
		"git@github.com:u/r.git",
		"git@gitlab-self.example.com:team/sub-group/r.git",
		"git@host.example:-leading-dash-path.git",
	}
	for _, u := range good {
		if err := validateGitURL(u); err != nil {
			t.Errorf("control: %q rejected: %v", u, err)
		}
	}
}
