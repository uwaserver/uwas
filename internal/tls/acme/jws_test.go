package acme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/logger"
)

func TestBase64url(t *testing.T) {
	tests := []struct {
		input []byte
		want  string
	}{
		{[]byte{}, ""},
		{[]byte{0}, "AA"},
		{[]byte{0xFF, 0xFF}, "__8"},
	}
	for _, tt := range tests {
		got := base64url(tt.input)
		if got != tt.want {
			t.Errorf("base64url(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestPadTo(t *testing.T) {
	b := []byte{1, 2, 3}
	padded := padTo(b, 5)
	if len(padded) != 5 {
		t.Errorf("len = %d, want 5", len(padded))
	}
	if padded[0] != 0 || padded[1] != 0 || padded[2] != 1 {
		t.Errorf("padding wrong: %v", padded)
	}

	// No padding needed
	exact := padTo(b, 3)
	if len(exact) != 3 {
		t.Errorf("len = %d, want 3", len(exact))
	}
}

func TestEcdsaJWK(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	jwk := ecdsaJWK(&key.PublicKey)

	if jwk["kty"] != "EC" {
		t.Errorf("kty = %q, want EC", jwk["kty"])
	}
	if jwk["crv"] != "P-256" {
		t.Errorf("crv = %q, want P-256", jwk["crv"])
	}
	if jwk["x"] == "" {
		t.Error("x should not be empty")
	}
	if jwk["y"] == "" {
		t.Error("y should not be empty")
	}
}

func TestJWKThumbprint(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	tp1, err := jwkThumbprint(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if tp1 == "" {
		t.Error("thumbprint should not be empty")
	}

	// Same key should produce same thumbprint
	tp2, _ := jwkThumbprint(&key.PublicKey)
	if tp1 != tp2 {
		t.Error("same key should produce same thumbprint")
	}

	// Different key should produce different thumbprint
	key2, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tp3, _ := jwkThumbprint(&key2.PublicKey)
	if tp1 == tp3 {
		t.Error("different keys should produce different thumbprints")
	}
}

func TestMustJSON(t *testing.T) {
	result := mustJSON(map[string]string{"key": "value"})
	if string(result) != `{"key":"value"}` {
		t.Errorf("mustJSON = %s", result)
	}
}

func TestECDSASignVerify(t *testing.T) {
	// Verify our JWS signing produces valid ECDSA signatures
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	message := []byte("test message")
	hash := sha256.Sum256(message)

	r, s, err := ecdsa.Sign(rand.Reader, key, hash[:])
	if err != nil {
		t.Fatal(err)
	}

	if !ecdsa.Verify(&key.PublicKey, hash[:], r, s) {
		t.Error("signature verification failed")
	}
}

// --- Mock ACME server tests ---

func TestEncodeECKey(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	pemBytes := encodeECKey(key)
	if len(pemBytes) == 0 {
		t.Fatal("encodeECKey returned empty")
	}

	block, _ := pem.Decode(pemBytes)
	if block == nil {
		t.Fatal("failed to decode PEM")
	}
	if block.Type != "EC PRIVATE KEY" {
		t.Errorf("type = %q, want EC PRIVATE KEY", block.Type)
	}

	parsed, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("ParseECPrivateKey: %v", err)
	}
	if parsed.Curve != elliptic.P256() {
		t.Error("curve should be P-256")
	}
}

func TestNoncePoolPutGet(t *testing.T) {
	pool := &noncePool{}

	// Put a nonce, then get it back
	pool.put("nonce-1")
	pool.put("nonce-2")

	// Should return the last one pushed (stack behavior)
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Replay-Nonce", "server-nonce")
		w.WriteHeader(http.StatusOK)
	}))
	defer mockServer.Close()

	nonce, err := pool.get(&http.Client{}, mockServer.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if nonce != "nonce-2" {
		t.Errorf("nonce = %q, want nonce-2", nonce)
	}

	nonce, err = pool.get(&http.Client{}, mockServer.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if nonce != "nonce-1" {
		t.Errorf("nonce = %q, want nonce-1", nonce)
	}
}

func TestNoncePoolGetFromServer(t *testing.T) {
	pool := &noncePool{}

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Replay-Nonce", "fresh-nonce")
		w.WriteHeader(http.StatusOK)
	}))
	defer mockServer.Close()

	// Empty pool, should fetch from server
	nonce, err := pool.get(&http.Client{}, mockServer.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if nonce != "fresh-nonce" {
		t.Errorf("nonce = %q, want fresh-nonce", nonce)
	}
}

func TestNoncePoolGetNoNonceInResponse(t *testing.T) {
	pool := &noncePool{}

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No Replay-Nonce header
		w.WriteHeader(http.StatusOK)
	}))
	defer mockServer.Close()

	_, err := pool.get(&http.Client{}, mockServer.URL)
	if err == nil {
		t.Error("expected error when no nonce in response")
	}
}

func TestHandleHTTPChallengeWithToken(t *testing.T) {
	log := logger.New("error", "text")
	c := NewClient("https://example.com/directory", t.TempDir(), log)

	// Store a token
	c.httpTokens.Store("test-token", "test-token.thumbprint")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/.well-known/acme-challenge/test-token", nil)

	handled := c.HandleHTTPChallenge(rec, req)
	if !handled {
		t.Error("should handle ACME challenge")
	}
	if rec.Body.String() != "test-token.thumbprint" {
		t.Errorf("body = %q, want test-token.thumbprint", rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "application/octet-stream" {
		t.Errorf("Content-Type = %q", rec.Header().Get("Content-Type"))
	}
}

func TestHandleHTTPChallengeWithoutToken(t *testing.T) {
	log := logger.New("error", "text")
	c := NewClient("https://example.com/directory", t.TempDir(), log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/.well-known/acme-challenge/unknown-token", nil)

	handled := c.HandleHTTPChallenge(rec, req)
	if !handled {
		t.Error("should handle ACME challenge path")
	}
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHandleHTTPChallengeNonChallengePath(t *testing.T) {
	log := logger.New("error", "text")
	c := NewClient("https://example.com/directory", t.TempDir(), log)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/some/other/path", nil)

	handled := c.HandleHTTPChallenge(rec, req)
	if handled {
		t.Error("should not handle non-challenge path")
	}
}

func TestEnsureDirectory(t *testing.T) {
	// Mock ACME directory server
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dir := Directory{
			NewNonce:   "https://acme.example.com/new-nonce",
			NewAccount: "https://acme.example.com/new-acct",
			NewOrder:   "https://acme.example.com/new-order",
			RevokeCert: "https://acme.example.com/revoke",
			KeyChange:  "https://acme.example.com/key-change",
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(dir)
	}))
	defer mockServer.Close()

	log := logger.New("error", "text")
	c := NewClient(mockServer.URL, t.TempDir(), log)

	err := c.ensureDirectory(context.Background())
	if err != nil {
		t.Fatalf("ensureDirectory: %v", err)
	}
	if c.directory == nil {
		t.Fatal("directory should be set")
	}
	if c.directory.NewNonce != "https://acme.example.com/new-nonce" {
		t.Errorf("NewNonce = %q", c.directory.NewNonce)
	}

	// Calling again should be a no-op (cached)
	err = c.ensureDirectory(context.Background())
	if err != nil {
		t.Fatalf("ensureDirectory second call: %v", err)
	}
}

func TestEnsureDirectoryBadStatus(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mockServer.Close()

	log := logger.New("error", "text")
	c := NewClient(mockServer.URL, t.TempDir(), log)

	err := c.ensureDirectory(context.Background())
	if err == nil {
		t.Error("expected error for bad status")
	}
}

func TestSignedRequest(t *testing.T) {
	// Create a mock server that provides nonces and accepts signed requests
	nonceCount := 0
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/new-nonce":
			nonceCount++
			w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-%d", nonceCount))
			w.WriteHeader(http.StatusOK)
		case "/test-endpoint":
			// Verify it's a JWS POST
			if r.Header.Get("Content-Type") != "application/jose+json" {
				t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
			}
			w.Header().Set("Replay-Nonce", "reply-nonce")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`))
		}
	}))
	defer mockServer.Close()

	log := logger.New("error", "text")
	c := NewClient(mockServer.URL, t.TempDir(), log)

	// Set up required fields
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	c.accountKey = key
	c.directory = &Directory{
		NewNonce: mockServer.URL + "/new-nonce",
	}

	// Test with payload (no accountURL, so JWK header is used)
	resp, err := c.signedRequest(context.Background(), mockServer.URL+"/test-endpoint", map[string]string{"test": "value"})
	if err != nil {
		t.Fatalf("signedRequest: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestSignedRequestWithAccountURL(t *testing.T) {
	nonceCount := 0
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/new-nonce":
			nonceCount++
			w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-%d", nonceCount))
			w.WriteHeader(http.StatusOK)
		case "/test-endpoint":
			w.Header().Set("Replay-Nonce", "reply-nonce")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`))
		}
	}))
	defer mockServer.Close()

	log := logger.New("error", "text")
	c := NewClient(mockServer.URL, t.TempDir(), log)

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	c.accountKey = key
	c.accountURL = "https://acme.example.com/acct/123"
	c.directory = &Directory{
		NewNonce: mockServer.URL + "/new-nonce",
	}

	// Test POST-as-GET (nil payload)
	resp, err := c.signedRequest(context.Background(), mockServer.URL+"/test-endpoint", nil)
	if err != nil {
		t.Fatalf("signedRequest: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestSignedRequestReplayNonceSaved(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/new-nonce" {
			w.Header().Set("Replay-Nonce", "initial-nonce")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Replay-Nonce", "saved-nonce")
		w.WriteHeader(http.StatusOK)
	}))
	defer mockServer.Close()

	log := logger.New("error", "text")
	c := NewClient(mockServer.URL, t.TempDir(), log)

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	c.accountKey = key
	c.directory = &Directory{
		NewNonce: mockServer.URL + "/new-nonce",
	}

	resp, err := c.signedRequest(context.Background(), mockServer.URL+"/endpoint", map[string]string{})
	if err != nil {
		t.Fatalf("signedRequest: %v", err)
	}
	resp.Body.Close()

	// The reply nonce should have been saved to the pool
	// Next call should use the saved nonce rather than fetching a new one
	nonceFetched := false
	mockServer2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/new-nonce" {
			nonceFetched = true
		}
		w.Header().Set("Replay-Nonce", "another-nonce")
		w.WriteHeader(http.StatusOK)
	}))
	defer mockServer2.Close()

	// The nonce pool should already have "saved-nonce"
	nonce, err := c.nonces.get(&http.Client{}, mockServer2.URL+"/new-nonce")
	if err != nil {
		t.Fatalf("get nonce: %v", err)
	}
	if nonce != "saved-nonce" {
		t.Errorf("nonce = %q, want saved-nonce", nonce)
	}
	if nonceFetched {
		t.Error("should have used cached nonce, not fetched a new one")
	}
}

func TestNewClient(t *testing.T) {
	log := logger.New("error", "text")
	c := NewClient("https://acme.example.com/directory", "/tmp/test-storage", log)

	if c.directoryURL != "https://acme.example.com/directory" {
		t.Errorf("directoryURL = %q", c.directoryURL)
	}
	if c.storageDir != "/tmp/test-storage" {
		t.Errorf("storageDir = %q", c.storageDir)
	}
	if c.nonces == nil {
		t.Error("nonces pool should be initialized")
	}
	if c.httpClient == nil {
		t.Error("httpClient should be initialized")
	}
}

func TestNoncePoolConcurrentPutGet(t *testing.T) {
	pool := &noncePool{}

	// Put multiple nonces
	for i := 0; i < 10; i++ {
		pool.put(fmt.Sprintf("nonce-%d", i))
	}

	// Verify we can get them all back
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Replay-Nonce", "server-nonce")
		w.WriteHeader(http.StatusOK)
	}))
	defer mockServer.Close()

	for i := 0; i < 10; i++ {
		nonce, err := pool.get(&http.Client{}, mockServer.URL)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if !strings.HasPrefix(nonce, "nonce-") {
			t.Errorf("unexpected nonce: %q", nonce)
		}
	}
}
