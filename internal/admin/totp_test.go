package admin

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"math"
	"testing"
	"time"
)

func TestGenerateTOTPSecret(t *testing.T) {
	s1, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	if len(s1) < 20 {
		t.Fatalf("secret too short: %q", s1)
	}
	// Must be valid base32
	_, err = base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s1)
	if err != nil {
		t.Fatalf("not valid base32: %v", err)
	}

	// Two secrets must differ
	s2, _ := GenerateTOTPSecret()
	if s1 == s2 {
		t.Fatal("two generated secrets are identical")
	}
}

func TestValidateTOTP(t *testing.T) {
	secret, _ := GenerateTOTPSecret()

	// Generate the current valid code manually
	key, _ := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	counter := uint64(time.Now().Unix() / 30)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf)
	h := mac.Sum(nil)
	offset := h[len(h)-1] & 0x0f
	truncated := binary.BigEndian.Uint32(h[offset:offset+4]) & 0x7fffffff
	code := fmt.Sprintf("%06d", truncated%uint32(math.Pow10(6)))

	// Must accept correct code (lastStep=0 disables the replay guard).
	valid, matchedStep := ValidateTOTP(secret, code, 0)
	if !valid {
		t.Fatalf("valid code %s was rejected", code)
	}
	if matchedStep != int64(counter) {
		t.Fatalf("matchedStep = %d, want %d", matchedStep, counter)
	}

	// Replay guard: same code with lastStep == matchedStep must be
	// rejected.
	if ok, _ := ValidateTOTP(secret, code, matchedStep); ok {
		t.Fatal("replay accepted: lastStep == matchedStep should reject")
	}
	// Higher lastStep also rejects.
	if ok, _ := ValidateTOTP(secret, code, matchedStep+5); ok {
		t.Fatal("replay accepted: lastStep > matchedStep should reject")
	}

	// Must reject wrong code (random 6-digit string).
	if ok, _ := ValidateTOTP(secret, "000000", 0); ok && code != "000000" {
		t.Fatal("accepted invalid code 000000")
	}

	// Must reject empty (length mismatch path).
	if ok, _ := ValidateTOTP(secret, "", 0); ok {
		t.Fatal("accepted empty code")
	}

	// Must reject wrong length even if it would otherwise hash-match.
	if ok, _ := ValidateTOTP(secret, code+"0", 0); ok {
		t.Fatal("accepted code of wrong length")
	}

	// Must reject bad secret.
	if ok, _ := ValidateTOTP("INVALIDSECRET!!!", code, 0); ok {
		t.Fatal("accepted code with bad secret")
	}
}

func TestTOTPProvisioningURI(t *testing.T) {
	uri := TOTPProvisioningURI("JBSWY3DPEHPK3PXP", "admin", "UWAS")
	if uri == "" {
		t.Fatal("empty URI")
	}
	if len(uri) < 30 {
		t.Fatal("URI too short")
	}
	// Must contain required parts
	for _, part := range []string{"otpauth://totp/", "secret=JBSWY3DPEHPK3PXP", "issuer=UWAS", "digits=6", "period=30"} {
		if !contains(uri, part) {
			t.Fatalf("URI missing %q: %s", part, uri)
		}
	}
}
