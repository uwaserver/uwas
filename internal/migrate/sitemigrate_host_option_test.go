package migrate

// F51301: validateSSHInput exists to stop a request value from reaching ssh or
// rsync as an option, but migrateHostRe let the user part of "user@host" start
// with "-": "-Jattacker@h.test" passed validation and became the first
// positional ssh argument and the start of rsync's source argument, where both
// parse it as a flag (-J ProxyJump sends the connection through a host the
// requester chose, -F reads a config file, ...). A user name never starts with
// "-" in a migration source, so refuse it.

import (
	"strings"
	"testing"
)

func TestValidateSSHInputRefusesOptionLookingUser(t *testing.T) {
	base := MigrateRequest{SourcePort: "22", SourcePath: "/var/www/site", SourceHost: ""}
	for _, host := range []string{"-Jattacker@h.test", "-F@h.test", "-oFoo@h.test", "--@h.test", "-x@10.0.0.5"} {
		req := base
		req.SourceHost = host
		err := validateSSHInput(req)
		args := buildRsyncArgs(buildSSHOpts("22", ""), host+":/var/www/site/", "/dst/")
		var leading string
		for _, a := range args {
			if strings.HasPrefix(a, "-") && strings.Contains(a, "@") {
				leading = a
			}
		}
		t.Logf("EXPECTED: %q refused\nACTUAL: validateSSHInput err=%v, rsync argv element=%q", host, err, leading)
		if err == nil {
			t.Errorf("PROBLEM CONFIRMED: source_host %q passed validation and reaches rsync as %q", host, leading)
		}
	}
}

func TestValidateSSHInputStillAcceptsOrdinaryHosts(t *testing.T) {
	for _, host := range []string{"example.com", "10.0.0.5", "deploy@example.com", "root@srv-1.example.com", "a.b_c-d@host-1.test", "_svc@h.test", "9user@h.test"} {
		req := MigrateRequest{SourcePort: "22", SourcePath: "/var/www/site", SourceHost: host}
		if err := validateSSHInput(req); err != nil {
			t.Errorf("source_host %q rejected: %v", host, err)
		}
	}
}
