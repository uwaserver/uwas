package deploy

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/apps"
)

// F66000 (regression): health_path must require a 2xx/3xx response, as the
// README documents. The probe failed only on 5xx, so a 404/403 health path
// (missing route, wrong app on the port, auth wall) passed the deploy and no
// rollback happened.
func TestProbeAppHealthRequires2xxOr3xx(t *testing.T) {
	mux := http.NewServeMux()
	for path, code := range map[string]int{"/ok": 200, "/nocontent": 204, "/missing": 404, "/forbidden": 403, "/unauth": 401, "/boom": 500} {
		code := code
		mux.HandleFunc(path, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) })
	}
	mux.HandleFunc("/redirect", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/ok", http.StatusFound) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	_, p, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	port, _ := strconv.Atoi(p)
	def := &apps.App{Name: "demo", Port: port}

	for path, wantErr := range map[string]bool{
		"/ok": false, "/nocontent": false, "/redirect": false,
		"/missing": true, "/forbidden": true, "/unauth": true, "/boom": true,
	} {
		err := ProbeAppHealth(def, path)
		if (err != nil) != wantErr {
			t.Errorf("ProbeAppHealth(%s) err=%v, want error=%v", path, err, wantErr)
		}
	}
	if err := ProbeAppHealth(def, ""); err != nil {
		t.Errorf("empty health path must skip the probe, got %v", err)
	}
}
