package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/uwaserver/uwas/internal/phpmanager"
)

// phpRunInstall is a test seam for the PHP install path, which shells out to
// apt / add-apt-repository. TestMain points it at a no-op.
var phpRunInstall = phpmanager.RunInstall

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
  update [version|all]    Upgrade installed PHP to the newest patch release
  uninstall <version>     Remove a PHP version (refused while domains use it)
  start <version>         Start PHP-CGI on a port (default 9000)
  stop <version>          Stop PHP-CGI for a version
  config <version>        Show PHP configuration
  extensions <version>    List enabled extensions

Flags:
  --api-url string   Admin API URL (default auto-detected from config)
  --api-key string   Admin API key (env: UWAS_ADMIN_KEY)
  --port string      Listen port for 'start' (default "9000")
  --purge            With 'uninstall': also delete the version's configuration

Examples:
  uwas php list
  uwas php install 8.4
  uwas php update all
  uwas php uninstall 8.1 --purge
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
	case "update":
		return p.update(subArgs)
	case "uninstall":
		return p.uninstall(subArgs)
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
		return fmt.Errorf("unknown subcommand %q (use: list, install, update, uninstall, start, stop, config, extensions)", sub)
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

	raw, err := apiGetItems(*apiURL, "/api/v1/php", *apiKey)
	if err != nil {
		return fmt.Errorf("list PHP installations: %w", err)
	}
	type phpInstall struct {
		Version    string   `json:"version"`
		Binary     string   `json:"binary"`
		SAPI       string   `json:"sapi"`
		ConfigFile string   `json:"config_file"`
		Extensions []string `json:"extensions"`
		Running    bool     `json:"running"`
		ListenAddr string   `json:"listen_addr"`
		PID        int      `json:"pid"`
		EOL        bool     `json:"eol"`
		Advisory   string   `json:"advisory"`
	}
	installs := make([]phpInstall, 0, len(raw))
	for _, item := range raw {
		var inst phpInstall
		if err := json.Unmarshal(item, &inst); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
		installs = append(installs, inst)
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
	for _, inst := range installs {
		if inst.Advisory != "" {
			fmt.Printf("\n  ! %s\n", inst.Advisory)
			if !inst.EOL {
				fmt.Println("    Fix: uwas php update", shortPHPVersion(inst.Version))
			}
		}
	}
	return nil
}

func (p *PHPCommand) start(args []string) error {
	fs := flag.NewFlagSet("php start", flag.ContinueOnError)
	apiURL := fs.String("api-url", "", "admin API URL")
	apiKey := fs.String("api-key", os.Getenv("UWAS_ADMIN_KEY"), "admin API key")
	port := fs.String("port", "9000", "listen port")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}

	if *apiURL == "" {
		*apiURL = adminURLFromConfig()
	}

	version := ""
	if len(rest) > 0 {
		version = rest[0]
	}
	if version == "" {
		return fmt.Errorf("version is required: uwas php start <version> [--port 9000]")
	}

	if n, err := strconv.Atoi(*port); err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != *port {
		return fmt.Errorf("invalid --port %q: must be a TCP port (1-65535)", *port)
	}

	payload := fmt.Sprintf(`{"listen_addr":"127.0.0.1:%s"}`, *port)
	body, err := apiRequest("POST", *apiURL+"/api/v1/php/"+url.PathEscape(version)+"/start", *apiKey, strings.NewReader(payload))
	if err != nil {
		return fmt.Errorf("start PHP %s: %w", version, err)
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("start: malformed response: %w", err)
	}
	fmt.Printf("PHP %s started on 127.0.0.1:%s\n", version, *port)
	return nil
}

func (p *PHPCommand) stop(args []string) error {
	fs := flag.NewFlagSet("php stop", flag.ContinueOnError)
	apiURL := fs.String("api-url", "", "admin API URL")
	apiKey := fs.String("api-key", os.Getenv("UWAS_ADMIN_KEY"), "admin API key")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}

	if *apiURL == "" {
		*apiURL = adminURLFromConfig()
	}

	version := ""
	if len(rest) > 0 {
		version = rest[0]
	}
	if version == "" {
		return fmt.Errorf("version is required: uwas php stop <version>")
	}

	_, err = apiRequest("POST", *apiURL+"/api/v1/php/"+url.PathEscape(version)+"/stop", *apiKey, nil)
	if err != nil {
		return fmt.Errorf("stop PHP %s: %w", version, err)
	}

	fmt.Printf("PHP %s stopped\n", version)
	return nil
}

func (p *PHPCommand) config(args []string) error {
	fs := flag.NewFlagSet("php config", flag.ContinueOnError)
	apiURL := fs.String("api-url", "", "admin API URL")
	apiKey := fs.String("api-key", os.Getenv("UWAS_ADMIN_KEY"), "admin API key")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}

	if *apiURL == "" {
		*apiURL = adminURLFromConfig()
	}

	version := ""
	if len(rest) > 0 {
		version = rest[0]
	}
	if version == "" {
		return fmt.Errorf("version is required: uwas php config <version>")
	}

	body, err := apiRequest("GET", *apiURL+"/api/v1/php/"+url.PathEscape(version)+"/config", *apiKey, nil)
	if err != nil {
		return fmt.Errorf("fetch PHP %s config: %w", version, err)
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
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}

	if *apiURL == "" {
		*apiURL = adminURLFromConfig()
	}

	version := ""
	if len(rest) > 0 {
		version = rest[0]
	}
	if version == "" {
		return fmt.Errorf("version is required: uwas php extensions <version>")
	}

	body, err := apiRequest("GET", *apiURL+"/api/v1/php/"+url.PathEscape(version)+"/extensions", *apiKey, nil)
	if err != nil {
		return fmt.Errorf("fetch PHP %s extensions: %w", version, err)
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
	output, err := phpRunInstall(version)
	if output != "" {
		fmt.Print(output)
	}
	if err != nil {
		return fmt.Errorf("install failed: %w", err)
	}
	fmt.Println("\n  \033[32m✓\033[0m PHP installed. Run 'uwas php list' to verify.")
	return nil
}

// phpPollInterval is how often update/uninstall poll the task; tests shorten it.
var phpPollInterval = 2 * time.Second

func shortPHPVersion(v string) string {
	if parts := strings.SplitN(v, ".", 3); len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return v
}

// update upgrades one version, or all supported ones, through the admin API so
// the server re-detects PHP and restarts php-fpm afterwards.
func (p *PHPCommand) update(args []string) error {
	fs := flag.NewFlagSet("php update", flag.ContinueOnError)
	apiURL := fs.String("api-url", "", "admin API URL")
	apiKey := fs.String("api-key", os.Getenv("UWAS_ADMIN_KEY"), "admin API key")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	version := "all"
	if len(rest) > 0 {
		version = rest[0]
	}
	return phpTask(fs, *apiURL, *apiKey, "/api/v1/php/update", map[string]any{"version": version}, "update")
}

// uninstall removes a version through the admin API, which refuses while any
// domain still uses it.
func (p *PHPCommand) uninstall(args []string) error {
	fs := flag.NewFlagSet("php uninstall", flag.ContinueOnError)
	apiURL := fs.String("api-url", "", "admin API URL")
	apiKey := fs.String("api-key", os.Getenv("UWAS_ADMIN_KEY"), "admin API key")
	purge := fs.Bool("purge", false, "also delete the version's configuration")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		return fmt.Errorf("usage: uwas php uninstall <version> [--purge]")
	}
	return phpTask(fs, *apiURL, *apiKey, "/api/v1/php/uninstall", map[string]any{"version": rest[0], "purge": *purge}, "uninstall")
}

// parseInterspersed lets flags appear before or after the positional version.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) > 0 {
			pos = append(pos, args[0])
			args = args[1:]
		}
	}
	return pos, nil
}

func phpTask(_ *flag.FlagSet, apiURL, apiKey, path string, body map[string]any, what string) error {
	if apiURL == "" {
		apiURL = adminURLFromConfig()
	}
	payload, _ := json.Marshal(body)
	if _, err := apiRequest("POST", apiURL+path, apiKey, bytes.NewReader(payload)); err != nil {
		return fmt.Errorf("php %s: %w", what, err)
	}
	fmt.Printf("  PHP %s started, waiting...\n", what)
	for i := 0; i < 900; i++ { // up to ~30 min at 2s
		time.Sleep(phpPollInterval)
		data, err := apiRequest("GET", apiURL+"/api/v1/php/install/status", apiKey, nil)
		if err != nil {
			return fmt.Errorf("php %s status: %w", what, err)
		}
		var st struct {
			Status string `json:"status"`
			Output string `json:"output"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(data, &st); err != nil {
			return fmt.Errorf("parse status: %w", err)
		}
		if st.Status == "running" || st.Status == "queued" || st.Status == "pending" {
			continue
		}
		if st.Output != "" {
			fmt.Print(st.Output)
		}
		if st.Status == "done" || st.Status == "completed" {
			fmt.Printf("\n  \033[32m✓\033[0m PHP %s finished. Run 'uwas php list' to verify.\n", what)
			return nil
		}
		return fmt.Errorf("php %s failed: %s", what, st.Error)
	}
	return fmt.Errorf("php %s timed out waiting for the task", what)
}
