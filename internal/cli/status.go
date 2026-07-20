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
	fs.Parse(args)

	if *apiURL == "" {
		*apiURL = adminURLFromConfig()
	}

	// Health
	healthData, err := apiRequest("GET", *apiURL+"/api/v1/health", *apiKey, nil)
	if err != nil {
		return fmt.Errorf("server not reachable: %w", err)
	}

	var health map[string]any
	json.Unmarshal(healthData, &health)

	// Stats
	statsData, _ := apiRequest("GET", *apiURL+"/api/v1/stats", *apiKey, nil)
	var stats map[string]any
	json.Unmarshal(statsData, &stats)

	// Domains — the API returns a paginated envelope: {"items":[...],"total":...}.
	var domains []map[string]any
	domainsData, err := apiRequest("GET", *apiURL+"/api/v1/domains", *apiKey, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: list domains: %v\n", err)
	} else {
		var envelope struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(domainsData, &envelope); err != nil {
			fmt.Fprintf(os.Stderr, "warning: parse domains response: %v\n", err)
		}
		domains = envelope.Items
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
	fs.Parse(args)

	if *apiURL == "" {
		*apiURL = adminURLFromConfig()
	}

	body, err := apiRequest("POST", *apiURL+"/api/v1/reload", *apiKey, nil)
	if err != nil {
		return fmt.Errorf("reload failed: %w", err)
	}

	var result map[string]string
	json.Unmarshal(body, &result)
	fmt.Printf("Config reloaded: %s\n", result["status"])
	return nil
}
