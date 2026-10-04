package router

import (
	"fmt"
	"net/http/httptest"
	"testing"
)

func TestResponseWriterFlushCommitsStatus(t *testing.T) {
	c := httptest.NewRecorder()
	cw := NewResponseWriter(c)
	defer ReleaseResponseWriter(cw)
	cw.WriteHeader(201)
	cw.Flush()
	if c.Code != 201 || cw.StatusCode() != 201 {
		t.Fatal("CONTROL FAILED")
	}
	fmt.Println("CONTROL PASSED")
	r := httptest.NewRecorder()
	w := NewResponseWriter(r)
	defer ReleaseResponseWriter(w)
	w.Flush()
	w.WriteHeader(201)
	fmt.Printf("EXPECTED: captured status matches flushed status 200 ACTUAL: captured=%d downstream=%d\n", w.StatusCode(), r.Code)
	if w.StatusCode() != r.Code {
		fmt.Println("PROBLEM CONFIRMED")
		t.FailNow()
	}
	fmt.Println("PROBLEM NOT REPRODUCED")
	for _, code := range []int{200, 204, 503} {
		r := httptest.NewRecorder()
		w := NewResponseWriter(r)
		w.WriteHeader(code)
		w.Flush()
		w.Flush()
		w.WriteHeader(201)
		if w.StatusCode() != code || r.Code != code {
			t.Fatalf("edge %d: %d %d", code, w.StatusCode(), r.Code)
		}
		ReleaseResponseWriter(w)
	}
	r = httptest.NewRecorder()
	w2 := NewResponseWriter(r)
	defer ReleaseResponseWriter(w2)
	w2.Flush()
	w2.Flush()
	w2.Write([]byte("abc"))
	if w2.StatusCode() != 200 || w2.BytesWritten() != 3 || !w2.headerWritten {
		t.Fatal("implicit flush/body state")
	}
	fmt.Println("FIX VERIFIED")

}
