package config

// ACMEConfig configures automatic TLS via ACME (Let's Encrypt etc.).
type ACMEConfig struct {
	Email              string            `yaml:"email"`
	CAURL              string            `yaml:"ca_url"`
	Storage            string            `yaml:"storage"`
	DNSProvider        string            `yaml:"dns_provider"`
	DNSCredentials     map[string]string `yaml:"dns_credentials"`
	OnDemand           bool              `yaml:"on_demand"`
	OnDemandAsk        string            `yaml:"on_demand_ask"`
	SelfSignedValidity Duration          `yaml:"self_signed_validity"` // validity period for self-signed fallback certs (default 24h)
}
