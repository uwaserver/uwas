package config

import (
	"fmt"
	"strings"
)

func validate(cfg *Config) error {
	var errs []string

	// Global validation
	switch cfg.Global.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Sprintf("invalid log_level: %q (must be debug, info, warn, error)", cfg.Global.LogLevel))
	}

	switch cfg.Global.LogFormat {
	case "json", "text", "clf":
	default:
		errs = append(errs, fmt.Sprintf("invalid log_format: %q (must be json, text, clf)", cfg.Global.LogFormat))
	}

	// Domain validation
	hosts := make(map[string]bool)
	for i, d := range cfg.Domains {
		prefix := fmt.Sprintf("domains[%d]", i)

		if d.Host == "" {
			errs = append(errs, fmt.Sprintf("%s: host is required", prefix))
			continue
		}

		if hosts[d.Host] {
			errs = append(errs, fmt.Sprintf("%s: duplicate host %q", prefix, d.Host))
		}
		hosts[d.Host] = true

		for _, alias := range d.Aliases {
			if hosts[alias] {
				errs = append(errs, fmt.Sprintf("%s: duplicate alias %q", prefix, alias))
			}
			hosts[alias] = true
		}

		switch d.Type {
		case "static", "php", "proxy", "redirect":
		default:
			errs = append(errs, fmt.Sprintf("%s: invalid type %q (must be static, php, proxy, redirect)", prefix, d.Type))
		}

		switch d.SSL.Mode {
		case "auto", "manual", "off":
		default:
			errs = append(errs, fmt.Sprintf("%s: invalid ssl.mode %q (must be auto, manual, off)", prefix, d.SSL.Mode))
		}

		if d.SSL.Mode == "manual" {
			if d.SSL.Cert == "" {
				errs = append(errs, fmt.Sprintf("%s: ssl.cert required when mode is manual", prefix))
			}
			if d.SSL.Key == "" {
				errs = append(errs, fmt.Sprintf("%s: ssl.key required when mode is manual", prefix))
			}
		}

		// Root required for static and php
		if (d.Type == "static" || d.Type == "php") && d.Root == "" {
			errs = append(errs, fmt.Sprintf("%s: root is required for type %q", prefix, d.Type))
		}

		// Proxy validation
		if d.Type == "proxy" && len(d.Proxy.Upstreams) == 0 {
			errs = append(errs, fmt.Sprintf("%s: proxy.upstreams required for type proxy", prefix))
		}

		// Redirect validation
		if d.Type == "redirect" && d.Redirect.Target == "" {
			errs = append(errs, fmt.Sprintf("%s: redirect.target required for type redirect", prefix))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}
