package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

Flags for 'remove':
  --cleanup        Also stop its PHP worker, purge its cache and remove its
                   cron jobs and SFTP user (never deletes the document root)
  --pin string     Admin PIN, required when the server has pin_code set
                   (env: UWAS_ADMIN_PIN; 'uwas install' and 'uwas init' save it
                   next to UWAS_ADMIN_KEY in the .env file)

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
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *apiURL == "" {
		*apiURL = adminURLFromConfig()
	}

	raw, err := apiGetItems(*apiURL, "/api/v1/domains", *apiKey)
	if err != nil {
		return fmt.Errorf("list domains: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "HOST\tTYPE\tSSL\tROOT\n")
	for _, item := range raw {
		var d struct {
			Host    string   `json:"host"`
			Aliases []string `json:"aliases"`
			Type    string   `json:"type"`
			SSL     string   `json:"ssl"`
			Root    string   `json:"root"`
		}
		if err := json.Unmarshal(item, &d); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
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
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}

	if *apiURL == "" {
		*apiURL = adminURLFromConfig()
	}

	host := ""
	if len(rest) > 0 {
		host = rest[0]
	}
	if host == "" {
		return fmt.Errorf("host is required: uwas domain add <host> [flags]")
	}

	payload := fmt.Sprintf(`{"host":%q,"type":%q,"root":%q,"ssl":{"mode":%q}}`,
		host, *domainType, *root, *ssl)

	body, err := apiRequest("POST", *apiURL+"/api/v1/domains", *apiKey, strings.NewReader(payload))
	if err != nil {
		return fmt.Errorf("add domain %s: %w", host, err)
	}

	fmt.Printf("Domain added: %s\n", string(body))
	return nil
}

func (d *DomainCommand) remove(args []string) error {
	fs := flag.NewFlagSet("domain remove", flag.ContinueOnError)
	apiURL := fs.String("api-url", "", "admin API URL")
	apiKey := fs.String("api-key", os.Getenv("UWAS_ADMIN_KEY"), "admin API key")
	cleanup := fs.Bool("cleanup", false, "also stop its PHP worker, purge its cache and remove its cron jobs and SFTP user")
	pin := fs.String("pin", adminPinFromEnv(), "admin PIN (env: UWAS_ADMIN_PIN); required when the server has pin_code set")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}

	if *apiURL == "" {
		*apiURL = adminURLFromConfig()
	}

	host := ""
	if len(rest) > 0 {
		host = rest[0]
	}
	if host == "" {
		return fmt.Errorf("host is required: uwas domain remove <host>")
	}

	// The API refuses a delete without ?confirm=true (F51000).
	target := *apiURL + "/api/v1/domains/" + url.PathEscape(host) + "?confirm=true"
	if *cleanup {
		target += "&cleanup=true"
	}
	_, err = apiRequestPin("DELETE", target, *apiKey, *pin, nil)
	if err != nil {
		return fmt.Errorf("remove domain %s: %w", host, err)
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
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *apiURL == "" {
		*apiURL = adminURLFromConfig()
	}

	payload := "{}"
	if *tag != "" {
		payload = fmt.Sprintf(`{"tag":%q}`, *tag)
	}

	body, err := apiRequest("POST", *apiURL+"/api/v1/cache/purge", *apiKey, strings.NewReader(payload))
	if err != nil {
		return fmt.Errorf("purge cache: %w", err)
	}

	fmt.Println(string(body))
	return nil
}

func (c *CacheCommand) stats(args []string) error {
	fs := flag.NewFlagSet("cache stats", flag.ContinueOnError)
	apiURL := fs.String("api-url", "", "admin API URL")
	apiKey := fs.String("api-key", os.Getenv("UWAS_ADMIN_KEY"), "admin API key")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *apiURL == "" {
		*apiURL = adminURLFromConfig()
	}

	body, err := apiRequest("GET", *apiURL+"/api/v1/stats", *apiKey, nil)
	if err != nil {
		return fmt.Errorf("fetch cache stats: %w", err)
	}

	var stats map[string]any
	if err := json.Unmarshal(body, &stats); err != nil {
		return fmt.Errorf("stats: malformed response: %w", err)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for k, v := range stats {
		fmt.Fprintf(w, "%s\t%v\n", k, v)
	}
	w.Flush()
	return nil
}

// apiError is a non-2xx answer from the admin API. Its text keeps the
// "API error <status>: <body>" form and adds, on the same line, how to fix the
// two refusals an operator can fix from the command line: no/wrong API key and
// a missing PIN.
type apiError struct {
	status int
	body   string
}

func (e *apiError) Error() string {
	msg := fmt.Sprintf("API error %d: %s", e.status, e.body)
	switch {
	case e.status == http.StatusUnauthorized:
		msg += " (the admin API key is missing or wrong: pass --api-key or set UWAS_ADMIN_KEY)"
	case e.status == http.StatusForbidden && (strings.Contains(e.body, "pin_required") || strings.Contains(e.body, "invalid_pin")):
		msg += " (this action needs the admin PIN: pass --pin <code> or set UWAS_ADMIN_PIN)"
	}
	return msg
}

// isAuthError reports whether err is the API refusing the CLI's credentials.
func isAuthError(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.status == http.StatusUnauthorized
}

// adminPinFromEnv is the PIN the CLI sends with every request when the
// operator has set UWAS_ADMIN_PIN (or put it in the .env file that holds
// UWAS_ADMIN_KEY). The server only checks it on destructive routes and ignores
// it elsewhere.
func adminPinFromEnv() string { return os.Getenv("UWAS_ADMIN_PIN") }

func apiRequest(method, url, apiKey string, body io.Reader) ([]byte, error) {
	return apiRequestPin(method, url, apiKey, adminPinFromEnv(), body)
}

// apiRequestPin is apiRequest with an explicit PIN (the --pin flag).
func apiRequestPin(method, url, apiKey, pin string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	if pin != "" {
		req.Header.Set("X-Pin-Code", pin)
	}
	req.Header.Set("Content-Type", "application/json")
	// The admin API refuses a state-changing request that carries neither this
	// header nor a dashboard Origin ("csrf: invalid origin"); without it every
	// add/remove/start/purge was rejected by a server with an API key (F51001).
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

	resp, err := apiHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10 MB cap
	if err != nil {
		return nil, fmt.Errorf("API response read: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, &apiError{status: resp.StatusCode, body: string(data)}
	}

	return data, nil
}

// apiGetItems reads every item of a paginated list endpoint. The admin API
// answers {"items","total","limit","offset"} with limit defaulting to 50 and
// capped at 500, so a bare GET returns the first page only: "domain list",
// "php list" and "status" listed (and counted) at most 50 entries (F14304).
// A server that ignores offset (an older release) repeats its first page; that
// is detected and the page is kept once.
func apiGetItems(baseURL, path, apiKey string) ([]json.RawMessage, error) {
	var all []json.RawMessage
	offset := 0
	var firstOfPrev json.RawMessage
	for page := 0; page < 1000; page++ {
		body, err := apiRequest("GET", fmt.Sprintf("%s%s?limit=500&offset=%d", baseURL, path, offset), apiKey, nil)
		if err != nil {
			return nil, err
		}
		var env struct {
			Items []json.RawMessage `json:"items"`
			Total int               `json:"total"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, fmt.Errorf("parse response: %w", err)
		}
		if len(env.Items) == 0 {
			break
		}
		if page > 0 && string(env.Items[0]) == string(firstOfPrev) {
			break
		}
		firstOfPrev = env.Items[0]
		all = append(all, env.Items...)
		offset += len(env.Items)
		if offset >= env.Total {
			break
		}
	}
	return all, nil
}
