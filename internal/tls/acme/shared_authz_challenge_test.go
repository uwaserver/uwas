package acme

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

// sharedAuthzDNS is a TXT store keyed by record name, holding a set of values.
type sharedAuthzDNS struct {
	mu   sync.Mutex
	txts map[string]map[string]bool
}

func (d *sharedAuthzDNS) PresentDNSChallenge(name, token, keyAuth string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.txts[name] == nil {
		d.txts[name] = map[string]bool{}
	}
	d.txts[name][keyAuth] = true
	return nil
}

func (d *sharedAuthzDNS) CleanupDNSChallenge(name, token, keyAuth string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.txts[name], keyAuth)
	return nil
}

func (d *sharedAuthzDNS) has(name, keyAuth string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.txts[name][keyAuth]
}

// sharedAuthzCA holds the first challenge poll until release is closed and
// then validates the challenge the way a CA does: over HTTP-01 through
// HandleHTTPChallenge, or by looking up the DNS-01 TXT record.
type sharedAuthzCA struct {
	c         *Client
	dns       *sharedAuthzDNS
	typ       string
	url       string
	mu        sync.Mutex
	polls     int
	polling   chan struct{}
	release   chan struct{}
	readyPost chan struct{}
}

func newSharedAuthzCA(t *testing.T, typ string) *sharedAuthzCA {
	t.Helper()
	ca := &sharedAuthzCA{typ: typ, polling: make(chan struct{}), release: make(chan struct{}),
		readyPost: make(chan struct{}, 4), dns: &sharedAuthzDNS{txts: map[string]map[string]bool{}}}
	srv := httptest.NewServer(http.HandlerFunc(ca.serve))
	t.Cleanup(srv.Close)
	ca.url = srv.URL
	ca.c = NewClient(srv.URL+"/dir", t.TempDir(), logger.New("error", "text"))
	if err := ca.c.loadOrCreateAccountKey(); err != nil {
		t.Fatal(err)
	}
	ca.c.accountURL = srv.URL + "/acct/1"
	ca.c.directory = &Directory{NewNonce: srv.URL + "/nonce", NewAccount: srv.URL + "/acct", NewOrder: srv.URL + "/order"}
	if typ == "dns-01" {
		ca.c.SetDNSProvider(ca.dns)
	}
	return ca
}

func (ca *sharedAuthzCA) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Replay-Nonce", "n")
	if !strings.HasPrefix(r.URL.Path, "/chall/") {
		w.WriteHeader(http.StatusOK)
		return
	}
	token := strings.TrimPrefix(r.URL.Path, "/chall/")
	var jws struct {
		Payload string `json:"payload"`
	}
	_ = json.NewDecoder(r.Body).Decode(&jws)
	if jws.Payload != "" { // the ready POST
		ca.readyPost <- struct{}{}
		fmt.Fprintf(w, `{"type":%q,"status":"pending"}`, ca.typ)
		return
	}
	ca.mu.Lock()
	ca.polls++
	first := ca.polls == 1
	ca.mu.Unlock()
	if first {
		close(ca.polling)
		<-ca.release
	}
	var found bool
	if ca.typ == "dns-01" {
		thumb, _ := thumbprintFunc(&ca.c.accountKey.PublicKey)
		found = ca.dns.has("_acme-challenge.shared.example", token+"."+thumb)
	} else {
		rec := httptest.NewRecorder()
		found = ca.c.HandleHTTPChallenge(rec, httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/"+token, nil)) &&
			strings.HasPrefix(rec.Body.String(), token+".")
	}
	if found {
		fmt.Fprintf(w, `{"type":%q,"status":"valid"}`, ca.typ)
		return
	}
	fmt.Fprintf(w, `{"type":%q,"status":"invalid","error":{"type":"urn:ietf:params:acme:error:unauthorized","detail":"challenge response not found"}}`, ca.typ)
}

func (ca *sharedAuthzCA) authz(token string) *Authorization {
	a := &Authorization{Status: "pending", Identifier: Identifier{Type: "dns", Value: "shared.example"}}
	a.Challenges = []Challenge{{Type: ca.typ, URL: ca.url + "/chall/" + token, Token: token}}
	return a
}

// solveOverlapping runs order B, holds its validation, lets order A (same or
// different token) publish and give up, then lets the CA validate B.
func (ca *sharedAuthzCA) solveOverlapping(t *testing.T, tokenA, tokenB string) error {
	t.Helper()
	bErr := make(chan error, 1)
	go func() { bErr <- ca.c.solveChallenge(context.Background(), ca.authz(tokenB)) }()
	<-ca.readyPost
	<-ca.polling

	actx, cancel := context.WithCancel(context.Background())
	aErr := make(chan error, 1)
	go func() { aErr <- ca.c.solveChallenge(actx, ca.authz(tokenA)) }()
	<-ca.readyPost
	cancel()
	if err := <-aErr; err == nil {
		t.Fatal("order A was expected to fail once cancelled")
	}
	close(ca.release)
	return <-bErr
}

// A CA may hand overlapping orders the same pending authorization (RFC 8555
// §7.4), so both publish the same challenge response. The order that gives up
// first must not withdraw the HTTP-01 token or DNS-01 TXT value the other is
// still being validated on (F66100/F66101).
func TestSharedAuthzChallengeSurvivesOtherOrder(t *testing.T) {
	for _, typ := range []string{"http-01", "dns-01"} {
		t.Run(typ+"/distinct", func(t *testing.T) {
			if err := newSharedAuthzCA(t, typ).solveOverlapping(t, "tokA", "tokB"); err != nil {
				t.Fatalf("distinct authorizations: B failed: %v", err)
			}
		})
		t.Run(typ+"/shared", func(t *testing.T) {
			ca := newSharedAuthzCA(t, typ)
			if err := ca.solveOverlapping(t, "shared", "shared"); err != nil {
				t.Fatalf("shared authorization: B failed after A gave up: %v", err)
			}
			// Once both orders are done the response is withdrawn.
			n := 0
			ca.c.httpTokens.Range(func(_, _ any) bool { n++; return true })
			thumb, _ := thumbprintFunc(&ca.c.accountKey.PublicKey)
			if n != 0 || ca.dns.has("_acme-challenge.shared.example", "shared."+thumb) {
				t.Fatalf("challenge response left behind: http tokens=%d", n)
			}
		})
	}
}
