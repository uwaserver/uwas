// Package childenv builds the environment for child processes that run
// tenant-controlled code (build scripts, deploy hooks, wp-cli loading a site's
// plugins). Inheriting os.Environ() there hands the site UWAS's own secrets: the
// admin API key, DNS/Cloudflare tokens, DB passwords supplied through ${VAR} in
// the config. The PHP workers already use an allowlist (phpmanager); build
// tooling needs far more of the ambient environment (proxy, node, toolchain
// variables), so this drops what looks like a secret instead.
package childenv

import (
	"os"
	"strings"
)

// secretWords are the name parts (split on '_') that mark a variable as secret.
var secretWords = map[string]bool{
	"KEY": true, "TOKEN": true, "SECRET": true, "PASSWORD": true, "PASSWD": true,
	"PASS": true, "CREDENTIAL": true, "CREDENTIALS": true, "APIKEY": true,
}

// secretSuffixes catch names that run the word into a prefix without a
// separator (PGPASSWORD, MYSQLPASSWORD), which the '_' split above cannot see.
// "_PWD" is the mysql client's password variable; a bare PWD (the working
// directory) and MYSQL_PWD_FILE (a path) are not secrets.
var secretSuffixes = []string{"PASSWORD", "PASSWD", "SECRET", "TOKEN", "APIKEY", "_PWD"}

// IsSecretName reports whether an environment variable name belongs to UWAS
// itself (UWAS_ prefix) or looks like it carries a secret.
func IsSecretName(name string) bool {
	up := strings.ToUpper(name)
	if strings.HasPrefix(up, "UWAS_") {
		return true
	}
	for _, suf := range secretSuffixes {
		if strings.HasSuffix(up, suf) {
			return true
		}
	}
	for _, part := range strings.Split(up, "_") {
		if secretWords[part] {
			return true
		}
	}
	return false
}

// Filtered returns the current environment without UWAS's own secrets, followed
// by extra (variables the operator or site configured explicitly, which are
// kept as given).
func Filtered(extra ...string) []string {
	env := make([]string, 0, len(os.Environ())+len(extra))
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if IsSecretName(name) {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}
