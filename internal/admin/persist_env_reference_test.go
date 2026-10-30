package admin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

// The loader expands ${VAR} in the YAML text before parsing, so the live
// configuration holds the secret's value. persistConfig marshals that live
// configuration, which replaced every ${VAR} reference with the secret
// itself the first time anything (a domain edit, a setting, a PHP assignment)
// was saved: the key was frozen into uwas.yaml, rotating the environment
// variable stopped working, and a secret that was only ever meant to live in
// the environment (docker/uwas.yaml reads UWAS_ADMIN_KEY this way) was written
// to disk.
func TestPersistConfigKeepsEnvReferences(t *testing.T) {
	const adminKey = "r186-admin-key-0123456789abcdef0123456789abcdef"
	const s3Secret = "r186-s3-secret-value-0123456789"
	t.Setenv("R186_ADMIN_KEY", adminKey)
	t.Setenv("R186_S3_SECRET", s3Secret)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	yamlText := "global:\n  admin:\n    enabled: true\n    listen: \"127.0.0.1:9443\"\n    api_key: \"${R186_ADMIN_KEY}\"\n  backup:\n    s3:\n      secret_key: \"${R186_S3_SECRET}\"\n"
	if err := os.WriteFile(cfgPath, []byte(yamlText), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Global.Admin.APIKey != adminKey {
		t.Fatalf("control: loader did not expand the reference: %q", cfg.Global.Admin.APIKey)
	}
	s := &Server{config: cfg, configPath: cfgPath, logger: logger.New("error", "text")}
	if err := s.persistConfig(); err != nil {
		t.Fatalf("persist: %v", err)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	t.Logf("EXPECTED: references kept, no secret on disk\nACTUAL file:\n%s", got)
	if strings.Contains(got, adminKey) || strings.Contains(got, s3Secret) {
		t.Fatal("PROBLEM CONFIRMED: persistConfig wrote the expanded environment secrets to uwas.yaml")
	}
	if !strings.Contains(got, "${R186_ADMIN_KEY}") || !strings.Contains(got, "${R186_S3_SECRET}") {
		t.Fatal("PROBLEM CONFIRMED: the ${VAR} references were dropped from uwas.yaml")
	}
}

// A value the operator changes through the panel is written as typed, not
// swapped back for the old reference.
func TestPersistConfigWritesChangedSecretLiterally(t *testing.T) {
	t.Setenv("R186_ADMIN_KEY", "r186-admin-key-0123456789abcdef0123456789abcdef")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "uwas.yaml")
	yamlText := "global:\n  admin:\n    enabled: true\n    listen: \"127.0.0.1:9443\"\n    api_key: \"${R186_ADMIN_KEY}\"\n"
	if err := os.WriteFile(cfgPath, []byte(yamlText), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Global.Admin.APIKey = "r186-rotated-key-fedcba9876543210fedcba9876543210"
	s := &Server{config: cfg, configPath: cfgPath, logger: logger.New("error", "text")}
	if err := s.persistConfig(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(data), "r186-rotated-key-fedcba9876543210fedcba9876543210") || strings.Contains(string(data), "${R186_ADMIN_KEY}") {
		t.Fatalf("changed key not written literally:\n%s", data)
	}
	if fi, _ := os.Stat(cfgPath); fi.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %v, want 0600", fi.Mode().Perm())
	}
}
