package cli

// F51000/F51001: what `uwas domain remove` puts on the wire. The older unit
// test answered 200 to anything and so never noticed that the admin API
// refuses a DELETE without ?confirm=true and any state-changing request
// without X-Requested-With.

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDomainRemoveRequestShape(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantCleanup string
	}{
		{"default", []string{"remove", "old.test"}, ""},
		{"cleanup flag", []string{"remove", "old.test", "--cleanup"}, "true"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got *http.Request
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Clone(r.Context())
				_, _ = w.Write([]byte(`{"status":"deleted"}`))
			}))
			defer srv.Close()
			args := append(append([]string{}, c.args...), "--api-url", srv.URL, "--api-key", "k")
			if err := (&DomainCommand{}).Run(args); err != nil {
				t.Fatal(err)
			}
			if got == nil {
				t.Fatal("no request reached the server")
			}
			if got.Method != "DELETE" || got.URL.Path != "/api/v1/domains/old.test" {
				t.Fatalf("request = %s %s", got.Method, got.URL.Path)
			}
			if got.URL.Query().Get("confirm") != "true" {
				t.Errorf("confirm = %q, want true (the API refuses a delete without it)", got.URL.Query().Get("confirm"))
			}
			if got.URL.Query().Get("cleanup") != c.wantCleanup {
				t.Errorf("cleanup = %q, want %q", got.URL.Query().Get("cleanup"), c.wantCleanup)
			}
			if got.Header.Get("X-Requested-With") != "XMLHttpRequest" {
				t.Errorf("X-Requested-With = %q, want XMLHttpRequest (CSRF marker)", got.Header.Get("X-Requested-With"))
			}
		})
	}
}

func TestAPIRequestSendsCSRFMarkerOnEveryMethod(t *testing.T) {
	for _, method := range []string{"GET", "POST", "PUT", "DELETE"} {
		var got string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = r.Header.Get("X-Requested-With")
			_, _ = w.Write([]byte(`{}`))
		}))
		if _, err := apiRequest(method, srv.URL+"/x", "k", nil); err != nil {
			t.Fatal(err)
		}
		srv.Close()
		if got != "XMLHttpRequest" {
			t.Errorf("%s: X-Requested-With = %q", method, got)
		}
	}
}
