package config

import "testing"

// F46302 (server side): MergeDomain replaces the whole proxy block when the
// patch carries proxy.upstreams and the whole redirect block when it carries
// redirect.target. This is the contract the dashboard edit form has to honour
// by sending the saved block back with its two managed fields changed; the
// test pins it so a change here is a conscious one.

func TestMergeDomainReplacesWholeProxyBlock(t *testing.T) {
	existing := Domain{Host: "api.example.com", Type: "proxy"}
	existing.Proxy.Upstreams = []Upstream{{Address: "10.0.0.1:8080", Weight: 3}}
	existing.Proxy.Algorithm = "least-conn"
	existing.Proxy.WebSocket = true
	existing.Proxy.MaxRetries = 2

	// A patch built the way the old edit form built it: addresses + algorithm.
	patch := Domain{}
	patch.Proxy.Upstreams = []Upstream{{Address: "10.0.0.1:8080", Weight: 1}}
	patch.Proxy.Algorithm = "least-conn"
	merged := MergeDomain(existing, patch, DomainPatchFields{}, false)
	if merged.Proxy.WebSocket || merged.Proxy.MaxRetries != 0 || merged.Proxy.Upstreams[0].Weight != 1 {
		t.Fatalf("expected the whole proxy block to be replaced, got %+v", merged.Proxy)
	}

	// Round-tripping the saved block (what the dashboard now does) keeps it.
	patch = Domain{}
	patch.Proxy = existing.Proxy
	merged = MergeDomain(existing, patch, DomainPatchFields{}, false)
	if !merged.Proxy.WebSocket || merged.Proxy.MaxRetries != 2 || merged.Proxy.Upstreams[0].Weight != 3 {
		t.Fatalf("a round-tripped proxy block must be preserved, got %+v", merged.Proxy)
	}

	// Control: a patch without upstreams leaves the saved block untouched.
	merged = MergeDomain(existing, Domain{}, DomainPatchFields{}, false)
	if !merged.Proxy.WebSocket || merged.Proxy.Upstreams[0].Weight != 3 {
		t.Fatalf("an unrelated patch must not touch the proxy block, got %+v", merged.Proxy)
	}
}

func TestMergeDomainReplacesWholeRedirectBlock(t *testing.T) {
	existing := Domain{Host: "old.example.com", Type: "redirect"}
	existing.Redirect = RedirectConfig{Target: "https://new.example.com", Status: 308, PreservePath: true}

	patch := Domain{}
	patch.Redirect = RedirectConfig{Target: "https://new.example.com", Status: 308}
	merged := MergeDomain(existing, patch, DomainPatchFields{}, false)
	if merged.Redirect.PreservePath {
		t.Fatalf("expected the whole redirect block to be replaced, got %+v", merged.Redirect)
	}

	patch = Domain{}
	patch.Redirect = existing.Redirect
	merged = MergeDomain(existing, patch, DomainPatchFields{}, false)
	if !merged.Redirect.PreservePath {
		t.Fatalf("a round-tripped redirect block must be preserved, got %+v", merged.Redirect)
	}

	merged = MergeDomain(existing, Domain{}, DomainPatchFields{}, false)
	if !merged.Redirect.PreservePath {
		t.Fatalf("an unrelated patch must not touch the redirect block, got %+v", merged.Redirect)
	}
}
