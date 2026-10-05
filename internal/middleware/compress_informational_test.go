package middleware

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type regressionAudit54Writer struct {
	header http.Header
	hints  []int
	status int
	body   bytes.Buffer
}

func (x *regressionAudit54Writer) Header() http.Header { return x.header }
func (x *regressionAudit54Writer) WriteHeader(code int) {
	if code >= 100 && code < 200 && code != 101 {
		x.hints = append(x.hints, code)
		return
	}
	if x.status == 0 {
		x.status = code
	}
}
func (x *regressionAudit54Writer) Write(b []byte) (int, error) {
	if x.status == 0 {
		x.WriteHeader(200)
	}
	return x.body.Write(b)
}
func TestCompressInformationalFinalResponse(t *testing.T) {
	for _, hints := range [][]int{nil, {100}, {102}, {103}, {103, 103}} {
		for _, status := range []int{0, 201, 500} {
			for _, payload := range []string{"", "small", strings.Repeat("response body ", 20)} {
				w := &regressionAudit54Writer{header: make(http.Header)}
				h := Compress(32)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/plain")
					w.Header().Set("X-Fixture", "preserved")
					for _, code := range hints {
						w.WriteHeader(code)
					}
					if status != 0 {
						w.WriteHeader(status)
						w.WriteHeader(418)
					}
					if payload != "" {
						w.Write([]byte(payload))
					}
				}))
				req := httptest.NewRequest("GET", "/fixture", nil)
				req.Header.Set("Accept-Encoding", "gzip")
				h.ServeHTTP(w, req)
				wantStatus := status
				if status == 0 {
					wantStatus = 200
				}
				if w.status != wantStatus || !reflect.DeepEqual(w.hints, hints) || w.header.Get("X-Fixture") != "preserved" {
					t.Fatalf("status=%d hints=%v got=%+v", status, hints, w)
				}
				got := w.body.Bytes()
				if len(payload) >= 32 {
					if w.header.Get("Content-Encoding") != "gzip" {
						t.Fatal("final body not compressed")
					}
					reader, err := gzip.NewReader(bytes.NewReader(got))
					if err != nil {
						t.Fatal(err)
					}
					got, err = io.ReadAll(reader)
					reader.Close()
					if err != nil {
						t.Fatal(err)
					}
				} else if w.header.Get("Content-Encoding") != "" {
					t.Fatal("small body compressed")
				}
				if string(got) != payload {
					t.Fatalf("body lost %q want %q", got, payload)
				}
			}
		}
	}
	w := &regressionAudit54Writer{header: make(http.Header)}
	cw := &compressResponseWriter{ResponseWriter: w, minSize: 32, encoding: encodingGzip}
	cw.WriteHeader(101)
	cw.Close()
	if w.status != 101 || w.header.Get("Content-Encoding") != "" {
		t.Fatal("switch protocol status", w)
	}
	fmt.Println("FIX VERIFIED")
}
