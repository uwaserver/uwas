package childenv

import (
	"strings"
	"testing"
)

func TestIsSecretName(t *testing.T) {
	for name, want := range map[string]bool{
		"UWAS_ADMIN_KEY": true, "uwas_purge_key": true, "UWAS_DB_HOST": true,
		"CLOUDFLARE_API_TOKEN": true, "DB_PASSWORD": true, "MYSQL_PWD_FILE": false,
		"AWS_SECRET_ACCESS_KEY": true, "APIKEY": true, "NPM_TOKEN": true,
		"PATH": false, "HOME": false, "HTTP_PROXY": false, "NODE_OPTIONS": false,
		"KEYBOARD_LAYOUT": false, "MONKEY": false, "LC_ALL": false,
		// F24200: separator-less and _PWD forms of the client password variables.
		"MYSQL_PWD": true, "PGPASSWORD": true, "mysqlpassword": true, "GITHUBTOKEN": true,
		"PWD": false, "OLDPWD": false, "PGPASSFILE": false,
	} {
		if got := IsSecretName(name); got != want {
			t.Errorf("IsSecretName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestFilteredDropsSecretsKeepsRestAndExtra(t *testing.T) {
	t.Setenv("UWAS_ADMIN_KEY", "s1")
	t.Setenv("SOME_API_TOKEN", "s2")
	t.Setenv("HTTP_PROXY", "http://proxy:3128")
	env := Filtered("NPM_TOKEN=explicit")
	joined := "\n" + strings.Join(env, "\n") + "\n"
	for _, bad := range []string{"\nUWAS_ADMIN_KEY=", "\nSOME_API_TOKEN="} {
		if strings.Contains(joined, bad) {
			t.Errorf("secret variable inherited: %q", bad)
		}
	}
	for _, good := range []string{"\nPATH=", "\nHTTP_PROXY=http://proxy:3128\n", "\nNPM_TOKEN=explicit\n"} {
		if !strings.Contains(joined, good) {
			t.Errorf("expected %q in the child environment", good)
		}
	}
}
