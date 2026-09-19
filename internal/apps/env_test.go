package apps

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestEnvMapPreservesJSONOrder(t *testing.T) {
	raw := []byte(`{"ZEBRA":"1","ALPHA":"2","MIDDLE":"3"}`)
	var e EnvMap
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatal(err)
	}
	var keys []string
	e.Range(func(k, _ string) bool {
		keys = append(keys, k)
		return true
	})
	want := "ZEBRA,ALPHA,MIDDLE"
	if got := strings.Join(keys, ","); got != want {
		t.Fatalf("order = %q, want %q", got, want)
	}
	out, err := json.Marshal(&e)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"ZEBRA":"1","ALPHA":"2","MIDDLE":"3"}` {
		t.Fatalf("marshal = %s", out)
	}
}

func TestEnvMapPreservesYAMLOrder(t *testing.T) {
	raw := []byte("ZEBRA: \"1\"\nALPHA: \"2\"\nMIDDLE: \"3\"\n")
	var e EnvMap
	if err := yaml.Unmarshal(raw, &e); err != nil {
		t.Fatal(err)
	}
	var keys []string
	e.Range(func(k, _ string) bool {
		keys = append(keys, k)
		return true
	})
	if got := strings.Join(keys, ","); got != "ZEBRA,ALPHA,MIDDLE" {
		t.Fatalf("order = %q", got)
	}
	out, err := yaml.Marshal(&e)
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	zi := strings.Index(got, "ZEBRA")
	ai := strings.Index(got, "ALPHA")
	mi := strings.Index(got, "MIDDLE")
	if zi < 0 || ai < 0 || mi < 0 || !(zi < ai && ai < mi) {
		t.Fatalf("yaml order lost:\n%s", got)
	}
}

func TestEnvMapAppRoundTripOrder(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	app := &App{
		Name:    "ordenv",
		Runtime: RuntimeNode,
		Command: "node index.js",
		Port:    3000,
		Env:     EnvFromPairs("ZEBRA", "1", "ALPHA", "2", "MIDDLE", "3"),
	}
	if err := s.Save(app); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("ordenv")
	if err != nil || got == nil {
		t.Fatalf("get: %v %#v", err, got)
	}
	var keys []string
	got.Env.Range(func(k, _ string) bool {
		keys = append(keys, k)
		return true
	})
	if join := strings.Join(keys, ","); join != "ZEBRA,ALPHA,MIDDLE" {
		t.Fatalf("saved order lost: %q (file may have sorted keys)", join)
	}
}
