package cli

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/uwaserver/uwas/internal/build"
	"github.com/uwaserver/uwas/internal/config"
)

const banner = `
  _   ___      __  _   ___
 | | | \ \    / / /_\ / __|
 | |_| |\ \/\/ / / _ \\__ \
  \___/  \_/\_/ /_/ \_\___/
`

// PrintBanner displays the startup banner with server configuration.
func PrintBanner(cfg *config.Config, configPath string) {
	fmt.Print("\033[36m") // cyan
	fmt.Print(banner)
	fmt.Print("\033[0m") // reset

	fmt.Printf("  \033[1mUnified Web Application Server\033[0m  %s\n", colorize("v"+build.Version, "green"))
	fmt.Printf("  %s/%s  %s\n\n", runtime.GOOS, runtime.GOARCH, runtime.Version())

	// Config source
	fmt.Printf("  \033[90mConfig:\033[0m   %s\n", configPath)
	fmt.Printf("  \033[90mDomains:\033[0m  %d configured\n", len(cfg.Domains))
	fmt.Println()

	// Listeners
	fmt.Println("  \033[1mListeners:\033[0m")
	fmt.Printf("    %s  HTTP    %s\n", colorize("->", "cyan"), colorize(cfg.Global.HTTPListen, "white"))
	if cfg.Global.HTTPSListen != "" {
		label := "HTTPS"
		if cfg.Global.HTTP3Enabled {
			label = "HTTPS+H3"
		}
		fmt.Printf("    %s  %-8s%s\n", colorize("->", "cyan"), label, colorize(cfg.Global.HTTPSListen, "white"))
	}
	if cfg.Global.Admin.Enabled {
		fmt.Printf("    %s  Admin   %s\n", colorize("->", "cyan"), colorize(cfg.Global.Admin.Listen, "white"))
	}
	if cfg.Global.MCP.Enabled {
		fmt.Printf("    %s  MCP     %s\n", colorize("->", "cyan"), colorize(cfg.Global.MCP.Listen, "white"))
	}
	fmt.Println()

	// Features
	features := []string{}
	if cfg.Global.Cache.Enabled {
		features = append(features, "Cache")
	}
	if cfg.Global.Backup.Enabled {
		features = append(features, "Backup")
	}
	if cfg.Global.HTTP3Enabled {
		features = append(features, "HTTP/3")
	}
	if cfg.Global.Alerting.Enabled {
		features = append(features, "Alerting")
	}

	// Collect domain types
	types := map[string]int{}
	for _, d := range cfg.Domains {
		types[d.Type]++
	}
	for _, t := range []string{"static", "php", "proxy", "redirect"} {
		if n, ok := types[t]; ok {
			features = append(features, fmt.Sprintf("%d %s", n, t))
		}
	}

	if len(features) > 0 {
		fmt.Printf("  \033[1mFeatures:\033[0m %s\n", strings.Join(features, " | "))
	}

	// Dashboard URL
	if cfg.Global.Admin.Enabled {
		scheme := "http"
		addr := cfg.Global.Admin.Listen
		if strings.HasPrefix(addr, ":") {
			addr = "localhost" + addr
		}
		fmt.Printf("\n  \033[1mDashboard:\033[0m %s://%s/_uwas/dashboard/\n", scheme, addr)
	}

	fmt.Println()
	fmt.Printf("  %s Press Ctrl+C to stop\n\n", colorize("*", "yellow"))
	fmt.Println(strings.Repeat("-", 50))
	fmt.Println()
}

func colorize(s, color string) string {
	switch color {
	case "green":
		return "\033[32m" + s + "\033[0m"
	case "cyan":
		return "\033[36m" + s + "\033[0m"
	case "yellow":
		return "\033[33m" + s + "\033[0m"
	case "white":
		return "\033[97m" + s + "\033[0m"
	case "red":
		return "\033[31m" + s + "\033[0m"
	default:
		return s
	}
}
