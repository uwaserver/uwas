package alerting

import (
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/notify"
)

func TestAlertChannelsOwnConfig(t *testing.T) {
	for _, mode := range []string{"new", "update"} {
		cfg := []notify.Channel{{Type: "slack", Enabled: true, Config: map[string]string{"webhook_url": "original"}}}
		a := New(true, "", nil, logger.New("error", "text"))
		if mode == "new" {
			a = New(true, "", cfg, logger.New("error", "text"))
		} else {
			a.Update(true, "", cfg)
		}
		arrived, release := make(chan struct{}, 1), make(chan struct{})
		delivered := make(chan string, 2)
		a.sendChannel = func(ch notify.Channel, _ notify.Message) error {
			arrived <- struct{}{}
			<-release
			delivered <- ch.Config["webhook_url"]
			return nil
		}
		a.Alert(Alert{Type: "test"})
		<-arrived
		cfg[0].Config["webhook_url"] = "changed"
		cfg[0].Enabled = false
		cfg[0].Type = "changed"
		close(release)
		if got := <-delivered; got != "original" {
			t.Fatalf("%s in-flight destination=%q", mode, got)
		}
		a.Alert(Alert{Type: "test"})
		<-arrived
		if got := <-delivered; got != "original" {
			t.Fatalf("%s registered destination=%q", mode, got)
		}
		a.Update(false, "", nil)
		before := len(a.Alerts())
		a.Alert(Alert{Type: "test"})
		if len(a.Alerts()) != before {
			t.Fatal("disabled recorded")
		}
		a.Update(true, "", []notify.Channel{{Type: "slack"}})
		_, _, chs := a.settings()
		if chs[0].Config != nil {
			t.Fatal("nil config changed")
		}
		a.Update(true, "", []notify.Channel{})
		_, _, chs = a.settings()
		if chs == nil {
			t.Fatal("empty slice lost")
		}
	}
}
