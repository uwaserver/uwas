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
	daemon := fs.Bool("d", false, "run as daemon (background)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	// Daemon mode: re-exec as detached process and exit parent
	if *daemon && os.Getenv("UWAS_DAEMON") != "1" {
		return daemonize(os.Args[1:])
	}

	// Find or create config
	cfgFile, found := findConfig(*configPath)

	// If user explicitly specified a config path that doesn't exist, fail immediately
	if !found && *configPath != "" {
		return fmt.Errorf("load config: read config: open %s: The system cannot find the file specified", *configPath)
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

		// Check for conflicting web servers
		if conflicts := DetectConflicts(); len(conflicts) > 0 {
			if PrintConflicts(conflicts) {
				OfferStopConflicts(conflicts)
			} else {
				fmt.Println()
			}
		}

		// Check for PHP and offer installation
		OfferPHPInstall()

		hp := *httpPort
		ap := *adminPort

		if hp == "" {
			hp = promptWithDefault("  HTTP port", "8080")
		}
		if ap == "" {
			ap = promptWithDefault("  Admin port", "9443")
		}

		// Ask about admin bind address
		adminBind := "0.0.0.0"
		fmt.Println()
		fmt.Println("  The admin dashboard lets you manage domains, view logs, and monitor your server.")
		fmt.Println("  Binding to \033[1m0.0.0.0\033[0m makes it accessible from any IP (for remote servers).")
		fmt.Println("  Binding to \033[1m127.0.0.1\033[0m restricts it to localhost only.")
		adminBind = promptWithDefault("  Admin bind address", "0.0.0.0")

		// Ask about web root
		fmt.Println()
		fmt.Println("  Domain web files will be stored under the web root directory.")
		fmt.Println("  Each domain gets its own subdirectory (e.g. /var/www/example.com/public_html).")
		webRoot := promptWithDefault("  Web root base directory", "/var/www")

		// Ask ACME email for auto SSL
		fmt.Println()
		fmt.Println("  For automatic HTTPS (Let's Encrypt), an email is required.")
		fmt.Println("  Leave empty to skip — you can add it later in the config.")
		acmeEmail := promptWithDefault("  ACME email (for Let's Encrypt)", "")

		// Explain the API key
		fmt.Println()
		fmt.Println("  \033[33m!\033[0m An API key will be generated to protect your dashboard and API.")
		fmt.Println("    \033[1mSave this key\033[0m — you'll need it to log into the dashboard and make API calls.")
		fmt.Println("    It will also be stored in \033[1m~/.uwas/.env\033[0m as UWAS_ADMIN_KEY.")

		var err error
		cfgFile, err = ensureDefaultConfig(hp, ap, adminBind, webRoot, acmeEmail)
		if err != nil {
			return fmt.Errorf("setup: %w", err)
		}

		// Ask about daemon mode
		if !*daemon {
			fmt.Println()
			daemonChoice := promptWithDefault("  Run as background daemon? (y/n)", "n")
			if strings.EqualFold(daemonChoice, "y") || strings.EqualFold(daemonChoice, "yes") {
				fmt.Println("  Starting UWAS in background...")
				return daemonize(filterArg(os.Args[1:], "-d"))
			}
		}
	}

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Check if another instance is already running.
	if pid, ok := readAlivePID(cfg.Global.PIDFile); ok {
		fmt.Println()
		fmt.Println("  \033[33mUWAS is already running\033[0m")
		fmt.Println()
		fmt.Printf("    PID:       %d\n", pid)
		fmt.Printf("    Config:    %s\n", cfgFile)
		fmt.Printf("    HTTP:      %s\n", cfg.Global.HTTPListen)
		if cfg.Global.HTTPSListen != "" {
			fmt.Printf("    HTTPS:     %s\n", cfg.Global.HTTPSListen)
		}
		if cfg.Global.Admin.Enabled {
			fmt.Printf("    Dashboard: http://%s/_uwas/dashboard/\n", cfg.Global.Admin.Listen)
		}
		fmt.Printf("    Domains:   %d configured\n", len(cfg.Domains))
		fmt.Println()
		fmt.Println("  Use \033[1muwas stop\033[0m to stop the running instance, or")
		fmt.Println("  \033[1muwas serve -c other.yaml\033[0m to run with a different config.")
		fmt.Println()
		return nil
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

// filterArg ensures the given flag is present in args, appending it if missing.
func filterArg(args []string, flag string) []string {
	for _, a := range args {
		if a == flag {
			return args
		}
	}
	return append(args, flag)
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
