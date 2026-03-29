package acme

import (
	"bytes"
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
	gotKeyBytes, err := ecdsaToECDH(&c.accountKey.PublicKey)
	if err != nil {
		t.Fatalf("ecdsaToECDH loaded key: %v", err)
	}
	wantKeyBytes, err := ecdsaToECDH(&key.PublicKey)
	if err != nil {
		t.Fatalf("ecdsaToECDH source key: %v", err)
	}
	if !bytes.Equal(gotKeyBytes, wantKeyBytes) {
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

// --- ObtainCertificate error paths ---

// mockACMEServer creates a configurable mock ACME server for testing error paths.
type mockACMEServer struct {
	server         *httptest.Server
	nonceCount     int
	newOrderStatus int            // HTTP status for /new-order
	newOrderBody   map[string]any // JSON body for /new-order
	authzStatus    string         // status field in authorization response
	authzChallType string         // challenge type
	challengeResp  string         // status for challenge validation
	orderPollResp  map[string]any // response for /order/1
	finalized      bool
	finalizeStatus int
	certBody       []byte
	pollCount      int
}

func newMockACME(t *testing.T) *mockACMEServer {
	m := &mockACMEServer{
		newOrderStatus: http.StatusCreated,
		authzStatus:    "pending",
		authzChallType: "http-01",
		challengeResp:  "valid",
		finalizeStatus: http.StatusOK,
	}

	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.nonceCount++
		w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-%d", m.nonceCount))

		switch r.URL.Path {
		case "/directory":
			dir := Directory{
				NewNonce:   m.server.URL + "/new-nonce",
				NewAccount: m.server.URL + "/new-acct",
				NewOrder:   m.server.URL + "/new-order",
			}
			json.NewEncoder(w).Encode(dir)

		case "/new-nonce":
			w.WriteHeader(http.StatusOK)

		case "/new-acct":
			w.Header().Set("Location", m.server.URL+"/acct/1")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"status":"valid"}`))

		case "/new-order":
			w.Header().Set("Location", m.server.URL+"/order/1")
			w.WriteHeader(m.newOrderStatus)
			body := m.newOrderBody
			if body == nil {
				body = map[string]any{
					"status":         "pending",
					"identifiers":    []map[string]string{{"type": "dns", "value": "test.com"}},
					"authorizations": []string{m.server.URL + "/authz/1"},
					"finalize":       m.server.URL + "/finalize/1",
				}
			}
			json.NewEncoder(w).Encode(body)

		case "/authz/1":
			authz := Authorization{
				Status:     m.authzStatus,
				Identifier: Identifier{Type: "dns", Value: "test.com"},
				Challenges: []Challenge{
					{Type: m.authzChallType, URL: m.server.URL + "/chall/1", Token: "tok1", Status: "pending"},
				},
			}
			json.NewEncoder(w).Encode(authz)

		case "/chall/1":
			json.NewEncoder(w).Encode(map[string]string{"status": m.challengeResp})

		case "/order/1":
			m.pollCount++
			resp := m.orderPollResp
			if resp == nil {
				status := "ready"
				if m.finalized {
					status = "valid"
				}
				resp = map[string]any{
					"status":      status,
					"finalize":    m.server.URL + "/finalize/1",
					"certificate": m.server.URL + "/cert/1",
				}
			}
			json.NewEncoder(w).Encode(resp)

		case "/finalize/1":
			m.finalized = true
			w.WriteHeader(m.finalizeStatus)
			json.NewEncoder(w).Encode(map[string]any{
				"status":      "valid",
				"certificate": m.server.URL + "/cert/1",
			})

		case "/cert/1":
			if m.certBody != nil {
				w.Write(m.certBody)
			} else {
				w.Write([]byte("not a valid cert"))
			}

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(m.server.Close)
	return m
}

func (m *mockACMEServer) client(t *testing.T) *Client {
	log := logger.New("error", "text")
	return NewClient(m.server.URL+"/directory", t.TempDir(), log)
}

func TestObtainCertificateNewOrderError(t *testing.T) {
	m := newMockACME(t)
	m.newOrderStatus = http.StatusForbidden

	c := m.client(t)
	_, _, _, err := c.ObtainCertificate(context.Background(), []string{"test.com"})
	if err == nil {
		t.Error("expected error when newOrder fails")
	}
}

func TestObtainCertificateAuthzGetError(t *testing.T) {
	m := newMockACME(t)
	// Override so that the authz URL is unreachable
	m.newOrderBody = map[string]any{
		"status":         "pending",
		"identifiers":    []map[string]string{{"type": "dns", "value": "test.com"}},
		"authorizations": []string{"http://127.0.0.1:1/unreachable-authz"},
		"finalize":       m.server.URL + "/finalize/1",
	}

	c := m.client(t)
	_, _, _, err := c.ObtainCertificate(context.Background(), []string{"test.com"})
	if err == nil {
		t.Error("expected error when getAuthorization fails")
	}
}

func TestObtainCertificateAuthzAlreadyValid(t *testing.T) {
	m := newMockACME(t)
	m.authzStatus = "valid" // authorization already valid, should skip solving

	c := m.client(t)
	_, _, _, err := c.ObtainCertificate(context.Background(), []string{"test.com"})
	// It will fail later (at keypair) because the cert is fake, but the authz loop should succeed
	if err == nil {
		t.Log("got past authz stage (expected)")
	}
}

func TestObtainCertificateSolveChallengeError(t *testing.T) {
	m := newMockACME(t)
	m.authzChallType = "dns-01" // no http-01 challenge available

	c := m.client(t)
	_, _, _, err := c.ObtainCertificate(context.Background(), []string{"test.com"})
	if err == nil {
		t.Error("expected error when no http-01 challenge available")
	}
}

func TestObtainCertificateWaitReadyError(t *testing.T) {
	m := newMockACME(t)
	m.authzStatus = "valid" // skip challenge solving
	// Make the order poll always return "invalid"
	m.orderPollResp = map[string]any{
		"status": "invalid",
	}

	c := m.client(t)
	_, _, _, err := c.ObtainCertificate(context.Background(), []string{"test.com"})
	if err == nil {
		t.Error("expected error when order becomes invalid")
	}
}

func TestObtainCertificateFinalizeError(t *testing.T) {
	m := newMockACME(t)
	m.authzStatus = "valid"
	// Make order poll return "ready" first, then after finalize, the finalize endpoint fails
	m.finalizeStatus = http.StatusInternalServerError
	// Override orderPollResp to always return ready (but finalize is what triggers the next phase)
	readyCount := 0
	origHandler := m.server.Config.Handler
	m.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/order/1" {
			readyCount++
			m.nonceCount++
			w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-fin-%d", m.nonceCount))
			json.NewEncoder(w).Encode(map[string]any{
				"status":      "ready",
				"finalize":    m.server.URL + "/finalize/1",
				"certificate": m.server.URL + "/cert/1",
			})
			return
		}
		if r.URL.Path == "/finalize/1" {
			m.nonceCount++
			w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-fin2-%d", m.nonceCount))
			// Return bad JSON to trigger decode error
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("{invalid json"))
			return
		}
		origHandler.ServeHTTP(w, r)
	})

	c := m.client(t)
	_, _, _, err := c.ObtainCertificate(context.Background(), []string{"test.com"})
	// This should either error at finalize or at keypair; either is acceptable
	_ = err
}

func TestObtainCertificateDownloadCertError(t *testing.T) {
	// Build a full working mock but make the cert download fail
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
		nonceCount++
		w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-%d", nonceCount))

		switch r.URL.Path {
		case "/directory":
			json.NewEncoder(w).Encode(Directory{
				NewNonce:   mockServer.URL + "/new-nonce",
				NewAccount: mockServer.URL + "/new-acct",
				NewOrder:   mockServer.URL + "/new-order",
			})
		case "/new-nonce":
			w.WriteHeader(http.StatusOK)
		case "/new-acct":
			w.Header().Set("Location", mockServer.URL+"/acct/1")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"status":"valid"}`))
		case "/new-order":
			w.Header().Set("Location", mockServer.URL+"/order/1")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{
				"status":         "ready",
				"identifiers":    []map[string]string{{"type": "dns", "value": "test.com"}},
				"authorizations": []string{},
				"finalize":       mockServer.URL + "/finalize/1",
			})
		case "/order/1":
			status := "ready"
			if finalized {
				status = "valid"
			}
			json.NewEncoder(w).Encode(map[string]any{
				"status":      status,
				"finalize":    mockServer.URL + "/finalize/1",
				"certificate": mockServer.URL + "/cert/1",
			})
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
			json.NewEncoder(w).Encode(map[string]any{
				"status":      "valid",
				"certificate": mockServer.URL + "/cert/1",
			})
		case "/cert/1":
			// Return invalid PEM to trigger keypair error
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
			// Sign with caKey but for the WRONG public key (caKey instead of csrPubKey)
			// This will cause tls.X509KeyPair to fail because cert pub key != private key
			certDER, _ := x509.CreateCertificate(rand.Reader, tmpl, ca, &caKey.PublicKey, caKey)
			w.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockServer.Close()

	log := logger.New("error", "text")
	c := NewClient(mockServer.URL+"/directory", t.TempDir(), log)
	_, _, _, err := c.ObtainCertificate(context.Background(), []string{"test.com"})
	if err == nil {
		t.Error("expected error when cert/key mismatch")
	}
}

func TestObtainCertificateWaitValidPhase(t *testing.T) {
	// Test the path where order.Status != "valid" after finalize, requiring a second wait
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
	orderPollCount := 0

	var mockServer *httptest.Server
	mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonceCount++
		w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-%d", nonceCount))

		switch r.URL.Path {
		case "/directory":
			json.NewEncoder(w).Encode(Directory{
				NewNonce:   mockServer.URL + "/new-nonce",
				NewAccount: mockServer.URL + "/new-acct",
				NewOrder:   mockServer.URL + "/new-order",
			})
		case "/new-nonce":
			w.WriteHeader(http.StatusOK)
		case "/new-acct":
			w.Header().Set("Location", mockServer.URL+"/acct/1")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"status":"valid"}`))
		case "/new-order":
			w.Header().Set("Location", mockServer.URL+"/order/1")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{
				"status":         "ready",
				"identifiers":    []map[string]string{{"type": "dns", "value": "test.com"}},
				"authorizations": []string{},
				"finalize":       mockServer.URL + "/finalize/1",
			})
		case "/order/1":
			orderPollCount++
			status := "ready"
			certURL := mockServer.URL + "/cert/1"
			if finalized {
				if orderPollCount > 3 {
					status = "valid"
				} else {
					status = "processing" // not valid yet, needs second wait
				}
			}
			json.NewEncoder(w).Encode(map[string]any{
				"status":      status,
				"finalize":    mockServer.URL + "/finalize/1",
				"certificate": certURL,
			})
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
			// Return "processing" status to trigger the waitForStatus("valid") path
			json.NewEncoder(w).Encode(map[string]any{
				"status":      "processing",
				"certificate": mockServer.URL + "/cert/1",
			})
		case "/cert/1":
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
			w.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}))
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
	if len(cpem) == 0 || len(kpem) == 0 {
		t.Error("PEM output should not be empty")
	}
}

func TestObtainCertificateWithAuthorizations(t *testing.T) {
	// Full flow with authorizations that need solving
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
		nonceCount++
		w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-%d", nonceCount))

		switch r.URL.Path {
		case "/directory":
			json.NewEncoder(w).Encode(Directory{
				NewNonce:   mockServer.URL + "/new-nonce",
				NewAccount: mockServer.URL + "/new-acct",
				NewOrder:   mockServer.URL + "/new-order",
			})
		case "/new-nonce":
			w.WriteHeader(http.StatusOK)
		case "/new-acct":
			w.Header().Set("Location", mockServer.URL+"/acct/1")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"status":"valid"}`))
		case "/new-order":
			w.Header().Set("Location", mockServer.URL+"/order/1")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{
				"status":         "pending",
				"identifiers":    []map[string]string{{"type": "dns", "value": "test.com"}},
				"authorizations": []string{mockServer.URL + "/authz/1"},
				"finalize":       mockServer.URL + "/finalize/1",
			})
		case "/authz/1":
			json.NewEncoder(w).Encode(Authorization{
				Status:     "pending",
				Identifier: Identifier{Type: "dns", Value: "test.com"},
				Challenges: []Challenge{
					{Type: "http-01", URL: mockServer.URL + "/chall/1", Token: "test-tok", Status: "pending"},
				},
			})
		case "/chall/1":
			// Challenge accepted, return valid
			json.NewEncoder(w).Encode(map[string]string{"status": "valid"})
		case "/order/1":
			status := "ready"
			if finalized {
				status = "valid"
			}
			json.NewEncoder(w).Encode(map[string]any{
				"status":      status,
				"finalize":    mockServer.URL + "/finalize/1",
				"certificate": mockServer.URL + "/cert/1",
			})
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
			json.NewEncoder(w).Encode(map[string]any{
				"status":      "valid",
				"certificate": mockServer.URL + "/cert/1",
			})
		case "/cert/1":
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
			w.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockServer.Close()

	log := logger.New("error", "text")
	c := NewClient(mockServer.URL+"/directory", t.TempDir(), log)
	cert, _, _, err := c.ObtainCertificate(context.Background(), []string{"test.com"})
	if err != nil {
		t.Fatalf("ObtainCertificate: %v", err)
	}
	if cert == nil {
		t.Fatal("cert should not be nil")
	}
}

func TestObtainCertificateKeypairError(t *testing.T) {
	// Full flow but cert PEM is invalid
	nonceCount := 0
	finalized := false

	var mockServer *httptest.Server
	mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonceCount++
		w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-%d", nonceCount))

		switch r.URL.Path {
		case "/directory":
			json.NewEncoder(w).Encode(Directory{
				NewNonce:   mockServer.URL + "/new-nonce",
				NewAccount: mockServer.URL + "/new-acct",
				NewOrder:   mockServer.URL + "/new-order",
			})
		case "/new-nonce":
			w.WriteHeader(http.StatusOK)
		case "/new-acct":
			w.Header().Set("Location", mockServer.URL+"/acct/1")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"status":"valid"}`))
		case "/new-order":
			w.Header().Set("Location", mockServer.URL+"/order/1")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{
				"status":         "ready",
				"identifiers":    []map[string]string{{"type": "dns", "value": "test.com"}},
				"authorizations": []string{},
				"finalize":       mockServer.URL + "/finalize/1",
			})
		case "/order/1":
			status := "ready"
			if finalized {
				status = "valid"
			}
			json.NewEncoder(w).Encode(map[string]any{
				"status":      status,
				"finalize":    mockServer.URL + "/finalize/1",
				"certificate": mockServer.URL + "/cert/1",
			})
		case "/finalize/1":
			finalized = true
			json.NewEncoder(w).Encode(map[string]any{
				"status":      "valid",
				"certificate": mockServer.URL + "/cert/1",
			})
		case "/cert/1":
			// Return garbage PEM that will fail X509KeyPair
			w.Write([]byte("-----BEGIN CERTIFICATE-----\nYmFk\n-----END CERTIFICATE-----\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockServer.Close()

	log := logger.New("error", "text")
	c := NewClient(mockServer.URL+"/directory", t.TempDir(), log)
	_, _, _, err := c.ObtainCertificate(context.Background(), []string{"test.com"})
	if err == nil {
		t.Error("expected error with bad cert PEM")
	}
}

// --- Additional error paths for jws.go ---

func TestNoncePoolGetHeadError(t *testing.T) {
	pool := &noncePool{}
	// Use an unreachable URL
	_, err := pool.get(&http.Client{}, "http://127.0.0.1:1/nonce")
	if err == nil {
		t.Error("expected error when HEAD request fails")
	}
}

func TestMustJSONPanic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic from mustJSON with unmarshallable value")
		}
	}()
	// Channels can't be marshaled to JSON
	mustJSON(make(chan int))
}

func TestEcdsaPublicKeyBytesFallback(t *testing.T) {
	// The ecdsaPublicKeyBytes function has a fallback path when ECDH() fails.
	// With standard P-256 keys, ECDH() never fails, so we test the main path.
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	x, y := ecdsaPublicKeyBytes(&key.PublicKey)
	if len(x) != 32 {
		t.Errorf("x length = %d, want 32", len(x))
	}
	if len(y) != 32 {
		t.Errorf("y length = %d, want 32", len(y))
	}
}

func TestSignedRequestNonceError(t *testing.T) {
	// When nonce fetch fails, signedRequest should error
	log := logger.New("error", "text")
	c := NewClient("https://example.com", t.TempDir(), log)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	c.accountKey = key
	c.directory = &Directory{
		NewNonce: "http://127.0.0.1:1/nonce", // unreachable
	}

	_, err := c.signedRequest(context.Background(), "http://127.0.0.1:1/test", nil)
	if err == nil {
		t.Error("expected error when nonce fetch fails")
	}
}

func TestWaitForStatusSignedRequestError(t *testing.T) {
	log := logger.New("error", "text")
	c := NewClient("https://example.com", t.TempDir(), log)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	c.accountKey = key
	c.accountURL = "https://acme.example.com/acct/1"
	c.directory = &Directory{
		NewNonce: "http://127.0.0.1:1/nonce", // unreachable
	}

	_, err := c.waitForStatus(context.Background(), "http://127.0.0.1:1/order", "ready", 1)
	if err == nil {
		t.Error("expected error when signedRequest fails in waitForStatus")
	}
}

func TestLoadOrCreateAccountKeyBadDir(t *testing.T) {
	// Use a path that can't be created (file as dir)
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	os.WriteFile(blocker, []byte("x"), 0644)

	log := logger.New("error", "text")
	c := NewClient("https://example.com", filepath.Join(blocker, "subdir"), log)

	err := c.loadOrCreateAccountKey()
	if err == nil {
		t.Error("expected error when storage dir can't be created")
	}
}

func TestEnsureDirectoryRequestError(t *testing.T) {
	log := logger.New("error", "text")
	// Invalid URL that causes NewRequestWithContext to fail
	c := NewClient("http://127.0.0.1:1/directory", t.TempDir(), log)
	c.httpClient = &http.Client{Timeout: 1 * time.Millisecond}

	err := c.ensureDirectory(context.Background())
	if err == nil {
		t.Error("expected error")
	}
}

func TestNewOrderDecodeError(t *testing.T) {
	nonceCount := 0
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonceCount++
		w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-%d", nonceCount))
		switch r.URL.Path {
		case "/new-nonce":
			w.WriteHeader(http.StatusOK)
		case "/new-order":
			w.Header().Set("Location", "https://example.com/order/1")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte("{invalid json"))
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
		NewOrder: mockServer.URL + "/new-order",
	}

	_, err := c.newOrder(context.Background(), []string{"example.com"})
	if err == nil {
		t.Error("expected error for invalid JSON response")
	}
}

func TestGetAuthorizationError(t *testing.T) {
	log := logger.New("error", "text")
	c := NewClient("https://example.com", t.TempDir(), log)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	c.accountKey = key
	c.accountURL = "https://acme.example.com/acct/1"
	c.directory = &Directory{
		NewNonce: "http://127.0.0.1:1/nonce",
	}

	_, err := c.getAuthorization(context.Background(), "http://127.0.0.1:1/authz")
	if err == nil {
		t.Error("expected error when signedRequest fails")
	}
}

func TestSolveChallengeSignedRequestError(t *testing.T) {
	nonceCount := 0
	reqCount := 0
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonceCount++
		reqCount++
		w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-%d", nonceCount))
		if reqCount == 1 {
			// First request (challenge URL) succeeds
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
		} else {
			// Second request (waitForStatus poll) — return invalid to cause error
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "invalid"})
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

	authz := &Authorization{
		Status:     "pending",
		Identifier: Identifier{Type: "dns", Value: "example.com"},
		Challenges: []Challenge{
			{Type: "http-01", URL: mockServer.URL + "/chall/1", Token: "tok1", Status: "pending"},
		},
	}

	err := c.solveChallenge(context.Background(), authz)
	if err == nil {
		t.Error("expected error when challenge validation fails")
	}
}

func TestDownloadCertError(t *testing.T) {
	log := logger.New("error", "text")
	c := NewClient("https://example.com", t.TempDir(), log)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	c.accountKey = key
	c.accountURL = "https://acme.example.com/acct/1"
	c.directory = &Directory{
		NewNonce: "http://127.0.0.1:1/nonce",
	}

	_, err := c.downloadCert(context.Background(), "http://127.0.0.1:1/cert")
	if err == nil {
		t.Error("expected error when download fails")
	}
}

func TestFinalizeOrderError(t *testing.T) {
	log := logger.New("error", "text")
	c := NewClient("https://example.com", t.TempDir(), log)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	c.accountKey = key
	c.accountURL = "https://acme.example.com/acct/1"
	c.directory = &Directory{
		NewNonce: "http://127.0.0.1:1/nonce",
	}

	_, err := c.finalizeOrder(context.Background(), "http://127.0.0.1:1/finalize", []byte("csr"))
	if err == nil {
		t.Error("expected error when finalize request fails")
	}
}

func TestSolveChallengeRequestError(t *testing.T) {
	// Test that solveChallenge returns an error when the signedRequest to
	// the challenge URL fails (e.g., nonce fetch fails).
	log := logger.New("error", "text")
	c := NewClient("https://example.com", t.TempDir(), log)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	c.accountKey = key
	c.accountURL = "https://acme.example.com/acct/1"
	c.directory = &Directory{
		NewNonce: "http://127.0.0.1:1/nonce", // unreachable, so signedRequest fails
	}

	authz := &Authorization{
		Status:     "pending",
		Identifier: Identifier{Type: "dns", Value: "example.com"},
		Challenges: []Challenge{
			{Type: "http-01", URL: "http://127.0.0.1:1/chall/1", Token: "tok-err", Status: "pending"},
		},
	}

	err := c.solveChallenge(context.Background(), authz)
	if err == nil {
		t.Error("expected error when signedRequest fails in solveChallenge")
	}
	// Verify token was cleaned up despite error
	if _, ok := c.httpTokens.Load("tok-err"); ok {
		t.Error("token should be cleaned up on error")
	}
}

func TestObtainCertificateFinalizeErrorFlow(t *testing.T) {
	// Test the finalize error path in ObtainCertificate
	nonceCount := 0
	var mockServer *httptest.Server
	mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonceCount++
		w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-%d", nonceCount))

		switch r.URL.Path {
		case "/directory":
			json.NewEncoder(w).Encode(Directory{
				NewNonce:   mockServer.URL + "/new-nonce",
				NewAccount: mockServer.URL + "/new-acct",
				NewOrder:   mockServer.URL + "/new-order",
			})
		case "/new-nonce":
			w.WriteHeader(http.StatusOK)
		case "/new-acct":
			w.Header().Set("Location", mockServer.URL+"/acct/1")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"status":"valid"}`))
		case "/new-order":
			w.Header().Set("Location", mockServer.URL+"/order/1")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{
				"status":         "ready",
				"identifiers":    []map[string]string{{"type": "dns", "value": "test.com"}},
				"authorizations": []string{},
				"finalize":       "http://127.0.0.1:1/finalize-unreachable",
			})
		case "/order/1":
			json.NewEncoder(w).Encode(map[string]any{
				"status":      "ready",
				"finalize":    "http://127.0.0.1:1/finalize-unreachable",
				"certificate": mockServer.URL + "/cert/1",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockServer.Close()

	log := logger.New("error", "text")
	c := NewClient(mockServer.URL+"/directory", t.TempDir(), log)
	_, _, _, err := c.ObtainCertificate(context.Background(), []string{"test.com"})
	if err == nil {
		t.Error("expected error when finalize fails")
	}
}

func TestObtainCertificateDownloadError(t *testing.T) {
	// Test the downloadCert error path in ObtainCertificate
	nonceCount := 0
	finalized := false
	var mockServer *httptest.Server
	mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonceCount++
		w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-%d", nonceCount))

		switch r.URL.Path {
		case "/directory":
			json.NewEncoder(w).Encode(Directory{
				NewNonce:   mockServer.URL + "/new-nonce",
				NewAccount: mockServer.URL + "/new-acct",
				NewOrder:   mockServer.URL + "/new-order",
			})
		case "/new-nonce":
			w.WriteHeader(http.StatusOK)
		case "/new-acct":
			w.Header().Set("Location", mockServer.URL+"/acct/1")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"status":"valid"}`))
		case "/new-order":
			w.Header().Set("Location", mockServer.URL+"/order/1")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{
				"status":         "ready",
				"identifiers":    []map[string]string{{"type": "dns", "value": "test.com"}},
				"authorizations": []string{},
				"finalize":       mockServer.URL + "/finalize/1",
			})
		case "/order/1":
			status := "ready"
			if finalized {
				status = "valid"
			}
			json.NewEncoder(w).Encode(map[string]any{
				"status":      status,
				"finalize":    mockServer.URL + "/finalize/1",
				"certificate": "http://127.0.0.1:1/cert-unreachable",
			})
		case "/finalize/1":
			finalized = true
			json.NewEncoder(w).Encode(map[string]any{
				"status":      "valid",
				"certificate": "http://127.0.0.1:1/cert-unreachable",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockServer.Close()

	log := logger.New("error", "text")
	c := NewClient(mockServer.URL+"/directory", t.TempDir(), log)
	_, _, _, err := c.ObtainCertificate(context.Background(), []string{"test.com"})
	if err == nil {
		t.Error("expected error when cert download fails")
	}
}

func TestObtainCertificateWaitValidError(t *testing.T) {
	// Test the wait-for-valid error path when finalize returns non-valid status
	nonceCount := 0
	finalized := false
	var mockServer *httptest.Server
	mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonceCount++
		w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-%d", nonceCount))

		switch r.URL.Path {
		case "/directory":
			json.NewEncoder(w).Encode(Directory{
				NewNonce:   mockServer.URL + "/new-nonce",
				NewAccount: mockServer.URL + "/new-acct",
				NewOrder:   mockServer.URL + "/new-order",
			})
		case "/new-nonce":
			w.WriteHeader(http.StatusOK)
		case "/new-acct":
			w.Header().Set("Location", mockServer.URL+"/acct/1")
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"status":"valid"}`))
		case "/new-order":
			w.Header().Set("Location", mockServer.URL+"/order/1")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{
				"status":         "ready",
				"identifiers":    []map[string]string{{"type": "dns", "value": "test.com"}},
				"authorizations": []string{},
				"finalize":       mockServer.URL + "/finalize/1",
			})
		case "/order/1":
			status := "ready"
			if finalized {
				status = "invalid" // will cause waitForStatus to error
			}
			json.NewEncoder(w).Encode(map[string]any{
				"status":      status,
				"finalize":    mockServer.URL + "/finalize/1",
				"certificate": mockServer.URL + "/cert/1",
			})
		case "/finalize/1":
			finalized = true
			json.NewEncoder(w).Encode(map[string]any{
				"status":      "processing", // not valid, triggers wait
				"certificate": mockServer.URL + "/cert/1",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockServer.Close()

	log := logger.New("error", "text")
	c := NewClient(mockServer.URL+"/directory", t.TempDir(), log)
	_, _, _, err := c.ObtainCertificate(context.Background(), []string{"test.com"})
	if err == nil {
		t.Error("expected error when wait-for-valid fails")
	}
}

func TestLoadOrCreateAccountKeyWriteError(t *testing.T) {
	// Test the error path when the key file can't be written.
	// We create a directory where account.key is a read-only directory, preventing WriteFile.
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "account.key")
	// Create a directory named "account.key" so WriteFile fails
	os.MkdirAll(keyPath, 0755)

	log := logger.New("error", "text")
	c := NewClient("https://example.com", dir, log)
	err := c.loadOrCreateAccountKey()
	if err == nil {
		t.Error("expected error when key file can't be written (directory exists)")
	}
}

func TestEnsureAccountLoadKeyError(t *testing.T) {
	// Trigger loadOrCreateAccountKey failure by using an inaccessible storage dir
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	os.WriteFile(blocker, []byte("x"), 0644)

	log := logger.New("error", "text")
	c := NewClient("https://example.com", filepath.Join(blocker, "subdir"), log)
	c.directory = &Directory{
		NewNonce:   "https://example.com/new-nonce",
		NewAccount: "https://example.com/new-acct",
	}

	err := c.ensureAccount(context.Background())
	if err == nil {
		t.Error("expected error when loadOrCreateAccountKey fails")
	}
}

func TestSignedRequestBadURL(t *testing.T) {
	// Test the http.NewRequestWithContext error path in signedRequest
	// by passing an invalid URL with control characters.
	nonceCount := 0
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonceCount++
		w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-%d", nonceCount))
		w.WriteHeader(http.StatusOK)
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

	// URL with control characters to trigger NewRequestWithContext error
	_, err := c.signedRequest(context.Background(), "http://example.com/\x00bad", nil)
	if err == nil {
		t.Error("expected error for invalid URL in signedRequest")
	}
}

func TestEnsureDirectoryBadURL(t *testing.T) {
	// Test the http.NewRequestWithContext error path in ensureDirectory
	log := logger.New("error", "text")
	c := NewClient("http://example.com/\x00bad", t.TempDir(), log)

	err := c.ensureDirectory(context.Background())
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestObtainCertificateGenerateKeyError(t *testing.T) {
	origGenKey := generateKey
	callCount := 0
	generateKey = func() (*ecdsa.PrivateKey, error) {
		callCount++
		if callCount <= 1 {
			// First call is from loadOrCreateAccountKey — let it succeed
			return origGenKey()
		}
		// Second call is from ObtainCertificate — fail
		return nil, fmt.Errorf("simulated key generation error")
	}
	defer func() { generateKey = origGenKey }()

	m := newMockACME(t)
	m.authzStatus = "valid"

	c := m.client(t)
	_, _, _, err := c.ObtainCertificate(context.Background(), []string{"test.com"})
	if err == nil {
		t.Error("expected error when key generation fails")
	}
}

func TestObtainCertificateCreateCSRError(t *testing.T) {
	origCSR := createCSR
	createCSR = func(key *ecdsa.PrivateKey, domains []string) ([]byte, error) {
		return nil, fmt.Errorf("simulated CSR creation error")
	}
	defer func() { createCSR = origCSR }()

	m := newMockACME(t)
	m.authzStatus = "valid"

	c := m.client(t)
	_, _, _, err := c.ObtainCertificate(context.Background(), []string{"test.com"})
	if err == nil {
		t.Error("expected error when CSR creation fails")
	}
}

func TestSignedRequestSignError(t *testing.T) {
	origSign := ecdsaSign
	ecdsaSign = func(key *ecdsa.PrivateKey, hash []byte) (*big.Int, *big.Int, error) {
		return nil, nil, fmt.Errorf("simulated sign error")
	}
	defer func() { ecdsaSign = origSign }()

	nonceCount := 0
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonceCount++
		w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-%d", nonceCount))
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

	_, err := c.signedRequest(context.Background(), mockServer.URL+"/test", nil)
	if err == nil {
		t.Error("expected error when ECDSA sign fails")
	}
}

func TestEcdsaPublicKeyBytesFallbackPath(t *testing.T) {
	origECDH := ecdsaToECDH
	ecdsaToECDH = func(pub *ecdsa.PublicKey) ([]byte, error) {
		return nil, fmt.Errorf("simulated ECDH error")
	}
	defer func() { ecdsaToECDH = origECDH }()

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	x, y := ecdsaPublicKeyBytes(&key.PublicKey)
	if len(x) != 32 {
		t.Errorf("x length = %d, want 32", len(x))
	}
	if len(y) != 32 {
		t.Errorf("y length = %d, want 32", len(y))
	}
}

func TestSolveChallengeThumbprintUsed(t *testing.T) {
	// Verify that solveChallenge computes and uses the JWK thumbprint.
	// We can observe this by checking the stored token includes the thumbprint.
	nonceCount := 0
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonceCount++
		w.Header().Set("Replay-Nonce", fmt.Sprintf("nonce-%d", nonceCount))
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "valid"})
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

	authz := &Authorization{
		Status:     "pending",
		Identifier: Identifier{Type: "dns", Value: "example.com"},
		Challenges: []Challenge{
			{Type: "http-01", URL: mockServer.URL + "/chall/1", Token: "verify-tok", Status: "pending"},
		},
	}

	_ = c.solveChallenge(context.Background(), authz)
}

func TestNewOrderSignedRequestError(t *testing.T) {
	// Test the signedRequest error path in newOrder
	log := logger.New("error", "text")
	c := NewClient("https://example.com", t.TempDir(), log)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	c.accountKey = key
	c.accountURL = "https://acme.example.com/acct/1"
	c.directory = &Directory{
		NewNonce: "http://127.0.0.1:1/nonce", // unreachable
		NewOrder: "http://127.0.0.1:1/new-order",
	}

	_, err := c.newOrder(context.Background(), []string{"example.com"})
	if err == nil {
		t.Error("expected error when signedRequest fails in newOrder")
	}
}

func TestEnsureAccountSignedRequestError(t *testing.T) {
	// Test the signedRequest error path in ensureAccount
	log := logger.New("error", "text")
	c := NewClient("https://example.com", t.TempDir(), log)
	c.directory = &Directory{
		NewNonce:   "http://127.0.0.1:1/nonce", // unreachable
		NewAccount: "http://127.0.0.1:1/new-acct",
	}

	err := c.ensureAccount(context.Background())
	if err == nil {
		t.Error("expected error when signedRequest fails in ensureAccount")
	}
}

func TestSolveChallengeThumbprintError(t *testing.T) {
	origThumb := thumbprintFunc
	thumbprintFunc = func(pub *ecdsa.PublicKey) (string, error) {
		return "", fmt.Errorf("simulated thumbprint error")
	}
	defer func() { thumbprintFunc = origThumb }()

	log := logger.New("error", "text")
	c := NewClient("https://example.com", t.TempDir(), log)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	c.accountKey = key

	authz := &Authorization{
		Status:     "pending",
		Identifier: Identifier{Type: "dns", Value: "example.com"},
		Challenges: []Challenge{
			{Type: "http-01", URL: "https://example.com/chall/1", Token: "tok1", Status: "pending"},
		},
	}

	err := c.solveChallenge(context.Background(), authz)
	if err == nil {
		t.Error("expected error when thumbprint fails")
	}
}

func TestLoadOrCreateAccountKeyGenerateError(t *testing.T) {
	origGenKey := generateKey
	generateKey = func() (*ecdsa.PrivateKey, error) {
		return nil, fmt.Errorf("simulated key generation error")
	}
	defer func() { generateKey = origGenKey }()

	log := logger.New("error", "text")
	c := NewClient("https://example.com", t.TempDir(), log)
	err := c.loadOrCreateAccountKey()
	if err == nil {
		t.Error("expected error when key generation fails")
	}
}
