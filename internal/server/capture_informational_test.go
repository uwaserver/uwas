package server

import (
	"fmt"
	"net/http"
	"testing"
)

type captureStatusWriter struct {
	header        http.Header
	status        int
	informational []int
}

func (w *captureStatusWriter) Header() http.Header { return w.header }
func (w *captureStatusWriter) WriteHeader(code int) {
	if code >= 100 && code < 200 && code != 101 {
		w.informational = append(w.informational, code)
		return
	}
	if w.status == 0 {
		w.status = code
	}
}
func (w *captureStatusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return len(b), nil
}
func captureStatusResponse(early bool) (int, int) {
	w := &captureStatusWriter{header: make(http.Header)}
	capture := newResponseCapture(w)
	if early {
		capture.WriteHeader(103)
	}
	capture.WriteHeader(201)
	capture.Write([]byte("created"))
	return w.status, capture.statusCode
}
func TestResponseCaptureInformationalStatus(t *testing.T) {
	wire, captured := captureStatusResponse(false)
	fmt.Printf("CONTROL EXPECTED: wire=201 captured=201 ACTUAL: wire=%d captured=%d\n", wire, captured)
	if wire != 201 || captured != 201 {
		t.Fatal("invalid control")
	}
	wire, captured = captureStatusResponse(true)
	fmt.Printf("EXPECTED: wire=201 captured=201 ACTUAL: wire=%d captured=%d\n", wire, captured)
	if wire != 201 || captured != 201 {
		fmt.Println("PROBLEM CONFIRMED")
		t.Fatal("early hints swallowed final status")
	}
	for _, code := range []int{100, 102, 103} {
		w := &captureStatusWriter{header: make(http.Header)}
		capture := newResponseCapture(w)
		capture.WriteHeader(code)
		capture.WriteHeader(code)
		capture.Write([]byte("body"))
		if w.status != 200 || capture.statusCode != 200 || len(w.informational) != 2 {
			t.Fatalf("informational %d: wire=%d capture=%d infos=%v", code, w.status, capture.statusCode, w.informational)
		}
	}
	w := &captureStatusWriter{header: make(http.Header)}
	capture := newResponseCapture(w)
	capture.WriteHeader(101)
	capture.WriteHeader(201)
	if w.status != 101 || capture.statusCode != 101 {
		t.Fatal("101 must remain final")
	}
	fmt.Println("FIX VERIFIED")
}
