package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

func testLogger() *logger.Logger {
	return logger.New("error", "text")
}

func TestMirrorShouldMirror(t *testing.T) {
	tests := []struct {
		name    string
		percent int
		expect  string // "always", "never", "sometimes"
	}{
		{"zero percent", 0, "never"},
		{"negative", -1, "never"},
		{"100 percent", 100, "always"},
		{"150 percent", 150, "always"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewMirror(MirrorConfig{Enabled: true, Backend: "http://mirror", Percent: tt.percent}, testLogger())
			result := m.ShouldMirror()
			switch tt.expect {
			case "always":
				if !result {
					t.Error("expected ShouldMirror to return true")
				}
			case "never":
				if result {
					t.Error("expected ShouldMirror to return false")
				}
			}
		})
	}
}

func TestMirrorShouldMirrorProbability(t *testing.T) {
	m := NewMirror(MirrorConfig{Enabled: true, Backend: "http://mirror", Percent: 50}, testLogger())

	hits := 0
	iterations := 1000
	for i := 0; i < iterations; i++ {
		if m.ShouldMirror() {
			hits++
		}
	}

	// With 50% probability and 1000 iterations, we should get somewhere between 350-650
	if hits < 300 || hits > 700 {
		t.Errorf("50%% mirror probability: expected ~500 hits out of %d, got %d", iterations, hits)
	}
}

func TestMirrorSend(t *testing.T) {
	var received atomic.Int32
	var mu sync.Mutex
	var receivedMethod string
	var receivedPath string
	var receivedBody string
	var receivedMirrorHeader string
	done := make(chan struct{}, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		receivedBody = string(body)
		receivedMirrorHeader = r.Header.Get("X-Mirror")
		mu.Unlock()
		w.WriteHeader(200)
		select {
		case done <- struct{}{}:
		default:
		}
	}))
	defer ts.Close()

	m := NewMirror(MirrorConfig{Enabled: true, Backend: ts.URL, Percent: 100}, testLogger())

	req := httptest.NewRequest("POST", "/api/test?q=1", strings.NewReader("hello"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Custom", "value")

	m.Send(req, []byte("hello"))

	// Wait for async mirror
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("mirror request was not received within timeout")
	}

	if received.Load() != 1 {
		t.Fatalf("expected 1 mirror request, got %d", received.Load())
	}

	mu.Lock()
	defer mu.Unlock()

	if receivedMethod != "POST" {
		t.Errorf("expected POST, got %s", receivedMethod)
	}
	if receivedPath != "/api/test" {
		t.Errorf("expected /api/test, got %s", receivedPath)
	}
	if receivedBody != "hello" {
		t.Errorf("expected body 'hello', got %s", receivedBody)
	}
	if receivedMirrorHeader != "true" {
		t.Errorf("expected X-Mirror header 'true', got %s", receivedMirrorHeader)
	}
}

func TestMirrorSendNoBackend(t *testing.T) {
	m := NewMirror(MirrorConfig{Enabled: true, Backend: "", Percent: 100}, testLogger())

	req := httptest.NewRequest("GET", "/test", nil)
	// Should not panic with empty backend
	m.Send(req, nil)

	// Give time for goroutine to run
	time.Sleep(50 * time.Millisecond)
}

func TestMirrorSendBadBackend(t *testing.T) {
	m := NewMirror(MirrorConfig{Enabled: true, Backend: "http://127.0.0.1:1", Percent: 100}, testLogger())
	m.transport.ResponseHeaderTimeout = 500 * time.Millisecond

	req := httptest.NewRequest("GET", "/test", nil)
	// Should not panic with unreachable backend
	m.Send(req, nil)

	// Give time for goroutine to run and fail (connection refused is near-instant)
	time.Sleep(500 * time.Millisecond)
}

func TestMirrorSendPreservesHeaders(t *testing.T) {
	var mu sync.Mutex
	var receivedHeaders http.Header
	done := make(chan struct{}, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		receivedHeaders = r.Header.Clone()
		mu.Unlock()
		w.WriteHeader(200)
		select {
		case done <- struct{}{}:
		default:
		}
	}))
	defer ts.Close()

	m := NewMirror(MirrorConfig{Enabled: true, Backend: ts.URL, Percent: 100}, testLogger())

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer token123")
	req.Header.Set("Accept", "application/json")

	m.Send(req, nil)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("mirror request was not received within timeout")
	}

	mu.Lock()
	defer mu.Unlock()

	if receivedHeaders == nil {
		t.Fatal("no request received by mirror backend")
	}

	if receivedHeaders.Get("Authorization") != "Bearer token123" {
		t.Errorf("Authorization header not preserved: %s", receivedHeaders.Get("Authorization"))
	}
	if receivedHeaders.Get("Accept") != "application/json" {
		t.Errorf("Accept header not preserved: %s", receivedHeaders.Get("Accept"))
	}
	if receivedHeaders.Get("X-Mirror") != "true" {
		t.Errorf("X-Mirror header missing: %s", receivedHeaders.Get("X-Mirror"))
	}
}

func TestNewMirror(t *testing.T) {
	cfg := MirrorConfig{
		Enabled: true,
		Backend: "http://mirror.local:8080",
		Percent: 50,
	}
	m := NewMirror(cfg, testLogger())

	if m.backend != "http://mirror.local:8080" {
		t.Errorf("expected backend http://mirror.local:8080, got %s", m.backend)
	}
	if m.percent != 50 {
		t.Errorf("expected percent 50, got %d", m.percent)
	}
}
