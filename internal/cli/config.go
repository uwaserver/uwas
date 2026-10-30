package cli

import (
	"flag"
	"fmt"
	"os"

	"github.com/uwaserver/uwas/internal/config"
)

// ConfigCommand validates a config file.
type ConfigCommand struct{}

func (c *ConfigCommand) Name() string        { return "config" }
func (c *ConfigCommand) Description() string { return "Validate configuration file" }

func (c *ConfigCommand) Run(args []string) error {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	configPath := fs.String("c", "", "path to config file (default: found the way 'uwas serve' finds it)")
	fs.StringVar(configPath, "config", "", "path to config file (same as -c)")

	subcommand := "validate"
	if len(args) > 0 && args[0] != "-c" && args[0] != "--config" {
		subcommand = args[0]
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Validate the file the server would start with: serve resolves its config
	// through findConfig (./uwas.yaml, ./.uwas, ~/.uwas, /etc/uwas), so a bare
	// "uwas config validate" must too (F14300).
	path, _ := findConfig(*configPath)

	switch subcommand {
	case "validate":
		cfg, err := config.Load(path)
		if err != nil {
			return fmt.Errorf("invalid: %w", err)
		}
		printConfigWarnings(cfg)
		fmt.Printf("Configuration valid: %d domains\n", len(cfg.Domains))
		return nil

	case "test":
		cfg, err := config.Load(path)
		if err != nil {
			return fmt.Errorf("invalid: %w", err)
		}
		printConfigWarnings(cfg)
		fmt.Printf("Config OK — %d domains loaded\n", len(cfg.Domains))
		for _, d := range cfg.Domains {
			fmt.Printf("  %s (type=%s, ssl=%s)\n", d.Host, d.Type, d.SSL.Mode)
		}
		return nil

	default:
		return fmt.Errorf("unknown subcommand %q (use: validate, test)", subcommand)
	}
}

// printConfigWarnings says what the loader noticed and could not act on, the
// same lines the server logs at startup and on reload (a key the schema does
// not know is otherwise ignored without a word, F14300).
func printConfigWarnings(cfg *config.Config) {
	for _, w := range cfg.Warnings {
		fmt.Fprintln(os.Stderr, "warning: config: "+w)
	}
}
