package admin

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/webhook"
)

// F65900: webhook create/delete published their snapshot to the live manager
// after releasing configMu, so a slower request's older list overwrote a newer
// one: a created webhook never went live, or a deleted one came back and kept
// receiving events. The first request is held at its publish point; when it no
// longer holds configMu the second request finishes first (gated, no sleeps).
func TestWebhookPublishFollowsConfigOrder(t *testing.T) {
	newServer := func() (*Server, *webhook.Manager) {
		s := testServer()
		s.SetConfigPath(filepath.Join(t.TempDir(), "uwas.yaml"))
		m := webhook.NewManager("", logger.New("error", "text"))
		t.Cleanup(m.Close)
		s.SetWebhookManager(m)
		return s, m
	}
	create := func(s *Server, host string) int {
		rec := httptest.NewRecorder()
		s.handleWebhookCreate(rec, totpActivationReq("POST", "/api/v1/webhooks", `{"url":"https://`+host+`/hook","events":["login.failed"]}`, "admin"))
		return rec.Code
	}
	del := func(s *Server, id string) int {
		rec := httptest.NewRecorder()
		req := totpActivationReq("DELETE", "/api/v1/webhooks/"+id, "", "admin")
		req.SetPathValue("id", id)
		s.handleWebhookDelete(rec, req)
		return rec.Code
	}
	live := func(m *webhook.Manager) []string {
		v := reflect.ValueOf(m).Elem().FieldByName("webhooks")
		out := []string{}
		for i := 0; i < v.Len(); i++ {
			out = append(out, v.Index(i).FieldByName("URL").String())
		}
		return out
	}
	cfg := func(s *Server) []string {
		s.configMu.RLock()
		defer s.configMu.RUnlock()
		out := []string{}
		for _, w := range s.config.Global.Webhooks {
			out = append(out, w.URL)
		}
		return out
	}
	gate := func(s *Server, first, second func() int) (int, int) {
		at, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		testWebhookPushHook = func() {
			mine := false
			once.Do(func() { mine = true })
			if mine {
				close(at)
				<-release
			}
		}
		defer func() { testWebhookPushHook = nil }()
		var c1, c2 int
		done1 := make(chan struct{})
		go func() { defer close(done1); c1 = first() }()
		<-at
		if s.configMu.TryLock() {
			s.configMu.Unlock()
			c2 = second()
			close(release)
			<-done1
		} else {
			done2 := make(chan struct{})
			go func() { defer close(done2); c2 = second() }()
			close(release)
			<-done1
			<-done2
		}
		return c1, c2
	}

	s, m := newServer()
	a, b := gate(s, func() int { return create(s, "93.184.216.34") }, func() int { return create(s, "93.184.216.35") })
	if a != http.StatusOK || b != http.StatusOK || !slices.Equal(live(m), cfg(s)) || len(cfg(s)) != 2 {
		t.Errorf("create/create: codes=%d,%d live=%v config=%v", a, b, live(m), cfg(s))
	}

	s, m = newServer()
	if c := create(s, "93.184.216.36"); c != http.StatusOK {
		t.Fatalf("seed create = %d", c)
	}
	a, b = gate(s, func() int { return create(s, "93.184.216.37") }, func() int { return del(s, "0") })
	if want := []string{"https://93.184.216.37/hook"}; a != http.StatusOK || b != http.StatusOK || !slices.Equal(live(m), want) || !slices.Equal(cfg(s), want) {
		t.Errorf("create/delete: codes=%d,%d live=%v config=%v, want %v", a, b, live(m), cfg(s), want)
	}
}
