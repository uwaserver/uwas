package apps

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRejectsUnknownYAMLFields(t *testing.T) {
	dir := t.TempDir()
	data := []byte("name: demo\nruntime: node\nauto_restar: true\n")
	if err := os.WriteFile(filepath.Join(dir, "demo.yaml"), data, 0600); err != nil {
		t.Fatalf("write app: %v", err)
	}

	if app, err := NewStore(dir).Get("demo"); err == nil {
		t.Fatalf("Get() = %#v, nil; want unknown-field error", app)
	}
}
