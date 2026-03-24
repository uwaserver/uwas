package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"text/tabwriter"

	"github.com/uwaserver/uwas/internal/phpmanager"
)

// PHPCommand manages PHP installations via the admin API.
type PHPCommand struct{}

func (p *PHPCommand) Name() string { return "php" }
func (p *PHPCommand) Description() string {
	return "Manage PHP installations (list, start, stop, config, extensions)"
}

func (p *PHPCommand) Help() string {
	return `Subcommands:
  list                    List detected PHP versions
  install <version>       Install PHP (auto-detects OS, needs root)
  install-info <version>  Show install commands without running them
  start <version>         Start PHP-CGI on a port (default 9000)
  stop <version>          Stop PHP-CGI for a version
  config <version>        Show PHP configuration
  extensions <version>    List enabled extensions

Flags:
  --api-url string   Admin API URL (default auto-detected from config)
  --api-key string   Admin API key (env: UWAS_ADMIN_KEY)
  --port string      Listen port for 'start' (default "9000")

Examples:
  uwas php list
  uwas php install 8.4
  uwas php install-info 8.3
  uwas php start 8.4 --port 9001
  uwas php config 8.4`
}

func (p *PHPCommand) Run(args []string) error {
	if len(args) == 0 {
		fmt.Println(p.Help())
		return nil
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "list":
		return p.list(subArgs)
	case "install":
		return p.install(subArgs)
	case "install-info":
		return p.installInfo(subArgs)
	case "start":
		return p.start(subArgs)
	case "stop":
		return p.stop(subArgs)
	case "config":
		return p.config(subArgs)
	case "extensions", "ext":
		return p.extensions(subArgs)
	default:
		return fmt.Errorf("unknown subcommand %q (use: list, install, start, stop, config, extensions)", sub)
	}
}

func (p *PHPCommand) list(args []string) error {
	fs := flag.NewFlagSet("php list", flag.ContinueOnError)
	apiURL := fs.String("api-url", "", "admin API URL")
	apiKey := fs.String("api-key", os.Getenv("UWAS_ADMIN_KEY"), "admin API key")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *apiURL == "" {
		*apiURL = adminURLFromConfig()
	}

	body, err := apiRequest("GET", *apiURL+"/api/v1/php", *apiKey, nil)
	if err != nil {
		return err
	}

	var installs []struct {
		Version    string   `json:"version"`
		Binary     string   `json:"binary"`
		SAPI       string   `json:"sapi"`
		ConfigFile string   `json:"config_file"`
		Extensions []string `json:"extensions"`
		Running    bool     `json:"running"`
		ListenAddr string   `json:"listen_addr"`
		PID        int      `json:"pid"`
	}
	if err := json.Unmarshal(body, &installs); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	if len(installs) == 0 {
		fmt.Println("No PHP installations detected.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "VERSION\tSAPI\tSTATUS\tLISTEN\tBINARY\n")
	for _, inst := range installs {
		status := "stopped"
		listen := "-"
		if inst.Running {
			status = fmt.Sprintf("running (pid %d)", inst.PID)
			listen = inst.ListenAddr
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", inst.Version, inst.SAPI, status, listen, inst.Binary)
	}
	w.Flush()
	return nil
}

func (p *PHPCommand) start(args []string) error {
	fs := flag.NewFlagSet("php start", flag.ContinueOnError)
	apiURL := fs.String("api-url", "", "admin API URL")
	apiKey := fs.String("api-key", os.Getenv("UWAS_ADMIN_KEY"), "admin API key")
	port := fs.String("port", "9000", "listen port")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *apiURL == "" {
		*apiURL = adminURLFromConfig()
	}

	version := fs.Arg(0)
	if version == "" {
		return fmt.Errorf("version is required: uwas php start <version> [--port 9000]")
	}

	payload := fmt.Sprintf(`{"listen_addr":"127.0.0.1:%s"}`, *port)
	body, err := apiRequest("POST", *apiURL+"/api/v1/php/"+version+"/start", *apiKey, strings.NewReader(payload))
	if err != nil {
		return err
	}

	var result map[string]any
	json.Unmarshal(body, &result)
	fmt.Printf("PHP %s started on 127.0.0.1:%s\n", version, *port)
	return nil
}

func (p *PHPCommand) stop(args []string) error {
	fs := flag.NewFlagSet("php stop", flag.ContinueOnError)
	apiURL := fs.String("api-url", "", "admin API URL")
	apiKey := fs.String("api-key", os.Getenv("UWAS_ADMIN_KEY"), "admin API key")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *apiURL == "" {
		*apiURL = adminURLFromConfig()
	}

	version := fs.Arg(0)
	if version == "" {
		return fmt.Errorf("version is required: uwas php stop <version>")
	}

	_, err := apiRequest("POST", *apiURL+"/api/v1/php/"+version+"/stop", *apiKey, nil)
	if err != nil {
		return err
	}

	fmt.Printf("PHP %s stopped\n", version)
	return nil
}

func (p *PHPCommand) config(args []string) error {
	fs := flag.NewFlagSet("php config", flag.ContinueOnError)
	apiURL := fs.String("api-url", "", "admin API URL")
	apiKey := fs.String("api-key", os.Getenv("UWAS_ADMIN_KEY"), "admin API key")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *apiURL == "" {
		*apiURL = adminURLFromConfig()
	}

	version := fs.Arg(0)
	if version == "" {
		return fmt.Errorf("version is required: uwas php config <version>")
	}

	body, err := apiRequest("GET", *apiURL+"/api/v1/php/"+version+"/config", *apiKey, nil)
	if err != nil {
		return err
	}

	var cfg map[string]any
	if err := json.Unmarshal(body, &cfg); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	fmt.Printf("PHP %s Configuration\n", version)
	fmt.Println("═══════════════════════════════════════")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for k, v := range cfg {
		fmt.Fprintf(w, "  %s\t%v\n", k, v)
	}
	w.Flush()
	fmt.Println("═══════════════════════════════════════")
	return nil
}

func (p *PHPCommand) extensions(args []string) error {
	fs := flag.NewFlagSet("php extensions", flag.ContinueOnError)
	apiURL := fs.String("api-url", "", "admin API URL")
	apiKey := fs.String("api-key", os.Getenv("UWAS_ADMIN_KEY"), "admin API key")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *apiURL == "" {
		*apiURL = adminURLFromConfig()
	}

	version := fs.Arg(0)
	if version == "" {
		return fmt.Errorf("version is required: uwas php extensions <version>")
	}

	body, err := apiRequest("GET", *apiURL+"/api/v1/php/"+version+"/extensions", *apiKey, nil)
	if err != nil {
		return err
	}

	var exts []string
	if err := json.Unmarshal(body, &exts); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	fmt.Printf("PHP %s Extensions (%d)\n", version, len(exts))
	fmt.Println("═══════════════════════════════════════")
	for _, ext := range exts {
		fmt.Printf("  %s\n", ext)
	}
	fmt.Println("═══════════════════════════════════════")
	return nil
}

func (p *PHPCommand) installInfo(args []string) error {
	version := "8.3"
	if len(args) > 0 {
		version = args[0]
	}

	info := phpmanager.GetInstallInfo(version)

	fmt.Printf("\n  PHP %s Install Guide (%s)\n\n", version, info.Distro)
	for i, cmd := range info.Commands {
		fmt.Printf("  %d. %s\n", i+1, cmd)
	}
	if info.Notes != "" {
		fmt.Printf("\n  Note: %s\n", info.Notes)
	}
	fmt.Printf("\n  After installing, run: uwas php list\n\n")
	return nil
}

func (p *PHPCommand) install(args []string) error {
	version := "8.3"
	if len(args) > 0 {
		version = args[0]
	}

	info := phpmanager.GetInstallInfo(version)
	fmt.Printf("\n  Installing PHP %s on %s\n\n", version, info.Distro)
	for _, cmd := range info.Commands {
		fmt.Printf("  > %s\n", cmd)
	}
	fmt.Println()

	// Check root (Unix only)
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		fmt.Println("  \033[33m!\033[0m Root access required. Run with sudo:")
		fmt.Printf("    sudo uwas php install %s\n\n", version)
		return nil
	}

	fmt.Println("  Running install...")
	output, err := phpmanager.RunInstall(version)
	if output != "" {
		fmt.Print(output)
	}
	if err != nil {
		return fmt.Errorf("install failed: %w", err)
	}
	fmt.Println("\n  \033[32m✓\033[0m PHP installed. Run 'uwas php list' to verify.")
	return nil
}
