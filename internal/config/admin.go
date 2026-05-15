package config

// AdminConfig controls the admin REST API server + dashboard.
type AdminConfig struct {
	Listen        string         `yaml:"listen"`
	Enabled       bool           `yaml:"enabled"`
	APIKey        string         `yaml:"api_key"`
	PinCode       string         `yaml:"pin_code,omitempty"`
	TOTPSecret    string         `yaml:"totp_secret,omitempty"`
	TLSCert       string         `yaml:"tls_cert,omitempty"`
	TLSKey        string         `yaml:"tls_key,omitempty"`
	RecoveryCodes []string       `yaml:"recovery_codes,omitempty"` // 2FA recovery codes
	Branding      BrandingConfig `yaml:"branding,omitempty"`
	OAuth         OAuthConfig    `yaml:"oauth,omitempty"`
}

// OAuthConfig enables OAuth2/SSO login (Google, GitHub).
type OAuthConfig struct {
	Enabled       bool     `yaml:"enabled,omitempty" json:"enabled,omitempty"`
	GoogleID      string   `yaml:"google_client_id,omitempty" json:"google_client_id,omitempty"`
	GoogleSecret  string   `yaml:"google_client_secret,omitempty" json:"google_client_secret,omitempty"`
	GitHubID      string   `yaml:"github_client_id,omitempty" json:"github_client_id,omitempty"`
	GitHubSecret  string   `yaml:"github_client_secret,omitempty" json:"github_client_secret,omitempty"`
	AllowedEmails []string `yaml:"allowed_emails,omitempty" json:"allowed_emails,omitempty"` // restrict to specific emails
}

// BrandingConfig allows white-labeling the dashboard.
type BrandingConfig struct {
	Name         string `yaml:"name,omitempty" json:"name,omitempty"`           // e.g. "My Hosting Panel"
	LogoURL      string `yaml:"logo_url,omitempty" json:"logo_url,omitempty"`   // URL or data: URI
	FaviconURL   string `yaml:"favicon_url,omitempty" json:"favicon_url,omitempty"`
	PrimaryColor string `yaml:"primary_color,omitempty" json:"primary_color,omitempty"` // hex color
	FooterText   string `yaml:"footer_text,omitempty" json:"footer_text,omitempty"`
}

// AuditConfig controls audit log behavior.
type AuditConfig struct {
	RecordIP bool `yaml:"record_ip"` // whether to record client IP addresses (GDPR compliance)
}

// MCPConfig controls the built-in MCP server for AI agent access.
type MCPConfig struct {
	Enabled bool   `yaml:"enabled"`
	Listen  string `yaml:"listen"`
}
