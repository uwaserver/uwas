package admin

import "testing"

// grpDResetCloudflare clears the Cloudflare state for test isolation.
// Extracted from the deleted coverpush_D_test.go.
func grpDResetCloudflare(t *testing.T) {
	t.Helper()
	cloudflareMu.Lock()
	cloudflareConfig = nil
	cloudflareMu.Unlock()
	t.Cleanup(func() {
		cloudflareMu.Lock()
		cloudflareConfig = nil
		cloudflareMu.Unlock()
	})
}
