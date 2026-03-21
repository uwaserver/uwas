package cli

import (
	"flag"
	"fmt"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/server"
)

type ServeCommand struct{}

func (s *ServeCommand) Name() string        { return "serve" }
func (s *ServeCommand) Description() string  { return "Start the UWAS server" }

func (s *ServeCommand) Help() string {
	return `Flags:
  -c, --config string   Path to config file (default "uwas.yaml")
  --log-level string    Log level: debug, info, warn, error (default "info")`
}

func (s *ServeCommand) Run(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	configPath := fs.String("c", "uwas.yaml", "path to config file")
	logLevel := fs.String("log-level", "", "log level override")

	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// CLI flag overrides config
	if *logLevel != "" {
		cfg.Global.LogLevel = *logLevel
	}

	log := logger.New(cfg.Global.LogLevel, cfg.Global.LogFormat)
	log.Info("configuration loaded", "path", *configPath, "domains", len(cfg.Domains))

	srv := server.New(cfg, log)
	return srv.Start()
}
