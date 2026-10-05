package middleware

import (
	"fmt"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/router"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAccessLogReleasesResponseWriter(t *testing.T) {
	for _, status := range []int{200, 500, 0, -1} {
		entered := make(chan *router.ResponseWriter)
		release := make(chan struct{})
		done := make(chan any, 1)
		out := httptest.NewRecorder()
		h := AccessLog(logger.New("error", "text"), nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			entered <- w.(*router.ResponseWriter)
			<-release
			if status < 0 {
				panic("fixture panic")
			}
			if status > 0 {
				w.WriteHeader(status)
				w.Write([]byte("fixture"))
			}
		}))
		go func() {
			var p any
			defer func() { p = recover(); done <- p }()
			h.ServeHTTP(out, httptest.NewRequest("GET", "/fixture", nil))
		}()
		rw := <-entered
		if rw.ResponseWriter != out {
			t.Fatal("wrapper released while handler active")
		}
		close(release)
		p := <-done
		if rw.ResponseWriter != nil {
			t.Fatalf("status %d retained underlying writer", status)
		}
		if status < 0 {
			if p != "fixture panic" {
				t.Fatal("panic not preserved")
			}
		} else {
			if p != nil {
				t.Fatal(p)
			}
			want := status
			if want == 0 {
				want = 200
			}
			if out.Code != want {
				t.Fatal("status altered")
			}
			if status > 0 && out.Body.String() != "fixture" {
				t.Fatal("body altered")
			}
		}
	}
	out := httptest.NewRecorder()
	AccessLog(logger.New("error", "text"), func() bool { return false })(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if w != out {
			t.Error("disabled middleware wrapped writer")
		}
	})).ServeHTTP(out, httptest.NewRequest("GET", "/", nil))
	fmt.Println("FIX VERIFIED")
}
