package backup

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

type listStub struct {
	name string
	list func(ctx context.Context) ([]BackupInfo, error)
}

func (s *listStub) Name() string { return s.name }
func (s *listStub) Upload(context.Context, string, io.Reader) error {
	return nil
}
func (s *listStub) Download(context.Context, string) (io.ReadCloser, error) {
	return nil, io.EOF
}
func (s *listStub) List(ctx context.Context) ([]BackupInfo, error) { return s.list(ctx) }
func (s *listStub) Delete(context.Context, string) error           { return nil }

// TestListBackupsHungProviderDoesNotStarveOthers: one deadline was shared by
// every provider, so a hung SFTP/S3 target used the whole budget and the
// providers listed after it saw an expired context and reported nothing.
func TestListBackupsHungProviderDoesNotStarveOthers(t *testing.T) {
	old := listBackupsTimeout
	listBackupsTimeout = 60 * time.Millisecond
	defer func() { listBackupsTimeout = old }()

	hung := func(ctx context.Context) ([]BackupInfo, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	good := func(ctx context.Context) ([]BackupInfo, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return []BackupInfo{{Name: "uwas-backup-x.tar.gz", Provider: "good"}}, nil
	}
	// Map iteration order is random, so repeat to make the old behaviour fail
	// with overwhelming probability.
	for i := 0; i < 12; i++ {
		m := &BackupManager{
			logger: logger.New("error", "text"),
			providers: map[string]StorageProvider{
				"a": &listStub{name: "a", list: hung},
				"b": &listStub{name: "b", list: hung},
				"c": &listStub{name: "c", list: good},
			},
		}
		got := m.ListBackups()
		if len(got) != 1 || got[0].Provider != "good" {
			t.Fatalf("iteration %d: ListBackups = %+v, want the healthy provider's backup", i, got)
		}
	}
}
