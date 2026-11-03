package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIRequestRejectsOversizedResponse(t *testing.T) {
	body := bytes.Repeat([]byte{'x'}, maxAPIResponseSize+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	got, err := apiRequest(http.MethodGet, srv.URL, "", nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("got len=%d err=%v, want size error", len(got), err)
	}
}
