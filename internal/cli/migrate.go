package cli

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"

	nginxmigrate "github.com/uwaserver/uwas/internal/migrate"
)

// MigrateCommand converts Nginx/Apache configs to UWAS YAML.
type MigrateCommand struct{}

func (m *MigrateCommand) Name() string        { return "migrate" }
func (m *MigrateCommand) Description() string { return "Convert Nginx/Apache config to UWAS YAML" }

func (m *MigrateCommand) Help() string {
	return `Subcommands:
  nginx <file>    Convert an Nginx config file to UWAS YAML
  apache <file>   Convert an Apache config file to UWAS YAML

Examples:
  uwas migrate nginx /etc/nginx/sites-enabled/example.conf
  uwas migrate apache /etc/apache2/sites-enabled/example.conf`
}

func (m *MigrateCommand) Run(args []string) error {
	if len(args) < 2 {
		fmt.Println(m.Help())
		return nil
	}

	format := args[0]
	file := args[1]

	switch format {
	case "nginx":
		return migrateNginx(file)
	case "apache":
		return migrateApache(file)
	default:
		return fmt.Errorf("unknown format %q (use: nginx, apache)", format)
	}
}

// --- Nginx migration ---

func migrateNginx(file string) error {
	f, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("open %s: %w", file, err)
	}
	defer f.Close()

	yaml, err := nginxmigrate.NginxToYAML(f)
	if err != nil {
		return fmt.Errorf("migrate %s: %w", file, err)
	}

	fmt.Print(yaml)
	return nil
}

// --- Apache migration ---

type apacheVHost struct {
	serverName   string
	documentRoot string
	sslEngine    bool
	sslCertFile  string
	sslKeyFile   string
	proxyPass    []apacheProxy
	listen       string
}

type apacheProxy struct {
	path   string
	target string
}

func migrateApache(file string) error {
	f, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("open %s: %w", file, err)
	}
	defer f.Close()

	vhosts := parseApacheConfig(f)
	if len(vhosts) == 0 {
		return fmt.Errorf("no VirtualHost blocks found in %s", file)
	}

	yaml := convertApacheToYAML(vhosts)
	fmt.Print(yaml)
	return nil
}

func parseApacheConfig(f *os.File) []apacheVHost {
	var vhosts []apacheVHost
	scanner := bufio.NewScanner(f)
	inVHost := false
	var current apacheVHost

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip comments and empty lines
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasPrefix(line, "<VirtualHost") {
			inVHost = true
			current = apacheVHost{}
			// Extract listen address from <VirtualHost *:443>
			if addr := extractApacheVHostAddr(line); addr != "" {
				current.listen = addr
			}
			continue
		}

		if strings.HasPrefix(line, "</VirtualHost") {
			inVHost = false
			vhosts = append(vhosts, current)
			continue
		}

		if !inVHost {
			continue
		}

		// Parse directives inside VirtualHost
		lower := strings.ToLower(line)

		if strings.HasPrefix(lower, "servername") {
			current.serverName = extractApacheValue(line)
		}
		if strings.HasPrefix(lower, "documentroot") {
			val := extractApacheValue(line)
			current.documentRoot = strings.Trim(val, `"'`)
		}
		if strings.HasPrefix(lower, "sslengine") {
			val := strings.ToLower(extractApacheValue(line))
			current.sslEngine = (val == "on")
		}
		if strings.HasPrefix(lower, "sslcertificatefile") {
			current.sslCertFile = extractApacheValue(line)
		}
		if strings.HasPrefix(lower, "sslcertificatekeyfile") {
			current.sslKeyFile = extractApacheValue(line)
		}
		if strings.HasPrefix(lower, "proxypass ") && !strings.HasPrefix(lower, "proxypassreverse") {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				current.proxyPass = append(current.proxyPass, apacheProxy{
					path:   parts[1],
					target: parts[2],
				})
			}
		}
	}

	return vhosts
}

var apacheVHostAddrRegexp = regexp.MustCompile(`<VirtualHost\s+([^>]+)>`)

func extractApacheVHostAddr(line string) string {
	m := apacheVHostAddrRegexp.FindStringSubmatch(line)
	if len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func extractApacheValue(line string) string {
	parts := strings.Fields(line)
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

func convertApacheToYAML(vhosts []apacheVHost) string {
	var b strings.Builder
	b.WriteString("# Converted from Apache config by UWAS\n")
	b.WriteString("domains:\n")

	for _, vh := range vhosts {
		host := vh.serverName
		if host == "" {
			host = "example.com"
		}

		b.WriteString(fmt.Sprintf("  - host: %s\n", host))

		// Determine type
		hasProxy := len(vh.proxyPass) > 0
		if hasProxy {
			b.WriteString("    type: proxy\n")
		} else {
			b.WriteString("    type: static\n")
		}

		if vh.documentRoot != "" {
			b.WriteString(fmt.Sprintf("    root: %s\n", vh.documentRoot))
		}

		// SSL
		if vh.sslEngine || vh.sslCertFile != "" {
			b.WriteString("    ssl:\n")
			if vh.sslCertFile != "" {
				b.WriteString("      mode: manual\n")
				b.WriteString(fmt.Sprintf("      cert: %s\n", vh.sslCertFile))
				if vh.sslKeyFile != "" {
					b.WriteString(fmt.Sprintf("      key: %s\n", vh.sslKeyFile))
				}
			} else {
				b.WriteString("      mode: auto\n")
			}
		} else if strings.Contains(vh.listen, "443") {
			b.WriteString("    ssl:\n")
			b.WriteString("      mode: auto\n")
		}

		// Proxy config
		if hasProxy {
			b.WriteString("    proxy:\n")
			b.WriteString("      upstreams:\n")
			for _, p := range vh.proxyPass {
				b.WriteString(fmt.Sprintf("        - address: %s\n", p.target))
			}
		}

		b.WriteString("\n")
	}

	return b.String()
}
