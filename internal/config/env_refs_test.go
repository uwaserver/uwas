package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestPreserveEnvReferences(t *testing.T) {
	t.Setenv("PER_KEY", "secret-value-1")
	t.Setenv("PER_PORT", "8080")
	t.Setenv("PER_EMPTY", "")
	os.Unsetenv("PER_UNSET")

	const prev = `global:
  admin:
    api_key: "${PER_KEY}"
    listen: 127.0.0.1:${PER_PORT}
  http_listen: ":${PER_HTTP:-80}"
  backup:
    s3:
      secret_key: ${PER_KEY}
      region: "${PER_UNSET}"
    keep: ${PER_PORT}
list:
  - "${PER_KEY}"
  - plain
`
	cases := []struct {
		name, written string
		want, absent  []string
	}{
		{"unchanged values get their references back",
			"global:\n    admin:\n        api_key: secret-value-1\n        listen: 127.0.0.1:8080\n    http_listen: :80\n    backup:\n        s3:\n            secret_key: secret-value-1\n            region: ${PER_UNSET}\n        keep: 8080\nlist:\n    - secret-value-1\n    - plain\n",
			[]string{`"${PER_KEY}"`, "127.0.0.1:${PER_PORT}", `":${PER_HTTP:-80}"`, "secret_key: ${PER_KEY}", "keep: ${PER_PORT}"},
			[]string{"secret-value-1", "8080"}},
		{"a value the operator changed keeps its new value",
			"global:\n    admin:\n        api_key: brand-new-key\n        listen: 127.0.0.1:8080\n",
			[]string{"api_key: brand-new-key", "127.0.0.1:${PER_PORT}"},
			[]string{"${PER_KEY}"}},
		{"a key that moved is not matched by value alone",
			"other:\n    api_key: secret-value-1\n",
			[]string{"api_key: secret-value-1"},
			[]string{"${PER_KEY}"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := string(PreserveEnvReferences([]byte(prev), []byte(c.written)))
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in:\n%s", w, got)
				}
			}
			for _, a := range c.absent {
				if strings.Contains(got, a) {
					t.Errorf("unexpected %q in:\n%s", a, got)
				}
			}
			var probe map[string]any
			if err := yaml.Unmarshal([]byte(expandEnvVars(got)), &probe); err != nil {
				t.Errorf("result does not load after expansion: %v\n%s", err, got)
			}
		})
	}

	t.Run("no reference or unparsable input is returned unchanged", func(t *testing.T) {
		w := []byte("a: 1\n")
		if got := PreserveEnvReferences([]byte("a: 2\n"), w); string(got) != string(w) {
			t.Errorf("got %q", got)
		}
		if got := PreserveEnvReferences([]byte("a: ${PER_KEY}\n"), []byte("{[bad")); string(got) != "{[bad" {
			t.Errorf("unparsable written changed: %q", got)
		}
		if got := PreserveEnvReferences([]byte("a: ${PER_KEY}\n  b: [\n"), w); string(got) != string(w) {
			t.Errorf("unparsable previous changed the output: %q", got)
		}
	})
}

// The restored file must load to the same configuration as the live one.
func TestPreserveEnvReferencesRoundTripLoads(t *testing.T) {
	t.Setenv("RT_KEY", "0123456789abcdef0123456789abcdef0123")
	dir := t.TempDir()
	path := filepath.Join(dir, "uwas.yaml")
	src := "global:\n  admin:\n    enabled: true\n    listen: \"127.0.0.1:9443\"\n    api_key: \"${RT_KEY}\"\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	marshaled, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	out := PreserveEnvReferences([]byte(src), marshaled)
	if strings.Contains(string(out), "0123456789abcdef") {
		t.Fatalf("secret written out:\n%s", out)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	again, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if again.Global.Admin.APIKey != cfg.Global.Admin.APIKey {
		t.Fatalf("api key after reload = %q, want %q", again.Global.Admin.APIKey, cfg.Global.Admin.APIKey)
	}
}
