package config

// AutoBlockConfig controls automatic source-IP blocking.
//
// The thresholds are deliberately split between connection-level and
// request-level signals. Only the connection-level ones can see a TLS
// handshake flood: those connections die before any HTTP request exists, so
// the WAF and rate limiter never get a look at them.
type AutoBlockConfig struct {
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`

	// Window is the counting period for every threshold below.
	Window Duration `yaml:"window,omitempty" json:"window,omitempty"`

	// Connection-level thresholds, per source IP per window.
	MaxConnections int `yaml:"max_connections,omitempty" json:"max_connections,omitempty"`
	MaxAborts      int `yaml:"max_aborts,omitempty" json:"max_aborts,omitempty"`
	MaxConcurrent  int `yaml:"max_concurrent,omitempty" json:"max_concurrent,omitempty"`

	// Request-level thresholds, per source IP per window.
	MaxWAFHits  int `yaml:"max_waf_hits,omitempty" json:"max_waf_hits,omitempty"`
	MaxRateHits int `yaml:"max_rate_hits,omitempty" json:"max_rate_hits,omitempty"`
	MaxNotFound int `yaml:"max_not_found,omitempty" json:"max_not_found,omitempty"`

	BlockDuration    Duration `yaml:"block_duration,omitempty" json:"block_duration,omitempty"`
	MaxBlockDuration Duration `yaml:"max_block_duration,omitempty" json:"max_block_duration,omitempty"`
	Escalate         bool     `yaml:"escalate,omitempty" json:"escalate,omitempty"`

	// DryRun detects and logs without enforcing. Run this first on a busy
	// server: thresholds that are wrong in the tight direction take real
	// visitors offline.
	DryRun bool `yaml:"dry_run,omitempty" json:"dry_run,omitempty"`

	// FirewallSync pushes blocks into ufw/iptables. Without it a blocked IP
	// still completes a TCP handshake before being dropped, which is enough
	// to keep a large flood expensive.
	FirewallSync bool `yaml:"firewall_sync,omitempty" json:"firewall_sync,omitempty"`

	// Whitelist never gets blocked. trusted_proxies and the Cloudflare edge
	// ranges are added automatically — behind a CDN every connection carries
	// an edge IP, and blocking one takes the site down for everyone it serves.
	Whitelist []string `yaml:"whitelist,omitempty" json:"whitelist,omitempty"`

	// StatePath persists active blocks across restarts.
	StatePath string `yaml:"state_path,omitempty" json:"state_path,omitempty"`
}

// WatchdogConfig controls the liveness probe that backs systemd's watchdog.
//
// systemd's Restart= only reacts to a process that exits. A server wedged by
// a flood — accept loop saturated, every worker blocked — stays "active" and
// answers nothing, which is exactly the state that produced the down alert
// this was written for. The watchdog makes that state fatal so the restart
// policy can act on it.
type WatchdogConfig struct {
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`

	// Interval between probes.
	Interval Duration `yaml:"interval,omitempty" json:"interval,omitempty"`

	// Timeout for a single probe.
	Timeout Duration `yaml:"timeout,omitempty" json:"timeout,omitempty"`

	// Failures is how many consecutive failed probes count as wedged.
	Failures int `yaml:"failures,omitempty" json:"failures,omitempty"`

	// SelfRestart exits the process (letting systemd restart it) when the
	// probe fails Failures times in a row and no systemd watchdog is
	// available to do it. Off by default: under systemd, notification is the
	// safer mechanism, since systemd owns the restart policy and its
	// rate limiting.
	SelfRestart bool `yaml:"self_restart,omitempty" json:"self_restart,omitempty"`
}
