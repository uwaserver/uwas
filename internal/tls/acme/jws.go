package acme

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
)

// Testable hook for ECDSA signing.
var ecdsaSign = func(key *ecdsa.PrivateKey, hash []byte) (*big.Int, *big.Int, error) {
	return ecdsa.Sign(rand.Reader, key, hash)
}

// signedRequest sends a JWS-signed POST to the ACME server.
func (c *Client) signedRequest(ctx context.Context, url string, payload any) (*http.Response, error) {
	// Get nonce
	nonce, err := c.nonces.get(c.httpClient, c.directory.NewNonce)
	if err != nil {
		return nil, fmt.Errorf("get nonce: %w", err)
	}

	// Build protected header
	protected := map[string]any{
		"alg":   "ES256",
		"nonce": nonce,
		"url":   url,
	}
	if c.accountURL != "" {
		protected["kid"] = c.accountURL
	} else {
		protected["jwk"] = ecdsaJWK(&c.accountKey.PublicKey)
	}

	protectedB64 := base64url(mustJSON(protected))

	// Encode payload (nil = POST-as-GET, empty string payload)
	var payloadB64 string
	if payload != nil {
		payloadB64 = base64url(mustJSON(payload))
	}

	// Sign: ECDSA-SHA256
	sigInput := protectedB64 + "." + payloadB64
	hash := sha256.Sum256([]byte(sigInput))
	r, s, err := ecdsaSign(c.accountKey, hash[:])
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}

	// ECDSA signature: r || s, each padded to 32 bytes for P-256
	sig := append(padTo(r.Bytes(), 32), padTo(s.Bytes(), 32)...)

	// Build JWS body
	body := map[string]string{
		"protected": protectedB64,
		"payload":   payloadB64,
		"signature": base64url(sig),
	}

	bodyJSON, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/jose+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	// Save replay nonce
	if n := resp.Header.Get("Replay-Nonce"); n != "" {
		c.nonces.put(n)
	}

	return resp, nil
}

// ecdsaToECDH is a testable hook for converting an ECDSA public key to ECDH.
var ecdsaToECDH = func(pub *ecdsa.PublicKey) ([]byte, error) {
	ecdhKey, err := pub.ECDH()
	if err != nil {
		return nil, err
	}
	return ecdhKey.Bytes(), nil
}

// ecdsaPublicKeyBytes returns the uncompressed X and Y coordinates of a P-256 key.
func ecdsaPublicKeyBytes(pub *ecdsa.PublicKey) (x, y []byte) {
	raw, err := ecdsaToECDH(pub)
	if err != nil || len(raw) < 65 || raw[0] != 4 {
		// Fallback for unexpected key conversion failures.
		// This avoids touching deprecated PublicKey.X/Y fields on Go 1.26+.
		return make([]byte, 32), make([]byte, 32)
	}
	// ECDH Bytes() returns uncompressed point: 0x04 || X(32) || Y(32)
	return raw[1:33], raw[33:65]
}

// ecdsaJWK returns the JWK representation of an ECDSA P-256 public key.
func ecdsaJWK(pub *ecdsa.PublicKey) map[string]string {
	x, y := ecdsaPublicKeyBytes(pub)
	return map[string]string{
		"kty": "EC",
		"crv": "P-256",
		"x":   base64url(x),
		"y":   base64url(y),
	}
}

// jwkThumbprint computes the JWK thumbprint (SHA-256) per RFC 7638.
func jwkThumbprint(pub *ecdsa.PublicKey) (string, error) {
	x, y := ecdsaPublicKeyBytes(pub)
	// Canonical JSON with lexicographic key order
	jwk := fmt.Sprintf(`{"crv":"P-256","kty":"EC","x":"%s","y":"%s"}`,
		base64url(x), base64url(y),
	)
	hash := sha256.Sum256([]byte(jwk))
	return base64url(hash[:]), nil
}

// base64url encodes bytes using base64url encoding without padding (RFC 4648 §5).
func base64url(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

// mustJSON marshals v to JSON bytes.
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic("json marshal: " + err.Error())
	}
	return b
}

// padTo left-pads b with zeros to length n.
func padTo(b []byte, n int) []byte {
	if len(b) >= n {
		return b
	}
	padded := make([]byte, n)
	copy(padded[n-len(b):], b)
	return padded
}

// noncePool manages ACME replay nonces.
type noncePool struct {
	mu     sync.Mutex
	nonces []string
}

func (p *noncePool) get(client *http.Client, newNonceURL string) (string, error) {
	p.mu.Lock()
	if len(p.nonces) > 0 {
		nonce := p.nonces[len(p.nonces)-1]
		p.nonces = p.nonces[:len(p.nonces)-1]
		p.mu.Unlock()
		return nonce, nil
	}
	p.mu.Unlock()

	// Fetch fresh nonce
	resp, err := client.Head(newNonceURL)
	if err != nil {
		return "", err
	}
	resp.Body.Close()

	nonce := resp.Header.Get("Replay-Nonce")
	if nonce == "" {
		return "", fmt.Errorf("no nonce in response")
	}
	return nonce, nil
}

func (p *noncePool) put(nonce string) {
	p.mu.Lock()
	if len(p.nonces) < 10 {
		p.nonces = append(p.nonces, nonce)
	}
	p.mu.Unlock()
}
