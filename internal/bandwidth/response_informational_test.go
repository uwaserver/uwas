package bandwidth

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

type bandwidthInformationalWriter struct {
	codes []int
	h     http.Header
}

func (w *bandwidthInformationalWriter) Header() http.Header         { return w.h }
func (w *bandwidthInformationalWriter) WriteHeader(code int)        { w.codes = append(w.codes, code) }
func (w *bandwidthInformationalWriter) Write(b []byte) (int, error) { return len(b), nil }
func bandwidthInformationalCodes(input []int) []int {
	w := &bandwidthInformationalWriter{h: make(http.Header)}
	rw := &responseWriter{ResponseWriter: w}
	for _, c := range input {
		rw.WriteHeader(c)
	}
	return w.codes
}
func TestResponseWriterInformationalHeaders(t *testing.T) {
	control := bandwidthInformationalCodes([]int{200, 500})
	if !reflect.DeepEqual(control, []int{200}) {
		t.Fatal("CONTROL FAILED", control)
	}
	fmt.Println("CONTROL PASSED")
	got := bandwidthInformationalCodes([]int{103, 201})
	fmt.Printf("EXPECTED: [103 201] ACTUAL: %v\n", got)
	if !reflect.DeepEqual(got, []int{103, 201}) {
		fmt.Println("PROBLEM CONFIRMED")
		t.FailNow()
	}
	fmt.Println("PROBLEM NOT REPRODUCED")
	for _, tc := range []struct{ input, want []int }{{[]int{100, 103, 204}, []int{100, 103, 204}}, {[]int{101, 200}, []int{101}}, {[]int{199, 200, 500}, []int{199, 200}}, {[]int{}, []int(nil)}} {
		got := bandwidthInformationalCodes(tc.input)
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("edge %v: got %v want %v", tc.input, got, tc.want)
		}
	}
	w := &bandwidthInformationalWriter{h: make(http.Header)}
	rw := &responseWriter{ResponseWriter: w}
	rw.WriteHeader(103)
	rw.Write([]byte("hello"))
	if !reflect.DeepEqual(w.codes, []int{103, 200}) {
		t.Fatal(w.codes)
	}
	fmt.Println("FIX VERIFIED")

}
