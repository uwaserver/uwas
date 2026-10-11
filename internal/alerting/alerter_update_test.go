package alerting

import (
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/notify"
)

// F2860: the delivery settings were fixed at construction, so a reload that
// switched alerting on or off never reached a running Alerter.

func TestUpdateTogglesRecording(t *testing.T) {
	a := New(false, "", nil, logger.New("error", "text"))
	a.Alert(Alert{Level: "warning", Type: "domain_down", Host: "a.test"})
	if n := len(a.Alerts()); n != 0 {
		t.Fatalf("disabled alerter recorded %d alerts, want 0", n)
	}

	a.Update(true, "", nil)
	a.Alert(Alert{Level: "warning", Type: "domain_down", Host: "a.test"})
	if n := len(a.Alerts()); n != 1 {
		t.Fatalf("after enabling, recorded %d alerts, want 1", n)
	}

	a.Update(false, "", nil)
	a.Alert(Alert{Level: "warning", Type: "domain_down", Host: "b.test"})
	if n := len(a.Alerts()); n != 1 {
		t.Fatalf("after disabling, recorded %d alerts, want 1 (no new one)", n)
	}
	a.RecordRequest(true) // must be a no-op while disabled
}

func TestUpdateReplacesDestinations(t *testing.T) {
	a := New(true, "http://old.invalid/hook", []notify.Channel{{Type: "slack", Enabled: true}}, logger.New("error", "text"))
	a.Update(true, "http://new.invalid/hook", nil)
	enabled, url, channels := a.settings()
	if !enabled || url != "http://new.invalid/hook" || len(channels) != 0 {
		t.Fatalf("settings = %v %q %d channels, want true new.invalid and none", enabled, url, len(channels))
	}
}

// Updates racing Alert/RecordRequest must be safe under -race.
func TestUpdateRace(t *testing.T) {
	a := New(true, "", nil, logger.New("error", "text"))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			<-start
			for j := 0; j < 200; j++ {
				a.Update(j%2 == 0, "", nil)
			}
		}(i)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 200; j++ {
				a.Alert(Alert{Level: "info", Type: "x", Host: "h"})
				a.RecordRequest(false)
			}
		}()
	}
	close(start)
	wg.Wait()
}
