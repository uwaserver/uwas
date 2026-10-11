package htaccess

import (
	"net"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// loadedModules tracks which Apache modules are "loaded" in UWAS.
// UWAS natively supports equivalent functionality for these modules.
var loadedModules = map[string]bool{
	"mod_rewrite.c":     true, // UWAS rewrite engine is fully compatible
	"mod_headers.c":     true, // UWAS supports custom headers
	"mod_expires.c":     true, // UWAS cache supports expires
	"mod_alias.c":       true, // UWAS supports Redirect/RedirectMatch
	"mod_deflate.c":     true, // UWAS gzip/brotli compression
	"mod_auth.c":        true, // UWAS supports BasicAuth
	"mod_auth_basic.c":  true,
	"mod_authn_file.c":  true,
	"mod_authz_core.c":  true,
	"mod_authz_user.c":  true,
	"mod_dir.c":         true,  // DirectoryIndex, DirectorySlash
	"mod_autoindex.c":   true,  // DirectoryListing
	"mod_env.c":         true,  // SetEnv/SetEnvIf (tracked but not enforced)
	"mod_mime.c":        true,  // MIME type handling
	"mod_negotiation.c": true,  // MultiViews
	"mod_setenvif.c":    true,  // SetEnvIf
	"mod_php.c":         false, // PHP handling is via FastCGI, not mod_php
	"mod_fcgid.c":       true,  // UWAS FastCGI support
	"mod_proxy.c":       true,  // UWAS proxy support
	"mod_proxy_http.c":  true,
	"mod_ssl.c":         false, // TLS handled by Go, not mod_ssl
}

// IsModuleLoaded returns true if the given module is loaded.
func IsModuleLoaded(module string) bool {
	return loadedModules[strings.ToLower(module)]
}

// AuthUserFileRequiresAuth reports whether the RuleSet names an htpasswd file,
// i.e. the directory requires HTTP Basic authentication.
//
// UWAS does not verify the credentials in that file, so a caller serving content
// from such a directory is silently discarding the operator's protection. The
// caller must therefore fail closed rather than treat the directive as absent.
// Only a converter-validated path reaches RuleSet.AuthUserFile — absolute and
// traversal forms are dropped at parse time (see the "authuserfile" case), so
// the path itself is never read. The directive is still an authentication
// requirement, though: AuthUserFileRejected remembers it so the common
// absolute form ("AuthUserFile /home/u/.htpasswd") also fails closed (F2800).
func AuthUserFileRequiresAuth(rules *RuleSet) bool {
	return rules != nil && (rules.AuthUserFile != "" || rules.AuthUserFileRejected)
}

// RuleSet represents the converted internal rules from .htaccess directives.
type RuleSet struct {
	RewriteEnabled   bool
	RewriteBase      string // base path for rewrites (e.g. "/" or "/subdir/")
	Rewrites         []RewriteRule
	Redirects        []RedirectRule
	ErrorDocuments   map[int]string
	DirectoryIndex   []string
	Headers          []HeaderRule
	ExpiresActive    bool
	ExpiresByType    map[string]string
	DirectoryListing *bool // nil = not set, true/false = explicit
	FollowSymlinks   *bool
	AuthType         string
	AuthName         string
	AuthUserFile     string
	// AuthUserFileRejected records an AuthUserFile directive whose path the
	// converter refused to keep (absolute or traversal). The directory is
	// still password-protected in Apache, so it must not be served openly.
	AuthUserFileRejected bool
	Require              string
	FilesMatch           []FilesMatchBlock
	PHPValues            map[string]string // php_value directives
	PHPFlags             map[string]string // php_flag directives (on/off → 1/0)
	// Access is the directory-wide decision of the top-level (outside
	// <Files>/<FilesMatch>) Require / Order-Allow-Deny directives for a
	// client that matches no IP condition. Use AccessFor for the decision
	// for a specific client.
	Access AccessDecision
	// topLevel keeps the top-level directives so AccessFor can evaluate
	// the IP-based forms against each client.
	topLevel []Directive
}

// AccessDecision is what a directory's top-level access-control directives
// decide for every request under it.
type AccessDecision int

const (
	AccessUnset   AccessDecision = iota // no unconditional Require/Allow/Deny
	AccessGranted                       // "Require all granted" / "Allow from all"
	AccessDenied                        // "Require all denied" / "Deny from all"
)

// RewriteRule is a converted rewrite rule.
type RewriteRule struct {
	Pattern    string
	Target     string
	Flags      string
	Conditions []RewriteCondition
}

// RewriteCondition is a converted rewrite condition.
type RewriteCondition struct {
	Variable string
	Pattern  string
	Flags    string
}

// RedirectRule is a converted redirect directive.
type RedirectRule struct {
	Status  int
	Pattern string
	Target  string
	IsRegex bool // RedirectMatch uses regex
}

// HeaderRule represents a Header directive.
type HeaderRule struct {
	Action string // "set", "unset", "append", "add"
	Name   string
	Value  string
}

// FilesMatchBlock represents a <FilesMatch> block.
type FilesMatchBlock struct {
	Pattern    string
	IsGlob     bool // <Files> wildcard pattern (path.Match), not a regex
	Directives []Directive
}

// NewRuleSet creates an empty RuleSet with initialized maps.
func NewRuleSet() *RuleSet {
	return &RuleSet{
		ErrorDocuments: make(map[int]string),
		ExpiresByType:  make(map[string]string),
		PHPValues:      make(map[string]string),
		PHPFlags:       make(map[string]string),
	}
}

// Convert transforms parsed .htaccess directives into an internal RuleSet.
func Convert(directives []Directive) *RuleSet {
	rules := NewRuleSet()
	var pendingConds []RewriteCondition

	for _, d := range directives {
		name := strings.ToLower(d.Name)

		switch name {
		case "rewriteengine":
			if len(d.Args) > 0 {
				rules.RewriteEnabled = strings.EqualFold(d.Args[0], "on")
			}

		case "rewritebase":
			if len(d.Args) > 0 {
				base := d.Args[0]
				// Ensure base ends with /
				if base != "/" && !strings.HasSuffix(base, "/") {
					base += "/"
				}
				rules.RewriteBase = base
			}

		case "rewritecond":
			if len(d.Args) >= 2 {
				cond := RewriteCondition{
					Variable: d.Args[0],
					Pattern:  d.Args[1],
				}
				if len(d.Args) >= 3 {
					cond.Flags = d.Args[2]
				}
				pendingConds = append(pendingConds, cond)
			}

		case "rewriterule":
			if len(d.Args) >= 2 {
				rr := RewriteRule{
					Pattern:    d.Args[0],
					Target:     d.Args[1],
					Conditions: pendingConds,
				}
				if len(d.Args) >= 3 {
					rr.Flags = d.Args[2]
				}
				pendingConds = nil
				rules.Rewrites = append(rules.Rewrites, rr)
			}

		case "redirect":
			rules.Redirects = append(rules.Redirects, parseRedirect(d, false))

		case "redirectmatch":
			rules.Redirects = append(rules.Redirects, parseRedirect(d, true))

		case "errordocument":
			if len(d.Args) >= 2 {
				code, _ := strconv.Atoi(d.Args[0])
				if code > 0 {
					rules.ErrorDocuments[code] = strings.Join(d.Args[1:], " ")
				}
			}

		case "directoryindex":
			rules.DirectoryIndex = append(rules.DirectoryIndex, d.Args...)

		case "header":
			if len(d.Args) >= 2 {
				hr := HeaderRule{Action: strings.ToLower(d.Args[0])}
				if hr.Action == "unset" {
					hr.Name = d.Args[1]
				} else if len(d.Args) >= 3 {
					hr.Name = d.Args[1]
					hr.Value = d.Args[2]
				}
				rules.Headers = append(rules.Headers, hr)
			}

		case "expiresactive":
			if len(d.Args) > 0 {
				rules.ExpiresActive = strings.EqualFold(d.Args[0], "on")
			}

		case "expiresbytype":
			if len(d.Args) >= 2 {
				rules.ExpiresByType[d.Args[0]] = d.Args[1]
			}

		case "options":
			for _, opt := range d.Args {
				switch strings.ToLower(opt) {
				case "-indexes":
					f := false
					rules.DirectoryListing = &f
				case "+indexes", "indexes":
					t := true
					rules.DirectoryListing = &t
				case "-followsymlinks":
					f := false
					rules.FollowSymlinks = &f
				case "+followsymlinks", "followsymlinks":
					t := true
					rules.FollowSymlinks = &t
				}
			}

		case "authtype":
			if len(d.Args) > 0 {
				rules.AuthType = d.Args[0]
			}

		case "authname":
			if len(d.Args) > 0 {
				rules.AuthName = d.Args[0]
			}

		case "authuserfile":
			if len(d.Args) > 0 {
				// Reject absolute paths and traversal to prevent reading arbitrary files.
				f := d.Args[0]
				clean := filepath.Clean(f)
				if !filepath.IsAbs(clean) && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
					rules.AuthUserFile = f
				} else {
					rules.AuthUserFileRejected = true
				}
			}

		case "require":
			rules.Require = strings.Join(d.Args, " ")

		case "php_value":
			if len(d.Args) >= 2 {
				rules.PHPValues[d.Args[0]] = strings.Join(d.Args[1:], " ")
			}

		case "php_flag":
			if len(d.Args) >= 2 {
				val := strings.ToLower(d.Args[1])
				if val == "on" || val == "1" || val == "true" {
					rules.PHPFlags[d.Args[0]] = "1"
				} else {
					rules.PHPFlags[d.Args[0]] = "0"
				}
			}

		default:
			// Block directives: <IfModule>, <FilesMatch>, etc.
			if len(d.Block) > 0 {
				if strings.EqualFold(d.Name, "IfModule") {
					// Check if the module is actually loaded.
					if len(d.Args) > 0 && IsModuleLoaded(d.Args[0]) {
						blockRules := Convert(d.Block)
						rules.Merge(blockRules)
					}
					// If module is not loaded, silently skip the block (Apache behavior).
				} else if strings.EqualFold(d.Name, "FilesMatch") || strings.EqualFold(d.Name, "Files") {
					block := FilesMatchBlock{Directives: d.Block}
					if len(d.Args) > 0 {
						block.Pattern = d.Args[0]
					}
					// <Files> takes a shell wildcard, or "~ regex" for the
					// regex form; <FilesMatch> is always a regex.
					if strings.EqualFold(d.Name, "Files") {
						if block.Pattern == "~" {
							block.Pattern = ""
							if len(d.Args) > 1 {
								block.Pattern = d.Args[1]
							}
						} else {
							block.IsGlob = true
						}
					}
					rules.FilesMatch = append(rules.FilesMatch, block)
				}
			}
		}
	}

	rules.topLevel = directives
	rules.Access = rules.AccessFor(nil)
	return rules
}

// AccessFor evaluates the directory's top-level (outside <Files>/
// <FilesMatch>) access-control directives for the client ip, descending into
// <IfModule> blocks whose condition holds. ip is the client address the
// server already resolved (RealIP, honouring trusted proxies); nil means
// unknown and matches no IP condition.
func (rs *RuleSet) AccessFor(ip net.IP) AccessDecision {
	return rs.AccessForMethod(ip, "")
}

// AccessForMethod is AccessFor for a request with the given HTTP method, so
// <Limit> and <LimitExcept> sections apply. An empty method means unknown:
// those sections are skipped.
func (rs *RuleSet) AccessForMethod(ip net.IP, method string) AccessDecision {
	if rs == nil {
		return AccessUnset
	}
	return evalAccess(rs.topLevel, ip, method)
}

// limitCovers reports whether method is among the section's listed methods;
// like Apache, a GET entry also covers HEAD.
func limitCovers(d Directive, method string) bool {
	for _, m := range d.Args {
		m = strings.ToUpper(m)
		if m == method || (method == "HEAD" && m == "GET") {
			return true
		}
	}
	return false
}

// evalAccess implements the access-control subset of mod_authz_core and
// mod_access_compat. Apache 2.4 Require lines (and <RequireAll>/
// <RequireAny>/<RequireNone> containers) are combined as an implicit
// <RequireAny> and take precedence; otherwise the 2.2 Order/Allow/Deny
// forms decide, with Apache's default "Order deny,allow". Supported
// conditions are "all granted|denied", "ip" (full, partial, CIDR or
// netmask), "local" and "not" inside <RequireAll>. Authentication-based
// lines ("valid-user", "user", "group", "file-owner", "file-group") are left
// to the AuthUserFile gate and ignored here. Anything else ("host", "env",
// "expr", hostnames in Allow/Deny ...) cannot be evaluated here and fails
// closed: it never grants, and an unevaluable "Deny from" counts as matching.
func evalAccess(ds []Directive, ip net.IP, method string) AccessDecision {
	var requires, allows, denies, limited []Directive
	order := ""
	var walk func([]Directive)
	walk = func(ds []Directive) {
		for _, d := range ds {
			switch strings.ToLower(d.Name) {
			case "require", "requireall", "requireany", "requirenone":
				requires = append(requires, d)
			case "allow":
				allows = append(allows, d)
			case "deny":
				denies = append(denies, d)
			case "order":
				order = strings.ToLower(strings.ReplaceAll(strings.Join(d.Args, ""), " ", ""))
			case "ifmodule":
				if ifModuleActive(d) {
					walk(d.Block)
				}
			case "limit":
				if method != "" && limitCovers(d, method) {
					limited = append(limited, d)
				}
			case "limitexcept":
				if method != "" && !limitCovers(d, method) {
					limited = append(limited, d)
				}
			}
		}
	}
	walk(ds)
	// An applicable <Limit>/<LimitExcept> section's own access directives
	// decide for this method (F2892): a denial wins, a grant overrides the
	// directory-wide directives.
	granted := false
	for _, l := range limited {
		switch evalAccess(l.Block, ip, method) {
		case AccessDenied:
			return AccessDenied
		case AccessGranted:
			granted = true
		}
	}
	if granted {
		return AccessGranted
	}
	if len(requireChildren(requires)) > 0 {
		if requireAnyPasses(requires, ip) {
			return AccessGranted
		}
		return AccessDenied
	}
	if hasAuthRequire(requires) {
		// Only authentication-based Require lines (valid-user, user, group,
		// ...) decide here, and credentials cannot be verified: fail closed
		// instead of serving the protected directory openly (F2801).
		return AccessDenied
	}
	if len(allows) == 0 && len(denies) == 0 {
		return AccessUnset
	}
	allowMatch := legacyMatches(allows, ip, false)
	denyMatch := legacyMatches(denies, ip, true)
	allowed := !denyMatch || allowMatch // "Order deny,allow" (default)
	if order == "allow,deny" || order == "mutual-failure" {
		allowed = allowMatch && !denyMatch
	}
	if allowed {
		return AccessGranted
	}
	return AccessDenied
}

func ifModuleActive(d Directive) bool {
	if len(d.Args) == 0 {
		return false
	}
	module, negated := strings.CutPrefix(d.Args[0], "!")
	return IsModuleLoaded(module) != negated
}

// requireChildren flattens active <IfModule> blocks among ds.
func requireChildren(ds []Directive) []Directive {
	var out []Directive
	for _, d := range ds {
		switch strings.ToLower(d.Name) {
		case "require":
			if !authRequire(d) {
				out = append(out, d)
			}
		case "requireall", "requireany", "requirenone":
			if len(requireChildren(d.Block)) > 0 {
				out = append(out, d)
			}
		case "ifmodule":
			if ifModuleActive(d) {
				out = append(out, requireChildren(d.Block)...)
			}
		}
	}
	return out
}

// hasAuthRequire reports whether any active Require line or container among
// ds (recursively) is authentication-based.
func hasAuthRequire(ds []Directive) bool {
	for _, d := range ds {
		switch strings.ToLower(d.Name) {
		case "require":
			if authRequire(d) {
				return true
			}
		case "requireall", "requireany", "requirenone":
			if hasAuthRequire(d.Block) {
				return true
			}
		case "ifmodule":
			if ifModuleActive(d) && hasAuthRequire(d.Block) {
				return true
			}
		}
	}
	return false
}

// authRequire reports whether d is an authentication-based Require line,
// which the AuthUserFile gate handles.
func authRequire(d Directive) bool {
	args := strings.Fields(strings.ToLower(strings.Join(d.Args, " ")))
	if len(args) > 0 && args[0] == "not" {
		args = args[1:]
	}
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "valid-user", "user", "group", "file-owner", "file-group":
		return true
	}
	return false
}

// requireAnyPasses: any child grants. A negated Require or <RequireNone>
// cannot grant on its own (Apache rejects them in this context).
func requireAnyPasses(ds []Directive, ip net.IP) bool {
	for _, d := range requireChildren(ds) {
		if requirePasses(d, ip, false) {
			return true
		}
	}
	return false
}

// requirePasses evaluates one Require line or container. inAll reports
// whether the parent is a <RequireAll>, where negative conditions apply.
func requirePasses(d Directive, ip net.IP, inAll bool) bool {
	switch strings.ToLower(d.Name) {
	case "requireany":
		return requireAnyPasses(d.Block, ip)
	case "requireall":
		kids := requireChildren(d.Block)
		if len(kids) == 0 {
			return false
		}
		for _, k := range kids {
			if !requirePasses(k, ip, true) {
				return false
			}
		}
		return true
	case "requirenone":
		if !inAll {
			return false
		}
		for _, k := range requireChildren(d.Block) {
			if requirePasses(k, ip, false) {
				return false
			}
		}
		return true
	}
	args := strings.Fields(strings.ToLower(strings.Join(d.Args, " ")))
	negated := len(args) > 0 && args[0] == "not"
	if negated {
		args = args[1:]
	}
	match, ok := requireMatches(args, ip)
	if !ok {
		return false // unevaluable condition: fail closed
	}
	if negated {
		return inAll && !match
	}
	return match
}

// requireMatches evaluates a Require condition; ok is false when it cannot
// be evaluated here.
func requireMatches(args []string, ip net.IP) (match, ok bool) {
	if len(args) == 0 {
		return false, false
	}
	switch args[0] {
	case "all":
		if len(args) == 2 && args[1] == "granted" {
			return true, true
		}
		if len(args) == 2 && args[1] == "denied" {
			return false, true
		}
	case "ip":
		if len(args) < 2 {
			return false, false
		}
		for _, spec := range args[1:] {
			n, valid := parseIPSpec(spec)
			if !valid {
				return false, false
			}
			if ip != nil && n.Contains(ip) {
				match = true
			}
		}
		return match, true
	case "local":
		return ip != nil && ip.IsLoopback(), true
	}
	return false, false
}

// legacyMatches reports whether any 2.2 Allow/Deny line ("from all", "from
// <ip|partial|cidr|ip/netmask> ...") matches ip. Unevaluable lines (env=,
// hostnames) count as matching for Deny and not matching for Allow.
func legacyMatches(ds []Directive, ip net.IP, isDeny bool) bool {
	for _, d := range ds {
		args := strings.Fields(strings.ToLower(strings.Join(d.Args, " ")))
		if len(args) < 2 || args[0] != "from" {
			if isDeny {
				return true
			}
			continue
		}
		for _, spec := range args[1:] {
			if spec == "all" {
				return true
			}
			n, valid := parseIPSpec(spec)
			if !valid {
				if isDeny {
					return true
				}
				continue
			}
			if ip != nil && n.Contains(ip) {
				return true
			}
		}
	}
	return false
}

// parseIPSpec parses the address forms mod_authz_host accepts: a full IP,
// a CIDR, "ip/netmask", or a partial IPv4 address such as "10.1" or "10.1.".
func parseIPSpec(spec string) (*net.IPNet, bool) {
	if addr, mask, found := strings.Cut(spec, "/"); found {
		if m := net.ParseIP(mask); m != nil && m.To4() != nil {
			ip := net.ParseIP(addr).To4()
			if ip == nil {
				return nil, false
			}
			im := net.IPMask(m.To4())
			if ones, bits := im.Size(); bits == 0 && ones == 0 {
				return nil, false
			}
			return &net.IPNet{IP: ip.Mask(im), Mask: im}, true
		}
		_, n, err := net.ParseCIDR(spec)
		return n, err == nil
	}
	if ip := net.ParseIP(spec); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return &net.IPNet{IP: v4, Mask: net.CIDRMask(32, 32)}, true
		}
		return &net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)}, true
	}
	parts := strings.Split(strings.TrimSuffix(spec, "."), ".")
	if len(parts) == 0 || len(parts) > 3 {
		return nil, false
	}
	ip := make(net.IP, 4)
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 || v > 255 || p == "" {
			return nil, false
		}
		ip[i] = byte(v)
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(8*len(parts), 32)}, true
}

// Merge combines another RuleSet into this one.
func (rs *RuleSet) Merge(other *RuleSet) {
	if other == nil {
		return
	}
	if other.RewriteEnabled {
		rs.RewriteEnabled = true
	}
	rs.Rewrites = append(rs.Rewrites, other.Rewrites...)
	rs.Redirects = append(rs.Redirects, other.Redirects...)
	rs.Headers = append(rs.Headers, other.Headers...)
	for k, v := range other.ErrorDocuments {
		rs.ErrorDocuments[k] = v
	}
	if len(other.DirectoryIndex) > 0 {
		rs.DirectoryIndex = other.DirectoryIndex
	}
	if other.DirectoryListing != nil {
		rs.DirectoryListing = other.DirectoryListing
	}
	if other.FollowSymlinks != nil {
		rs.FollowSymlinks = other.FollowSymlinks
	}
	if other.AuthUserFileRejected {
		rs.AuthUserFileRejected = true
	}
	if other.AuthUserFile != "" {
		// An AuthUserFile in a merged block without its own AuthType (split
		// across <IfModule> blocks) must not lose the requirement.
		rs.AuthUserFile = other.AuthUserFile
	}
	if other.AuthType != "" {
		rs.AuthType = other.AuthType
		rs.AuthName = other.AuthName
		rs.AuthUserFile = other.AuthUserFile
		rs.Require = other.Require
	}
	if other.ExpiresActive {
		rs.ExpiresActive = true
	}
	for k, v := range other.ExpiresByType {
		rs.ExpiresByType[k] = v
	}
	rs.FilesMatch = append(rs.FilesMatch, other.FilesMatch...)
	for k, v := range other.PHPValues {
		rs.PHPValues[k] = v
	}
	for k, v := range other.PHPFlags {
		rs.PHPFlags[k] = v
	}
}

func parseRedirect(d Directive, isRegex bool) RedirectRule {
	r := RedirectRule{
		Status:  302,
		IsRegex: isRegex,
	}

	switch len(d.Args) {
	case 2:
		// "Redirect /old /new" — unless the first argument is a status: the
		// status forms with an omitted target ("Redirect gone /old") also
		// have two arguments. Without this detection the keyword landed in
		// Pattern and the URL-path in Target, so gone rules never matched
		// at request time (404 instead of 410).
		if status, ok := redirectStatus(d.Args[0]); ok {
			r.Status = status
			r.Pattern = d.Args[1]
			return r
		}
		r.Pattern = d.Args[0]
		r.Target = d.Args[1]
	case 3:
		if status, ok := redirectStatus(d.Args[0]); ok {
			r.Status = status
			r.Pattern = d.Args[1]
			r.Target = d.Args[2]
			return r
		}
		// Apache requires a status first when three arguments are given;
		// be liberal with invalid config and treat it as path + target.
		r.Pattern = d.Args[0]
		r.Target = d.Args[1]
	}

	return r
}

// redirectStatus reports the redirect status named by s — a 3xx number, 410,
// or one of Apache's status keywords — and ok=false when s is a URL-path.
func redirectStatus(s string) (int, bool) {
	if code, err := strconv.Atoi(s); err == nil {
		if (code >= 300 && code <= 399) || code == 410 {
			return code, true
		}
		return 0, false
	}
	switch strings.ToLower(s) {
	case "permanent":
		return 301, true
	case "temp":
		return 302, true
	case "seeother":
		return 303, true
	case "gone":
		return 410, true
	}
	return 0, false
}

// MatchRedirect applies one parsed Redirect/RedirectRule to a request path
// with Apache mod_alias semantics: plain patterns are URL-path prefixes and
// the unmatched suffix is carried onto the target (also for absolute target
// URLs); regex patterns match the whole path and $1..$9 backrefs expand from
// the match. Returns ok=false when the rule does not match. An empty target
// means "gone" — the caller responds 410 without a Location header.
func MatchRedirect(rule RedirectRule, urlPath string) (location string, status int, ok bool) {
	if rule.Pattern == "" {
		return "", 0, false
	}
	status = rule.Status
	if status == 0 {
		status = 302
	}
	if rule.IsRegex {
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			// An uncompilable pattern is operator config to fix; skipping
			// the rule fails closed without taking the whole site down
			// (same policy as unwas rewrite patterns).
			return "", 0, false
		}
		m := re.FindStringSubmatch(urlPath)
		if m == nil {
			return "", 0, false
		}
		return sameHostRedirectPath(expandBackrefs(rule.Target, m)), status, true
	}
	if !strings.HasPrefix(urlPath, rule.Pattern) {
		return "", 0, false
	}
	// mod_alias matches whole path segments only. Without the boundary,
	// "Redirect /old https://new.example.com" matched "/old@evil.com" and
	// produced "https://new.example.com@evil.com", a visitor-chosen host (F2710).
	if rest := urlPath[len(rule.Pattern):]; rest != "" && rest[0] != '/' && !strings.HasSuffix(rule.Pattern, "/") {
		return "", 0, false
	}
	if rule.Target == "" {
		// The "gone" form has no target: report gone, no Location.
		return "", status, true
	}
	return sameHostRedirectPath(rule.Target + urlPath[len(rule.Pattern):]), status, true
}

// sameHostRedirectPath collapses the leading run of '/' and '\\' in a
// scheme-less redirect target to a single '/'. The Location is built from the
// decoded request path (suffix append, $N backrefs), so "Redirect 301 /old /"
// on "/old/evil.com" or "/old/%5Cevil.com" would otherwise yield the
// protocol-relative "//evil.com" (browsers treat "/\" the same way) — an
// off-site redirect. Apache prepends this server's scheme and host to URL-path
// targets, so the redirect stays on this host there too.
func sameHostRedirectPath(loc string) string {
	if loc == "" || (loc[0] != '/' && loc[0] != '\\') {
		return loc
	}
	return "/" + strings.TrimLeft(loc, "/\\")
}

// expandBackrefs replaces $1..$9 in target with the corresponding regexp
// submatches. Out-of-range or non-participating groups expand to "".
func expandBackrefs(target string, m []string) string {
	if !strings.Contains(target, "$") {
		return target
	}
	var b strings.Builder
	for i := 0; i < len(target); i++ {
		ch := target[i]
		if ch != '$' || i+1 >= len(target) || target[i+1] < '1' || target[i+1] > '9' {
			b.WriteByte(ch)
			continue
		}
		n := int(target[i+1] - '0')
		i++
		if n < len(m) {
			b.WriteString(m[n])
		}
	}
	return b.String()
}

// FilesMatchDenies reports whether any <FilesMatch>/<Files> block whose
// pattern matches filename carries a deny directive — Apache 2.4
// "Require all denied"/"Require ip …" denied forms or 2.2 "Deny from …".
// Deny wins across blocks (fail closed): a grant in one matching block does
// not unlock another matching block's deny. Uncompilable patterns skip that
// block only (same policy as rewrite patterns — operator config to fix).
func FilesMatchDenies(rules *RuleSet, filename string) bool {
	if rules == nil {
		return false
	}
	for _, block := range rules.FilesMatch {
		if block.Pattern == "" || !filesBlockMatches(block, filename) {
			continue
		}
		if directivesDeny(block.Directives) {
			return true
		}
	}
	return false
}

// FilesMatchDeniesFor is FilesMatchDenies evaluated for the client ip: a
// matching block denies when its access-control directives deny that client
// (so "Require ip"/"Allow from <ip>" allowlists deny everyone else and a
// "Deny from <ip>" denies only that address). A block without access
// directives does not deny.
func FilesMatchDeniesFor(rules *RuleSet, filename string, ip net.IP) bool {
	if rules == nil {
		return false
	}
	for _, block := range rules.FilesMatch {
		if block.Pattern == "" || !filesBlockMatches(block, filename) {
			continue
		}
		if evalAccess(block.Directives, ip, "") == AccessDenied {
			return true
		}
	}
	return false
}

// filesBlockMatches reports whether a <Files>/<FilesMatch> block applies to
// filename. A regex RE2 cannot compile — a PCRE-only construct such as the
// common "^(?!index\.php$).*\.php$" lookahead, or a typo Apache would refuse
// to load — is treated as matching every file, so the block's deny fails
// closed instead of being silently dropped.
func filesBlockMatches(block FilesMatchBlock, filename string) bool {
	if block.IsGlob {
		ok, err := path.Match(block.Pattern, filename)
		return err == nil && ok
	}
	re, err := regexp.Compile(block.Pattern)
	if err != nil {
		return true
	}
	return re.MatchString(filename)
}

// directivesDeny reports whether ds carries a deny directive, descending into
// <IfModule> blocks whose condition holds — the dual-syntax idiom wraps the
// 2.4 "Require all denied" and 2.2 "Deny from all" forms in <IfModule>.
func directivesDeny(ds []Directive) bool {
	for _, d := range ds {
		switch strings.ToLower(d.Name) {
		case "require":
			arg := strings.ToLower(strings.Join(d.Args, " "))
			if strings.Contains(arg, "denied") || strings.HasPrefix(arg, "deny") {
				return true
			}
		case "deny":
			// 2.2 form: "Deny from all" / "Deny from 1.2.3.4".
			return true
		case "ifmodule":
			if len(d.Args) == 0 {
				continue
			}
			module, negated := strings.CutPrefix(d.Args[0], "!")
			if IsModuleLoaded(module) != negated && directivesDeny(d.Block) {
				return true
			}
		}
	}
	return false
}
