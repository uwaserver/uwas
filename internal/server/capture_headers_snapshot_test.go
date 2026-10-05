package server

import (
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func captureHeaderSnapshotCapture() *responseCapture {
	rc := newResponseCapture(httptest.NewRecorder())
	rc.Header()["X-Values"] = []string{"one", "two"}
	return rc
}
func captureHeaderSnapshotWait(t *testing.T, c <-chan struct{}) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(5 * time.Second):
		t.Fatal("gate timed out")
	}
}
func TestCapturedHeadersOwnValueSlices(t *testing.T) {
	t.Run("gated source mutation", func(t *testing.T) {
		rc := captureHeaderSnapshotCapture()
		ready := make(chan struct{})
		release := make(chan struct{})
		done := make(chan struct{})
		go func() { close(ready); <-release; rc.Header()["X-Values"][0] = "changed"; close(done) }()
		captureHeaderSnapshotWait(t, ready)
		snapshot := rc.capturedHeaders()
		close(release)
		captureHeaderSnapshotWait(t, done)
		if !reflect.DeepEqual(snapshot.Values("X-Values"), []string{"one", "two"}) {
			t.Fatal("snapshot changed after source update")
		}
	})
	t.Run("snapshot mutation independent", func(t *testing.T) {
		rc := captureHeaderSnapshotCapture()
		first := rc.capturedHeaders()
		second := rc.capturedHeaders()
		first["X-Values"][1] = "changed"
		delete(first, "X-Values")
		for _, h := range []map[string][]string{rc.Header(), second} {
			if !reflect.DeepEqual(h["X-Values"], []string{"one", "two"}) {
				t.Fatal("snapshot mutation escaped")
			}
		}
	})
	t.Run("nil and empty values", func(t *testing.T) {
		rc := newResponseCapture(httptest.NewRecorder())
		empty := rc.capturedHeaders()
		empty.Set("X-New", "value")
		if rc.Header().Get("X-New") != "" {
			t.Fatal("empty map aliased")
		}
		rc.Header()["X-Nil"] = nil
		rc.Header()["X-Empty"] = []string{}
		snapshot := rc.capturedHeaders()
		if !reflect.DeepEqual(snapshot, rc.Header()) {
			t.Fatal("nil or empty value changed")
		}
	})
}
