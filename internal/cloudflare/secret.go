package cloudflare

import (
	crand "crypto/rand"
	"encoding/base64"
)

// randomTunnelSecret returns a 32-byte URL-safe base64 string.
// Cloudflare requires the tunnel_secret field on creation; for cloudflared-managed
// (config_src=cloudflare) tunnels this value is essentially a random token that
// authenticates the cloudflared process — we never reuse it because we fetch the
// connector token via GetTunnelToken instead.
func randomTunnelSecret() string {
	b := make([]byte, 32)
	if _, err := crand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return base64.StdEncoding.EncodeToString(b)
}
