package acme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

// --- Certificate request with mock ACME directory ---

func TestObtainCertificateWithMockACMEDirectory(t *testing.T) {
	// This test verifies the full ACME flow with a working mock that properly
	// signs the certificate using the CSR's public key.

	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	ca, _ := x509.ParseCertificate(caDER)

	var csrPubKey any
	nonceCount := 0
	finalized := false

	var mockServer *httptest.Server
	mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/directory":
			dir := Directory{
				NewNonce:   mockServer.URL + "/new-nonce",
				NewAccount: mockServer.URL + "/new-acct",
				NewOrder:   mockServer.URL + "/new-order",
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(dir)

		case "/new-nonce":
			nonceCount++
			w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-%d", nonceCount))
			w.WriteHeader(http.StatusOK)

		case "/new-acct":
			w.Header().Set("Location", mockServer.URL+"/acct/1")
			w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-acct-%d", nonceCount))
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"status":"valid"}`))

		case "/new-order":
			w.Header().Set("Location", mockServer.URL+"/order/1")
			w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-order-%d", nonceCount))
			w.WriteHeader(http.StatusCreated)
			order := map[string]any{
				"status":         "ready",
				"identifiers":    []map[string]string{{"type": "dns", "value": "test.com"}},
				"authorizations": []string{},
				"finalize":       mockServer.URL + "/finalize/1",
				"certificate":    mockServer.URL + "/cert/1",
			}
			json.NewEncoder(w).Encode(order)

		case "/order/1":
			w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-poll-%d", nonceCount))
			w.WriteHeader(http.StatusOK)
			status := "ready"
			if finalized {
				status = "valid"
			}
			order := map[string]any{
				"status":      status,
				"finalize":    mockServer.URL + "/finalize/1",
				"certificate": mockServer.URL + "/cert/1",
			}
			json.NewEncoder(w).Encode(order)

		case "/finalize/1":
			finalized = true
			var jws struct {
				Protected string `json:"protected"`
				Payload   string `json:"payload"`
				Signature string `json:"signature"`
			}
			json.NewDecoder(r.Body).Decode(&jws)
			var payload struct {
				CSR string `json:"csr"`
			}
			if decoded, err := b64urldec(jws.Payload); err == nil {
				json.Unmarshal(decoded, &payload)
				if csrBytes, err := b64urldec(payload.CSR); err == nil {
					if csr, err := x509.ParseCertificateRequest(csrBytes); err == nil {
						csrPubKey = csr.PublicKey
					}
				}
			}

			w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-fin-%d", nonceCount))
			w.WriteHeader(http.StatusOK)
			order := map[string]any{
				"status":      "valid",
				"certificate": mockServer.URL + "/cert/1",
			}
			json.NewEncoder(w).Encode(order)

		case "/cert/1":
			w.Header().Set("Content-Type", "application/pem-certificate-chain")
			w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-cert-%d", nonceCount))
			pubKey := csrPubKey
			if pubKey == nil {
				pubKey = &caKey.PublicKey
			}
			tmpl := &x509.Certificate{
				SerialNumber: big.NewInt(2),
				Subject:      pkix.Name{CommonName: "test.com"},
				DNSNames:     []string{"test.com"},
				NotBefore:    time.Now().Add(-time.Hour),
				NotAfter:     time.Now().Add(24 * time.Hour),
			}
			certDER, _ := x509.CreateCertificate(rand.Reader, tmpl, ca, pubKey, caKey)
			certPEMBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
			w.Write(certPEMBytes)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockServer.Close()

	log := logger.New("error", "text")
	c := NewClient(mockServer.URL+"/directory", t.TempDir(), log)

	cert, cpem, kpem, err := c.ObtainCertificate(context.Background(), []string{"test.com"})
	if err != nil {
		t.Fatalf("ObtainCertificate: %v", err)
	}
	if cert == nil {
		t.Fatal("cert should not be nil")
	}
	if len(cpem) == 0 {
		t.Error("certPEM should not be empty")
	}
	if len(kpem) == 0 {
		t.Error("keyPEM should not be empty")
	}
	if cert.Leaf == nil {
		t.Error("cert.Leaf should be populated")
	}
}

// --- Challenge handling (HTTP-01) ---

func TestHTTP01ChallengeHandling(t *testing.T) {
	log := logger.New("error", "text")
	c := NewClient("https://acme.example.com/directory", t.TempDir(), log)

	// Store a challenge token.
	c.httpTokens.Store("my-token-123", "my-token-123.thumbprint-value")

	// Verify the token is served correctly.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/.well-known/acme-challenge/my-token-123", nil)
	handled := c.HandleHTTPChallenge(rec, req)
	if !handled {
		t.Error("should handle ACME challenge")
	}
	if rec.Body.String() != "my-token-123.thumbprint-value" {
		t.Errorf("body = %q, want my-token-123.thumbprint-value", rec.Body.String())
	}

	// Non-matching token.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/.well-known/acme-challenge/wrong-token", nil)
	handled2 := c.HandleHTTPChallenge(rec2, req2)
	if !handled2 {
		t.Error("should still handle the ACME challenge path even for unknown token")
	}
	if rec2.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec2.Code)
	}

	// Non-ACME path.
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest("GET", "/normal/page", nil)
	handled3 := c.HandleHTTPChallenge(rec3, req3)
	if handled3 {
		t.Error("should not handle non-ACME path")
	}
}

// --- Error paths: directory fetch failure ---

func TestDirectoryFetchFailure(t *testing.T) {
	// Server that always returns 500.
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mockServer.Close()

	log := logger.New("error", "text")
	c := NewClient(mockServer.URL, t.TempDir(), log)

	err := c.ensureDirectory(context.Background())
	if err == nil {
		t.Error("expected error when directory returns 500")
	}
}

func TestDirectoryFetchConnectionRefused(t *testing.T) {
	log := logger.New("error", "text")
	// Use a port that no server is listening on.
	c := NewClient("http://127.0.0.1:1/directory", t.TempDir(), log)

	_, _, _, err := c.ObtainCertificate(context.Background(), []string{"example.com"})
	if err == nil {
		t.Error("expected error when directory is unreachable")
	}
}

// --- Error paths: account creation failure ---

func TestAccountCreationFailure(t *testing.T) {
	nonceCount := 0
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/new-nonce":
			nonceCount++
			w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-%d", nonceCount))
			w.WriteHeader(http.StatusOK)
		case "/new-acct":
			w.Header().Set("Replay-Nonce", fmt.Sprintf("reply-%d", nonceCount))
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"detail":"account creation forbidden"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockServer.Close()

	log := logger.New("error", "text")
	c := NewClient(mockServer.URL, t.TempDir(), log)
	c.directory = &Directory{
		NewNonce:   mockServer.URL + "/new-nonce",
		NewAccount: mockServer.URL + "/new-acct",
	}

	err := c.ensureAccount(context.Background())
	if err == nil {
		t.Error("expected error when account creation fails")
	}
}

// --- Key generation ---

func TestKeyGenerationAndPersistence(t *testing.T) {
	dir := t.TempDir()
	log := logger.New("error", "text")
	c := NewClient("https://acme.example.com/directory", dir, log)

	// Generate a new key.
	err := c.loadOrCreateAccountKey()
	if err != nil {
		t.Fatalf("loadOrCreateAccountKey: %v", err)
	}
	if c.accountKey == nil {
		t.Fatal("accountKey should not be nil")
	}

	// Verify key file was written.
	keyPath := filepath.Join(dir, "account.key")
	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatal("failed to decode PEM from key file")
	}
	if block.Type != "EC PRIVATE KEY" {
		t.Errorf("PEM type = %q, want EC PRIVATE KEY", block.Type)
	}

	parsedKey, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("ParseECPrivateKey: %v", err)
	}
	if parsedKey.Curve != elliptic.P256() {
		t.Error("curve should be P-256")
	}
}

// --- CSR generation ---

func TestCSRGeneration(t *testing.T) {
	// Generate a key for CSR.
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	domains := []string{"example.com", "www.example.com"}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		DNSNames: domains,
	}, key)
	if err != nil {
		t.Fatalf("CreateCertificateRequest: %v", err)
	}

	// Parse the CSR to verify.
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		t.Fatalf("ParseCertificateRequest: %v", err)
	}

	if len(csr.DNSNames) != 2 {
		t.Errorf("DNSNames count = %d, want 2", len(csr.DNSNames))
	}
	if csr.DNSNames[0] != "example.com" {
		t.Errorf("DNSNames[0] = %q, want example.com", csr.DNSNames[0])
	}
	if csr.DNSNames[1] != "www.example.com" {
		t.Errorf("DNSNames[1] = %q, want www.example.com", csr.DNSNames[1])
	}
}

// --- LoadOrCreateAccountKey with existing valid key ---

func TestLoadOrCreateAccountKeyExisting(t *testing.T) {
	dir := t.TempDir()
	log := logger.New("error", "text")

	// Generate and save a key.
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	keyPEM := encodeECKey(key)
	keyPath := filepath.Join(dir, "account.key")
	os.WriteFile(keyPath, keyPEM, 0600)

	// Load it.
	c := NewClient("https://acme.example.com/directory", dir, log)
	err := c.loadOrCreateAccountKey()
	if err != nil {
		t.Fatalf("loadOrCreateAccountKey: %v", err)
	}
	if c.accountKey == nil {
		t.Fatal("accountKey should be set")
	}

	// Verify it's the same key.
	if c.accountKey.PublicKey.X.Cmp(key.PublicKey.X) != 0 {
		t.Error("loaded key should match the saved key")
	}
}

// --- ObtainCertificate with account creation error ---

func TestObtainCertificateAccountError(t *testing.T) {
	nonceCount := 0
	var mockServer *httptest.Server
	mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/directory":
			dir := Directory{
				NewNonce:   mockServer.URL + "/new-nonce",
				NewAccount: mockServer.URL + "/new-acct",
				NewOrder:   mockServer.URL + "/new-order",
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(dir)
		case "/new-nonce":
			nonceCount++
			w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-%d", nonceCount))
			w.WriteHeader(http.StatusOK)
		case "/new-acct":
			w.Header().Set("Replay-Nonce", fmt.Sprintf("reply-%d", nonceCount))
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"detail":"server error"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockServer.Close()

	log := logger.New("error", "text")
	c := NewClient(mockServer.URL+"/directory", t.TempDir(), log)

	_, _, _, err := c.ObtainCertificate(context.Background(), []string{"example.com"})
	if err == nil {
		t.Error("expected error when account creation fails")
	}
}

// --- WaitForStatus timeout ---

func TestWaitForStatusTimeout(t *testing.T) {
	nonceCount := 0
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/new-nonce":
			nonceCount++
			w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-%d", nonceCount))
			w.WriteHeader(http.StatusOK)
		case "/order/1":
			w.Header().Set("Replay-Nonce", fmt.Sprintf("reply-%d", nonceCount))
			w.WriteHeader(http.StatusOK)
			// Always return "pending" so it never reaches target status.
			json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockServer.Close()

	log := logger.New("error", "text")
	c := NewClient(mockServer.URL, t.TempDir(), log)

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	c.accountKey = key
	c.accountURL = "https://acme.example.com/acct/1"
	c.directory = &Directory{
		NewNonce: mockServer.URL + "/new-nonce",
	}

	_, err := c.waitForStatus(context.Background(), mockServer.URL+"/order/1", "ready", 1)
	if err == nil {
		t.Error("expected timeout error")
	}
}

// --- EnsureDirectory caches result ---

func TestEnsureDirectoryCache(t *testing.T) {
	callCount := 0
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		dir := Directory{
			NewNonce:   "https://acme.example.com/new-nonce",
			NewAccount: "https://acme.example.com/new-acct",
			NewOrder:   "https://acme.example.com/new-order",
		}
		json.NewEncoder(w).Encode(dir)
	}))
	defer mockServer.Close()

	log := logger.New("error", "text")
	c := NewClient(mockServer.URL, t.TempDir(), log)

	// First call fetches from server.
	if err := c.ensureDirectory(context.Background()); err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Second call should use cache.
	if err := c.ensureDirectory(context.Background()); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected 1 server call, got %d", callCount)
	}
}

// --- EnsureAccount caches result ---

func TestEnsureAccountCache(t *testing.T) {
	nonceCount := 0
	acctCalls := 0
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/new-nonce":
			nonceCount++
			w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-%d", nonceCount))
			w.WriteHeader(http.StatusOK)
		case "/new-acct":
			acctCalls++
			w.Header().Set("Location", "https://acme.example.com/acct/1")
			w.Header().Set("Replay-Nonce", fmt.Sprintf("reply-%d", nonceCount))
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"status":"valid"}`))
		}
	}))
	defer mockServer.Close()

	log := logger.New("error", "text")
	c := NewClient(mockServer.URL, t.TempDir(), log)
	c.directory = &Directory{
		NewNonce:   mockServer.URL + "/new-nonce",
		NewAccount: mockServer.URL + "/new-acct",
	}

	// First call creates account.
	if err := c.ensureAccount(context.Background()); err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Second call should use cache.
	if err := c.ensureAccount(context.Background()); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if acctCalls != 1 {
		t.Errorf("expected 1 account call, got %d", acctCalls)
	}
}

// --- Nonce pool overflow (more than 10 nonces) ---

func TestNoncePoolOverflow(t *testing.T) {
	pool := &noncePool{}

	// Put more than 10 nonces.
	for i := 0; i < 15; i++ {
		pool.put(fmt.Sprintf("nonce-%d", i))
	}

	// Pool should have at most 10.
	pool.mu.Lock()
	count := len(pool.nonces)
	pool.mu.Unlock()

	if count > 10 {
		t.Errorf("nonce pool size = %d, should be capped at 10", count)
	}
}
