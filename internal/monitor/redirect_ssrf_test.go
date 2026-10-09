package monitor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// The probed site is tenant-controlled: a "/" that redirects to an internal
// address must not turn the monitor into an SSRF client. Both servers bind
// loopback, so the tenant site is allowed by exact host and every other URL
// goes through the production policy.
func TestCheckDomainRefusesRedirectToBlockedAddress(t *testing.T) {
	prevCheck, prevDial := monitorURLSafetyCheck, monitorDialControl
	t.Cleanup(func() { monitorURLSafetyCheck, monitorDialControl = prevCheck, prevDial })

	var internalHits atomic.Int32
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		internalHits.Add(1)
	}))
	defer internal.Close()
	tenant := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL+"/latest/meta-data/", http.StatusFound)
	}))
	defer tenant.Close()

	tenantHost := strings.TrimPrefix(tenant.URL, "http://")
	monitorURLSafetyCheck = func(raw string) error {
		if u, err := url.Parse(raw); err == nil && u.Host == tenantHost {
			return nil
		}
		return config.IsWebhookURLSafe(raw)
	}
	monitorDialControl = nil

	d := config.Domain{Host: tenantHost, Type: "static", SSL: config.SSLConfig{Mode: "off"}}
	m := New([]config.Domain{d}, testLogger())
	m.checkDomain(context.Background(), d)

	if n := internalHits.Load(); n != 0 {
		t.Fatalf("internal server reached %d times through the tenant redirect", n)
	}
	rs := m.Results()
	if len(rs) != 1 || !strings.Contains(rs[0].Checks[0].Error, "SSRF") {
		t.Fatalf("expected one check refused with an SSRF error, got %+v", rs)
	}
}

// A host that passes the URL pre-check but connects to a blocked address
// (DNS rebinding) is refused at dial time.
func TestCheckDomainDialControlRefusesBlockedAddress(t *testing.T) {
	prevCheck, prevDial := monitorURLSafetyCheck, monitorDialControl
	t.Cleanup(func() { monitorURLSafetyCheck, monitorDialControl = prevCheck, prevDial })

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer srv.Close()

	monitorURLSafetyCheck = func(string) error { return nil }
	monitorDialControl = config.SafeDialControl

	d := config.Domain{Host: strings.TrimPrefix(srv.URL, "http://"), Type: "static", SSL: config.SSLConfig{Mode: "off"}}
	m := New([]config.Domain{d}, testLogger())
	m.checkDomain(context.Background(), d)

	if hits.Load() != 0 {
		t.Fatal("connection to a blocked address was not refused at dial time")
	}
}
