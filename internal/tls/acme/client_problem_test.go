package acme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

// acmeStub serves a nonce endpoint plus one order endpoint. polls counts the
// order-endpoint requests so tests can assert fail-fast vs. retry behaviour.
type acmeStub struct {
	client    *Client
	orderURL  string
	pollCount *int32
}

func newACMEStub(t *testing.T, handleOrder http.HandlerFunc) *acmeStub {
	t.Helper()

	var nonce int32
	polls := new(int32)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/new-nonce":
			atomic.AddInt32(&nonce, 1)
			w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-%d", atomic.LoadInt32(&nonce)))
			w.WriteHeader(http.StatusOK)
		case "/order/1":
			atomic.AddInt32(polls, 1)
			w.Header().Set("Replay-Nonce", "reply-1")
			handleOrder(w, r)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, t.TempDir(), logger.New("error", "text"))
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	c.accountKey = key
	c.accountURL = "https://acme.example.com/acct/1"
	c.directory = &Directory{NewNonce: srv.URL + "/new-nonce"}

	return &acmeStub{client: c, orderURL: srv.URL + "/order/1", pollCount: polls}
}

func (s *acmeStub) polls() int { return int(atomic.LoadInt32(s.pollCount)) }

// problemDoc writes an RFC 7807 problem document with the given status code.
func problemDoc(w http.ResponseWriter, code int, problemType, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(code)
	if problemType == "" && detail == "" {
		fmt.Fprint(w, "upstream failure")
		return
	}
	body := map[string]string{}
	if problemType != "" {
		body["type"] = problemType
	}
	if detail != "" {
		body["detail"] = detail
	}
	json.NewEncoder(w).Encode(body)
}

// TestWaitForStatusSurfacesACMEProblemDocument pins that a non-2xx response is
// reported as the ACME error it is.
//
// The three other ACME entry points all check resp.StatusCode —
// ensureDirectory ("directory returned %d"), ensureAccount ("account creation
// returned %d") and newOrder ("new order returned %d") — but waitForStatus did
// not. An RFC 7807 problem document decodes into an Order whose Status is "",
// which the poll loop read as "still pending": a hard rate-limit or
// authorization failure was retried for the whole attempt budget and then
// reported as "timeout waiting for status", hiding the real cause.
func TestWaitForStatusSurfacesACMEProblemDocument(t *testing.T) {
	tests := []struct {
		name    string
		code    int
		typ     string
		detail  string
		wantSub string
	}{
		{
			name:    "rate limited",
			code:    http.StatusTooManyRequests,
			typ:     "urn:ietf:params:acme:error:rateLimited",
			detail:  "too many certificates already issued for these identifiers",
			wantSub: "rateLimited",
		},
		{
			name:    "unauthorized",
			code:    http.StatusForbidden,
			typ:     "urn:ietf:params:acme:error:unauthorized",
			detail:  "account is not authorized for this order",
			wantSub: "403",
		},
		{
			name:    "detail only",
			code:    http.StatusBadRequest,
			detail:  "malformed request",
			wantSub: "malformed request",
		},
		{
			name:    "type only",
			code:    http.StatusInternalServerError,
			typ:     "urn:ietf:params:acme:error:serverInternal",
			wantSub: "serverInternal",
		},
		{
			name:    "non json body",
			code:    http.StatusBadGateway,
			wantSub: "upstream failure",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := newACMEStub(t, func(w http.ResponseWriter, r *http.Request) {
				problemDoc(w, tt.code, tt.typ, tt.detail)
			})

			_, err := stub.client.waitForStatus(context.Background(), stub.orderURL, "valid", 3)
			if err == nil {
				t.Fatalf("expected an error for HTTP %d", tt.code)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error = %q, want it to mention %q", err.Error(), tt.wantSub)
			}
			if !strings.Contains(err.Error(), fmt.Sprint(tt.code)) {
				t.Errorf("error = %q, want it to carry the HTTP status %d", err.Error(), tt.code)
			}

			// A non-success status is final: retrying cannot change it.
			if got := stub.polls(); got != 1 {
				t.Errorf("polled %d times; a hard HTTP %d must fail after one attempt", got, tt.code)
			}
		})
	}
}

// TestWaitForStatusStillRetriesOnPending is the control: a 200 with a
// non-terminal order status is genuinely still in progress, so the poll loop
// must keep retrying until the budget is spent.
func TestWaitForStatusStillRetriesOnPending(t *testing.T) {
	stub := newACMEStub(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "processing"})
	})

	_, err := stub.client.waitForStatus(context.Background(), stub.orderURL, "valid", 2)
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("error = %v, want a timeout after exhausting the budget", err)
	}
	if got := stub.polls(); got != 2 {
		t.Errorf("polled %d times, want the full budget of 2", got)
	}
}

// TestWaitForStatusSucceedsOnTargetStatus is the control for the success path:
// a 200 carrying the awaited status must return on the first poll.
func TestWaitForStatusSucceedsOnTargetStatus(t *testing.T) {
	stub := newACMEStub(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "valid"})
	})

	order, err := stub.client.waitForStatus(context.Background(), stub.orderURL, "valid", 3)
	if err != nil {
		t.Fatalf("waitForStatus: %v", err)
	}
	if order.Status != "valid" {
		t.Errorf("status = %q, want %q", order.Status, "valid")
	}
	if got := stub.polls(); got != 1 {
		t.Errorf("polled %d times, want exactly 1", got)
	}
}

// TestWaitForStatusAbortsOnInvalidOrder is the control for the terminal
// failure branch: a 200 whose order status is "invalid" must stop immediately.
func TestWaitForStatusAbortsOnInvalidOrder(t *testing.T) {
	stub := newACMEStub(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "invalid"})
	})

	_, err := stub.client.waitForStatus(context.Background(), stub.orderURL, "valid", 5)
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("error = %v, want an invalid-order error", err)
	}
	if got := stub.polls(); got != 1 {
		t.Errorf("polled %d times, want exactly 1", got)
	}
}

// TestACMEErrorFormatsProblemDocument covers the formatter directly, including
// the empty-body case where only the status is available.
func TestACMEErrorFormatsProblemDocument(t *testing.T) {
	tests := []struct {
		name    string
		code    int
		body    string
		wantSub string
	}{
		{name: "type and detail", code: 429, body: `{"type":"urn:x:rateLimited","detail":"slow down"}`, wantSub: "urn:x:rateLimited: slow down"},
		{name: "detail only", code: 400, body: `{"detail":"bad request"}`, wantSub: "bad request"},
		{name: "type only", code: 500, body: `{"type":"urn:x:serverInternal"}`, wantSub: "urn:x:serverInternal"},
		{name: "plain text body", code: 502, body: "upstream failure", wantSub: "upstream failure"},
		{name: "empty body", code: 503, body: "", wantSub: "acme error 503"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := acmeError(tt.code, []byte(tt.body))
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tt.wantSub)
			}
			if !strings.Contains(err.Error(), fmt.Sprint(tt.code)) {
				t.Errorf("error = %q, want it to carry status %d", err.Error(), tt.code)
			}
		})
	}
}
