package admin

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// copySampleConfig copies a sample configuration (and the domains.d/ next to
// it, if any) into a fresh temp dir so persistConfig can rewrite it.
func copySampleConfig(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "uwas.yaml")
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if ents, err := os.ReadDir(filepath.Join(filepath.Dir(src), "domains.d")); err == nil {
		_ = os.MkdirAll(filepath.Join(dir, "domains.d"), 0o755)
		for _, e := range ents {
			if b, err := os.ReadFile(filepath.Join(filepath.Dir(src), "domains.d", e.Name())); err == nil {
				_ = os.WriteFile(filepath.Join(dir, "domains.d", e.Name()), b, 0o600)
			}
		}
	}
	return dst
}

// What the panel saves must load again, and as the same configuration: the
// next restart (or a systemd restart loop) is the only place a save that wrote
// something the loader refuses, or that quietly dropped a setting, shows up.
func TestPersistConfigRoundTripsSampleConfigs(t *testing.T) {
	samples := []string{
		"../../uwas.example.yaml",
		"../../docker/uwas.yaml",
		"../../test/docker/uwas-test.yaml",
	}
	more, _ := filepath.Glob("../../examples/*/*.yaml")
	samples = append(samples, more...)
	more, _ = filepath.Glob("../../examples/*.yaml")
	samples = append(samples, more...)
	t.Setenv("UWAS_ADMIN_KEY", "sample-admin-key-0123456789abcdef0123456789")
	for _, src := range samples {
		if _, err := os.Stat(src); err != nil {
			continue
		}
		t.Run(filepath.Base(filepath.Dir(src))+"/"+filepath.Base(src), func(t *testing.T) {
			path := copySampleConfig(t, src)
			before, err := config.Load(path)
			if err != nil {
				t.Skipf("sample does not load on its own: %v", err)
			}
			s := &Server{config: before, configPath: path, logger: logger.New("error", "text")}
			if err := s.persistConfig(); err != nil {
				t.Fatalf("persistConfig: %v", err)
			}
			after, err := config.Load(path)
			t.Logf("EXPECTED: loads again, same configuration\nACTUAL: err=%v", err)
			if err != nil {
				t.Fatalf("PROBLEM CONFIRMED: configuration saved by the panel does not load: %v", err)
			}
			for _, diff := range structDiff("global", reflect.ValueOf(before.Global), reflect.ValueOf(after.Global)) {
				t.Errorf("PROBLEM CONFIRMED: a save changed %s", diff)
			}
			if len(before.Domains) != len(after.Domains) {
				t.Fatalf("PROBLEM CONFIRMED: %d domains before a save, %d after", len(before.Domains), len(after.Domains))
			}
			byHost := map[string]config.Domain{}
			for _, d := range after.Domains {
				byHost[d.Host] = d
			}
			for _, d := range before.Domains {
				a, ok := byHost[d.Host]
				if !ok {
					t.Errorf("PROBLEM CONFIRMED: domain %s missing after a save", d.Host)
					continue
				}
				for _, diff := range structDiff("domain "+d.Host, reflect.ValueOf(d), reflect.ValueOf(a)) {
					t.Errorf("PROBLEM CONFIRMED: a save changed %s", diff)
				}
			}
		})
	}
}

// structDiff names the fields (recursively) in which two values of the same
// struct type differ, with both values, so a round-trip failure points at the
// setting instead of dumping two whole configurations.
func structDiff(path string, a, b reflect.Value) []string {
	if reflect.DeepEqual(a.Interface(), b.Interface()) {
		return nil
	}
	// A nil slice or map and an empty one are the same setting.
	if (a.Kind() == reflect.Slice || a.Kind() == reflect.Map) && a.Len() == 0 && b.Len() == 0 {
		return nil
	}
	switch a.Kind() {
	case reflect.Struct:
		var out []string
		for i := 0; i < a.NumField(); i++ {
			if !a.Type().Field(i).IsExported() {
				continue
			}
			out = append(out, structDiff(path+"."+a.Type().Field(i).Name, a.Field(i), b.Field(i))...)
		}
		return out
	case reflect.Ptr:
		if !a.IsNil() && !b.IsNil() {
			return structDiff(path, a.Elem(), b.Elem())
		}
	}
	return []string{fmt.Sprintf("%s: before=%v after=%v", path, a.Interface(), b.Interface())}
}
