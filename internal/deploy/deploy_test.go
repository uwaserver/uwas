package deploy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectBuildCmdNode(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"build":"vite build"}}`), 0644)
	cmd := detectBuildCmd(dir)
	if cmd != "npm install && npm run build" {
		t.Errorf("got %q", cmd)
	}
}

func TestDetectBuildCmdNodeNoBuild(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"start":"node index.js"}}`), 0644)
	cmd := detectBuildCmd(dir)
	if cmd != "npm install" {
		t.Errorf("got %q", cmd)
	}
}

func TestDetectBuildCmdPython(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "requirements.txt"), []byte("flask\n"), 0644)
	cmd := detectBuildCmd(dir)
	if cmd != "pip install -r requirements.txt" {
		t.Errorf("got %q", cmd)
	}
}

func TestDetectBuildCmdGo(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module test\n"), 0644)
	cmd := detectBuildCmd(dir)
	if cmd != "go build -o app ." {
		t.Errorf("got %q", cmd)
	}
}

func TestDetectBuildCmdEmpty(t *testing.T) {
	cmd := detectBuildCmd(t.TempDir())
	if cmd != "" {
		t.Errorf("got %q", cmd)
	}
}

func TestSanitizeName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"example.com", "example-com"},
		{"My App!", "my-app-"},
		{"test-123", "test-123"},
	}
	for _, tt := range tests {
		got := sanitizeName(tt.in)
		if got != tt.want {
			t.Errorf("sanitizeName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNewManager(t *testing.T) {
	m := New(nil)
	if m == nil {
		t.Fatal("nil manager")
	}
	if s := m.Status("nope"); s != nil {
		t.Error("expected nil status for unknown domain")
	}
}

func TestAllStatuses(t *testing.T) {
	m := New(nil)
	all := m.AllStatuses()
	if len(all) != 0 {
		t.Errorf("expected 0, got %d", len(all))
	}
}
