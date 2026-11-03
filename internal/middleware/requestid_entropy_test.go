package middleware

import (
	cryptorand "crypto/rand"
	"errors"
	"testing"
)

type failedRequestIDEntropy struct{}

func (failedRequestIDEntropy) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}

func TestGenerateRequestIDSurvivesEntropyFailureWithoutCollision(t *testing.T) {
	originalReader := cryptorand.Reader
	cryptorand.Reader = failedRequestIDEntropy{}
	t.Cleanup(func() { cryptorand.Reader = originalReader })

	first := generateRequestID()
	second := generateRequestID()
	if first == second {
		t.Fatalf("fallback request IDs collided: %q", first)
	}
	for _, id := range []string{first, second} {
		if len(id) != 36 || id[14] != '7' || (id[19] != '8' && id[19] != '9' && id[19] != 'a' && id[19] != 'b') {
			t.Fatalf("invalid fallback request ID: %q", id)
		}
	}
}
