package monitor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
)

func testLogger() *logger.Logger {
	return logger.New("error", "text")
}

func TestNewMonitor(t *testing.T) {
	domains := []config.Domain{
		{Host: "example.com", Type: "static"},
	}
	m := New(domains, testLogger())
	if m == nil {
		t.Fatal("New returned nil")
	}
	if len(m.domains) != 1 {
		t.Errorf("domains = %d, want 1", len(m.domains))
	}
	if m.client == nil {
		t.Error("client is nil")
	}
}

func TestCheckDomainUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("OK"))
	}))
	defer srv.Close()

	// Extract host from test server URL
	host := strings.TrimPrefix(srv.URL, "http://")
	domains := []config.Domain{
		{Host: host, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
	}

	m := New(domains, testLogger())
	m.checkDomain(domains[0])

	results := m.Results()
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}

	r := results[0]
	if r.Host != host {
		t.Errorf("host = %q, want %q", r.Host, host)
	}
	if r.Status != "up" {
		t.Errorf("status = %q, want up", r.Status)
	}
	if r.StatusCode != 200 {
		t.Errorf("status_code = %d, want 200", r.StatusCode)
	}
	if r.ResponseMs < 0 {
		t.Errorf("response_ms = %d, should be >= 0", r.ResponseMs)
	}
	if len(r.Checks) != 1 {
		t.Errorf("checks = %d, want 1", len(r.Checks))
	}
	if r.Uptime != 100.0 {
		t.Errorf("uptime = %f, want 100.0", r.Uptime)
	}
}

func TestCheckDomainDown(t *testing.T) {
	// Use a server that returns 500
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	domains := []config.Domain{
		{Host: host, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
	}

	m := New(domains, testLogger())
	m.checkDomain(domains[0])

	results := m.Results()
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}

	r := results[0]
	if r.Status != "down" {
		t.Errorf("status = %q, want down", r.Status)
	}
	if r.StatusCode != 500 {
		t.Errorf("status_code = %d, want 500", r.StatusCode)
	}
}

func TestCheckDomainDegraded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	domains := []config.Domain{
		{Host: host, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
	}

	m := New(domains, testLogger())
	m.checkDomain(domains[0])

	results := m.Results()
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}

	if results[0].Status != "degraded" {
		t.Errorf("status = %q, want degraded", results[0].Status)
	}
}

func TestCheckDomainUnreachable(t *testing.T) {
	// Use a host that cannot be reached
	domains := []config.Domain{
		{Host: "127.0.0.1:1", Type: "static", SSL: config.SSLConfig{Mode: "off"}},
	}

	m := New(domains, testLogger())
	m.client.Timeout = 1 * time.Second
	m.checkDomain(domains[0])

	results := m.Results()
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}

	r := results[0]
	if r.Status != "down" {
		t.Errorf("status = %q, want down", r.Status)
	}
	if r.StatusCode != 0 {
		t.Errorf("status_code = %d, want 0", r.StatusCode)
	}
	if len(r.Checks) == 0 {
		t.Fatal("expected at least 1 check")
	}
	if r.Checks[0].Error == "" {
		t.Error("expected error in check")
	}
}

func TestCalculateUptime(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name   string
		checks []Check
		want   float64
	}{
		{
			name:   "no checks",
			checks: nil,
			want:   100.0,
		},
		{
			name: "all up",
			checks: []Check{
				{Time: now, StatusCode: 200},
				{Time: now, StatusCode: 301},
				{Time: now, StatusCode: 200},
			},
			want: 100.0,
		},
		{
			name: "half down",
			checks: []Check{
				{Time: now, StatusCode: 200},
				{Time: now, StatusCode: 500},
			},
			want: 50.0,
		},
		{
			name: "all errors",
			checks: []Check{
				{Time: now, Error: "connection refused"},
				{Time: now, Error: "timeout"},
			},
			want: 0.0,
		},
		{
			name: "old checks excluded",
			checks: []Check{
				{Time: now.Add(-48 * time.Hour), StatusCode: 500}, // older than 24h
				{Time: now, StatusCode: 200},
			},
			want: 100.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateUptime(tt.checks)
			if got != tt.want {
				t.Errorf("calculateUptime = %f, want %f", got, tt.want)
			}
		})
	}
}

func TestMaxChecksLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	domains := []config.Domain{
		{Host: host, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
	}

	m := New(domains, testLogger())

	// Run more checks than maxChecks
	for i := 0; i < maxChecks+20; i++ {
		m.checkDomain(domains[0])
	}

	results := m.Results()
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}

	if len(results[0].Checks) > maxChecks {
		t.Errorf("checks = %d, should be <= %d", len(results[0].Checks), maxChecks)
	}
}

func TestStartStopsOnCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	domains := []config.Domain{
		{Host: host, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
	}

	m := New(domains, testLogger())

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		m.Start(ctx)
		close(done)
	}()

	// Give it time to run the initial check
	time.Sleep(100 * time.Millisecond)

	// Verify it ran at least once
	results := m.Results()
	if len(results) == 0 {
		t.Error("expected at least one result after start")
	}

	// Cancel and wait for goroutine to finish
	cancel()
	select {
	case <-done:
		// OK
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not stop after context cancel")
	}
}

func TestSSLSchemeSelection(t *testing.T) {
	// This tests that auto/manual SSL domains use https scheme
	// We can't easily test the actual HTTPS request, but we verify
	// the logic by checking with off mode uses http
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")

	// With SSL off, should use http (which hits our test server)
	domains := []config.Domain{
		{Host: host, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
	}
	m := New(domains, testLogger())
	m.checkDomain(domains[0])

	results := m.Results()
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].Status != "up" {
		t.Errorf("status = %q, want up", results[0].Status)
	}
}

func TestResultsReturnsCopy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	domains := []config.Domain{
		{Host: host, Type: "static", SSL: config.SSLConfig{Mode: "off"}},
	}

	m := New(domains, testLogger())
	m.checkDomain(domains[0])

	results1 := m.Results()
	results2 := m.Results()

	// Modifying results1 should not affect results2
	if len(results1) > 0 && len(results1[0].Checks) > 0 {
		results1[0].Checks[0].StatusCode = 999
		if len(results2) > 0 && len(results2[0].Checks) > 0 {
			if results2[0].Checks[0].StatusCode == 999 {
				t.Error("Results should return independent copies")
			}
		}
	}
}
