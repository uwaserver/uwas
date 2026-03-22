package cli

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/server"
)

type ServeCommand struct{}

func (s *ServeCommand) Name() string        { return "serve" }
func (s *ServeCommand) Description() string { return "Start the UWAS server" }

func (s *ServeCommand) Help() string {
	return `Flags:
  -c, --config string   Path to config file (auto-detected if omitted)
  --http-port string    HTTP port (default "80", used for first-run setup)
  --admin-port string   Admin API port (default "9443", used for first-run setup)
  --log-level string    Log level: debug, info, warn, error
  --no-banner           Suppress the startup banner
  -d                    Run as daemon (background, detached)

If no config file is found, UWAS creates a default configuration in ~/.uwas/
and starts with sensible defaults.`
}

func (s *ServeCommand) Run(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	configPath := fs.String("c", "", "path to config file")
	logLevel := fs.String("log-level", "", "log level override")
	httpPort := fs.String("http-port", "", "HTTP port for first-run setup")
	adminPort := fs.String("admin-port", "", "admin API port for first-run setup")
	noBanner := fs.Bool("no-banner", false, "suppress startup banner")
	// daemon := fs.Bool("d", false, "run as daemon") // TODO: platform-specific daemon

	if err := fs.Parse(args); err != nil {
		return err
	}

	// Find or create config
	cfgFile, found := findConfig(*configPath)

	// If user explicitly specified a config path that doesn't exist, fail immediately
	if !found && *configPath != "" {
		return fmt.Errorf("load config: read config: open %s: The system cannot find the file specified.", *configPath)
	}

	if !found {
		// First-run experience
		fmt.Print("\033[36m")
		fmt.Println(`
  _   ___      __  _   ___
 | | | \ \    / / /_\ / __|
 | |_| |\ \/\/ / / _ \\__ \
  \___/  \_/\_/ /_/ \_\___/`)
		fmt.Print("\033[0m")
		fmt.Println()
		fmt.Println("  Welcome to \033[1mUWAS\033[0m — Unified Web Application Server")
		fmt.Println("  No configuration file found. Let's set up your server.")
		fmt.Println()

		hp := *httpPort
		ap := *adminPort

		if hp == "" {
			hp = promptWithDefault("  HTTP port", "8080")
		}
		if ap == "" {
			ap = promptWithDefault("  Admin port", "9443")
		}

		var err error
		cfgFile, err = ensureDefaultConfig(hp, ap)
		if err != nil {
			return fmt.Errorf("setup: %w", err)
		}
	}

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// CLI flag overrides
	if *logLevel != "" {
		cfg.Global.LogLevel = *logLevel
	}

	log := logger.New(cfg.Global.LogLevel, cfg.Global.LogFormat)

	// Print startup banner
	if !*noBanner {
		PrintBanner(cfg, cfgFile)
	} else {
		log.Info("configuration loaded", "path", cfgFile, "domains", len(cfg.Domains))
	}

	srv := server.New(cfg, log)
	srv.SetConfigPath(cfgFile)
	return srv.Start()
}

// promptWithDefault reads a line from stdin with a default value.
func promptWithDefault(prompt, defaultVal string) string {
	fmt.Printf("%s [%s]: ", prompt, defaultVal)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultVal
	}
	return line
}
