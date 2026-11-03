package acme

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

func TestEnsureAccountRequiresLocation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		location   string
		wantErr    bool
		wantSuffix string
	}{
		{name: "created", status: http.StatusCreated, location: "/accounts/123", wantSuffix: "/accounts/123"},
		{name: "existing", status: http.StatusOK, location: "/accounts/456", wantSuffix: "/accounts/456"},
		{name: "created missing location", status: http.StatusCreated, wantErr: true},
		{name: "existing blank location", status: http.StatusOK, location: "   ", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/nonce":
					w.Header().Set("Replay-Nonce", "test-nonce")
					w.WriteHeader(http.StatusOK)
				case "/account":
					if strings.TrimSpace(tc.location) != "" {
						w.Header().Set("Location", server.URL+tc.location)
					} else if tc.location != "" {
						w.Header().Set("Location", tc.location)
					}
					w.Header().Set("Replay-Nonce", "next-nonce")
					w.WriteHeader(tc.status)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			t.Cleanup(server.Close)

			client := NewClient(server.URL+"/directory", t.TempDir(), logger.New("error", "text"))
			client.directory = &Directory{
				NewNonce:   server.URL + "/nonce",
				NewAccount: server.URL + "/account",
			}

			err := client.ensureAccount(context.Background())
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "Location") {
					t.Fatalf("ensureAccount() error = %v, want Location error", err)
				}
				if client.accountURL != "" {
					t.Fatalf("accountURL = %q after rejected response, want empty", client.accountURL)
				}
				return
			}
			if err != nil {
				t.Fatalf("ensureAccount() error = %v", err)
			}
			if !strings.HasSuffix(client.accountURL, tc.wantSuffix) {
				t.Fatalf("accountURL = %q, want suffix %q", client.accountURL, tc.wantSuffix)
			}
		})
	}
}
