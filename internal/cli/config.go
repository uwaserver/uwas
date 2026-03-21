package cli

import (
	"flag"
	"fmt"

	"github.com/uwaserver/uwas/internal/config"
)

// ConfigCommand validates a config file.
type ConfigCommand struct{}

func (c *ConfigCommand) Name() string        { return "config" }
func (c *ConfigCommand) Description() string  { return "Validate configuration file" }

func (c *ConfigCommand) Run(args []string) error {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	configPath := fs.String("c", "uwas.yaml", "path to config file")

	subcommand := "validate"
	if len(args) > 0 && args[0] != "-c" {
		subcommand = args[0]
		args = args[1:]
	}
	fs.Parse(args)

	switch subcommand {
	case "validate":
		cfg, err := config.Load(*configPath)
		if err != nil {
			return fmt.Errorf("invalid: %w", err)
		}
		fmt.Printf("Configuration valid: %d domains\n", len(cfg.Domains))
		return nil

	case "test":
		cfg, err := config.Load(*configPath)
		if err != nil {
			return fmt.Errorf("invalid: %w", err)
		}
		fmt.Printf("Config OK — %d domains loaded\n", len(cfg.Domains))
		for _, d := range cfg.Domains {
			fmt.Printf("  %s (type=%s, ssl=%s)\n", d.Host, d.Type, d.SSL.Mode)
		}
		return nil

	default:
		return fmt.Errorf("unknown subcommand %q (use: validate, test)", subcommand)
	}
}
