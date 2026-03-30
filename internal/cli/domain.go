package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

// DomainCommand manages domains via the admin API.
type DomainCommand struct{}

var apiHTTPClient = &http.Client{
	Timeout: 15 * time.Second,
}

func (d *DomainCommand) Name() string        { return "domain" }
func (d *DomainCommand) Description() string { return "Manage domains (list, add, remove)" }

func (d *DomainCommand) Help() string {
	return `Subcommands:
  list                 List all configured domains
  add <host> [flags]   Add a new domain
  remove <host>        Remove a domain

Flags for 'add':
  --type string    Domain type: static, php, proxy, redirect (default "static")
  --root string    Document root path
  --ssl string     SSL mode: auto, manual, off (default "off")

Examples:
  uwas domain list --api-url http://127.0.0.1:9443 --api-key mykey
  uwas domain add example.com --type static --root /var/www --ssl auto
  uwas domain remove old.example.com`
}

func (d *DomainCommand) Run(args []string) error {
	if len(args) == 0 {
		fmt.Println(d.Help())
		return nil
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "list":
		return d.list(subArgs)
	case "add":
		return d.add(subArgs)
	case "remove", "rm", "delete":
		return d.remove(subArgs)
	default:
		return fmt.Errorf("unknown subcommand %q (use: list, add, remove)", sub)
	}
}

func (d *DomainCommand) list(args []string) error {
	fs := flag.NewFlagSet("domain list", flag.ContinueOnError)
	apiURL := fs.String("api-url", "", "admin API URL")
	apiKey := fs.String("api-key", os.Getenv("UWAS_ADMIN_KEY"), "admin API key")
	fs.Parse(args)

	if *apiURL == "" {
		*apiURL = adminURLFromConfig()
	}

	body, err := apiRequest("GET", *apiURL+"/api/v1/domains", *apiKey, nil)
	if err != nil {
		return err
	}

	var domains []struct {
		Host    string   `json:"host"`
		Aliases []string `json:"aliases"`
		Type    string   `json:"type"`
		SSL     string   `json:"ssl"`
		Root    string   `json:"root"`
	}
	if err := json.Unmarshal(body, &domains); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "HOST\tTYPE\tSSL\tROOT\n")
	for _, d := range domains {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", d.Host, d.Type, d.SSL, d.Root)
	}
	w.Flush()
	return nil
}

func (d *DomainCommand) add(args []string) error {
	fs := flag.NewFlagSet("domain add", flag.ContinueOnError)
	apiURL := fs.String("api-url", "", "admin API URL")
	apiKey := fs.String("api-key", os.Getenv("UWAS_ADMIN_KEY"), "admin API key")
	domainType := fs.String("type", "static", "domain type")
	root := fs.String("root", "", "document root")
	ssl := fs.String("ssl", "off", "SSL mode")
	fs.Parse(args)

	if *apiURL == "" {
		*apiURL = adminURLFromConfig()
	}

	host := fs.Arg(0)
	if host == "" {
		return fmt.Errorf("host is required: uwas domain add <host> [flags]")
	}

	payload := fmt.Sprintf(`{"host":%q,"type":%q,"root":%q,"ssl":{"mode":%q}}`,
		host, *domainType, *root, *ssl)

	body, err := apiRequest("POST", *apiURL+"/api/v1/domains", *apiKey, strings.NewReader(payload))
	if err != nil {
		return err
	}

	fmt.Printf("Domain added: %s\n", string(body))
	return nil
}

func (d *DomainCommand) remove(args []string) error {
	fs := flag.NewFlagSet("domain remove", flag.ContinueOnError)
	apiURL := fs.String("api-url", "", "admin API URL")
	apiKey := fs.String("api-key", os.Getenv("UWAS_ADMIN_KEY"), "admin API key")
	fs.Parse(args)

	if *apiURL == "" {
		*apiURL = adminURLFromConfig()
	}

	host := fs.Arg(0)
	if host == "" {
		return fmt.Errorf("host is required: uwas domain remove <host>")
	}

	_, err := apiRequest("DELETE", *apiURL+"/api/v1/domains/"+host, *apiKey, nil)
	if err != nil {
		return err
	}

	fmt.Printf("Domain removed: %s\n", host)
	return nil
}

// CacheCommand manages cache via the admin API.
type CacheCommand struct{}

func (c *CacheCommand) Name() string        { return "cache" }
func (c *CacheCommand) Description() string { return "Manage cache (purge, stats)" }

func (c *CacheCommand) Help() string {
	return `Subcommands:
  purge [--tag <tag>]   Purge cache entries (all or by tag)
  stats                 Show cache statistics

Examples:
  uwas cache purge                    # purge all
  uwas cache purge --tag site:blog    # purge by tag
  uwas cache stats`
}

func (c *CacheCommand) Run(args []string) error {
	if len(args) == 0 {
		fmt.Println(c.Help())
		return nil
	}

	switch args[0] {
	case "purge":
		return c.purge(args[1:])
	case "stats":
		return c.stats(args[1:])
	default:
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func (c *CacheCommand) purge(args []string) error {
	fs := flag.NewFlagSet("cache purge", flag.ContinueOnError)
	apiURL := fs.String("api-url", "", "admin API URL")
	apiKey := fs.String("api-key", os.Getenv("UWAS_ADMIN_KEY"), "admin API key")
	tag := fs.String("tag", "", "purge by tag")
	fs.Parse(args)

	if *apiURL == "" {
		*apiURL = adminURLFromConfig()
	}

	payload := "{}"
	if *tag != "" {
		payload = fmt.Sprintf(`{"tag":%q}`, *tag)
	}

	body, err := apiRequest("POST", *apiURL+"/api/v1/cache/purge", *apiKey, strings.NewReader(payload))
	if err != nil {
		return err
	}

	fmt.Println(string(body))
	return nil
}

func (c *CacheCommand) stats(args []string) error {
	fs := flag.NewFlagSet("cache stats", flag.ContinueOnError)
	apiURL := fs.String("api-url", "", "admin API URL")
	apiKey := fs.String("api-key", os.Getenv("UWAS_ADMIN_KEY"), "admin API key")
	fs.Parse(args)

	if *apiURL == "" {
		*apiURL = adminURLFromConfig()
	}

	body, err := apiRequest("GET", *apiURL+"/api/v1/stats", *apiKey, nil)
	if err != nil {
		return err
	}

	var stats map[string]any
	json.Unmarshal(body, &stats)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for k, v := range stats {
		fmt.Fprintf(w, "%s\t%v\n", k, v)
	}
	w.Flush()
	return nil
}

func apiRequest(method, url, apiKey string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := apiHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(data))
	}

	return data, nil
}
