package apps

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScaffoldDemoNodeCreatesDetectableEntrypoint(t *testing.T) {
	dir := t.TempDir()
	app := &App{Name: "demo-node", Runtime: RuntimeNode, WorkDir: dir}

	scaffolded, err := ScaffoldDemo(app)
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if !scaffolded {
		t.Fatal("expected node scaffold to write demo files")
	}
	if _, err := os.Stat(filepath.Join(dir, "index.js")); err != nil {
		t.Fatalf("index.js not written: %v", err)
	}
	if got := detectCommand(string(RuntimeNode), dir); got != "node index.js" {
		t.Fatalf("detectCommand = %q, want node index.js", got)
	}
}

func TestScaffoldDemoDoesNotOverwriteExistingWorkdir(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "index.js")
	if err := os.WriteFile(existing, []byte("console.log('mine')\n"), 0644); err != nil {
		t.Fatal(err)
	}
	app := &App{Name: "demo-node", Runtime: RuntimeNode, WorkDir: dir}

	scaffolded, err := ScaffoldDemo(app)
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if scaffolded {
		t.Fatal("expected existing workdir to be left untouched")
	}
	data, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "console.log('mine')\n" {
		t.Fatalf("existing file was overwritten: %q", string(data))
	}
}

func TestScaffoldDemoGoSetsRunnableCommand(t *testing.T) {
	dir := t.TempDir()
	app := &App{Name: "demo-go", Runtime: RuntimeGo, WorkDir: dir}

	scaffolded, err := ScaffoldDemo(app)
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	if !scaffolded {
		t.Fatal("expected go scaffold to write demo files")
	}
	if app.Command != "go run main.go" {
		t.Fatalf("go scaffold command = %q, want go run main.go", app.Command)
	}
}
