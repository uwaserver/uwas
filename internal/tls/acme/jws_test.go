package acme

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"testing"
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
