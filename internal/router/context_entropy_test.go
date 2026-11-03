package router

import (
	cryptorand "crypto/rand"
	"errors"
	"testing"
)

type failedContextEntropy struct{}

func (failedContextEntropy) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}

func TestGenerateIDBytesSurvivesPrimaryEntropyFailure(t *testing.T) {
	originalReader := cryptorand.Reader
	cryptorand.Reader = failedContextEntropy{}
	t.Cleanup(func() { cryptorand.Reader = originalReader })

	var id [16]byte
	generateIDBytes(&id)

	if id[6]>>4 != 7 {
		t.Fatalf("version nibble = %x, want 7; id=%x", id[6]>>4, id)
	}
	if id[8]>>6 != 2 {
		t.Fatalf("variant bits = %b, want 10; id=%x", id[8]>>6, id)
	}
}
