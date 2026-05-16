package apps

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreSaveLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	s.DataRoot = filepath.Join(dir, "data")

	want := &App{
		Name:    "my-api",
		Runtime: RuntimeNode,
		Command: "node index.js",
		Port:    3001,
		Env:     map[string]string{"FOO": "bar"},
	}
	if err := s.Save(want); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Timestamps should have been stamped.
	if want.CreatedAt.IsZero() || want.UpdatedAt.IsZero() {
		t.Fatalf("save did not stamp timestamps: created=%v updated=%v", want.CreatedAt, want.UpdatedAt)
	}

	// WorkDir defaulted from DataRoot.
	wantWorkDir := filepath.Join(dir, "data", "my-api")
	if want.WorkDir != wantWorkDir {
		t.Fatalf("workdir not defaulted: got %q want %q", want.WorkDir, wantWorkDir)
	}

	// File on disk has expected name.
	if _, err := os.Stat(filepath.Join(dir, "my-api.yaml")); err != nil {
		t.Fatalf("file not written: %v", err)
	}

	got, err := s.Get("my-api")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("get returned nil")
	}
	if got.Name != "my-api" || got.Port != 3001 || got.Command != "node index.js" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got.Env["FOO"] != "bar" {
		t.Fatalf("env not preserved: %+v", got.Env)
	}
}

func TestStoreLoadAllSkipsInvalid(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	// One valid app.
	good := &App{Name: "good", Runtime: RuntimeNode, Command: "node x.js"}
	if err := s.Save(good); err != nil {
		t.Fatalf("save good: %v", err)
	}

	// One malformed YAML.
	if err := os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte("name: broken\nruntime: [not, valid"), 0600); err != nil {
		t.Fatal(err)
	}

	// One name-mismatch.
	if err := os.WriteFile(filepath.Join(dir, "mismatch.yaml"),
		[]byte("name: other\nruntime: node\ncommand: node x.js\n"), 0600); err != nil {
		t.Fatal(err)
	}

	apps, skipped, err := s.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(apps) != 1 || apps[0].Name != "good" {
		t.Fatalf("expected only 'good' loaded, got %d apps: %+v", len(apps), apps)
	}
	if len(skipped) != 2 {
		t.Fatalf("expected 2 skip errors, got %d: %v", len(skipped), skipped)
	}
}

func TestStoreDeleteIdempotent(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Delete("nope"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}

	a := &App{Name: "x", Runtime: RuntimeCustom, Command: "./run"}
	if err := s.Save(a); err != nil {
		t.Fatal(err)
	}
	if !s.Exists("x") {
		t.Fatal("Exists false after save")
	}
	if err := s.Delete("x"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if s.Exists("x") {
		t.Fatal("Exists true after delete")
	}
}

func TestStoreSavePreservesCreatedAt(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	a := &App{Name: "a", Runtime: RuntimeCustom, Command: "./x"}
	if err := s.Save(a); err != nil {
		t.Fatal(err)
	}
	created := a.CreatedAt
	if created.IsZero() {
		t.Fatal("first save did not stamp CreatedAt")
	}

	// Wait a hair so UpdatedAt is observably different, then clear
	// CreatedAt on the in-memory struct and re-save. Save should pick
	// up the original timestamp from disk rather than re-stamping.
	time.Sleep(2 * time.Millisecond)
	a.CreatedAt = time.Time{}
	a.Command = "./y"
	if err := s.Save(a); err != nil {
		t.Fatal(err)
	}
	if !a.CreatedAt.Equal(created) {
		t.Fatalf("CreatedAt not preserved on re-save: got %v want %v", a.CreatedAt, created)
	}
	if !a.UpdatedAt.After(created) {
		t.Fatalf("UpdatedAt should be after original CreatedAt: updated=%v created=%v", a.UpdatedAt, created)
	}
}

func TestValidateRejectsBadName(t *testing.T) {
	cases := []string{"", "has space", "has/slash", "has.dot", strings.Repeat("a", 65)}
	for _, c := range cases {
		a := &App{Name: c, Runtime: RuntimeNode, Command: "node x.js"}
		if err := a.Validate(); err == nil {
			t.Errorf("name %q should be rejected", c)
		}
	}
}

func TestValidateDockerRequiresImageOrBuild(t *testing.T) {
	a := &App{Name: "d", Runtime: RuntimeDocker, Docker: DockerSpec{ContainerPort: 3000}}
	if err := a.Validate(); err == nil {
		t.Error("docker app without image or build context should fail")
	}
	a.Docker.Image = "nginx:latest"
	if err := a.Validate(); err != nil {
		t.Errorf("docker app with image should pass: %v", err)
	}
}

func TestValidateDockerRequiresContainerPort(t *testing.T) {
	a := &App{Name: "d", Runtime: RuntimeDocker, Docker: DockerSpec{Image: "nginx:latest"}}
	if err := a.Validate(); err == nil {
		t.Error("docker app without container_port should fail")
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"My App":            "my-app",
		"foo.example.com":   "foo-example-com",
		"weird!@#$%chars":   "weirdchars",
		"--leading-dashes":  "leading-dashes",
		"":                  "app",
		"!!!":               "app",
		strings.Repeat("a", 100): strings.Repeat("a", 64),
	}
	for in, want := range cases {
		if got := SanitizeName(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}
