package htaccess

import (
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
// this cannot be triggered by a hostile path.
func AuthUserFileRequiresAuth(rules *RuleSet) bool {
	return rules != nil && rules.AuthUserFile != ""
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
	Require          string
	FilesMatch       []FilesMatchBlock
	PHPValues        map[string]string // php_value directives
	PHPFlags         map[string]string // php_flag directives (on/off → 1/0)
}

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

	return rules
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
		return expandBackrefs(rule.Target, m), status, true
	}
	if !strings.HasPrefix(urlPath, rule.Pattern) {
		return "", 0, false
	}
	if rule.Target == "" {
		// The "gone" form has no target: report gone, no Location.
		return "", status, true
	}
	return rule.Target + urlPath[len(rule.Pattern):], status, true
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
		if block.Pattern == "" {
			continue
		}
		if block.IsGlob {
			if ok, err := path.Match(block.Pattern, filename); err != nil || !ok {
				continue
			}
		} else {
			re, err := regexp.Compile(block.Pattern)
			if err != nil || !re.MatchString(filename) {
				continue
			}
		}
		if directivesDeny(block.Directives) {
			return true
		}
	}
	return false
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
