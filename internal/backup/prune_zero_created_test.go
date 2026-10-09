package backup

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

type pruneRecorder struct {
	StorageProvider
	list    func(context.Context) ([]BackupInfo, error)
	deleted []string
}

func (r *pruneRecorder) List(ctx context.Context) ([]BackupInfo, error) { return r.list(ctx) }
func (r *pruneRecorder) Delete(_ context.Context, name string) error {
	r.deleted = append(r.deleted, name)
	return nil
}

// TestPruneOldKeepsNewestWhenListHasNoTimes pins retention on an SFTP server
// without GNU find: the ls fallback reports every backup with a zero Created
// time in oldest-first order, and pruneOld used to delete the newest archives
// (including the one just created) instead of the oldest.
func TestPruneOldKeepsNewestWhenListHasNoTimes(t *testing.T) {
	names := []string{
		"uwas-backup-20260101-020000.000000000.tar.gz",
		"uwas-backup-20260102-020000.000000000.tar.gz",
		"uwas-backup-20260103-020000.000000000.tar.gz",
		"uwas-backup-20260104-020000.000000000.tar.gz",
	}
	storage := t.TempDir()
	if err := os.MkdirAll(filepath.Join(storage, "backups"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(storage, "backups", n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	host, port, cleanup := startTestSSHServerLsFallback(t, storage)
	defer cleanup()
	sp := NewSFTPProvider(host, port, "testuser", "", "testpass", "/backups", true)

	rec := &pruneRecorder{list: sp.List}
	m := &BackupManager{
		logger:    logger.New("error", "text"),
		providers: map[string]StorageProvider{"sftp": rec},
		keepCount: 2,
	}
	m.pruneOld("sftp")

	sort.Strings(rec.deleted)
	if got, want := strings.Join(rec.deleted, ","), strings.Join(names[:2], ","); got != want {
		t.Fatalf("pruneOld deleted %s, want the two oldest %s", got, want)
	}
}
