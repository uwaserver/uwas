package acme

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

// F64901: a newOrder response without Location has no URL to poll; it must be
// rejected at once instead of solving every challenge and failing later.
func TestNewOrderRequiresLocation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		location string
		wantErr  bool
	}{
		{"present", "/order/1", false},
		{"missing", "", true},
		{"blank", "  ", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var srv *httptest.Server
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Replay-Nonce", "n")
				if r.URL.Path == "/order" {
					if strings.HasPrefix(tc.location, "/") {
						w.Header().Set("Location", srv.URL+tc.location)
					} else if tc.location != "" {
						w.Header().Set("Location", tc.location)
					}
					w.WriteHeader(http.StatusCreated)
					fmt.Fprintf(w, `{"status":"pending","finalize":%q}`, srv.URL+"/finalize/1")
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			c := NewClient(srv.URL+"/dir", t.TempDir(), logger.New("error", "text"))
			if err := c.loadOrCreateAccountKey(); err != nil {
				t.Fatal(err)
			}
			c.accountURL = srv.URL + "/acct/1"
			c.directory = &Directory{NewNonce: srv.URL + "/nonce", NewAccount: srv.URL + "/acct", NewOrder: srv.URL + "/order"}

			order, err := c.newOrder(context.Background(), []string{"a.example"})
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "Location") {
					t.Fatalf("err = %v, want missing Location error", err)
				}
				return
			}
			if err != nil || order.URL != srv.URL+"/order/1" {
				t.Fatalf("order = %+v, err = %v", order, err)
			}
		})
	}
}
