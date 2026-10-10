package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"
)

// A slow shadow backend must not pile up unbounded copies: at most
// maxMirrorInflight are in flight and the rest are shed and counted (F901).
// Send decides synchronously and the shadow is blocked, so the counts are exact.
func TestMirrorInflightIsCapped(t *testing.T) {
	release := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer ts.Close()
	m := NewMirror(MirrorConfig{Enabled: true, Backend: ts.URL, Percent: 100}, testLogger())

	const sends = 200
	for i := 0; i < sends; i++ {
		m.Send(httptest.NewRequest("GET", fmt.Sprintf("/r%d", i), nil), nil)
	}
	if got := len(m.sem); got != maxMirrorInflight {
		t.Errorf("in flight = %d, want %d", got, maxMirrorInflight)
	}
	if got, want := m.Dropped(), int64(sends-maxMirrorInflight); got != want {
		t.Errorf("dropped = %d, want %d", got, want)
	}

	close(release)
	deadline := time.Now().Add(10 * time.Second)
	for len(m.sem) > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("slots not released: %d still held", len(m.sem))
		}
		runtime.Gosched()
	}
	before := m.Dropped()
	m.Send(httptest.NewRequest("GET", "/after", nil), nil)
	if m.Dropped() != before {
		t.Error("a copy after the shadow recovered was shed")
	}
}
