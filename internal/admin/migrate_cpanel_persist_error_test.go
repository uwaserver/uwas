package admin

import (
	"path/filepath"
	"testing"
)

// A cPanel import copies the site and registers its domains in memory; the
// configuration write used to be ignored, so an unwritable configuration still
// answered "imported" and the new domains vanished on restart.
func TestCPanelImportPersistFailureIsReported(t *testing.T) {
	t.Run("control: writable config", func(t *testing.T) {
		s, _ := testServerWithRoot(t)
		s.SetConfigPath(filepath.Join(t.TempDir(), "uwas.yaml"))
		if rec := cpanelUploadpost(t, s, "backup", cpanelUploadarchive(t)); rec.Code != 200 {
			t.Fatalf("import = %d %s, want 200", rec.Code, rec.Body.String())
		}
	})
	t.Run("unwritable config", func(t *testing.T) {
		s, _ := testServerWithRoot(t)
		p := filepath.Join(t.TempDir(), "uwas.yaml")
		s.SetConfigPath(p)
		blockConfigPath(t, p)
		rec := cpanelUploadpost(t, s, "backup", cpanelUploadarchive(t))
		if rec.Code != 500 {
			t.Fatalf("EXPECTED: 500 when the configuration cannot be written\nACTUAL: %d %s", rec.Code, rec.Body.String())
		}
	})
}
