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

	// Must accept correct code
	if !ValidateTOTP(secret, code) {
		t.Fatalf("valid code %s was rejected", code)
	}

	// Must reject wrong code
	if ValidateTOTP(secret, "000000") && code != "000000" {
		t.Fatal("accepted invalid code 000000")
	}

	// Must reject empty
	if ValidateTOTP(secret, "") {
		t.Fatal("accepted empty code")
	}

	// Must reject bad secret
	if ValidateTOTP("INVALIDSECRET!!!", code) {
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
