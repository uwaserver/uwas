package monitor

import (
	"context"
	"fmt"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"io"
	"net/http"
	"strings"
	"testing"
)

type replacedProbeTransport struct {
	arrived chan struct{}
	release chan struct{}
	status  int
}

func (rt replacedProbeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	if rt.arrived != nil {
		rt.arrived <- struct{}{}
		<-rt.release
	}
	return &http.Response{StatusCode: rt.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
}
func TestReaddedMonitorHostRejectsOldSweep(t *testing.T) {
	old := monitorURLSafetyCheck
	monitorURLSafetyCheck = func(string) error { return nil }
	defer func() { monitorURLSafetyCheck = old }()
	for _, status := range []int{200, 500} {
		domains := make([]config.Domain, checkConcurrency+1)
		for i := range domains {
			domains[i] = config.Domain{Host: fmt.Sprintf("scope%d.test", i)}
		}
		m := New(domains, logger.New("error", "text"))
		arrived, release, done := make(chan struct{}, len(domains)), make(chan struct{}), make(chan struct{})
		m.client = &http.Client{Transport: replacedProbeTransport{arrived, release, status}}
		go func() { m.sweep(context.Background()); close(done) }()
		for i := 0; i < checkConcurrency; i++ {
			<-arrived
		}
		m.UpdateDomains(nil)
		m.UpdateDomains(domains)
		close(release)
		<-done
		if got := m.Results(); len(got) != 0 {
			t.Fatalf("stale completed/queued checks published: %+v", got)
		}
		m.client = &http.Client{Transport: replacedProbeTransport{status: 200}}
		m.checkDomain(context.Background(), domains[0])
		if got := m.Results(); len(got) != 1 || got[0].Status != "up" {
			t.Fatal("new-generation control", got)
		}
		m.UpdateDomains(nil)
		if len(m.Results()) != 0 {
			t.Fatal("removed host retained")
		}
	}
}
