package config

import "testing"

// A config with autoblock enabled but the numeric thresholds left at zero is
// exactly what an older uwas.yaml (or a restored backup) looks like. The loader
// fills those with defaults before validating, so it loads fine — the raw
// config editor's save gate must behave identically, or saving a loadable
// config fails with "max_rate_hits must be greater than 0".
func TestValidateWithDefaultsAcceptsZeroAutoblockThresholds(t *testing.T) {
	mk := func() *Config {
		return &Config{
			Global: GlobalConfig{
				LogLevel: "info", LogFormat: "json",
				Admin:   AdminConfig{Listen: "127.0.0.1:9443"},
				WebRoot: "/var/www",
				AutoBlock: AutoBlockConfig{Enabled: true}, // no thresholds set
			},
		}
	}

	// Bare Validate rejects it — the zero thresholds are invalid.
	if err := Validate(mk()); err == nil {
		t.Fatal("Validate should reject autoblock enabled with zero thresholds")
	}

	// ValidateWithDefaults fills the defaults first, so it accepts the same config.
	cfg := mk()
	if err := ValidateWithDefaults(cfg); err != nil {
		t.Fatalf("ValidateWithDefaults should accept it after defaulting: %v", err)
	}
	if cfg.Global.AutoBlock.MaxRateHits != 120 {
		t.Errorf("expected max_rate_hits defaulted to 120, got %d", cfg.Global.AutoBlock.MaxRateHits)
	}
}
