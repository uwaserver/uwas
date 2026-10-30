package apps

import (
	"sync"
	"testing"
)

// F57003 sweep: concurrent Start/Stop/Register/Instances/StopAll on one app
// must be race-free and must leave nothing running after StopAll.
func TestLifecycleConcurrentStartStopNoRaceNoOrphan(t *testing.T) {
	m := testManager(t, NewStore(t.TempDir()), nil)
	if err := m.Register(&App{Name: "stress", Runtime: RuntimeCustom, Command: "sleep 30", WorkDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 4; j++ {
				switch (i + j) % 4 {
				case 0:
					_ = m.Start("stress")
				case 1:
					_ = m.Stop("stress")
				case 2:
					_ = m.Instances()
				case 3:
					_ = m.Get("stress")
				}
			}
		}(i)
	}
	wg.Wait()
	m.StopAll()
	if inst := m.Get("stress"); inst != nil && inst.Running {
		t.Fatalf("app still running after StopAll (pid %d)", inst.PID)
	}
}
