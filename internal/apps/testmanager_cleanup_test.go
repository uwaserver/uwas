package apps

import (
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

// testManager is NewManager for tests: it stops every app the test started
// when the test ends. A native app that exits on its own is auto-restarted by
// its monitor goroutine, and that restart reads package-level hooks
// (execCommandFn, isPortFreeFn, ...) which later tests swap; a leaked monitor
// turned that into a data race that only showed under load.
func testManager(t testing.TB, store *Store, log *logger.Logger) *Manager {
	t.Helper()
	m := NewManager(store, log)
	t.Cleanup(m.StopAll)
	return m
}
