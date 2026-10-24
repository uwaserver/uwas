package acme

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

// F25900: a 200 response whose body is valid JSON but not an ACME directory
// ("{}", "null") was cached as the directory for the life of the client, so
// every later order failed with `Head "": unsupported protocol scheme` and
// the directory was never fetched again.
func TestEnsureDirectoryRejectsIncompleteDirectory(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Write([]byte(`{}`))
			return
		}
		json.NewEncoder(w).Encode(Directory{
			NewNonce:   "https://acme.example.com/nonce",
			NewAccount: "https://acme.example.com/acct",
			NewOrder:   "https://acme.example.com/order",
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, t.TempDir(), logger.New("error", "text"))
	if err := c.ensureDirectory(context.Background()); err == nil {
		t.Fatalf("EXPECTED: error for a directory without endpoints; ACTUAL: nil, directory=%+v", c.directory)
	}
	if c.directory != nil {
		t.Fatalf("incomplete directory was cached: %+v", c.directory)
	}
	if err := c.ensureDirectory(context.Background()); err != nil {
		t.Fatalf("directory must be re-fetched after a rejected one: %v", err)
	}
	if c.directory == nil || c.directory.NewOrder == "" {
		t.Fatalf("complete directory not stored: %+v", c.directory)
	}
}

// F25901: error bodies were read without a bound and embedded in the error,
// so a proxy or captive-portal page answering for the CA ended up, whole, in
// logs, the failure state and the admin API.
func TestACMEErrorBodyIsBounded(t *testing.T) {
	huge := strings.Repeat("x", 1<<20)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Replay-Nonce", "n")
		if r.Method == http.MethodHead {
			return
		}
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(huge))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, t.TempDir(), logger.New("error", "text"))
	c.directory = &Directory{NewNonce: srv.URL, NewOrder: srv.URL}
	c.accountKey, _ = generateKey()

	_, err := c.newOrder(context.Background(), []string{"example.com"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if n := len(err.Error()); n > 16<<10 {
		t.Fatalf("EXPECTED: error <= 16KiB; ACTUAL: %d bytes", n)
	}
	if !strings.Contains(err.Error(), "403") {
		t.Fatalf("status lost from error: %.80s", err)
	}

	// Control: a short problem document is reported in full.
	if got := acmeError(429, []byte(`{"type":"urn:x:rateLimited","detail":"slow down"}`)).Error(); !strings.Contains(got, "slow down") {
		t.Fatalf("problem document lost: %s", got)
	}
}
