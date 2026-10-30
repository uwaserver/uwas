package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

// StatusCommand shows the running server status.
type StatusCommand struct{}

func (s *StatusCommand) Name() string        { return "status" }
func (s *StatusCommand) Description() string { return "Show running server status" }

func (s *StatusCommand) Run(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	apiURL := fs.String("api-url", "", "admin API URL")
	apiKey := fs.String("api-key", os.Getenv("UWAS_ADMIN_KEY"), "admin API key")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *apiURL == "" {
		*apiURL = adminURLFromConfig()
	}

	// Health
	healthData, err := apiRequest("GET", *apiURL+"/api/v1/health", *apiKey, nil)
	if err != nil {
		return fmt.Errorf("server not reachable: %w", err)
	}

	var health map[string]any
	if err := json.Unmarshal(healthData, &health); err != nil {
		fmt.Fprintf(os.Stderr, "warning: malformed health response: %v\n", err)
	}

	// Stats
	// /health is public, so a missing or wrong API key got this far and then
	// printed "Status: ok, Domains: 0" with exit code 0 (the stats error was
	// dropped and the empty body reported as "malformed stats response").
	var stats map[string]any
	statsData, statsErr := apiRequest("GET", *apiURL+"/api/v1/stats", *apiKey, nil)
	switch {
	case isAuthError(statsErr):
		return fmt.Errorf("server is up but refused the credentials: %w", statsErr)
	case statsErr != nil:
		fmt.Fprintf(os.Stderr, "warning: stats unavailable: %v\n", statsErr)
	default:
		if err := json.Unmarshal(statsData, &stats); err != nil {
			fmt.Fprintf(os.Stderr, "warning: malformed stats response: %v\n", err)
		}
	}

	// Domains — the API returns a paginated envelope: {"items":[...],"total":...}.
	var domains []map[string]any
	domainItems, err := apiGetItems(*apiURL, "/api/v1/domains", *apiKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: list domains: %v\n", err)
	}
	for _, item := range domainItems {
		var d map[string]any
		if err := json.Unmarshal(item, &d); err != nil {
			fmt.Fprintf(os.Stderr, "warning: parse domains response: %v\n", err)
			break
		}
		domains = append(domains, d)
	}

	fmt.Println("UWAS Server Status")
	fmt.Println("═══════════════════════════════════════")
	fmt.Printf("  Status:       %s\n", health["status"])
	fmt.Printf("  Uptime:       %s\n", health["uptime"])

	if stats != nil {
		fmt.Printf("  Requests:     %.0f\n", stats["requests_total"])
		fmt.Printf("  Active Conns: %.0f\n", stats["active_conns"])
		fmt.Printf("  Cache Hits:   %.0f\n", stats["cache_hits"])
		fmt.Printf("  Cache Misses: %.0f\n", stats["cache_misses"])
		fmt.Printf("  Bytes Sent:   %.0f\n", stats["bytes_sent"])
	}

	fmt.Printf("  Domains:      %d\n", len(domains))
	for _, d := range domains {
		ssl := d["ssl"]
		if ssl == nil {
			ssl = "off"
		}
		fmt.Printf("    %-30s  %s  ssl=%s\n", d["host"], d["type"], ssl)
	}
	fmt.Println("═══════════════════════════════════════")

	return nil
}

// ReloadCommand sends SIGHUP or triggers reload via API.
type ReloadCommand struct{}

func (r *ReloadCommand) Name() string        { return "reload" }
func (r *ReloadCommand) Description() string { return "Reload server configuration" }

func (r *ReloadCommand) Run(args []string) error {
	fs := flag.NewFlagSet("reload", flag.ContinueOnError)
	apiURL := fs.String("api-url", "", "admin API URL")
	apiKey := fs.String("api-key", os.Getenv("UWAS_ADMIN_KEY"), "admin API key")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *apiURL == "" {
		*apiURL = adminURLFromConfig()
	}

	body, err := apiRequest("POST", *apiURL+"/api/v1/reload", *apiKey, nil)
	if err != nil {
		return fmt.Errorf("reload failed: %w", err)
	}

	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("reload: malformed response: %w", err)
	}
	fmt.Printf("Config reloaded: %s\n", result["status"])
	return nil
}
