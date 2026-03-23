package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

// CertCommand manages TLS certificates.
type CertCommand struct{}

func (c *CertCommand) Name() string        { return "cert" }
func (c *CertCommand) Description() string { return "Manage TLS certificates (list, renew)" }

func (c *CertCommand) Help() string {
	return `Subcommands:
  list                  List all domain certificates with status
  renew <domain>        Force renewal of a certificate

Flags:
  --api-url string   Admin API URL (auto-detected from config)
  --api-key string   Admin API key (env: UWAS_ADMIN_KEY)

Examples:
  uwas cert list
  uwas cert renew example.com`
}

func (c *CertCommand) Run(args []string) error {
	if len(args) == 0 {
		fmt.Println(c.Help())
		return nil
	}

	switch args[0] {
	case "list", "ls":
		return c.list(args[1:])
	case "renew":
		if len(args) < 2 {
			return fmt.Errorf("domain required: uwas cert renew <domain>")
		}
		return c.renew(args[1], args[2:])
	default:
		return fmt.Errorf("unknown subcommand %q (use: list, renew)", args[0])
	}
}

func (c *CertCommand) list(args []string) error {
	fs := flag.NewFlagSet("cert list", flag.ContinueOnError)
	apiURL := fs.String("api-url", "", "admin API URL")
	apiKey := fs.String("api-key", os.Getenv("UWAS_ADMIN_KEY"), "admin API key")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *apiURL == "" {
		*apiURL = adminURLFromConfig()
	}

	body, err := apiRequest("GET", *apiURL+"/api/v1/certs", *apiKey, nil)
	if err != nil {
		return fmt.Errorf("fetch certs: %w", err)
	}

	var certs []struct {
		Host     string `json:"host"`
		SSLMode  string `json:"ssl_mode"`
		Status   string `json:"status"`
		Issuer   string `json:"issuer"`
		Expiry   string `json:"expiry"`
		DaysLeft int    `json:"days_left"`
	}
	if err := json.Unmarshal(body, &certs); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}

	if len(certs) == 0 {
		fmt.Println("No certificates configured.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "HOST\tSSL\tSTATUS\tISSUER\tEXPIRY\tDAYS LEFT\n")
	for _, cert := range certs {
		expiry := cert.Expiry
		if expiry == "" {
			expiry = "-"
		} else if len(expiry) > 10 {
			expiry = expiry[:10]
		}
		days := "-"
		if cert.Status == "active" {
			days = fmt.Sprintf("%d", cert.DaysLeft)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			cert.Host, cert.SSLMode, cert.Status, cert.Issuer, expiry, days)
	}
	w.Flush()
	return nil
}

func (c *CertCommand) renew(domain string, args []string) error {
	fs := flag.NewFlagSet("cert renew", flag.ContinueOnError)
	apiURL := fs.String("api-url", "", "admin API URL")
	apiKey := fs.String("api-key", os.Getenv("UWAS_ADMIN_KEY"), "admin API key")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *apiURL == "" {
		*apiURL = adminURLFromConfig()
	}

	fmt.Printf("Renewing certificate for %s...\n", domain)
	body, err := apiRequest("POST", *apiURL+"/api/v1/certs/"+domain+"/renew", *apiKey, strings.NewReader("{}"))
	if err != nil {
		return fmt.Errorf("renewal failed: %w", err)
	}

	var result map[string]string
	json.Unmarshal(body, &result)
	fmt.Printf("Certificate renewed: %s\n", result["status"])
	return nil
}
