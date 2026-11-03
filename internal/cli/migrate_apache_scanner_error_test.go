package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateApachePropagatesScannerError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "site.conf")
	validPrefix := "<VirtualHost *:80>\nServerName first.test\nDocumentRoot /srv/first\n</VirtualHost>\n"
	contents := validPrefix + strings.Repeat("x", 1024*1024+1024) + "\n" +
		"<VirtualHost *:80>\nServerName omitted.test\nDocumentRoot /srv/omitted\n</VirtualHost>\n"
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	err := migrateApache(path)
	if err == nil || !strings.Contains(err.Error(), "scan Apache config") {
		t.Fatalf("migrateApache() error = %v, want scanner error", err)
	}
}
