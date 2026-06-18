package config

import (
	"net"
	"strings"
	"syscall"
	"testing"

	"gopkg.in/yaml.v3"
)

// --- SafeDialControl ---

func TestSafeDialControl(t *testing.T) {
	tests := []struct {
		name    string
		address string
		wantErr bool
	}{
		{"host:port blocked metadata", "169.254.169.254:80", true},
		{"host:port loopback blocked", "127.0.0.1:25", true},
		{"host:port private blocked", "10.0.0.5:443", true},
		{"host:port public ok", "8.8.8.8:53", false},
		{"bare ip no port public", "8.8.8.8", false},
		{"bare ip no port blocked", "127.0.0.1", true},
		{"non-ip hostname passes", "example.com:80", false},
		{"link-local blocked", "169.254.1.1:80", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := SafeDialControl("tcp", tt.address, syscall.RawConn(nil))
			if (err != nil) != tt.wantErr {
				t.Fatalf("SafeDialControl(%q) err=%v wantErr=%v", tt.address, err, tt.wantErr)
			}
		})
	}
}

// --- ValidateDomain / ValidateDomainPartial / validateDomain ---

func TestValidateDomain_Nil(t *testing.T) {
	if err := ValidateDomain(nil); err == nil {
		t.Fatal("expected error for nil domain")
	}
}

func TestValidateDomain_InvalidType(t *testing.T) {
	d := &Domain{Host: "example.com", Type: "bogus"}
	if err := ValidateDomain(d); err == nil {
		t.Fatal("expected invalid type error")
	}
}

func TestValidateDomain_InvalidCanonicalHost(t *testing.T) {
	d := &Domain{Host: "example.com", Type: "static", Root: "/srv", CanonicalHost: "nope"}
	if err := ValidateDomain(d); err == nil {
		t.Fatal("expected invalid canonical_host error")
	}
}

func TestValidateDomain_ValidCanonicalHost(t *testing.T) {
	d := &Domain{Host: "example.com", Type: "static", Root: "/srv", CanonicalHost: "WWW"}
	if err := ValidateDomain(d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateDomain_InvalidSSLMode(t *testing.T) {
	d := &Domain{Host: "example.com", Type: "static", Root: "/srv"}
	d.SSL.Mode = "weird"
	if err := ValidateDomain(d); err == nil {
		t.Fatal("expected invalid ssl.mode error")
	}
}

func TestValidateDomain_ManualSSLMissingCertKey(t *testing.T) {
	d := &Domain{Host: "example.com", Type: "static", Root: "/srv"}
	d.SSL.Mode = "manual"
	if err := ValidateDomain(d); err == nil {
		t.Fatal("expected manual ssl requires cert/key error")
	}
}

func TestValidateDomain_ProxyRequiresUpstream(t *testing.T) {
	d := &Domain{Host: "example.com", Type: "proxy"}
	if err := ValidateDomain(d); err == nil {
		t.Fatal("expected proxy requires upstream error")
	}
	// Partial mode skips the invariant.
	if err := ValidateDomainPartial(d); err != nil {
		t.Fatalf("partial should not require upstream: %v", err)
	}
}

func TestValidateDomain_RedirectRequiresTarget(t *testing.T) {
	d := &Domain{Host: "example.com", Type: "redirect"}
	if err := ValidateDomain(d); err == nil {
		t.Fatal("expected redirect requires target error")
	}
	if err := ValidateDomainPartial(d); err != nil {
		t.Fatalf("partial should not require target: %v", err)
	}
}

func TestValidateDomain_BasicAuthInvalid(t *testing.T) {
	d := &Domain{Host: "example.com", Type: "static", Root: "/srv"}
	d.BasicAuth = BasicAuthConfig{Enabled: true} // no users
	if err := ValidateDomain(d); err == nil {
		t.Fatal("expected basic auth error")
	}
}

func TestValidateDomain_LocationBasicAuthInvalid(t *testing.T) {
	ba := &BasicAuthConfig{Enabled: true} // no users
	d := &Domain{
		Host: "example.com", Type: "static", Root: "/srv",
		Locations: []LocationConfig{
			{Match: "/admin", BasicAuth: ba},
		},
	}
	if err := ValidateDomain(d); err == nil {
		t.Fatal("expected location basic auth error")
	}
}

func TestValidateDomain_LocationBasicAuthNilSkipped(t *testing.T) {
	d := &Domain{
		Host: "example.com", Type: "static", Root: "/srv",
		Locations: []LocationConfig{
			{Match: "/public", BasicAuth: nil},
		},
	}
	if err := ValidateDomain(d); err != nil {
		t.Fatalf("nil location basic auth should be skipped: %v", err)
	}
}

func TestValidateDomain_LocationBasicAuthScopeNoMatch(t *testing.T) {
	// Location with empty Match exercises the "location[%d]" scope label path.
	ba := &BasicAuthConfig{Enabled: true}
	d := &Domain{
		Host: "example.com", Type: "static", Root: "/srv",
		Locations: []LocationConfig{
			{Match: "", BasicAuth: ba},
		},
	}
	if err := ValidateDomain(d); err == nil {
		t.Fatal("expected error for enabled location basic auth without users")
	}
}

func TestValidateDomain_NegativeCacheTTL(t *testing.T) {
	d := &Domain{Host: "example.com", Type: "static", Root: "/srv"}
	d.Cache.TTL = -1
	if err := ValidateDomain(d); err == nil {
		t.Fatal("expected negative cache TTL error")
	}
}

func TestValidateDomain_NegativeRateLimit(t *testing.T) {
	d := &Domain{Host: "example.com", Type: "static", Root: "/srv"}
	d.Security.RateLimit.Requests = -5
	if err := ValidateDomain(d); err == nil {
		t.Fatal("expected negative rate limit error")
	}
}

func TestValidateDomain_Valid(t *testing.T) {
	d := &Domain{Host: "example.com", Type: "static", Root: "/srv"}
	if err := ValidateDomain(d); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- ValidateBasicAuth ---

func TestValidateBasicAuth(t *testing.T) {
	tests := []struct {
		name    string
		ba      BasicAuthConfig
		wantErr bool
	}{
		{"realm with newline", BasicAuthConfig{Realm: "a\nb"}, true},
		{"disabled empty ok", BasicAuthConfig{Enabled: false}, false},
		{"enabled no users", BasicAuthConfig{Enabled: true}, true},
		{"empty username", BasicAuthConfig{Enabled: true, Users: map[string]string{"": "p"}}, true},
		{"username with spaces", BasicAuthConfig{Enabled: true, Users: map[string]string{" u ": "p"}}, true},
		{"username with colon", BasicAuthConfig{Enabled: true, Users: map[string]string{"u:x": "p"}}, true},
		{"empty password", BasicAuthConfig{Enabled: true, Users: map[string]string{"u": ""}}, true},
		{"password with newline", BasicAuthConfig{Enabled: true, Users: map[string]string{"u": "p\nq"}}, true},
		{"valid", BasicAuthConfig{Enabled: true, Users: map[string]string{"u": "p"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBasicAuth("scope", tt.ba)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateBasicAuth err=%v wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

// --- IsValidHostname ---

func TestIsValidHostname(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"", false},
		{strings.Repeat("a", 254), false},
		{"example.com", true},
		{"*.example.com", true},
		{"sub.example.com", true},
		{"a..b", false},                           // empty label
		{"-bad.com", false},                       // leading hyphen
		{"bad-.com", false},                       // trailing hyphen
		{strings.Repeat("a", 64) + ".com", false}, // label > 63
		{"bad_underscore.com", false},             // invalid char
		{"good-name.com", true},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			if got := IsValidHostname(tt.host); got != tt.want {
				t.Fatalf("IsValidHostname(%q)=%v want %v", tt.host, got, tt.want)
			}
		})
	}
}

// --- isCloudMetadataHost ---

func TestIsCloudMetadataHost(t *testing.T) {
	if !isCloudMetadataHost("169.254.169.254") {
		t.Error("expected AWS metadata IP to be flagged")
	}
	if !isCloudMetadataHost("fd00:ec2::254") {
		t.Error("expected GCP/AWS v6 metadata IP to be flagged")
	}
	if isCloudMetadataHost("8.8.8.8") {
		t.Error("public IP should not be metadata")
	}
	if isCloudMetadataHost("not-an-ip") {
		t.Error("non-IP host should not be metadata")
	}
}

// --- ipBlockedReason ---

func TestIPBlockedReason(t *testing.T) {
	tests := []struct {
		name   string
		ip     string
		policy urlSafetyPolicy
		want   string
	}{
		{"unspecified v4", "0.0.0.0", urlSafetyPolicy{}, "unspecified address"},
		{"unspecified v6", "::", urlSafetyPolicy{}, "unspecified address"},
		{"metadata", "169.254.169.254", urlSafetyPolicy{}, "cloud metadata endpoint"},
		{"loopback blocked", "127.0.0.1", urlSafetyPolicy{}, "loopback address"},
		{"loopback allowed", "127.0.0.1", urlSafetyPolicy{allowLoopback: true}, ""},
		{"private blocked", "10.1.2.3", urlSafetyPolicy{}, "private address"},
		{"private allowed", "10.1.2.3", urlSafetyPolicy{allowPrivate: true}, ""},
		{"cgnat private", "100.64.0.1", urlSafetyPolicy{}, "private address"},
		{"link-local", "169.254.1.1", urlSafetyPolicy{}, "link-local address"},
		{"documentation", "192.0.2.1", urlSafetyPolicy{}, "documentation address"},
		{"documentation 2", "198.51.100.1", urlSafetyPolicy{}, "documentation address"},
		{"documentation 3", "203.0.113.1", urlSafetyPolicy{}, "documentation address"},
		{"public", "8.8.8.8", urlSafetyPolicy{}, ""},
		{"ipv4-mapped loopback", "::ffff:127.0.0.1", urlSafetyPolicy{}, "loopback address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := mustIP(t, tt.ip)
			if got := ipBlockedReason(ip, tt.policy); got != tt.want {
				t.Fatalf("ipBlockedReason(%s)=%q want %q", tt.ip, got, tt.want)
			}
		})
	}
}

// --- isLoopback ---

func TestIsLoopback(t *testing.T) {
	if !isLoopback("localhost") {
		t.Error("localhost should be loopback")
	}
	if !isLoopback("127.0.0.1") {
		t.Error("127.0.0.1 should be loopback")
	}
	if !isLoopback("::1") {
		t.Error("::1 should be loopback")
	}
	if isLoopback("8.8.8.8") {
		t.Error("8.8.8.8 should not be loopback")
	}
	if isLoopback("example.com") {
		t.Error("non-ip hostname should not be loopback")
	}
}

// --- IsHostSafe ---

func TestIsHostSafe(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		wantErr bool
	}{
		{"empty ok", "", false},
		{"loopback allowed", "127.0.0.1", false},
		{"private allowed", "10.0.0.1", false},
		{"metadata blocked", "169.254.169.254", true},
		{"link-local blocked", "169.254.1.1", true},
		{"documentation blocked", "192.0.2.1", true},
		{"unspecified blocked", "0.0.0.0", true},
		{"public ip ok", "8.8.8.8", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := IsHostSafe(tt.host)
			if (err != nil) != tt.wantErr {
				t.Fatalf("IsHostSafe(%q) err=%v wantErr=%v", tt.host, err, tt.wantErr)
			}
		})
	}
}

// IsHostSafe with an unresolvable hostname should not error (DNS may be down).
func TestIsHostSafe_UnresolvableHostname(t *testing.T) {
	host := "this-host-should-not-resolve.invalid"
	if err := IsHostSafe(host); err != nil {
		t.Fatalf("unresolvable host should be allowed, got %v", err)
	}
}

// --- isURLSafe via public wrappers (IP-only, deterministic) ---

func TestURLSafetyWrappers(t *testing.T) {
	tests := []struct {
		name    string
		fn      func(string) error
		url     string
		wantErr bool
	}{
		{"webhook blocks loopback", IsWebhookURLSafe, "http://127.0.0.1/x", true},
		{"webhook blocks private", IsWebhookURLSafe, "http://10.0.0.1/x", true},
		{"webhook allows public", IsWebhookURLSafe, "http://8.8.8.8/x", false},
		{"webhook empty ok", IsWebhookURLSafe, "", false},
		{"webhook no host", IsWebhookURLSafe, "notaurl", true},
		{"proxy allows loopback", IsProxyUpstreamSafe, "http://127.0.0.1:3000", false},
		{"proxy blocks private", IsProxyUpstreamSafe, "http://10.0.0.1:8080", true},
		{"private proxy allows private", IsPrivateProxyUpstreamSafe, "http://10.0.0.1:8080", false},
		{"private proxy blocks metadata", IsPrivateProxyUpstreamSafe, "http://169.254.169.254/", true},
		{"invalid url scheme parse", IsWebhookURLSafe, "http://[::1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn(tt.url)
			if (err != nil) != tt.wantErr {
				t.Fatalf("%s(%q) err=%v wantErr=%v", tt.name, tt.url, err, tt.wantErr)
			}
		})
	}
}

// isURLSafe with allowLoopback + a known loopback NAME short-circuits before DNS.
func TestIsURLSafe_LoopbackHostnameAllowed(t *testing.T) {
	if err := IsProxyUpstreamSafe("http://localhost:3000"); err != nil {
		t.Fatalf("localhost upstream should be allowed: %v", err)
	}
}

// isURLSafe with an unresolvable hostname returns nil (DNS path).
func TestIsURLSafe_UnresolvableHostname(t *testing.T) {
	if err := IsWebhookURLSafe("http://this-host-should-not-resolve.invalid/path"); err != nil {
		t.Fatalf("unresolvable hostname should be allowed: %v", err)
	}
}

func mustIP(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatalf("bad test IP %q", s)
	}
	return ip
}

// --- MarshalYAML coverage (yamlMapFromStruct / yamlFieldName / yamlValue / yamlEmpty) ---

func TestDomainMarshalYAML_ForcedAndOmitted(t *testing.T) {
	d := Domain{
		Host: "example.com",
		Type: "static",
		// SSL is forced even when zero; Root omitted when empty.
	}
	out, err := d.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", out)
	}
	if _, ok := m["host"]; !ok {
		t.Error("forced field host missing")
	}
	if _, ok := m["ssl"]; !ok {
		t.Error("forced field ssl missing")
	}
	if _, ok := m["root"]; ok {
		t.Error("empty root should be omitted")
	}
}

func TestDomainMarshalYAML_NestedTypes(t *testing.T) {
	// Exercise map (ErrorPages), slice of structs (Locations) with a pointer
	// field (BasicAuth), and Duration/ByteSize custom marshalers.
	d := Domain{
		Host:       "example.com",
		Type:       "static",
		Root:       "/srv/www",
		ErrorPages: map[int]string{404: "/404.html"},
		Locations: []LocationConfig{
			{
				Match:     "/admin",
				BasicAuth: &BasicAuthConfig{Enabled: true, Users: map[string]string{"u": "p"}},
				Headers:   map[string]string{"X-Test": "1"},
			},
		},
	}
	d.Cache.TTL = 0
	d.Bandwidth.MonthlyLimit = 5 * GB

	out, err := d.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	// Round-trip through the yaml encoder to ensure the produced structure is
	// serializable (catches nil/invalid value branches).
	if _, err := yaml.Marshal(out); err != nil {
		t.Fatalf("yaml.Marshal of MarshalYAML output: %v", err)
	}
	m := out.(map[string]any)
	if _, ok := m["error_pages"]; !ok {
		t.Error("error_pages map should be present")
	}
	if _, ok := m["locations"]; !ok {
		t.Error("locations slice should be present")
	}
}

// yamlValue must handle a nil pointer field (BasicAuth nil) inside a location
// without panicking, and a nil map.
func TestDomainMarshalYAML_NilPointerAndMap(t *testing.T) {
	d := Domain{
		Host: "example.com",
		Type: "static",
		Root: "/srv",
		Locations: []LocationConfig{
			{Match: "/x", BasicAuth: nil, Headers: nil},
		},
	}
	out, err := d.MarshalYAML()
	if err != nil {
		t.Fatalf("MarshalYAML: %v", err)
	}
	if _, err := yaml.Marshal(out); err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
}
