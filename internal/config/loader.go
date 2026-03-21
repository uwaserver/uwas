package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var envPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// Expand environment variables: ${VAR} -> value
	expanded := expandEnvVars(string(data))

	cfg := &Config{}
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	applyDefaults(cfg)

	if err := validate(cfg); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return cfg, nil
}

func expandEnvVars(s string) string {
	return envPattern.ReplaceAllStringFunc(s, func(match string) string {
		varName := match[2 : len(match)-1] // strip ${ and }

		// Support ${VAR:-default}
		if name, fallback, ok := strings.Cut(varName, ":-"); ok {
			if val, found := os.LookupEnv(name); found {
				return val
			}
			return fallback
		}

		if val, ok := os.LookupEnv(varName); ok {
			return val
		}
		return match // keep original if not found
	})
}
