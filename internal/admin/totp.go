package admin

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"
)

const (
	totpDigits = 6
	totpPeriod = 30
	totpWindow = 1 // allow ±1 period for clock skew
)

// GenerateTOTPSecret creates a new random 20-byte base32-encoded secret.
func GenerateTOTPSecret() (string, error) {
	secret := make([]byte, 20)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret), nil
}

// ValidateTOTP checks if the given code matches the TOTP for the secret,
// allowing ±1 time step for clock skew. Returns the matched step (Unix
// seconds / 30) on success, or -1 on failure.
func ValidateTOTP(secret, code string) (bool, int64) {
	secret = strings.TrimSpace(strings.ToUpper(secret))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		return false, -1
	}

	now := time.Now().Unix()
	for i := -totpWindow; i <= totpWindow; i++ {
		counter := uint64((now / totpPeriod) + int64(i))
		if generateCode(key, counter) == code {
			return true, int64(now / totpPeriod)
		}
	}
	return false, -1
}

// generateCode computes a single TOTP code for the given key and counter.
func generateCode(key []byte, counter uint64) string {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(buf)
	h := mac.Sum(nil)

	offset := h[len(h)-1] & 0x0f
	truncated := binary.BigEndian.Uint32(h[offset:offset+4]) & 0x7fffffff
	code := truncated % uint32(math.Pow10(totpDigits))

	return fmt.Sprintf("%0*d", totpDigits, code)
}

// TOTPProvisioningURI returns an otpauth:// URI for QR code generation.
func TOTPProvisioningURI(secret, accountName, issuer string) string {
	return fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s&digits=%d&period=%d",
		url.PathEscape(issuer),
		url.PathEscape(accountName),
		secret,
		url.QueryEscape(issuer),
		totpDigits,
		totpPeriod,
	)
}
