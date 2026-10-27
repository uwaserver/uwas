package htaccess

import (
	"net"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/uwaserver/uwas/pkg/rxguard"
)

// loadedModules tracks which Apache modules are "loaded" in UWAS.
// UWAS natively supports equivalent functionality for these modules.
var loadedModules = map[string]bool{
	"mod_rewrite.c":         true, // UWAS rewrite engine is fully compatible
	"mod_headers.c":         true, // UWAS supports custom headers
	"mod_expires.c":         true, // UWAS cache supports expires
	"mod_alias.c":           true, // UWAS supports Redirect/RedirectMatch
	"mod_deflate.c":         true, // UWAS gzip/brotli compression
	"mod_auth.c":            true, // UWAS supports BasicAuth
	"mod_auth_basic.c":      true,
	"mod_authn_file.c":      true,
	"mod_authz_core.c":      true,
	"mod_authz_user.c":      true,
	"mod_authz_host.c":      true, // Require ip / Allow,Deny (F3000)
	"mod_access_compat.c":   true,
	"mod_authn_core.c":      true,
	"mod_authz_groupfile.c": true,
	"mod_dir.c":             true,  // DirectoryIndex, DirectorySlash
	"mod_autoindex.c":       true,  // DirectoryListing
	"mod_env.c":             true,  // SetEnv/SetEnvIf (tracked but not enforced)
	"mod_mime.c":            true,  // MIME type handling
	"mod_negotiation.c":     true,  // MultiViews
	"mod_setenvif.c":        true,  // SetEnvIf
	"mod_php.c":             false, // PHP handling is via FastCGI, not mod_php
	"mod_php5.c":            false,
	"mod_php7.c":            false,
	"mod_php8.c":            false,
	"mod_fcgid.c":           true, // UWAS FastCGI support
	"mod_proxy.c":           true, // UWAS proxy support
	"mod_proxy_http.c":      true,
	"mod_ssl.c":             false, // TLS handled by Go, not mod_ssl
}

// IsModuleLoaded returns true if the given module is loaded.
func IsModuleLoaded(module string) bool {
	module = strings.ToLower(module)
	// Apache also accepts the module's identifier ("rewrite_module") in
	// <IfModule>; without the mapping such a block is skipped, silently
	// dropping any deny it carries (F3000).
	if base, ok := strings.CutSuffix(module, "_module"); ok {
		module = "mod_" + base + ".c"
	}
	return loadedModules[module]
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
	ExpiresDefault   string
	MimeTypes        map[string]string
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
	// PHPDisabled is the directory's decision to stop executing PHP scripts:
	// "php_flag engine off", "RemoveHandler .php" and "SetHandler none" all
	// say it, and "php_flag engine on" says the opposite. nil = not set. A
	// deeper directory overrides a shallower one (see PHPDisabledFor). Without
	// it the usual "no PHP in uploads/" protection did nothing and an uploaded
	// script ran (F12100).
	PHPDisabled *bool
	// Access is the directory-wide decision of the top-level (outside
	// <Files>/<FilesMatch>) Require / Order-Allow-Deny directives for a
	// client that matches no IP condition. Use AccessFor for the decision
	// for a specific client.
	Access AccessDecision
	// topLevel keeps the top-level directives so AccessFor can evaluate
	// the IP-based forms against each client.
	topLevel []Directive

	// ErrorTexts holds the ErrorDocument arguments written as a double-quoted
	// message ('ErrorDocument 404 "Not here"'), which Apache sends as the body
	// instead of reading a file (F9401). ErrorDocuments keeps the same text so
	// code that only knows paths sees what it always saw.
	ErrorTexts map[int]string

	// ExpiresActiveSet records that an ExpiresActive directive was present, so
	// "ExpiresActive Off" in a sub-directory can switch an inherited "On" off
	// (F9402).
	ExpiresActiveSet bool

	// LimitRequestBody caps the request body, in bytes, for the directory and
	// everything below it; 0 lifts a cap set above. nil = not set. A deeper level
	// overrides a shallower one (see LimitRequestBodyFor). It was parsed by
	// nothing, so a tenant who capped uploads in .htaccess had no cap (F16901).
	LimitRequestBody *int64
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
	// Env is the "env=VAR" / "env=!VAR" condition the rule carries, as written
	// ("VAR" or "!VAR"); empty for an unconditional rule. See AppliesTo.
	Env string
}

// FilesMatchBlock represents a <FilesMatch> block.
type FilesMatchBlock struct {
	Pattern    string
	IsGlob     bool // <Files> wildcard pattern (path.Match), not a regex
	Directives []Directive
	// Uncertain marks a block kept from a section whose condition could not be
	// decided: its denies stay in force (F5501) but nothing else it carries
	// (Header, ...) is applied.
	Uncertain bool
}

// parseHeaderDirective converts one Header directive.
func parseHeaderDirective(d Directive) (HeaderRule, bool) {
	args := d.Args
	// "Header always set ..." / "Header onsuccess set ..." only choose
	// which responses the rule covers; the action follows (F5800).
	if len(args) > 0 && (strings.EqualFold(args[0], "always") || strings.EqualFold(args[0], "onsuccess")) {
		args = args[1:]
	}
	if len(args) < 2 {
		return HeaderRule{}, false
	}
	hr := HeaderRule{Action: strings.ToLower(args[0])}
	rest := args[2:]
	if hr.Action == "unset" {
		hr.Name = args[1]
	} else if len(args) >= 3 {
		hr.Name = args[1]
		hr.Value = args[2]
		rest = args[3:]
	}
	// "... env=VAR" / "env=!VAR" limits the rule to requests where
	// the variable is (not) set (F8830).
	for _, a := range rest {
		if len(a) > 4 && strings.EqualFold(a[:4], "env=") {
			hr.Env = a[4:]
		}
	}
	return hr, true
}

// NewRuleSet creates an empty RuleSet with initialized maps.
func NewRuleSet() *RuleSet {
	return &RuleSet{
		ErrorDocuments: make(map[int]string),
		ErrorTexts:     make(map[int]string),
		ExpiresByType:  make(map[string]string),
		MimeTypes:      make(map[string]string),
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
					delete(rules.ErrorTexts, code)
					if len(d.Args) == 2 && len(d.Quoted) > 1 && d.Quoted[1] &&
						!strings.HasPrefix(d.Args[1], "/") && !strings.Contains(d.Args[1], "://") {
						rules.ErrorTexts[code] = d.Args[1]
					}
				}
			}

		case "directoryindex":
			rules.DirectoryIndex = append(rules.DirectoryIndex, d.Args...)

		case "header":
			if hr, ok := parseHeaderDirective(d); ok {
				rules.Headers = append(rules.Headers, hr)
			}

		case "addtype":
			// "AddType type .ext [.ext ...]" maps file extensions to a content
			// type (F9101).
			if len(d.Args) >= 2 {
				for _, ext := range d.Args[1:] {
					ext = strings.ToLower(ext)
					if !strings.HasPrefix(ext, ".") {
						ext = "." + ext
					}
					rules.MimeTypes[ext] = d.Args[0]
				}
			}

		case "expiresactive":
			if len(d.Args) > 0 {
				rules.ExpiresActive = strings.EqualFold(d.Args[0], "on")
				rules.ExpiresActiveSet = true
			}

		case "expiresbytype":
			if len(d.Args) >= 2 {
				rules.ExpiresByType[d.Args[0]] = d.Args[1]
			}

		case "expiresdefault":
			// The lifetime of every type ExpiresByType does not name (F9102).
			if len(d.Args) >= 1 {
				rules.ExpiresDefault = strings.Join(d.Args, " ")
			}

		case "options":
			// A list with at least one unsigned option is absolute: it replaces the
			// inherited options, so unless it names Indexes the listing is off
			// ("Options None", "Options FollowSymLinks"). It used to be ignored and
			// the listing stayed as the domain config or a parent file had it (F9400).
			if optionsAbsolute(d.Args) && !optionsNameIndexes(d.Args) {
				f := false
				rules.DirectoryListing = &f
			}
			// The same rule governs symlinks: an absolute list that does not name
			// FollowSymLinks (or All) leaves them off, so "Options None" and
			// "Options Indexes" refuse symlinks as Apache does.
			if optionsAbsolute(d.Args) && !optionsNameFollowSymlinks(d.Args) {
				f := false
				rules.FollowSymlinks = &f
			}
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
				case "+followsymlinks", "followsymlinks", "all":
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

		case "limitrequestbody":
			// Apache takes a byte count from 0 (unlimited) up; a value it would
			// refuse is not a cap the tenant can rely on, so it is not recorded.
			if len(d.Args) >= 1 {
				if n, err := strconv.ParseInt(d.Args[0], 10, 64); err == nil && n >= 0 {
					rules.LimitRequestBody = &n
				}
			}

		case "php_value":
			if len(d.Args) >= 2 && strings.EqualFold(d.Args[0], "engine") {
				// mod_php's engine switch is not an ini setting PHP-FPM knows;
				// it is the "run PHP here" decision (F12100).
				setPHPDisabled(rules, d.Args[1])
			} else if len(d.Args) >= 2 {
				rules.PHPValues[d.Args[0]] = strings.Join(d.Args[1:], " ")
			}

		case "removehandler":
			for _, a := range d.Args {
				if isPHPHandlerExt(a) {
					t := true
					rules.PHPDisabled = &t
				}
			}

		case "sethandler":
			if len(d.Args) >= 1 {
				switch strings.ToLower(d.Args[0]) {
				case "none", "default-handler":
					t := true
					rules.PHPDisabled = &t
				}
			}

		case "php_flag":
			if len(d.Args) >= 2 && strings.EqualFold(d.Args[0], "engine") {
				setPHPDisabled(rules, d.Args[1])
			} else if len(d.Args) >= 2 {
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
				if isConditional(d.Name) {
					// Only a section whose condition UWAS can decide and that
					// holds is applied; an inactive <IfModule> is skipped
					// silently (Apache behavior).
					switch conditionState(d) {
					case condActive:
						blockRules := Convert(d.Block)
						rules.Merge(blockRules)
					case condInactive:
						// "<IfModule mod_php7.c> php_flag engine off" is the
						// usual way to keep PHP out of uploads/. mod_php is not
						// loaded here, but the switch only ever restricts, so it
						// is honoured (F12100).
						if phpModuleEngineOff(d) {
							t := true
							rules.PHPDisabled = &t
						}
					case condUnknown:
						// An <IfModule mod_php*.c> the table does not list still
						// only ever restricts when it turns the engine off.
						if phpModuleEngineOff(d) {
							t := true
							rules.PHPDisabled = &t
						}
						// A request-body cap in a section whose condition cannot
						// be decided only ever restricts, so it is kept (F16901).
						if lb := Convert(d.Block).LimitRequestBody; lb != nil && *lb > 0 {
							rules.LimitRequestBody = lb
						}
						// Only the per-file denies of a section whose condition
						// cannot be decided stay in force; dropping them would
						// serve the files it protects (F5501).
						for _, fb := range Convert(d.Block).FilesMatch {
							fb.Uncertain = true
							rules.FilesMatch = append(rules.FilesMatch, fb)
						}
					}
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
	return rs.AccessForRequest(ip, method, nil)
}

// AccessForRequest is AccessForMethod with the request's attributes, so
// "env=" conditions (SetEnvIf, BrowserMatch, SetEnv) can be decided: a good
// User-Agent is no longer refused by "Deny from env=bad_bot" (F6700). With
// nil attrs every env condition fails closed.
func (rs *RuleSet) AccessForRequest(ip net.IP, method string, attrs *RequestAttrs) AccessDecision {
	if rs == nil {
		return AccessUnset
	}
	return evalAccess(rs.topLevel, ip, method, buildEnv(rs.topLevel, attrs))
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
// netmask), "local", "env" (decided from SetEnvIf/BrowserMatch/SetEnv when
// the request attributes are known) and "not" inside <RequireAll>. Authentication-based
// lines ("valid-user", "user", "group", "file-owner", "file-group") are left
// to the AuthUserFile gate and ignored here. Anything else ("host", "expr",
// hostnames in Allow/Deny, an env condition without request attributes ...)
// cannot be evaluated here and fails
// closed: it never grants, and an unevaluable "Deny from" counts as matching.
func evalAccess(ds []Directive, ip net.IP, method string, env *envState) AccessDecision {
	var requires, allows, denies, limited []Directive
	order := ""
	opaqueDeny := false
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
			case "ifmodule", "ifdefine", "ifversion", "if", "elseif", "else", "ifdirective", "ifsection", "iffile":
				switch conditionState(d) {
				case condActive:
					walk(d.Block)
				case condUnknown:
					// A section whose condition cannot be decided here may
					// well apply; dropping its denial would serve the
					// directory openly (F5201).
					if unevaluableDeny(d.Block) {
						opaqueDeny = true
					}
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
	if opaqueDeny {
		return AccessDenied
	}
	// An applicable <Limit>/<LimitExcept> section's own access directives
	// decide for this method (F2892): a denial wins, a grant overrides the
	// directory-wide directives.
	granted := false
	for _, l := range limited {
		switch evalAccess(l.Block, ip, method, env) {
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
		if requireAnyPasses(requires, ip, env) {
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
	allowMatch := legacyMatches(allows, ip, false, env)
	denyMatch := legacyMatches(denies, ip, true, env)
	allowed := !denyMatch || allowMatch // "Order deny,allow" (default)
	if order == "allow,deny" || order == "mutual-failure" {
		allowed = allowMatch && !denyMatch
	}
	if allowed {
		return AccessGranted
	}
	return AccessDenied
}

// emulatedApacheVersion is what <IfVersion> compares against: the 2.4 series
// whose access syntax (Require) UWAS implements.
const emulatedApacheVersion = "2.4.62"

type condState int

const (
	condInactive condState = iota
	condActive
	condUnknown // the condition cannot be decided here
)

// legacyAllowlist reports whether ds uses the 2.2 allowlist form: "Order
// Allow,Deny" (or "Mutual-failure") without an "Allow from all" line denies every
// client the Allow lines do not name. Only Deny lines and Require were counted
// as restrictions, so an allowlist inside a section that cannot be decided was
// dropped and the directory served openly (F12700).
func legacyAllowlist(ds []Directive) bool {
	order := ""
	for _, d := range ds {
		if strings.EqualFold(d.Name, "order") {
			order = strings.ToLower(strings.ReplaceAll(strings.Join(d.Args, ""), " ", ""))
		}
	}
	if order != "allow,deny" && order != "mutual-failure" {
		return false
	}
	for _, d := range ds {
		if !strings.EqualFold(d.Name, "allow") {
			continue
		}
		for _, a := range strings.Fields(strings.ToLower(strings.Join(d.Args, " "))) {
			if a == "all" {
				return false
			}
		}
	}
	return true
}

// isConditional reports whether name is a conditional section.
func isConditional(name string) bool {
	switch strings.ToLower(name) {
	case "ifmodule", "ifdefine", "ifversion", "if", "elseif", "else", "ifdirective", "ifsection", "iffile":
		return true
	}
	return false
}

// conditionState decides a conditional section. <IfModule>, <IfDefine> (the
// server is started with no -D flags) and <IfVersion> (emulating Apache
// 2.4) are decided; <If>/<ElseIf>/<Else> expressions and the Directive/
// Section/File probes are not (F5200, F5201).
func conditionState(d Directive) condState {
	switch strings.ToLower(d.Name) {
	case "ifmodule":
		return ifModuleState(d)
	case "ifdefine":
		if len(d.Args) == 0 {
			return condUnknown
		}
		if strings.HasPrefix(d.Args[0], "!") {
			return condActive
		}
		return condInactive
	case "ifversion":
		if on, ok := ifVersionHolds(d.Args); ok {
			if on {
				return condActive
			}
			return condInactive
		}
	}
	return condUnknown
}

// ifVersionHolds evaluates "[!][op] version" against emulatedApacheVersion.
// Like Apache, a version with fewer components compares only those.
func ifVersionHolds(args []string) (holds, ok bool) {
	spec := strings.TrimSpace(strings.Join(args, " "))
	neg := false
	if rest, found := strings.CutPrefix(spec, "!"); found {
		neg, spec = true, strings.TrimSpace(rest)
	}
	op := "="
	for _, o := range []string{"==", ">=", "<=", "=", ">", "<", "~"} {
		if rest, found := strings.CutPrefix(spec, o); found {
			op, spec = o, strings.TrimSpace(rest)
			break
		}
	}
	if spec == "" {
		return false, false
	}
	if op == "~" {
		re, err := rxguard.Compile(strings.Trim(spec, `"'`))
		if err != nil {
			return false, false
		}
		return re.MatchString(emulatedApacheVersion) != neg, true
	}
	want := strings.Split(spec, ".")
	have := strings.Split(emulatedApacheVersion, ".")
	cmp := 0
	for i, w := range want {
		n, err := strconv.Atoi(w)
		if err != nil || n < 0 {
			return false, false
		}
		h := 0
		if i < len(have) {
			h, _ = strconv.Atoi(have[i])
		}
		if h != n {
			cmp = 1
			if h < n {
				cmp = -1
			}
			break
		}
	}
	var res bool
	switch op {
	case "=", "==":
		res = cmp == 0
	case ">":
		res = cmp > 0
	case ">=":
		res = cmp >= 0
	case "<":
		res = cmp < 0
	case "<=":
		res = cmp <= 0
	}
	return res != neg, true
}

// unevaluableDeny reports whether ds (recursively) holds a directive that can
// deny a client: a Deny line or any Require other than "all granted".
func unevaluableDeny(ds []Directive) bool {
	if legacyAllowlist(ds) {
		return true
	}
	for _, d := range ds {
		switch strings.ToLower(d.Name) {
		case "deny":
			return true
		case "require":
			if strings.ToLower(strings.Join(d.Args, " ")) != "all granted" {
				return true
			}
		case "files", "filesmatch":
			continue // denies only the files it names, not the directory
		}
		if unevaluableDeny(d.Block) {
			return true
		}
	}
	return false
}

// ifModuleState decides an <IfModule>. A module the table lists (loaded or
// deliberately not) is decided. A module it does not list at all —
// mod_auth_digest, mod_authnz_ldap, mod_authz_owner, ... — may well be loaded on
// the server the file was written for, so its section is undecided rather than
// inactive: a denial or authentication requirement inside it must not be
// silently dropped (F12700). The negated form keeps "not loaded" for an unlisted
// module, which applies the section; an <IfModule> without a module name is
// undecided too.
func ifModuleState(d Directive) condState {
	if len(d.Args) == 0 {
		return condUnknown
	}
	module, negated := strings.CutPrefix(d.Args[0], "!")
	loaded := IsModuleLoaded(module)
	if negated {
		if loaded {
			return condInactive
		}
		return condActive
	}
	if loaded {
		return condActive
	}
	if !moduleListed(module) {
		return condUnknown
	}
	return condInactive
}

// moduleListed reports whether the table takes a position on module, as loaded
// or as deliberately not loaded.
func moduleListed(module string) bool {
	module = strings.ToLower(module)
	if base, ok := strings.CutSuffix(module, "_module"); ok {
		module = "mod_" + base + ".c"
	}
	_, ok := loadedModules[module]
	return ok
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
		case "ifmodule", "ifdefine", "ifversion":
			if conditionState(d) == condActive {
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
		case "ifmodule", "ifdefine", "ifversion":
			if conditionState(d) == condActive && hasAuthRequire(d.Block) {
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
func requireAnyPasses(ds []Directive, ip net.IP, env *envState) bool {
	for _, d := range requireChildren(ds) {
		if requirePasses(d, ip, false, env) {
			return true
		}
	}
	return false
}

// requirePasses evaluates one Require line or container. inAll reports
// whether the parent is a <RequireAll>, where negative conditions apply.
func requirePasses(d Directive, ip net.IP, inAll bool, env *envState) bool {
	switch strings.ToLower(d.Name) {
	case "requireany":
		return requireAnyPasses(d.Block, ip, env)
	case "requireall":
		// An authentication condition cannot be verified here, so the
		// container cannot be satisfied by its IP conditions alone (F5502).
		if hasAuthRequire(d.Block) {
			return false
		}
		kids := requireChildren(d.Block)
		if len(kids) == 0 {
			return false
		}
		for _, k := range kids {
			if !requirePasses(k, ip, true, env) {
				return false
			}
		}
		return true
	case "requirenone":
		if !inAll {
			return false
		}
		for _, k := range requireChildren(d.Block) {
			if requirePasses(k, ip, false, env) {
				return false
			}
			// A condition that cannot be evaluated here (Require expr, host,
			// method, ...) might match the client: "none of them match" is not
			// established, so the container must not grant (F12400).
			if requireUnknown(k, ip, env) {
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
	match, ok := requireMatches(args, ip, env)
	if !ok {
		return false // unevaluable condition: fail closed
	}
	if negated {
		return inAll && !match
	}
	return match
}

// requireUnknown reports whether the outcome of d depends on a condition that
// cannot be evaluated here and was not already settled by one that can.
func requireUnknown(d Directive, ip net.IP, env *envState) bool {
	switch strings.ToLower(d.Name) {
	case "requireany":
		if requireAnyPasses(d.Block, ip, env) {
			return false
		}
		if hasAuthRequire(d.Block) {
			return true
		}
		for _, k := range requireChildren(d.Block) {
			if requireUnknown(k, ip, env) {
				return true
			}
		}
		return false
	case "requireall":
		if hasAuthRequire(d.Block) {
			return true
		}
		unknown := false
		for _, k := range requireChildren(d.Block) {
			if requireUnknown(k, ip, env) {
				unknown = true
			} else if !requirePasses(k, ip, true, env) {
				return false // settled: one condition decidedly fails
			}
		}
		return unknown
	case "requirenone":
		for _, k := range requireChildren(d.Block) {
			if requirePasses(k, ip, false, env) {
				return false
			}
		}
		for _, k := range requireChildren(d.Block) {
			if requireUnknown(k, ip, env) {
				return true
			}
		}
		return false
	}
	args := strings.Fields(strings.ToLower(strings.Join(d.Args, " ")))
	if len(args) > 0 && args[0] == "not" {
		args = args[1:]
	}
	_, ok := requireMatches(args, ip, env)
	return !ok
}

// requireMatches evaluates a Require condition; ok is false when it cannot
// be evaluated here.
func requireMatches(args []string, ip net.IP, env *envState) (match, ok bool) {
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
	case "env":
		// "Require env A B": any of the variables is defined (F6700).
		if len(args) < 2 {
			return false, false
		}
		for _, name := range args[1:] {
			set, known := env.has(name)
			if !known {
				return false, false
			}
			if set {
				match = true
			}
		}
		return match, true
	}
	return false, false
}

// legacyMatches reports whether any 2.2 Allow/Deny line ("from all", "from
// <ip|partial|cidr|ip/netmask> ...") matches ip. Unevaluable lines (env=,
// hostnames) count as matching for Deny and not matching for Allow.
func legacyMatches(ds []Directive, ip net.IP, isDeny bool, env *envState) bool {
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
			if name, isEnv := strings.CutPrefix(spec, "env="); isEnv {
				neg := strings.HasPrefix(name, "!")
				set, known := env.has(strings.TrimPrefix(name, "!"))
				if !known {
					if isDeny {
						return true
					}
					continue
				}
				if set != neg {
					return true
				}
				continue
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

// DirSettings combines the settings that follow a directory chain — the
// .htaccess files from the document root down to the requested directory,
// outermost first — into one RuleSet: the deepest file wins for a setting it
// names, ExpiresByType and AddType entries merge per key, and
// "ExpiresActive Off" below switches an inherited "On" off. Rewrite, redirect,
// access and Header rules are not part of it; they have their own per-level
// evaluation. Only the document root's file used to be read for these, so an
// uploads/.htaccess with AddType, ExpiresDefault or DirectoryIndex had no effect
// (F9402). Nil entries are skipped.
func DirSettings(chain []*RuleSet) *RuleSet {
	out := &RuleSet{
		ErrorDocuments: make(map[int]string),
		ErrorTexts:     make(map[int]string),
		ExpiresByType:  make(map[string]string),
		MimeTypes:      make(map[string]string),
	}
	for _, rs := range chain {
		if rs == nil {
			continue
		}
		if rs.ExpiresActiveSet || rs.ExpiresActive {
			out.ExpiresActive = rs.ExpiresActive
			out.ExpiresActiveSet = true
		}
		for k, v := range rs.ExpiresByType {
			out.ExpiresByType[k] = v
		}
		if rs.ExpiresDefault != "" {
			out.ExpiresDefault = rs.ExpiresDefault
		}
		for k, v := range rs.MimeTypes {
			out.MimeTypes[k] = v
		}
		if len(rs.DirectoryIndex) > 0 {
			out.DirectoryIndex = rs.DirectoryIndex
		}
		for k, v := range rs.ErrorDocuments {
			out.ErrorDocuments[k] = v
			delete(out.ErrorTexts, k)
		}
		for k, v := range rs.ErrorTexts {
			out.ErrorTexts[k] = v
		}
	}
	return out
}

// optionsAbsolute reports whether an Options argument list sets the options
// outright: at least one option carries no "+" or "-" prefix.
func optionsAbsolute(args []string) bool {
	for _, a := range args {
		if a != "" && a[0] != '+' && a[0] != '-' {
			return true
		}
	}
	return false
}

// optionsNameFollowSymlinks reports whether an absolute Options list turns
// FollowSymLinks on, by name or through "All". A later "-FollowSymLinks" is
// applied by the caller's loop.
func optionsNameFollowSymlinks(args []string) bool {
	for _, a := range args {
		switch strings.ToLower(a) {
		case "followsymlinks", "all":
			return true
		}
	}
	return false
}

// FollowSymlinksFor reports whether symbolic links may be followed in a
// directory whose .htaccess levels are chain (outermost first), and the index
// of the level that decided it (-1 when none said anything: the default is to
// follow). The deepest level that says anything wins, so "+FollowSymLinks" in a
// subdirectory lifts a parent's refusal. Nil entries are skipped.
func FollowSymlinksFor(chain []*RuleSet) (follow bool, level int) {
	follow, level = true, -1
	for i, rs := range chain {
		if rs != nil && rs.FollowSymlinks != nil {
			follow, level = *rs.FollowSymlinks, i
		}
	}
	return follow, level
}

// optionsNameIndexes reports whether an absolute Options list turns Indexes
// on. "All" is left out on purpose: it would enable a listing nothing else
// asked for, so only the safe direction (switching it off) is applied.
func optionsNameIndexes(args []string) bool {
	for _, a := range args {
		switch strings.ToLower(a) {
		case "indexes", "all":
			return true
		}
	}
	return false
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
		delete(rs.ErrorTexts, k)
	}
	for k, v := range other.ErrorTexts {
		if rs.ErrorTexts == nil {
			rs.ErrorTexts = make(map[int]string)
		}
		rs.ErrorTexts[k] = v
	}
	if len(other.DirectoryIndex) > 0 {
		rs.DirectoryIndex = other.DirectoryIndex
	}
	if other.DirectoryListing != nil {
		rs.DirectoryListing = other.DirectoryListing
	}
	if other.LimitRequestBody != nil {
		rs.LimitRequestBody = other.LimitRequestBody
	}
	if other.FollowSymlinks != nil {
		rs.FollowSymlinks = other.FollowSymlinks
	}
	if other.PHPDisabled != nil {
		rs.PHPDisabled = other.PHPDisabled
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
		rs.Require = other.Require
	}
	if other.ExpiresActiveSet {
		rs.ExpiresActive = other.ExpiresActive
		rs.ExpiresActiveSet = true
	} else if other.ExpiresActive {
		rs.ExpiresActive = true
	}
	for k, v := range other.ExpiresByType {
		rs.ExpiresByType[k] = v
	}
	if other.ExpiresDefault != "" {
		rs.ExpiresDefault = other.ExpiresDefault
	}
	for k, v := range other.MimeTypes {
		rs.MimeTypes[k] = v
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
		re, err := rxguard.Compile(rule.Pattern)
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
	// The suffix is the decoded path: escape it so "%3F" / "%23" stay path bytes
	// instead of becoming a query or fragment delimiter (F30601).
	suffix := (&url.URL{Path: urlPath[len(rule.Pattern):]}).EscapedPath()
	return sameHostRedirectPath(rule.Target + suffix), status, true
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
	// This check carries no request method, so a <Limit> section's denial must
	// not be dropped: judge the file as a GET or POST would meet it (F5500).
	return FilesMatchDeniesForMethod(rules, filename, ip, "GET") ||
		FilesMatchDeniesForMethod(rules, filename, ip, "POST")
}

// FilesMatchDeniesForMethod is FilesMatchDeniesFor for a request with the
// given HTTP method, so <Limit>/<LimitExcept> sections inside the block apply.
func FilesMatchDeniesForMethod(rules *RuleSet, filename string, ip net.IP, method string) bool {
	return FilesMatchDeniesForRequest(rules, filename, ip, method, nil)
}

// FilesMatchDeniesForRequest is FilesMatchDeniesForMethod with the request's
// attributes, so "env=" conditions inside the block are decided (F6700).
func FilesMatchDeniesForRequest(rules *RuleSet, filename string, ip net.IP, method string, attrs *RequestAttrs) bool {
	if rules == nil {
		return false
	}
	env := buildEnv(rules.topLevel, attrs)
	for _, block := range rules.FilesMatch {
		if block.Pattern == "" || !filesBlockMatches(block, filename) {
			continue
		}
		if evalAccess(block.Directives, ip, method, extendEnv(env, block.Directives, attrs)) == AccessDenied {
			return true
		}
	}
	return false
}

// filesBlockMatchesStrict is filesBlockMatches for directives that must apply
// only to files that really match: a pattern RE2 cannot compile matches nothing.
func filesBlockMatchesStrict(block FilesMatchBlock, filename string) bool {
	if block.IsGlob {
		ok, err := path.Match(block.Pattern, filename)
		return err == nil && ok
	}
	re, err := rxguard.Compile(block.Pattern)
	return err == nil && re.MatchString(filename)
}

// HeadersForFile returns the Header directives of the <Files>/<FilesMatch>
// blocks that match filename, in file order. They used to be dropped, so a
// per-file header (a security header for scripts, a Cache-Control for assets)
// never reached the response (F9100). Callers decide each rule with
// HeaderRule.AppliesTo like the top-level ones.
func (rs *RuleSet) HeadersForFile(filename string) []HeaderRule {
	if rs == nil {
		return nil
	}
	var out []HeaderRule
	for _, block := range rs.FilesMatch {
		if block.Uncertain || block.Pattern == "" || !filesBlockMatchesStrict(block, filename) {
			continue
		}
		out = append(out, collectHeaders(block.Directives)...)
	}
	return out
}

// collectHeaders gathers the Header directives of ds, descending into
// conditional sections whose condition holds.
func collectHeaders(ds []Directive) []HeaderRule {
	var out []HeaderRule
	for _, d := range ds {
		switch {
		case strings.EqualFold(d.Name, "Header"):
			if hr, ok := parseHeaderDirective(d); ok {
				out = append(out, hr)
			}
		case len(d.Block) > 0 && isConditional(d.Name) && conditionState(d) == condActive:
			out = append(out, collectHeaders(d.Block)...)
		}
	}
	return out
}

// MimeTypeFor returns the content type AddType assigned to filename's
// extension ("" when none), so a type Go's table does not know (or knows
// differently) is served as the operator declared it (F9101).
func (rs *RuleSet) MimeTypeFor(filename string) string {
	if rs == nil || len(rs.MimeTypes) == 0 {
		return ""
	}
	return rs.MimeTypes[strings.ToLower(path.Ext(filename))]
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
	re, err := rxguard.Compile(block.Pattern)
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
		case "ifmodule", "ifdefine", "ifversion", "if", "elseif", "else", "ifdirective", "ifsection", "iffile":
			switch conditionState(d) {
			case condActive:
				if directivesDeny(d.Block) {
					return true
				}
			case condUnknown:
				if unevaluableDeny(d.Block) {
					return true
				}
			}
		}
	}
	return false
}

// setPHPDisabled records a "php_flag engine <value>" decision: off/0/false
// disables PHP for the directory, on/1/true re-enables it.
func setPHPDisabled(rules *RuleSet, value string) {
	switch strings.ToLower(value) {
	case "off", "0", "false":
		t := true
		rules.PHPDisabled = &t
	case "on", "1", "true":
		f := false
		rules.PHPDisabled = &f
	}
}

// isPHPHandlerExt reports whether a RemoveHandler argument names an extension
// PHP scripts are served under.
func isPHPHandlerExt(ext string) bool {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "php", "phtml", "pht", "php3", "php4", "php5", "php7", "php8", "phps":
		return true
	}
	return false
}

// PHPDisabledFor reports whether PHP execution is switched off for a directory
// whose .htaccess levels are chain (outermost first): the deepest level that
// says anything wins, so "php_flag engine on" in a subdirectory lifts a parent's
// "engine off". Nil entries are skipped.
func PHPDisabledFor(chain []*RuleSet) bool {
	disabled := false
	for _, rs := range chain {
		if rs != nil && rs.PHPDisabled != nil {
			disabled = *rs.PHPDisabled
		}
	}
	return disabled
}

// LimitRequestBodyFor returns the request-body cap, in bytes, for a directory
// whose .htaccess levels are chain (outermost first): the deepest level that
// says anything wins, so "LimitRequestBody 0" in a subdirectory lifts a parent's
// cap. 0 means no cap. Nil entries are skipped (F16901).
func LimitRequestBodyFor(chain []*RuleSet) int64 {
	var limit int64
	for _, rs := range chain {
		if rs != nil && rs.LimitRequestBody != nil {
			limit = *rs.LimitRequestBody
		}
	}
	return limit
}

// phpModuleEngineOff reports whether an <IfModule mod_php*.c> section (not
// negated) switches the PHP engine off.
func phpModuleEngineOff(d Directive) bool {
	if !strings.EqualFold(d.Name, "IfModule") || len(d.Args) == 0 {
		return false
	}
	mod := strings.ToLower(d.Args[0])
	if !strings.HasPrefix(mod, "mod_php") && !strings.HasPrefix(mod, "php") {
		return false
	}
	for _, c := range d.Block {
		switch strings.ToLower(c.Name) {
		case "php_flag", "php_value":
			if len(c.Args) >= 2 && strings.EqualFold(c.Args[0], "engine") {
				switch strings.ToLower(c.Args[1]) {
				case "off", "0", "false":
					return true
				}
			}
		}
	}
	return false
}
