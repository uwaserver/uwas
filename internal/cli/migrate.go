package cli

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
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
	// The YAML begins with "# WARNING" lines for access rules that were not
	// migrated; repeat the count on stderr, where redirecting stdout to a
	// file does not hide it (F8900).
	if n := strings.Count(yaml, "# WARNING:"); n > 0 {
		fmt.Fprintf(os.Stderr, "uwas: %d access-control directive(s) were NOT migrated; see the # WARNING lines at the top of the output\n", n)
	}
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
	locations    []apacheProxy // path-scoped ProxyPass entries
	aliases      []string
	listen       string
	warnings     []string // access rules that are not migrated (F8901)
	notes        []string // other directives with no translation (F41003)
	php          bool     // a PHP handler is configured (F41000)
	phpFPM       string   // FPM address from SetHandler "proxy:...fcgi" (F41000)
}

type apacheProxy struct {
	path   string
	target string
	weight int // BalancerMember loadfactor (F42005); 0 = default
}

// apacheBalancerBlock matches `<Proxy balancer://name>`; other <Proxy ...>
// sections are access-control containers.
var apacheBalancerBlock = regexp.MustCompile(`(?i)^<proxy\s+"?balancer://([^\s/>"]+)[^\s>"]*"?\s*>$`)

type apacheMember struct {
	address string
	weight  int
}

// expandApacheBalancers replaces ProxyPass targets that name a
// <Proxy balancer://name> cluster with the cluster's members. A cluster used
// as a path-scoped target can only name one backend, so its first member is
// used and the vhost is annotated. Config validation rejects a literal
// balancer:// upstream, which used to make the whole converted file unloadable
// (F42005).
func expandApacheBalancers(vhosts []apacheVHost, balancers map[string][]apacheMember) {
	name := func(target string) (string, bool) {
		if !hasPrefixFold(target, "balancer://") {
			return "", false
		}
		n := target[len("balancer://"):]
		if i := strings.Index(n, "/"); i >= 0 {
			n = n[:i]
		}
		return n, true
	}
	for i := range vhosts {
		vh := &vhosts[i]
		var expanded []apacheProxy
		for _, p := range vh.proxyPass {
			n, ok := name(p.target)
			if !ok {
				expanded = append(expanded, p)
				continue
			}
			members := balancers[strings.ToLower(n)]
			if len(members) == 0 {
				vh.notes = append(vh.notes, "ProxyPass "+p.path+" "+p.target+" (no <Proxy balancer://"+n+"> block with BalancerMember found; not migrated)")
				continue
			}
			for _, m := range members {
				expanded = append(expanded, apacheProxy{path: p.path, target: m.address, weight: m.weight})
			}
		}
		vh.proxyPass = expanded
		var locs []apacheProxy
		for _, l := range vh.locations {
			n, ok := name(l.target)
			if !ok {
				locs = append(locs, l)
				continue
			}
			members := balancers[strings.ToLower(n)]
			if len(members) == 0 {
				vh.notes = append(vh.notes, "ProxyPass "+l.path+" "+l.target+" (no <Proxy balancer://"+n+"> block with BalancerMember found; not migrated)")
				continue
			}
			if len(members) > 1 {
				vh.notes = append(vh.notes, "ProxyPass "+l.path+" "+l.target+" (a path-scoped proxy takes one backend; only "+members[0].address+" was kept)")
			}
			locs = append(locs, apacheProxy{path: l.path, target: members[0].address})
		}
		vh.locations = locs
	}
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
	if n := strings.Count(yaml, "# WARNING:"); n > 0 {
		fmt.Fprintf(os.Stderr, "uwas: %d directive(s) were NOT migrated or need review; see the # WARNING lines at the top of the output\n", n)
	}
	return nil
}

func parseApacheConfig(f *os.File) []apacheVHost {
	var vhosts []apacheVHost
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 64KB initial, 1MB max
	inVHost := false
	var current apacheVHost
	var global []string // directives outside any vhost that are not migrated
	balancers := map[string][]apacheMember{}
	curBalancer := ""

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// A trailing backslash continues the directive on the next line
		// ("ServerAlias a.test \\\n b.test"); without joining, the "\\" became
		// an alias and the continued names were lost (F41001).
		for strings.HasSuffix(line, "\\") && !strings.HasPrefix(line, "#") && scanner.Scan() {
			line = strings.TrimSpace(strings.TrimSuffix(line, "\\")) + " " + strings.TrimSpace(scanner.Text())
		}

		// Skip comments and empty lines
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// <Proxy balancer://name> ... BalancerMember ... </Proxy> (F42005); the
		// cluster may sit inside or outside a VirtualHost and be defined after
		// the ProxyPass that names it, so members are collected here and
		// ProxyPass targets are resolved once the file has been read.
		if m := apacheBalancerBlock.FindStringSubmatch(line); m != nil {
			curBalancer = strings.ToLower(m[1])
			continue
		}
		if curBalancer != "" {
			if hasPrefixFold(line, "</proxy") {
				curBalancer = ""
			} else if hasPrefixFold(line, "balancermember") {
				parts := splitApacheArgs(line)
				if len(parts) >= 2 {
					mem := apacheMember{address: parts[1]}
					for _, kv := range parts[2:] {
						if v, ok := strings.CutPrefix(strings.ToLower(kv), "loadfactor="); ok {
							if w, err := strconv.Atoi(v); err == nil && w > 0 {
								mem.weight = w
							}
						}
					}
					balancers[curBalancer] = append(balancers[curBalancer], mem)
				}
			}
			continue
		}

		// Apache directive and section names are case-insensitive (F41002).
		if hasPrefixFold(line, "<virtualhost") {
			inVHost = true
			current = apacheVHost{}
			// Extract listen address from <VirtualHost *:443>
			if addr := extractApacheVHostAddr(line); addr != "" {
				current.listen = addr
			}
			continue
		}

		if hasPrefixFold(line, "</virtualhost") {
			inVHost = false
			if len(vhosts) == 0 && len(global) > 0 {
				current.notes = append(append([]string(nil), global...), current.notes...)
			}
			vhosts = append(vhosts, current)
			continue
		}

		if !inVHost {
			if apacheUnmigratedDirective(strings.ToLower(line)) {
				global = append(global, "[outside any VirtualHost] "+line)
			}
			continue
		}

		// Parse directives inside VirtualHost
		lower := strings.ToLower(line)

		if apacheAccessDirective(lower) {
			current.warnings = append(current.warnings, line)
		}
		if apacheUnmigratedDirective(lower) {
			current.notes = append(current.notes, line)
		}
		if isPHP, fpm := apachePHPHandler(line, lower); isPHP {
			current.php = true
			if fpm != "" && current.phpFPM == "" {
				current.phpFPM = fpm
			}
		}

		if strings.HasPrefix(lower, "servername") {
			current.serverName = stripApachePort(extractApacheValue(line))
		}
		if strings.HasPrefix(lower, "serveralias") {
			for _, a := range splitApacheArgs(line)[1:] {
				if a = stripApachePort(a); a != "" {
					current.aliases = append(current.aliases, a)
				}
			}
		}
		if strings.HasPrefix(lower, "documentroot") {
			current.documentRoot = extractApacheValue(line)
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
			parts := splitApacheArgs(line)
			// "ProxyPass /static !" excludes a path from proxying; it has no
			// upstream to migrate (F8601).
			if len(parts) >= 3 && parts[2] != "!" {
				p := apacheProxy{path: parts[1], target: parts[2]}
				if p.path == "/" {
					current.proxyPass = append(current.proxyPass, p)
				} else {
					current.locations = append(current.locations, p)
				}
			}
		}
	}

	expandApacheBalancers(vhosts, balancers)
	return vhosts
}

// stripApachePort drops the ":port" of "ServerName host:port" (and of an
// alias): a UWAS host never carries a port (F8600).
func stripApachePort(h string) string {
	if i := strings.LastIndex(h, ":"); i > 0 && !strings.Contains(h[i+1:], "]") && strings.Count(h, ":") == 1 {
		return h[:i]
	}
	return h
}

// mergeApacheVHosts folds the vhosts of one ServerName (the usual *:80 and
// *:443 pair) into a single domain; two domains with one host fail config
// validation (F8600). The first non-empty value of each setting wins, except
// that a certificate pair or an https listener is never lost to a plain one.
func mergeApacheVHosts(vhosts []apacheVHost) []apacheVHost {
	var out []apacheVHost
	idx := map[string]int{}
	for _, vh := range vhosts {
		key := strings.ToLower(vh.serverName)
		i, ok := idx[key]
		if key == "" || !ok {
			if key != "" {
				idx[key] = len(out)
			}
			out = append(out, vh)
			continue
		}
		m := &out[i]
		if m.documentRoot == "" {
			m.documentRoot = vh.documentRoot
		}
		m.sslEngine = m.sslEngine || vh.sslEngine
		if m.sslCertFile == "" {
			m.sslCertFile, m.sslKeyFile = vh.sslCertFile, vh.sslKeyFile
		}
		if strings.Contains(vh.listen, "443") && !strings.Contains(m.listen, "443") {
			m.listen = vh.listen
		}
		m.proxyPass = append(m.proxyPass, vh.proxyPass...)
		m.locations = append(m.locations, vh.locations...)
		m.aliases = append(m.aliases, vh.aliases...)
		m.warnings = append(m.warnings, vh.warnings...)
		m.notes = append(m.notes, vh.notes...)
		m.php = m.php || vh.php
		if m.phpFPM == "" {
			m.phpFPM = vh.phpFPM
		}
	}
	return out
}

// apacheAccessDirective reports a directive that restricts or redirects access
// and has no translation in the migrated config: the site is more open after
// migration than before, so it is reported instead of dropped (F8901). lower is
// the lower-cased, trimmed line.
func apacheAccessDirective(lower string) bool {
	for _, p := range []string{
		"<directory", "<location", "<files", "<limit",
		"require ", "<requireall", "<requireany", "<requirenone",
		"order ", "allow from", "deny from",
		"authtype", "authname", "authuserfile", "authgroupfile",
		"redirect ", "redirectmatch", "redirectpermanent", "rewriterule", "rewritecond",
	} {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// apacheUnmigratedDirective reports a directive that changes what the site
// serves and has no translation: a path mapped outside the document root, a
// regex ProxyPass, another file's vhosts, or the intermediate certificate chain
// (F41003). lower is the lower-cased, trimmed line.
func apacheUnmigratedDirective(lower string) bool {
	for _, p := range []string{
		"proxypassmatch", "alias ", "aliasmatch", "scriptalias",
		"include ", "includeoptional ", "sslcertificatechainfile",
	} {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

// apachePHPHandler reports whether a directive makes PHP run for the vhost and,
// for SetHandler "proxy:...fcgi://", the FPM address UWAS should use (F41000).
// Source viewers (x-httpd-php-source, .phps) and "SetHandler none" are not PHP.
func apachePHPHandler(line, lower string) (bool, string) {
	switch {
	case strings.HasPrefix(lower, "php_value ") || strings.HasPrefix(lower, "php_admin_value ") ||
		strings.HasPrefix(lower, "php_flag ") || strings.HasPrefix(lower, "php_admin_flag "):
		return true, ""
	case strings.HasPrefix(lower, "sethandler "):
		val := strings.ToLower(extractApacheValue(line))
		if strings.HasPrefix(val, "proxy:") && strings.Contains(val, "fcgi") {
			return true, apacheFPMAddress(extractApacheValue(line))
		}
		return strings.Contains(val, "php") && !strings.Contains(val, "php-source"), ""
	case strings.HasPrefix(lower, "addhandler ") || strings.HasPrefix(lower, "addtype "):
		return strings.Contains(lower, "php") && !strings.Contains(lower, "php-source") && !strings.Contains(lower, ".phps"), ""
	}
	return false, ""
}

// apacheFPMAddress turns "proxy:unix:/run/php/fpm.sock|fcgi://localhost" into
// "unix:/run/php/fpm.sock" and "proxy:fcgi://127.0.0.1:9000/" into
// "127.0.0.1:9000".
func apacheFPMAddress(v string) string {
	v = strings.TrimPrefix(v, "proxy:")
	if strings.HasPrefix(v, "unix:") {
		if i := strings.Index(v, "|"); i > 0 {
			v = v[:i]
		}
		return v
	}
	v = strings.TrimPrefix(v, "fcgi://")
	if i := strings.Index(v, "/"); i >= 0 {
		v = v[:i]
	}
	return v
}

var apacheVHostAddrRegexp = regexp.MustCompile(`(?i)<VirtualHost\s+([^>]+)>`)

func extractApacheVHostAddr(line string) string {
	m := apacheVHostAddrRegexp.FindStringSubmatch(line)
	if len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// splitApacheArgs splits a directive line into whitespace-separated arguments,
// honouring Apache's quoted arguments ("a b", 'a b') and the backslash escape
// of a quote or backslash inside them, so ProxyPass "/" "http://x/" yields
// unquoted arguments (F22000).
func splitApacheArgs(line string) []string {
	var args []string
	var cur strings.Builder
	var quote byte
	inArg := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else if c == '\\' && i+1 < len(line) && (line[i+1] == quote || line[i+1] == '\\') {
				i++
				cur.WriteByte(line[i])
			} else {
				cur.WriteByte(c)
			}
		case c == '"' || c == '\'':
			quote = c
			inArg = true
		case c == ' ' || c == '\t':
			if inArg {
				args = append(args, cur.String())
				cur.Reset()
				inArg = false
			}
		default:
			cur.WriteByte(c)
			inArg = true
		}
	}
	if inArg {
		args = append(args, cur.String())
	}
	return args
}

func extractApacheValue(line string) string {
	line = strings.TrimSpace(line)
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return ""
	}
	arg := strings.TrimSpace(line[len(parts[0]):])
	quote := arg[0]
	if quote != '"' && quote != '\'' {
		return parts[1]
	}
	var value strings.Builder
	for i := 1; i < len(arg); i++ {
		if arg[i] == quote {
			break
		}
		if arg[i] == '\\' && i+1 < len(arg) && (arg[i+1] == quote || arg[i+1] == '\\') {
			i++
		}
		value.WriteByte(arg[i])
	}
	return value.String()
}

func convertApacheToYAML(vhosts []apacheVHost) string {
	var b strings.Builder
	merged := mergeApacheVHosts(vhosts)
	// A vhost without ServerName gets a placeholder host; two of them used to
	// share "example.com", which config validation rejects for the whole file
	// (F41004).
	hosts := make([]string, len(merged))
	placeholders := 0
	for i, vh := range merged {
		hosts[i] = vh.serverName
		if hosts[i] == "" {
			placeholders++
			hosts[i] = "example.com"
			if placeholders > 1 {
				hosts[i] = fmt.Sprintf("example-%d.com", placeholders)
			}
		}
	}
	for i, vh := range merged {
		host := hosts[i]
		if vh.serverName == "" {
			b.WriteString("# WARNING: not migrated, edit the host: " + host + ": the VirtualHost has no ServerName\n")
		}
		for _, w := range vh.warnings {
			b.WriteString("# WARNING: not migrated, the site is NOT protected by it: " + host + ": " + w + "\n")
		}
		for _, n := range vh.notes {
			b.WriteString("# WARNING: not migrated, behaviour differs after migration: " + host + ": " + n + "\n")
		}
	}
	b.WriteString("# Converted from Apache config by UWAS\n")
	b.WriteString("domains:\n")

	for i, vh := range merged {
		host := hosts[i]

		b.WriteString(fmt.Sprintf("  - host: %s\n", nginxmigrate.YAMLScalar(host)))
		if aliases := uniqueApacheAliases(host, vh.aliases); len(aliases) > 0 {
			b.WriteString("    aliases:\n")
			for _, a := range aliases {
				b.WriteString(fmt.Sprintf("      - %s\n", nginxmigrate.YAMLScalar(a)))
			}
		}

		// Determine type
		hasProxy := len(vh.proxyPass) > 0
		switch {
		case hasProxy:
			b.WriteString("    type: proxy\n")
		case vh.php:
			// A static site answers 403 for every .php file (F41000).
			b.WriteString("    type: php\n")
		default:
			b.WriteString("    type: static\n")
		}

		if vh.documentRoot != "" {
			b.WriteString(fmt.Sprintf("    root: %s\n", nginxmigrate.YAMLScalar(vh.documentRoot)))
		}

		// SSL
		if vh.sslEngine || vh.sslCertFile != "" {
			b.WriteString("    ssl:\n")
			if vh.sslCertFile != "" {
				b.WriteString("      mode: manual\n")
				b.WriteString(fmt.Sprintf("      cert: %s\n", nginxmigrate.YAMLScalar(vh.sslCertFile)))
				if vh.sslKeyFile != "" {
					b.WriteString(fmt.Sprintf("      key: %s\n", nginxmigrate.YAMLScalar(vh.sslKeyFile)))
				}
			} else {
				b.WriteString("      mode: auto\n")
			}
		} else if strings.Contains(vh.listen, "443") {
			b.WriteString("    ssl:\n")
			b.WriteString("      mode: auto\n")
		}

		if !hasProxy && vh.php && vh.phpFPM != "" {
			b.WriteString("    php:\n")
			b.WriteString(fmt.Sprintf("      fpm_address: %s\n", nginxmigrate.YAMLScalar(vh.phpFPM)))
		}

		// Proxy config
		if hasProxy {
			b.WriteString("    proxy:\n")
			b.WriteString("      upstreams:\n")
			for _, p := range vh.proxyPass {
				b.WriteString(fmt.Sprintf("        - address: %s\n", nginxmigrate.YAMLScalar(p.target)))
				if p.weight > 0 {
					b.WriteString(fmt.Sprintf("          weight: %d\n", p.weight))
				}
			}
		}

		// Path-scoped ProxyPass: only that prefix goes to the backend, the
		// rest of the vhost keeps being served from its root (F8601).
		if len(vh.locations) > 0 {
			b.WriteString("    locations:\n")
			for _, l := range vh.locations {
				b.WriteString(fmt.Sprintf("      - match: %s\n", nginxmigrate.YAMLScalar(l.path)))
				b.WriteString(fmt.Sprintf("        proxy_pass: %s\n", nginxmigrate.YAMLScalar(strings.TrimRight(l.target, "/"))))
				b.WriteString("        strip_prefix: true\n")
			}
		}

		b.WriteString("\n")
	}

	return b.String()
}

// uniqueApacheAliases returns the aliases without repeats and without the
// primary host itself.
func uniqueApacheAliases(host string, aliases []string) []string {
	seen := map[string]bool{strings.ToLower(host): true}
	var out []string
	for _, a := range aliases {
		if k := strings.ToLower(a); !seen[k] {
			seen[k] = true
			out = append(out, a)
		}
	}
	return out
}
