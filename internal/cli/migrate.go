package cli

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
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

type nginxServer struct {
	serverName  string
	root        string
	listen      string
	sslCert     string
	sslKey      string
	proxyPass   string
	locations   []nginxLocation
}

type nginxLocation struct {
	path      string
	proxyPass string
}

func migrateNginx(file string) error {
	f, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("open %s: %w", file, err)
	}
	defer f.Close()

	servers := parseNginxConfig(f)
	if len(servers) == 0 {
		return fmt.Errorf("no server blocks found in %s", file)
	}

	yaml := convertNginxToYAML(servers)
	fmt.Print(yaml)
	return nil
}

func parseNginxConfig(f *os.File) []nginxServer {
	var servers []nginxServer
	scanner := bufio.NewScanner(f)
	inServer := false
	inLocation := false
	braceDepth := 0
	var current nginxServer
	var currentLocation nginxLocation

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip comments and empty lines
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Track brace depth
		openBraces := strings.Count(line, "{")
		closeBraces := strings.Count(line, "}")

		if !inServer && strings.HasPrefix(line, "server") && strings.Contains(line, "{") {
			inServer = true
			braceDepth = 1
			current = nginxServer{}
			continue
		}

		if inServer {
			braceDepth += openBraces - closeBraces

			if braceDepth <= 0 {
				// End of server block
				inServer = false
				servers = append(servers, current)
				continue
			}

			// Check for location block
			if strings.HasPrefix(line, "location") && strings.Contains(line, "{") {
				inLocation = true
				path := extractNginxLocationPath(line)
				currentLocation = nginxLocation{path: path}
				continue
			}

			if inLocation {
				if closeBraces > 0 && !strings.Contains(line, "{") {
					inLocation = false
					current.locations = append(current.locations, currentLocation)
					continue
				}
				if val := extractNginxDirective(line, "proxy_pass"); val != "" {
					currentLocation.proxyPass = val
				}
				continue
			}

			// Server-level directives
			if val := extractNginxDirective(line, "server_name"); val != "" {
				current.serverName = strings.Fields(val)[0] // take first name
			}
			if val := extractNginxDirective(line, "root"); val != "" {
				current.root = val
			}
			if val := extractNginxDirective(line, "listen"); val != "" {
				current.listen = val
			}
			if val := extractNginxDirective(line, "ssl_certificate_key"); val != "" {
				current.sslKey = val
			} else if val := extractNginxDirective(line, "ssl_certificate"); val != "" {
				current.sslCert = val
			}
			if val := extractNginxDirective(line, "proxy_pass"); val != "" {
				current.proxyPass = val
			}
		}
	}

	return servers
}

func extractNginxDirective(line, directive string) string {
	line = strings.TrimSpace(line)
	prefix := directive + " "
	if !strings.HasPrefix(line, prefix) {
		return ""
	}
	val := strings.TrimPrefix(line, prefix)
	val = strings.TrimSuffix(val, ";")
	return strings.TrimSpace(val)
}

var locationPathRegexp = regexp.MustCompile(`location\s+(?:~\s+|~\*\s+|=\s+)?([^\s{]+)`)

func extractNginxLocationPath(line string) string {
	m := locationPathRegexp.FindStringSubmatch(line)
	if len(m) > 1 {
		return m[1]
	}
	return "/"
}

func convertNginxToYAML(servers []nginxServer) string {
	var b strings.Builder
	b.WriteString("# Converted from Nginx config by UWAS\n")
	b.WriteString("domains:\n")

	for _, s := range servers {
		host := s.serverName
		if host == "" || host == "_" {
			host = "example.com"
		}

		b.WriteString(fmt.Sprintf("  - host: %s\n", host))

		// Determine type
		hasProxy := s.proxyPass != "" || hasProxyLocations(s.locations)
		if hasProxy {
			b.WriteString("    type: proxy\n")
		} else {
			b.WriteString("    type: static\n")
		}

		if s.root != "" {
			b.WriteString(fmt.Sprintf("    root: %s\n", s.root))
		}

		// SSL
		if s.sslCert != "" {
			b.WriteString("    ssl:\n")
			b.WriteString("      mode: manual\n")
			b.WriteString(fmt.Sprintf("      cert: %s\n", s.sslCert))
			if s.sslKey != "" {
				b.WriteString(fmt.Sprintf("      key: %s\n", s.sslKey))
			}
		} else if strings.Contains(s.listen, "ssl") || strings.Contains(s.listen, "443") {
			b.WriteString("    ssl:\n")
			b.WriteString("      mode: auto\n")
		}

		// Proxy config
		if hasProxy {
			b.WriteString("    proxy:\n")
			b.WriteString("      upstreams:\n")

			if s.proxyPass != "" {
				b.WriteString(fmt.Sprintf("        - address: %s\n", s.proxyPass))
			}
			for _, loc := range s.locations {
				if loc.proxyPass != "" {
					b.WriteString(fmt.Sprintf("        - address: %s\n", loc.proxyPass))
				}
			}
		}

		b.WriteString("\n")
	}

	return b.String()
}

func hasProxyLocations(locations []nginxLocation) bool {
	for _, l := range locations {
		if l.proxyPass != "" {
			return true
		}
	}
	return false
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
