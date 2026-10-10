package admin

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// composeEnvSeen returns the environment entries compose would hand the
// container: YAML decode, then compose interpolation ("$$" -> "$"; a lone
// "$NAME" would be substituted away).
func composeEnvSeen(t *testing.T, doc string) map[string]string {
	t.Helper()
	var f struct {
		Services map[string]struct {
			Environment []string `yaml:"environment"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal([]byte(doc), &f); err != nil {
		t.Fatalf("compose does not parse: %v\n%s", err, doc)
	}
	out := map[string]string{}
	for _, svc := range f.Services {
		for _, e := range svc.Environment {
			k, v, _ := strings.Cut(e, "=")
			var b strings.Builder
			for i := 0; i < len(v); i++ {
				if v[i] != '$' {
					b.WriteByte(v[i])
				} else if i+1 < len(v) && v[i+1] == '$' {
					b.WriteByte('$')
					i++
				}
			}
			out[k] = b.String()
		}
	}
	return out
}

// F1090: user-supplied env values ("a: b", " #", "$", newlines) must reach the
// container unchanged and must not alter the compose file's structure.
func TestSoftwareComposeEnvValuesRoundTrip(t *testing.T) {
	keys := map[string][]string{
		"postgres":         {"POSTGRES_DB", "POSTGRES_USER", "POSTGRES_PASSWORD"},
		"adminer-postgres": {"POSTGRES_DB", "POSTGRES_USER", "POSTGRES_PASSWORD"},
		"mysql":            {"MYSQL_DATABASE", "MYSQL_USER", "MYSQL_PASSWORD", "MYSQL_ROOT_PASSWORD"},
		"mariadb":          {"MARIADB_DATABASE", "MARIADB_USER", "MARIADB_PASSWORD", "MARIADB_ROOT_PASSWORD"},
		"minio":            {"MINIO_ROOT_USER", "MINIO_ROOT_PASSWORD"},
		"n8n":              {"N8N_BASIC_AUTH_USER", "N8N_BASIC_AUTH_PASSWORD"},
	}
	values := []string{"plain123", "pa: ss", "pa #ss", "pa$word", "a\nb: c", `q"uote\back`, "ünï", "$$x"}
	for id, ks := range keys {
		tpl := findSoftwareTemplate(id)
		if tpl == nil {
			t.Fatalf("template %s missing", id)
		}
		for _, v := range values {
			env := map[string]string{}
			for _, k := range ks {
				env[k] = v
			}
			got := composeEnvSeen(t, tpl.compose(softwareInstallRequest{Name: "x", HostPort: 3001, Env: env}, *tpl))
			for _, k := range ks {
				if got[k] != v {
					t.Errorf("%s %s: container sees %q, want %q", id, k, got[k], v)
				}
			}
		}
	}
}

// Defaults still apply and the n8n domain rewrite keeps working on the
// quoted output.
func TestSoftwareComposeEnvDefaultsAndDomainRewrite(t *testing.T) {
	tpl := findSoftwareTemplate("n8n")
	doc := tpl.compose(softwareInstallRequest{Name: "n", HostPort: 5678, Domain: "a.example.com", Env: map[string]string{"N8N_BASIC_AUTH_PASSWORD": "p$w"}}, *tpl)
	got := composeEnvSeen(t, doc)
	if got["N8N_BASIC_AUTH_USER"] != "admin" || got["N8N_BASIC_AUTH_PASSWORD"] != "p$w" || got["N8N_HOST"] != "a.example.com" {
		t.Fatalf("unexpected env: %v", got)
	}
	updated, changed := replaceComposeEnvironmentLine(doc, "N8N_HOST", "b.example.com")
	if !changed {
		t.Fatal("N8N_HOST line not rewritten")
	}
	if got := composeEnvSeen(t, updated); got["N8N_HOST"] != "b.example.com" || got["N8N_BASIC_AUTH_PASSWORD"] != "p$w" {
		t.Fatalf("after rewrite: %v", got)
	}
}
