package acme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type nonceLifecycleTransport func(*http.Request) (*http.Response, error)

func (f nonceLifecycleTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestNonceRequestFollowsParentCancellation(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fresh := func(rt nonceLifecycleTransport) *Client {
		return &Client{accountKey: key, directory: &Directory{NewNonce: "https://ca.test/nonce"}, nonces: &noncePool{}, httpClient: &http.Client{Transport: rt}}
	}
	response := func(code int) *http.Response {
		return &http.Response{StatusCode: code, Header: http.Header{"Replay-Nonce": []string{"nonce"}}, Body: io.NopCloser(strings.NewReader(""))}
	}
	heads := 0
	c := fresh(func(r *http.Request) (*http.Response, error) {
		if r.Method == "HEAD" {
			heads++
		}
		return response(200), nil
	})
	for range 2 {
		res, err := c.signedRequestOnce(context.Background(), "https://ca.test/order", nil)
		if err != nil {
			t.Fatal("active control", err)
		}
		res.Body.Close()
	}
	if heads != 1 {
		t.Fatal("cached nonce not used", heads)
	}
	arrived, done := make(chan struct{}), make(chan error, 1)
	c = fresh(func(r *http.Request) (*http.Response, error) {
		if r.Method != "HEAD" {
			return nil, errors.New("POST after canceled nonce")
		}
		close(arrived)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		res, err := c.signedRequestOnce(ctx, "https://ca.test/order", nil)
		if res != nil {
			res.Body.Close()
		}
		done <- err
	}()
	select {
	case <-arrived:
	case <-time.After(5 * time.Second):
		t.Fatal("nonce gate not reached")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("cancel not propagated", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nonce did not observe cancellation")
	}
	calls := 0
	c = fresh(func(*http.Request) (*http.Response, error) { calls++; return response(200), nil })
	c.nonces.put("cached")
	_, err = c.signedRequestOnce(ctx, "https://ca.test/order", nil)
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal("already-canceled cached request", err, calls)
	}
	c = fresh(func(*http.Request) (*http.Response, error) { return response(503), nil })
	if _, err = c.signedRequestOnce(context.Background(), "https://ca.test/order", nil); err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatal("nonce server failure hidden", err)
	}
}
