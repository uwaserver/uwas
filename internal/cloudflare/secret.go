package cloudflare

import (
	crand "crypto/rand"
	"encoding/base64"
	"fmt"
)

// randomTunnelSecret returns a 32-byte URL-safe base64 string.
// Cloudflare requires the tunnel_secret field on creation; for cloudflared-managed
// (config_src=cloudflare) tunnels this value is essentially a random token that
// authenticates the cloudflared process — we never reuse it because we fetch the
// connector token via GetTunnelToken instead.
func randomTunnelSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := crand.Read(b); err != nil {
		return "", fmt.Errorf("generate tunnel secret: %w", err)
	}
	return base64.StdEncoding.EncodeToString(b), nil
}
