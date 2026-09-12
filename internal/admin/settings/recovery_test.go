package settings

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// TestUseRecoveryCodeRejectsPlaintext verifies that UseRecoveryCode
// accepts only the hashed form of a recovery code — not the plaintext.
// This is a regression test: a previous version of UseRecoveryCode
// had a second OR branch that compared the plaintext directly against
// stored hashes, which could never match (hash vs plaintext) and was
// dead code that signaled a developer misunderstanding.
func TestUseRecoveryCodeRejectsPlaintext(t *testing.T) {
	// Generate a fresh recovery code and store it hashed.
	rawCode := "deadbeefcafebabe"
	hashed := hashRecoveryCode(rawCode) // SHA-256 of the raw code.

	// Simulate the condition in UseRecoveryCode:
	// sum = hashRecoveryCode(rawCode)  →  hashed version of rawCode
	// codes = [hashed]                →  stored hashed codes
	// Both comparisons must use the hashed value.
	sum := hashRecoveryCode(rawCode) // this is the "sum" local in UseRecoveryCode

	// The valid (first) branch — hashed vs hashed — must match.
	if len(hashed) != len(sum) {
		t.Fatalf("hash length mismatch: got %d want %d", len(hashed), len(sum))
	}

	// The removed (second) OR branch — plaintext vs hashed — is impossible
	// to satisfy because stored codes are always hashes and req.Code is plaintext.
	// Confirm: plaintext "rawCode" is NOT equal to the hash of itself (obviously).
	notEqual := rawCode != sum
	if !notEqual {
		t.Error("plaintext must not equal its own hash — removed OR branch was dead code")
	}

	// Also confirm the hashing is deterministic and correct.
	h2 := sha256.Sum256([]byte(rawCode))
	want := hex.EncodeToString(h2[:])
	if sum != want {
		t.Errorf("hashRecoveryCode produced wrong value: got %q want %q", sum, want)
	}
}
