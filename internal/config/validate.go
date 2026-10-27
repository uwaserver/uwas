package config

import (
	"fmt"
	"math"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/uwaserver/uwas/internal/pathmatch"
	"github.com/uwaserver/uwas/internal/rewrite"
)

// SafeDialControl is a net.Dialer.Control hook that rejects connections to
// SSRF-blocked addresses at the moment of dial. Because it inspects the IP the
// OS is actually about to connect to, it closes the DNS-rebinding TOCTOU window
// that a pre-flight URL check (which resolves the name separately) cannot. Wire
// it into the Transport of any client that dials user-supplied external URLs
// (webhooks, notifications); do NOT use it for clients that legitimately reach
// loopback/private upstreams (reverse proxy, uptime monitor).
func SafeDialControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	if reason := ipBlockedReason(ip, urlSafetyPolicy{}); reason != "" {
		return fmt.Errorf("blocked %s %q (SSRF)", reason, host)
	}
	return nil
}

// ProxyDialControl applies the reverse-proxy address policy to the IP selected
// by the OS at connect time. Loopback is always supported for local app
// upstreams; RFC1918/ULA addresses require the domain's explicit opt-in.
func ProxyDialControl(allowPrivate bool) func(string, string, syscall.RawConn) error {
	policy := urlSafetyPolicy{allowLoopback: true, allowPrivate: allowPrivate}
	return func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			host = address
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return nil
		}
		if reason := ipBlockedReason(ip, policy); reason != "" {
			return fmt.Errorf("blocked %s %q (SSRF)", reason, host)
		}
		return nil
	}
}

// Validate performs structural and semantic validation on a Config.
func Validate(cfg *Config) error {
	var errs []string

	// Global validation
	switch cfg.Global.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Sprintf("invalid log_level: %q (must be debug, info, warn, error)", cfg.Global.LogLevel))
	}

	switch cfg.Global.LogFormat {
	case "json", "text", "clf":
	default:
		errs = append(errs, fmt.Sprintf("invalid log_format: %q (must be json, text, clf)", cfg.Global.LogFormat))
	}

	// Listen address validation
	if cfg.Global.HTTPListen != "" {
		validateListenAddr(cfg.Global.HTTPListen, "global.http_listen", &errs)
	}
	if cfg.Global.HTTPSListen != "" {
		validateListenAddr(cfg.Global.HTTPSListen, "global.https_listen", &errs)
	}
	if cfg.Global.Admin.Listen != "" {
		validateListenAddr(cfg.Global.Admin.Listen, "global.admin.listen", &errs)
	}
	if cfg.Global.MCP.Listen != "" {
		validateListenAddr(cfg.Global.MCP.Listen, "global.mcp.listen", &errs)
	}

	validateRateLimitConfig("global.rate_limit", cfg.Global.RateLimit, &errs)
	validateAutoBlockConfig(cfg.Global.AutoBlock, &errs)
	validateWatchdogConfig(cfg.Global.Watchdog, &errs)

	// Defaults only replace 0, so a negative value would reach net/http,
	// where a timeout <= 0 means "no timeout" (and a negative shutdown grace
	// forces an immediate hard close).
	for _, t := range []struct {
		name string
		d    Duration
	}{
		{"read", cfg.Global.Timeouts.Read},
		{"read_header", cfg.Global.Timeouts.ReadHeader},
		{"write", cfg.Global.Timeouts.Write},
		{"idle", cfg.Global.Timeouts.Idle},
		{"shutdown_grace", cfg.Global.Timeouts.ShutdownGrace},
	} {
		if t.d.Duration < 0 {
			errs = append(errs, fmt.Sprintf("global.timeouts.%s: must be >= 0, got %s", t.name, t.d.Duration))
		}
	}
	if cfg.Global.MaxConnections < 0 {
		errs = append(errs, fmt.Sprintf("global.max_connections: must be >= 0, got %d", cfg.Global.MaxConnections))
	}

	// Trusted proxies validation (CIDR notation)
	for i, cidr := range cfg.Global.TrustedProxies {
		_, _, err := net.ParseCIDR(cidr)
		if err != nil {
			// Also allow plain IPs (not CIDR)
			if ip := net.ParseIP(cidr); ip == nil {
				errs = append(errs, fmt.Sprintf("global.trusted_proxies[%d]: invalid CIDR or IP %q: %v", i, cidr, err))
			}
		}
	}

	// The ACME manager matches dns_provider exactly and only warns about any
	// other value, so a misspelling (or "Cloudflare") quietly left wildcard
	// names with no DNS-01 solver (F13800).
	if p := cfg.Global.ACME.DNSProvider; p != "" && !KnownACMEDNSProvider(p) {
		errs = append(errs, fmt.Sprintf("global.acme.dns_provider: unknown provider %q (must be %s)", p, strings.Join(acmeDNSProviders, ", ")))
	}

	for i, wh := range cfg.Global.Webhooks {
		if err := ValidateWebhookEvents(wh.Events); err != nil {
			errs = append(errs, fmt.Sprintf("global.webhooks[%d].events: %v", i, err))
		}
	}

	// ACME email validation
	if cfg.Global.ACME.Email != "" {
		if !strings.Contains(cfg.Global.ACME.Email, "@") {
			errs = append(errs, fmt.Sprintf("global.acme.email: invalid email %q (must contain @)", cfg.Global.ACME.Email))
		}
	}

	// Cache TTL validation
	if cfg.Global.Cache.DefaultTTL < 0 {
		errs = append(errs, fmt.Sprintf("global.cache.default_ttl: must be >= 0, got %d", cfg.Global.Cache.DefaultTTL))
	}
	if cfg.Global.Cache.GraceTTL < 0 {
		errs = append(errs, fmt.Sprintf("global.cache.grace_ttl: must be >= 0, got %d", cfg.Global.Cache.GraceTTL))
	}
	// Defaults only replace 0, so a negative size would reach the cache: the
	// disk cache treats a limit <= 0 as "no limit" (unbounded growth) and the
	// memory cache rejects every entry (F24800).
	if cfg.Global.Cache.MemoryLimit < 0 {
		errs = append(errs, fmt.Sprintf("global.cache.memory_limit: must be >= 0, got %d", int64(cfg.Global.Cache.MemoryLimit)))
	}
	if cfg.Global.Cache.DiskLimit < 0 {
		errs = append(errs, fmt.Sprintf("global.cache.disk_limit: must be >= 0, got %d", int64(cfg.Global.Cache.DiskLimit)))
	}

	// Backup validation
	if cfg.Global.Backup.Enabled {
		switch cfg.Global.Backup.Provider {
		case "local", "s3", "sftp":
		default:
			errs = append(errs, fmt.Sprintf("global.backup.provider: invalid provider %q (must be local, s3, sftp)", cfg.Global.Backup.Provider))
		}

		if cfg.Global.Backup.Keep <= 0 {
			errs = append(errs, fmt.Sprintf("global.backup.keep: must be > 0, got %d", cfg.Global.Backup.Keep))
		}

		validateBackupSchedule(cfg.Global.Backup, &errs)

		// S3-specific validation
		if cfg.Global.Backup.Provider == "s3" {
			if cfg.Global.Backup.S3.Bucket == "" {
				errs = append(errs, "global.backup.s3.bucket: required when provider is s3")
			}
		}

		// SFTP-specific validation
		if cfg.Global.Backup.Provider == "sftp" {
			if cfg.Global.Backup.SFTP.Host == "" {
				errs = append(errs, "global.backup.sftp.host: required when provider is sftp")
			}
			if cfg.Global.Backup.SFTP.User == "" {
				errs = append(errs, "global.backup.sftp.user: required when provider is sftp")
			}
		}
	}

	// Domain validation
	// Track exact host ownership globally, but do not reject duplicate aliases
	// inside the same domain. Dashboard/API normalization drops self-aliases;
	// config validation should be equally forgiving so an apex + www alias list
	// cannot prevent UWAS from starting.
	hosts := make(map[string]int)
	for i, d := range cfg.Domains {
		prefix := fmt.Sprintf("domains[%d]", i)

		if d.Host == "" {
			errs = append(errs, fmt.Sprintf("%s: host is required", prefix))
			continue
		}

		hostKey := canonicalValidateHostname(d.Host)
		if owner, ok := hosts[hostKey]; ok && owner != i {
			errs = append(errs, fmt.Sprintf("%s: duplicate host %q", prefix, d.Host))
		}
		hosts[hostKey] = i

		for _, alias := range d.Aliases {
			aliasKey := canonicalValidateHostname(alias)
			if aliasKey == "" {
				continue
			}
			if owner, ok := hosts[aliasKey]; ok {
				if owner == i {
					continue
				}
				errs = append(errs, fmt.Sprintf("%s: duplicate alias %q", prefix, alias))
			}
			hosts[aliasKey] = i
		}

		if !DomainType(d.Type).IsValid() {
			errs = append(errs, fmt.Sprintf("%s: invalid type %q (must be static, php, proxy, app, redirect)", prefix, d.Type))
		}
		if d.CanonicalHost != "" {
			switch strings.ToLower(strings.TrimSpace(d.CanonicalHost)) {
			case "apex", "root", "naked", "domain", "www":
			default:
				errs = append(errs, fmt.Sprintf("%s: invalid canonical_host %q (must be apex or www)", prefix, d.CanonicalHost))
			}
		}

		switch d.SSL.Mode {
		case "auto", "manual", "off":
		default:
			errs = append(errs, fmt.Sprintf("%s: invalid ssl.mode %q (must be auto, manual, off)", prefix, d.SSL.Mode))
		}

		if d.SSL.Mode == "manual" {
			if d.SSL.Cert == "" {
				errs = append(errs, fmt.Sprintf("%s: ssl.cert required when mode is manual", prefix))
			} else {
				if _, err := os.Stat(d.SSL.Cert); err != nil {
					errs = append(errs, fmt.Sprintf("%s: ssl.cert file %q not found: %v", prefix, d.SSL.Cert, err))
				}
			}
			if d.SSL.Key == "" {
				errs = append(errs, fmt.Sprintf("%s: ssl.key required when mode is manual", prefix))
			} else {
				if _, err := os.Stat(d.SSL.Key); err != nil {
					errs = append(errs, fmt.Sprintf("%s: ssl.key file %q not found: %v", prefix, d.SSL.Key, err))
				}
			}
		}

		// Root: auto-fill from web_root if empty, only error if still empty
		dt := DomainType(d.Type)
		if (dt == DomainTypeStatic || dt == DomainTypePHP) && d.Root == "" {
			if cfg.Global.WebRoot != "" {
				cfg.Domains[i].Root = filepath.Join(cfg.Global.WebRoot, d.Host, "public_html")
			} else {
				errs = append(errs, fmt.Sprintf("%s: root is required for type %q", prefix, d.Type))
			}
		}

		validateDomainShape(prefix, &d, false, &errs)
	}

	if len(errs) > 0 {
		return fmt.Errorf("config validation failed:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}

// validateDomainShape holds the per-domain checks that need nothing but the
// domain itself. Validate (the loader) and validateDomain (the admin API) both
// run it, so the API cannot accept and persist a domain that the next Load
// would reject — which would stop the whole server from starting or reloading.
// partial skips the two cross-field "required" invariants, as
// ValidateDomainPartial documents.
func validateDomainShape(prefix string, d *Domain, partial bool, errs *[]string) {
	dt := DomainType(d.Type)

	// TLS min_version validation
	if d.SSL.MinVersion != "" {
		switch d.SSL.MinVersion {
		case "1.0", "1.1", "1.2", "1.3":
		default:
			*errs = append(*errs, fmt.Sprintf("%s: invalid ssl.min_version %q (must be 1.0, 1.1, 1.2, 1.3)", prefix, d.SSL.MinVersion))
		}
	}

	// htaccess.mode is an enum with exactly two values, and only "import"
	// turns the engine on. Anything else was accepted and silently meant
	// "off", so an operator writing the obvious `mode: on` got no .htaccess
	// and no indication why, and the site's own `Require all denied` rules did
	// nothing. The loader skipped this check while the admin API applied it
	// (F16500).
	if m := d.Htaccess.Mode; m != "" && m != "off" && m != "import" {
		*errs = append(*errs, fmt.Sprintf("%s: htaccess.mode = %q: must be \"import\" or \"off\"", prefix, m))
	}

	// basic_auth that is enabled but has no (usable) user is not enforced at
	// all: enforceBasicAuth lets every request through, so a mistyped `users:`
	// key leaves the domain, or the /admin location, open while the operator
	// believes it is protected. The admin API refused this; the loader, the
	// reload path and the raw editor did not (F16501).
	if err := ValidateBasicAuth("domain", d.BasicAuth); err != nil {
		*errs = append(*errs, fmt.Sprintf("%s: %v", prefix, err))
	}
	for i, loc := range d.Locations {
		if loc.BasicAuth == nil {
			continue
		}
		scope := fmt.Sprintf("location[%d]", i)
		if loc.Match != "" {
			scope = fmt.Sprintf("location %q", loc.Match)
		}
		if err := ValidateBasicAuth(scope, *loc.BasicAuth); err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: %v", prefix, err))
		}
	}

	// try_files, index_files and php.index_files are tried one stat at a time
	// for every request that reaches the file handler. They have no natural
	// size, and a reseller edits them for their own domain, so an unbounded list
	// made each request cost ~17 us per entry (1.8 s for 100k) (F14403).
	for _, l := range []struct {
		name string
		list []string
		max  int
	}{
		{"try_files", d.TryFiles, MaxTryFiles},
		{"index_files", d.IndexFiles, MaxIndexFiles},
		{"php.index_files", d.PHP.IndexFiles, MaxIndexFiles},
	} {
		if len(l.list) > l.max {
			*errs = append(*errs, fmt.Sprintf("%s.%s: %d entries, at most %d are allowed", prefix, l.name, len(l.list), l.max))
		}
	}

	// internal_aliases unlock X-Sendfile/X-Accel-Redirect targets outside
	// the document root. Reject obvious system directories — these are
	// never legitimate values and quietly accepting them would let a
	// compromised PHP app exfiltrate /etc, /root, etc. via X-Sendfile.
	for j, alias := range d.InternalAliases {
		if reason := badInternalAliasReason(alias); reason != "" {
			*errs = append(*errs, fmt.Sprintf("%s.internal_aliases[%d]: %q rejected — %s",
				prefix, j, alias, reason))
		}
	}

	// Proxy validation
	if dt == DomainTypeProxy {
		if len(d.Proxy.Upstreams) == 0 {
			if !partial {
				*errs = append(*errs, fmt.Sprintf("%s: proxy.upstreams required for type proxy", prefix))
			}
		} else {
			seen := make(map[string]bool)
			for j, u := range d.Proxy.Upstreams {
				uprefix := fmt.Sprintf("%s.proxy.upstreams[%d]", prefix, j)
				validateProxyUpstreamAddress(uprefix, u.Address, errs)
				if seen[u.Address] {
					*errs = append(*errs, fmt.Sprintf("%s: duplicate upstream address %q", uprefix, u.Address))
				}
				seen[u.Address] = true
				if u.Weight < 0 {
					*errs = append(*errs, fmt.Sprintf("%s: weight must be >= 0, got %d", uprefix, u.Weight))
				}
			}
		}

		// Proxy algorithm validation (accept both dashed and underscored forms;
		// the balancer normalises before dispatch).
		if d.Proxy.Algorithm != "" && !IsValidProxyAlgorithm(d.Proxy.Algorithm) {
			*errs = append(*errs, fmt.Sprintf("%s: invalid proxy.algorithm %q (must be round-robin, least-conn, weighted, ip-hash, uri-hash, random, sticky)", prefix, d.Proxy.Algorithm))
		}

		// Canary weight validation
		if d.Proxy.Canary.Enabled {
			if d.Proxy.Canary.Weight < 0 || d.Proxy.Canary.Weight > 100 {
				*errs = append(*errs, fmt.Sprintf("%s: proxy.canary.weight must be 0-100, got %d", prefix, d.Proxy.Canary.Weight))
			}
			if len(d.Proxy.Canary.Upstreams) == 0 {
				*errs = append(*errs, fmt.Sprintf("%s: proxy.canary.upstreams requires at least one backend", prefix))
			}
			for j, u := range d.Proxy.Canary.Upstreams {
				uprefix := fmt.Sprintf("%s.proxy.canary.upstreams[%d]", prefix, j)
				validateProxyUpstreamAddress(uprefix, u.Address, errs)
				if u.Weight < 0 {
					*errs = append(*errs, fmt.Sprintf("%s: weight must be >= 0, got %d", uprefix, u.Weight))
				}
			}
		}

		// Mirror percent validation
		if d.Proxy.Mirror.Enabled {
			if d.Proxy.Mirror.Percent < 0 || d.Proxy.Mirror.Percent > 100 {
				*errs = append(*errs, fmt.Sprintf("%s: proxy.mirror.percent must be 0-100, got %d", prefix, d.Proxy.Mirror.Percent))
			}
			validateProxyUpstreamAddress(prefix+".proxy.mirror", d.Proxy.Mirror.Backend, errs)
			if d.Proxy.Mirror.MaxBodyBytes < 0 {
				*errs = append(*errs, fmt.Sprintf("%s: proxy.mirror.max_body_bytes must be >= 0", prefix))
			}
		}
	}

	// Redirect validation
	if dt == DomainTypeRedirect {
		if d.Redirect.Target == "" && !partial {
			*errs = append(*errs, fmt.Sprintf("%s: redirect.target required for type redirect", prefix))
		}
		if d.Redirect.Status != 0 {
			switch d.Redirect.Status {
			case 301, 302, 307, 308:
			default:
				*errs = append(*errs, fmt.Sprintf("%s: invalid redirect.status %d (must be 301, 302, 307, 308)", prefix, d.Redirect.Status))
			}
		}
	}

	validateRateLimitConfig(prefix+": security.rate_limit", d.Security.RateLimit, errs)

	// Every limiter reads a value <= 0 as "no limit", so a negative number
	// (a typo for the positive one) leaves the domain unlimited with nothing to
	// say so (F7800).
	for _, r := range []struct {
		name string
		v    int
	}{
		{"cpu_percent", d.Resources.CPUPercent},
		{"memory_mb", d.Resources.MemoryMB},
		{"pid_max", d.Resources.PIDMax},
	} {
		if r.v < 0 {
			*errs = append(*errs, fmt.Sprintf("%s: resources.%s must be >= 0, got %d", prefix, r.name, r.v))
		}
	}

	// php.max_upload feeds upload_max_filesize and post_max_size (max_upload
	// plus 1MB of headroom) and the chunked-body cap. A negative value was read
	// as "unset" without a word, and one within 1MB of MaxInt64 overflowed that
	// sum to a negative limit, so every chunked upload was answered 413
	// (F19500).
	if mu := d.PHP.MaxUpload; mu < 0 || mu > math.MaxInt64-MB {
		*errs = append(*errs, fmt.Sprintf("%s: php.max_upload must be between 0 and %d bytes, got %d", prefix, int64(math.MaxInt64-MB), int64(mu)))
	}

	// The bandwidth manager enforces only "block" and "throttle" (and treats an
	// empty action as throttle), so an action spelled any other way, or a
	// negative limit, was accepted and never enforced (F7801).
	if d.Bandwidth.MonthlyLimit < 0 {
		*errs = append(*errs, fmt.Sprintf("%s: bandwidth.monthly_limit must be >= 0", prefix))
	}
	if d.Bandwidth.DailyLimit < 0 {
		*errs = append(*errs, fmt.Sprintf("%s: bandwidth.daily_limit must be >= 0", prefix))
	}
	switch d.Bandwidth.Action {
	case "", "block", "throttle", "alert":
	default:
		*errs = append(*errs, fmt.Sprintf("%s: invalid bandwidth.action %q (must be block, throttle or alert)", prefix, d.Bandwidth.Action))
	}

	// Rewrite rules validation. The rule is judged by the same parser the
	// engine uses, so a pattern it accepts (a leading negative lookahead) is
	// not refused here, and a "conditions:" entry it cannot translate is
	// refused instead of being dropped, which would make the rule fire for
	// every request (F6600).
	for j, rw := range d.Rewrites {
		if rw.Match == "" {
			continue
		}
		if _, err := rewrite.ParseRule(rw.Match, rw.To, strings.Join(rw.Flags, ",")); err != nil {
			*errs = append(*errs, fmt.Sprintf("%s.rewrites[%d]: invalid regex in match %q: %v", prefix, j, rw.Match, err))
			continue
		}
		// The engine ignores a flag it does not know, so "Forbidden" typed for
		// [F] left a guard rule that forbade nothing (F6602).
		for k, fl := range rw.Flags {
			for _, tok := range strings.Split(strings.Trim(strings.TrimSpace(fl), "[]"), ",") {
				if tok = strings.TrimSpace(tok); tok != "" && !apacheRewriteFlags[strings.ToUpper(strings.SplitN(tok, "=", 2)[0])] {
					*errs = append(*errs, fmt.Sprintf("%s.rewrites[%d].flags[%d]: unknown flag %q", prefix, j, k, tok))
				}
			}
		}
		for k, cond := range rw.Conditions {
			one := rewrite.ConfigRewrite{Match: rw.Match, To: rw.To, Flags: rw.Flags, Conditions: []string{cond}}
			if rules := rewrite.ConvertConfigRewrites([]rewrite.ConfigRewrite{one}); len(rules) == 1 && len(rules[0].Conditions) == 0 {
				*errs = append(*errs, fmt.Sprintf("%s.rewrites[%d].conditions[%d]: unsupported condition %q (use is_file, !is_file, is_dir or !is_dir)", prefix, j, k, cond))
			}
		}
	}

	// A blacklist entry that is not an IP or CIDR is skipped by the guard, so
	// the address it was meant to block is let through (F6601).
	for j, entry := range d.Security.IPBlacklist {
		if !validACLEntry(entry) {
			*errs = append(*errs, fmt.Sprintf("%s.security.ip_blacklist[%d]: invalid IP or CIDR %q", prefix, j, entry))
		}
	}

	// A maintenance bypass entry that is not an IP or CIDR never matches, so the
	// operator it was written for is shown the maintenance page (F15900).
	for j, entry := range d.Maintenance.AllowedIPs {
		if !validACLEntry(entry) {
			*errs = append(*errs, fmt.Sprintf("%s.maintenance.allowed_ips[%d]: invalid IP or CIDR %q", prefix, j, entry))
		}
	}

	// ssl.client_auth is compared exactly when the handshake is configured:
	// any other spelling means "none", and "request"/"require" with no CA to
	// verify against is skipped. Either way the domain serves anonymous
	// clients while the operator believes mutual TLS is on (F13500, F13501).
	switch d.SSL.ClientAuth {
	case "", "none":
	case "request", "require":
		if !partial {
			if err := ValidateClientAuthCA(d.SSL); err != nil {
				*errs = append(*errs, fmt.Sprintf("%s: %v", prefix, err))
			}
		}
	default:
		*errs = append(*errs, fmt.Sprintf("%s: invalid ssl.client_auth %q (must be none, request or require)", prefix, d.SSL.ClientAuth))
	}

	// A WAF family name the WAF does not have is dropped, so a typo next to
	// valid entries silently removes that family's checks (F13502). Left alone
	// when the WAF is off, like the compression block below: nothing runs.
	if d.Security.WAF.Enabled {
		for j, rule := range d.Security.WAF.Rules {
			if !KnownWAFRuleName(rule) {
				*errs = append(*errs, fmt.Sprintf("%s.security.waf.rules[%d]: unknown rule %q (known: %s)", prefix, j, rule, strings.Join(wafRuleNames, ", ")))
			}
		}
	}

	// The geo guard compares the two-letter code the lookup returns, so any
	// other spelling never matches: a block list entry blocks nothing (F13503).
	for _, list := range []struct {
		field   string
		entries []string
	}{
		{"geo_block_countries", d.Security.GeoBlockCountries},
		{"geo_allow_countries", d.Security.GeoAllowCountries},
	} {
		for j, code := range list.entries {
			if !validGeoCode(code) {
				*errs = append(*errs, fmt.Sprintf("%s.security.%s[%d]: %q is not a two-character country code (e.g. TR)", prefix, list.field, j, code))
			}
		}
	}

	// Compression algorithm validation. Runs when compression is in effect,
	// which now includes domains that never mention it (Enabled is a
	// pointer and nil means on). A block the operator explicitly disabled
	// is left alone: rejecting a typo there would refuse to start on a
	// setting that does nothing.
	if d.Compression.CompressionEnabled() {
		for j, alg := range d.Compression.Algorithms {
			switch alg {
			case "gzip", "br":
			default:
				*errs = append(*errs, fmt.Sprintf("%s.compression.algorithms[%d]: invalid algorithm %q (must be gzip, br)", prefix, j, alg))
			}
		}
	}

	// Image optimization format validation
	if d.ImageOptimization.Enabled {
		for j, f := range d.ImageOptimization.Formats {
			switch f {
			case "webp", "avif":
			default:
				*errs = append(*errs, fmt.Sprintf("%s.image_optimization.formats[%d]: invalid format %q (must be webp, avif)", prefix, j, f))
			}
		}
	}

	// A "~" location whose regex does not compile never matches, so its
	// basic_auth, rate_limit and headers silently never apply (F3300).
	for j, loc := range d.Locations {
		if err := pathmatch.ValidLocation(loc.Match); err != nil {
			*errs = append(*errs, fmt.Sprintf("%s.locations[%d].match: invalid regular expression %q: %v", prefix, j, loc.Match, err))
		}
	}

	// Domain-level cache TTL validation
	if d.Cache.TTL < 0 {
		*errs = append(*errs, fmt.Sprintf("%s: cache.ttl must be >= 0, got %d", prefix, d.Cache.TTL))
	}
	for j, rule := range d.Cache.Rules {
		if rule.TTL < 0 {
			*errs = append(*errs, fmt.Sprintf("%s.cache.rules[%d]: ttl must be >= 0, got %d", prefix, j, rule.TTL))
		}
		// match is a REGEX, not a glob. An uncompilable pattern used to be
		// swallowed at request time (matchPath returns false), so a rule
		// written as "*.html" silently never matched and the operator got
		// no bypass, no Cache-Control and no ttl — with nothing to explain
		// why. Report it here instead.
		if rule.Match != "" {
			if err := pathmatch.ValidRegex(rule.Match); err != nil {
				*errs = append(*errs, fmt.Sprintf(
					"%s.cache.rules[%d].match: not a valid regular expression (%v) — this field is a regex, not a glob; use \\.html$ rather than *.html",
					prefix, j, err))
			}
		}
	}
}

func canonicalValidateHostname(host string) string {
	host = CanonicalHost(host)
	if strings.HasPrefix(host, "www.") && !strings.Contains(host, ":") && !strings.HasPrefix(host, "*.") {
		apex := strings.TrimPrefix(host, "www.")
		if apex != "" && strings.Contains(apex, ".") {
			return apex
		}
	}
	return host
}

func validateRateLimitConfig(path string, rl RateLimitConfig, errs *[]string) {
	if rl.Requests < 0 {
		*errs = append(*errs, fmt.Sprintf("%s.requests must be >= 0, got %d", path, rl.Requests))
	}
	if rl.Window.Duration < 0 {
		*errs = append(*errs, fmt.Sprintf("%s.window must be >= 0", path))
	}
}

// ValidateDomain runs strict single-domain static validation. Returns the
// first error encountered (admin-style fail-fast), unlike Validate(*Config)
// which accumulates all errors across the whole config.
//
// Strict mode enforces cross-field invariants — proxy type requires at least
// one upstream, redirect requires a target. Use ValidateDomainPartial for
// PATCH-style updates that legitimately omit dependent blocks.
//
// Runtime checks that need a live *Server (PHP availability, web-root
// containment, etc.) stay in the admin package and run alongside this call.
func ValidateDomain(d *Domain) error {
	return validateDomain(d, false)
}

// ValidateDomainPartial is the lenient variant used on PUT/PATCH update paths
// where the dashboard may send only the changed fields. Skips the
// proxy-needs-upstreams and redirect-needs-target invariants so a partial
// merge isn't rejected for missing the half it intentionally didn't touch.
func ValidateDomainPartial(d *Domain) error {
	return validateDomain(d, true)
}

// ValidateMergedDomain is what an update must run on the domain it is about to
// store: the patch has already been merged into the stored domain, so nothing
// is "the half the patch did not touch" any more, and every cross-field rule
// ValidateDomainPartial skips (proxy needs an upstream, redirect needs a
// target, client_auth needs a CA, the redirect SSRF guard) applies to the
// whole. A merged domain missing one of them is saved, then refused by the
// next start or reload, which keeps the whole server from starting (F16800).
//
// previous is the stored domain before the update (nil for none). Its redirect
// target is not re-judged by the SSRF guard when the update left it alone, so
// a legacy, operator-written target does not block unrelated edits.
func ValidateMergedDomain(merged, previous *Domain) error {
	if merged == nil {
		return fmt.Errorf("domain is nil")
	}
	changed := previous == nil ||
		DomainType(previous.Type) != DomainTypeRedirect ||
		previous.Redirect.Target != merged.Redirect.Target
	return validateDomainWith(merged, false, changed)
}

// ValidatePHPEnv rejects php.env keys that would reach PHP as ini directives.
// validateDomain (the admin JSON API) always applies it; the raw YAML editor
// validates with the loader, which deliberately accepts an operator-written,
// reviewed PHP_ADMIN_VALUE (UPGRADING.md), so callers that serve non-admins
// (the raw editor) must run this themselves (F12000).
func ValidatePHPEnv(d *Domain) error {
	if d == nil {
		return nil
	}
	// PHP_ADMIN_VALUE is a FastCGI/SCGI directive that sets PHP_INI_SYSTEM and
	// PHP_INI_PERDIR ini options at runtime — including open_basedir, which
	// controls filesystem access per-process.  UWAS sets open_basedir via
	// PHP_ADMIN_VALUE in BuildEnv (handler/fastcgi/env.go:90) to enforce per-
	// domain path isolation.  A domain admin who injects their own PHP_ADMIN_VALUE
	// could widen or clear open_basedir and bypass UWAS's isolation.
	// PHP_VALUE is blocked separately below (sets PHP_INI_USER directives).
	for k := range d.PHP.Env {
		if k == "PHP_ADMIN_VALUE" {
			return fmt.Errorf("php.env: PHP_ADMIN_VALUE is not allowed — it would override UWAS's open_basedir restriction")
		}
		if strings.HasPrefix(k, "PHP_") {
			// Block PHP_VALUE — sets PHP_INI_USER directives at runtime.
			// (PHP_ADMIN_VALUE is already blocked above.)
			if k == "PHP_VALUE" {
				return fmt.Errorf("php.env: PHP_VALUE is not allowed — it can override ini directives")
			}
			// All other PHP_* vars are rejected unless they are safe read-only
			// CGI-provided variables (not ini-setting directives).
			switch k {
			case "PHP_AUTH_USER", "PHP_AUTH_PW", "PHP_AUTH_TYPE",
				"PHP_SELF", "SCRIPT_NAME", "REQUEST_TIME", "REQUEST_TIME_FLOAT":
				// These are read-only; harmless to forward.
			default:
				return fmt.Errorf("php.env: %q is not allowed — PHP_* vars can set ini directives", k)
			}
		}
	}
	return nil
}

func validateDomain(d *Domain, partial bool) error {
	return validateDomainWith(d, partial, true)
}

// validateDomainWith is validateDomain with the redirect-target SSRF guard
// switchable: Update re-checks a merged domain whose redirect target the
// patch did not touch, and a legacy (operator-written) internal target must
// not make every unrelated edit fail (F16800).
func validateDomainWith(d *Domain, partial, judgeRedirectTarget bool) error {
	if d == nil {
		return fmt.Errorf("domain is nil")
	}

	if err := ValidatePHPEnv(d); err != nil {
		return err
	}

	if !DomainType(d.Type).IsValid() {
		return fmt.Errorf("invalid type %q — must be static, php, proxy, app, or redirect", d.Type)
	}

	if d.CanonicalHost != "" {
		switch strings.ToLower(strings.TrimSpace(d.CanonicalHost)) {
		case "apex", "root", "naked", "domain", "www":
		default:
			return fmt.Errorf("invalid canonical_host %q - must be apex or www", d.CanonicalHost)
		}
	}

	switch d.SSL.Mode {
	case "auto", "manual", "off", "":
	default:
		return fmt.Errorf("invalid ssl.mode %q — must be auto, manual, or off", d.SSL.Mode)
	}
	if d.SSL.Mode == "manual" && (d.SSL.Cert == "" || d.SSL.Key == "") {
		return fmt.Errorf("ssl.mode=manual requires cert and key paths")
	}

	if DomainType(d.Type) == DomainTypeRedirect && !partial {
		if d.Redirect.Target == "" {
			return fmt.Errorf("redirect type requires a target URL")
		}
		// SSRF guard: reject redirects to private/internal/cloud-metadata hosts.
		// A domain with redirect.type can be used to bounce requests to an internal
		// service (e.g. 169.254.169.254, 10.0.0.1, localhost) via a redirect.
		// isURLSafe uses the same blocklist as proxy upstreams.
		if judgeRedirectTarget {
			if err := isURLSafe(d.Redirect.Target, urlSafetyPolicy{}); err != nil {
				return err
			}
		}
	}

	if !partial {
		if DomainType(d.Type) == DomainTypeProxy && len(d.Proxy.Upstreams) == 0 {
			return fmt.Errorf("proxy type requires at least one upstream")
		}
	}

	if d.Cache.TTL < 0 {
		return fmt.Errorf("cache TTL cannot be negative")
	}
	if d.Security.RateLimit.Requests < 0 {
		return fmt.Errorf("rate limit requests cannot be negative")
	}

	var shapeErrs []string
	validateDomainShape("domain", d, partial, &shapeErrs)
	if len(shapeErrs) > 0 {
		return fmt.Errorf("%s", shapeErrs[0])
	}

	return nil
}

// ValidateBasicAuth checks a BasicAuthConfig for shape errors: empty realm
// chars, missing users when enabled, malformed usernames/passwords. Scope is
// included in error messages so callers can identify which auth block failed.
func ValidateBasicAuth(scope string, ba BasicAuthConfig) error {
	if strings.ContainsAny(ba.Realm, "\r\n") {
		return fmt.Errorf("%s basic_auth realm contains invalid characters", scope)
	}
	if !ba.Enabled {
		return nil
	}
	if len(ba.Users) == 0 {
		return fmt.Errorf("%s basic_auth enabled requires at least one user", scope)
	}
	for username, password := range ba.Users {
		trimmed := strings.TrimSpace(username)
		if trimmed == "" {
			return fmt.Errorf("%s basic_auth username cannot be empty", scope)
		}
		if trimmed != username {
			return fmt.Errorf("%s basic_auth username %q has leading/trailing spaces", scope, username)
		}
		if strings.ContainsAny(username, ":\r\n") {
			return fmt.Errorf("%s basic_auth username %q contains invalid characters", scope, username)
		}
		if password == "" {
			return fmt.Errorf("%s basic_auth user %q must have a non-empty password", scope, username)
		}
		if strings.ContainsAny(password, "\r\n") {
			return fmt.Errorf("%s basic_auth user %q password contains invalid characters", scope, username)
		}
	}
	return nil
}

// IsValidHostname checks if s is a valid domain name per RFC 1035.
// Rejects: empty, >253 chars, labels >63 chars, leading/trailing hyphens or
// dots, path-traversal sequences (..), and characters outside [a-zA-Z0-9-.*].
func IsValidHostname(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	labels := strings.Split(s, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '.' || c == '*') {
			return false
		}
	}
	return !strings.Contains(s, "..")
}

// validateListenAddr checks that a listen address is a valid host:port with port in range.
func validateListenAddr(addr, field string, errs *[]string) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("%s: invalid listen address %q: %v", field, addr, err))
		return
	}
	// Host can be empty (meaning all interfaces), but if specified must be valid
	if host != "" {
		if ip := net.ParseIP(host); ip == nil {
			// Not an IP; could be a hostname - just do a basic sanity check
			if strings.ContainsAny(host, " \t/\\") {
				*errs = append(*errs, fmt.Sprintf("%s: invalid host in address %q", field, addr))
			}
		}
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("%s: invalid port in address %q: %v", field, addr, err))
		return
	}
	if port < 1 || port > 65535 {
		*errs = append(*errs, fmt.Sprintf("%s: port must be 1-65535, got %d", field, port))
	}
}

// isCloudMetadataHost returns true for cloud provider metadata endpoints
// that should never be used as proxy upstreams (SSRF prevention).
func isCloudMetadataHost(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	// AWS/GCP/Azure instance metadata
	return ip.Equal(net.ParseIP("169.254.169.254")) ||
		ip.Equal(net.ParseIP("fd00:ec2::254"))
}

var loopbackIPBlocks = []*net.IPNet{
	mustParseCIDR("127.0.0.0/8"),
	mustParseCIDR("::1/128"),
}

var privateIPBlocks = []*net.IPNet{
	mustParseCIDR("10.0.0.0/8"),
	mustParseCIDR("172.16.0.0/12"),
	mustParseCIDR("192.168.0.0/16"),
	mustParseCIDR("100.64.0.0/10"), // CGNAT shared address space (RFC 6598)
	mustParseCIDR("fc00::/7"),
}

// unspecifiedIPBlocks covers "this host"/unspecified ranges that must never be
// a valid egress target regardless of policy.
var unspecifiedIPBlocks = []*net.IPNet{
	mustParseCIDR("0.0.0.0/8"),
	mustParseCIDR("::/128"),
}

var cloudMetadataIPBlocks = []*net.IPNet{
	mustParseCIDR("169.254.169.254/32"),
	mustParseCIDR("fd00:ec2::254/128"),
}

var linkLocalIPBlocks = []*net.IPNet{
	mustParseCIDR("169.254.0.0/16"),
	mustParseCIDR("fe80::/10"),
}

var documentationIPBlocks = []*net.IPNet{
	mustParseCIDR("192.0.2.0/24"),
	mustParseCIDR("198.51.100.0/24"),
	mustParseCIDR("203.0.113.0/24"),
}

func mustParseCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic("config: invalid CIDR " + s + ": " + err.Error())
	}
	return n
}

type urlSafetyPolicy struct {
	allowLoopback bool
	allowPrivate  bool
}

func ipBlockedReason(ip net.IP, policy urlSafetyPolicy) string {
	// Normalize IPv4-mapped IPv6 (e.g. ::ffff:127.0.0.1) to its 4-byte form so
	// it can't slip past the IPv4 range checks below.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if inAnyIPBlock(ip, unspecifiedIPBlocks) {
		return "unspecified address"
	}
	if inAnyIPBlock(ip, cloudMetadataIPBlocks) {
		return "cloud metadata endpoint"
	}
	if inAnyIPBlock(ip, loopbackIPBlocks) && !policy.allowLoopback {
		return "loopback address"
	}
	if inAnyIPBlock(ip, privateIPBlocks) && !policy.allowPrivate {
		return "private address"
	}
	if inAnyIPBlock(ip, linkLocalIPBlocks) {
		return "link-local address"
	}
	if inAnyIPBlock(ip, documentationIPBlocks) {
		return "documentation address"
	}
	// IPv6 translation/transition forms designate an IPv4 host; classify that
	// host so 64:ff9b::a9fe:a9fe can't reach 169.254.169.254 through a NAT64.
	if kind, v4 := embeddedIPv4(ip); v4 != nil {
		if reason := ipBlockedReason(v4, policy); reason != "" {
			return kind + "-embedded " + reason
		}
	}
	if nat64LocalUse.Contains(ip) && !policy.allowPrivate {
		return "local-use NAT64 address"
	}
	return ""
}

var (
	nat64WellKnown = mustParseCIDR("64:ff9b::/96")   // RFC 6052
	nat64LocalUse  = mustParseCIDR("64:ff9b:1::/48") // RFC 8215, operator-defined layout
	sixToFour      = mustParseCIDR("2002::/16")      // RFC 3056
	ipv4Compatible = mustParseCIDR("::/96")          // RFC 4291 (deprecated)
)

// embeddedIPv4 returns the IPv4 address designated by a NAT64, 6to4 or
// IPv4-compatible IPv6 address, or nil. ip must already be normalized so that
// IPv4-mapped addresses are 4 bytes. :: and ::1 are not IPv4-compatible.
func embeddedIPv4(ip net.IP) (string, net.IP) {
	if len(ip) != net.IPv6len {
		return "", nil
	}
	switch {
	case nat64WellKnown.Contains(ip):
		return "NAT64", net.IPv4(ip[12], ip[13], ip[14], ip[15]).To4()
	case sixToFour.Contains(ip):
		return "6to4", net.IPv4(ip[2], ip[3], ip[4], ip[5]).To4()
	case ipv4Compatible.Contains(ip) && !ip.Equal(net.IPv6unspecified) && !ip.Equal(net.IPv6loopback):
		return "IPv4-compatible", net.IPv4(ip[12], ip[13], ip[14], ip[15]).To4()
	}
	return "", nil
}

func inAnyIPBlock(ip net.IP, blocks []*net.IPNet) bool {
	for _, block := range blocks {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

// isLoopback returns true if the host is a loopback address (localhost, 127.0.0.0/8, ::1).
// This is used to allow test servers that run on loopback addresses.
func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// forbiddenAliasRoots returns the list of system directories that must never
// appear in internal_aliases on the current operating system. The lists are
// kept separate (rather than merged) because filepath.Abs on Windows turns
// "/etc" into "C:\etc", which would otherwise sneak past the Unix check.
func forbiddenAliasRoots() []string {
	if filepath.Separator == '\\' {
		return []string{
			`C:\Windows`, `C:\ProgramData`,
			`C:\Program Files`, `C:\Program Files (x86)`,
		}
	}
	return []string{
		"/etc", "/root", "/proc", "/sys", "/dev", "/boot",
		"/var/log", "/var/lib/uwas", "/var/run", "/run",
		"/usr/bin", "/usr/sbin", "/sbin", "/bin",
	}
}

// badInternalAliasReason returns a human-readable reason when an
// internal_aliases entry points at an obviously dangerous system directory.
// It refuses to load such configs because internal_aliases bypass the
// document-root containment check for X-Sendfile / X-Accel-Redirect; a
// compromised PHP application or an honest-but-buggy framework could
// otherwise emit X-Sendfile: /etc/passwd and have the server stream the
// file straight back.
func badInternalAliasReason(alias string) string {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return "must not be empty"
	}
	abs, err := filepath.Abs(alias)
	if err != nil {
		return "invalid path"
	}
	abs = filepath.Clean(abs)
	cmp := abs
	if filepath.Separator == '\\' {
		cmp = strings.ToLower(cmp)
	}
	// Reject the filesystem root itself.
	if cmp == string(filepath.Separator) || (len(cmp) == 3 && cmp[1:] == `:\`) {
		return "filesystem root is never a valid alias"
	}
	for _, root := range forbiddenAliasRoots() {
		rootCmp := filepath.Clean(root)
		if filepath.Separator == '\\' {
			rootCmp = strings.ToLower(rootCmp)
		}
		if cmp == rootCmp || strings.HasPrefix(cmp, rootCmp+string(filepath.Separator)) {
			return fmt.Sprintf("system directory %s is forbidden", root)
		}
	}
	return ""
}

// NormalizeProxyUpstreamAddress returns the address with an http:// scheme if
// no scheme was provided. Users frequently configure upstreams as "host:port"
// (e.g. "127.0.0.1:3000") which url.Parse silently mis-parses as an opaque URL
// without a host — dropping the backend from the pool with no warning.
func NormalizeProxyUpstreamAddress(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return addr
	}
	if strings.Contains(addr, "://") {
		return addr
	}
	return "http://" + addr
}

func validateProxyUpstreamAddress(prefix, address string, errs *[]string) {
	if strings.TrimSpace(address) == "" {
		*errs = append(*errs, fmt.Sprintf("%s: address is required", prefix))
		return
	}
	addr := NormalizeProxyUpstreamAddress(address)
	parsed, err := url.Parse(addr)
	if err != nil || parsed.Host == "" {
		*errs = append(*errs, fmt.Sprintf("%s: invalid URL %q", prefix, address))
		return
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "ws", "wss":
		// Network upstreams: the address is dialled as written.
	case "apps":
		// Managed app upstream. The host part is an app NAME, not a network
		// address — resolveAppsUpstream turns it into the live 127.0.0.1:port
		// the supervisor assigned, at pool-build time (see
		// internal/server/apps_upstream.go). The dashboard writes this form
		// whenever an app is picked for a proxy domain.
		//
		// The metadata guard below is skipped for the same reason: it compares
		// against link-local addresses, and an app named like one still routes
		// through the supervisor, never to the metadata service.
		return
	default:
		*errs = append(*errs, fmt.Sprintf("%s: unsupported URL scheme %q", prefix, parsed.Scheme))
	}
	if isCloudMetadataHost(parsed.Hostname()) {
		*errs = append(*errs, fmt.Sprintf("%s: cloud metadata endpoint blocked (%s)", prefix, parsed.Hostname()))
	}
}

// IsValidProxyAlgorithm reports whether the algorithm name is recognised by
// the proxy balancer. Both dashed and underscored forms are accepted because
// the dashboard emits dashed names while internal tests use underscores.
func IsValidProxyAlgorithm(algorithm string) bool {
	switch strings.ReplaceAll(strings.ToLower(algorithm), "-", "_") {
	case "round_robin", "least_conn", "weighted", "ip_hash", "uri_hash", "random", "sticky":
		return true
	}
	return false
}

// IsWebhookURLSafe checks whether a URL resolves to an address that external
// callbacks must not reach, including loopback, private, link-local, metadata,
// and documentation ranges.
func IsWebhookURLSafe(rawURL string) error {
	return isURLSafe(rawURL, urlSafetyPolicy{})
}

// IsProxyUpstreamSafe checks whether a reverse proxy upstream is safe when
// private upstreams are disabled. Loopback is allowed because UWAS commonly
// proxies local application processes, but private/link-local/metadata ranges
// remain blocked unless the domain explicitly enables private upstreams.
func IsProxyUpstreamSafe(rawURL string) error {
	return isURLSafe(rawURL, urlSafetyPolicy{allowLoopback: true})
}

// IsPrivateProxyUpstreamSafe allows private upstream addresses for domains that
// explicitly opt into them, while still blocking metadata, link-local, and
// documentation ranges.
func IsPrivateProxyUpstreamSafe(rawURL string) error {
	return isURLSafe(rawURL, urlSafetyPolicy{allowLoopback: true, allowPrivate: true})
}

// IsHostSafe checks whether a bare hostname or IP (no scheme/port) is safe to
// dial for non-HTTP egress such as SMTP. Loopback and private addresses are
// permitted because internal SMTP relays (localhost:25, a mail server on a
// private LAN) are a legitimate and common configuration; only the cloud
// metadata endpoint, link-local, documentation and unspecified ranges are
// rejected. The notify-test endpoint that reaches this is itself admin-gated.
func IsHostSafe(host string) error {
	if host == "" {
		return nil
	}
	policy := urlSafetyPolicy{allowLoopback: true, allowPrivate: true}
	if ip := net.ParseIP(host); ip != nil {
		if reason := ipBlockedReason(ip, policy); reason != "" {
			return fmt.Errorf("host %q is a blocked %s (SSRF)", host, reason)
		}
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("host %q: cannot resolve: %w", host, err)
	}
	for _, ip := range ips {
		if reason := ipBlockedReason(ip, policy); reason != "" {
			return fmt.Errorf("host %q resolves to blocked %s %q (SSRF)", host, reason, ip.String())
		}
	}
	return nil
}

func isURLSafe(rawURL string, policy urlSafetyPolicy) error {
	if rawURL == "" {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL has no host")
	}

	// Check if the hostname itself is a blocked IP
	if ip := net.ParseIP(host); ip != nil {
		if reason := ipBlockedReason(ip, policy); reason != "" {
			return fmt.Errorf("URL host %q is a blocked %s (SSRF)", host, reason)
		}
		return nil
	}

	// For hostnames (not IPs), check if it's a known loopback name before DNS lookup.
	if policy.allowLoopback && isLoopback(host) {
		return nil
	}

	// Resolve the hostname and check all resolved IPs
	ips, err := net.LookupIP(host)
	if err != nil {
		// Cannot resolve — reject it; allowing unknown hosts creates an SSRF bypass.
		return fmt.Errorf("host %q: cannot resolve: %w", host, err)
	}
	for _, ip := range ips {
		if reason := ipBlockedReason(ip, policy); reason != "" {
			return fmt.Errorf("URL host %q resolves to blocked %s %q (SSRF)", host, reason, ip.String())
		}
	}
	return nil
}

// validateAutoBlockConfig rejects an autoblock policy that would misfire.
// A malformed whitelist entry is fatal rather than skipped: silently dropping
// the Cloudflare range an operator meant to exempt is how an autoblocker takes
// a site off the internet.
func validateAutoBlockConfig(ab AutoBlockConfig, errs *[]string) {
	if !ab.Enabled {
		return
	}
	if ab.Window.Duration <= 0 {
		*errs = append(*errs, "global.autoblock.window must be greater than 0")
	}
	for name, v := range map[string]int{
		"max_aborts":    ab.MaxAborts,
		"max_waf_hits":  ab.MaxWAFHits,
		"max_rate_hits": ab.MaxRateHits,
		"max_not_found": ab.MaxNotFound,
	} {
		if v <= 0 {
			*errs = append(*errs, fmt.Sprintf("global.autoblock.%s must be greater than 0", name))
		}
	}
	// MaxConnections / MaxConcurrent are pointers: nil means "use the default",
	// an explicit 0 disables the check, and a negative value is invalid.
	if ab.MaxConnections != nil && *ab.MaxConnections < 0 {
		*errs = append(*errs, "global.autoblock.max_connections cannot be negative (0 disables the check)")
	}
	if ab.MaxConcurrent != nil && *ab.MaxConcurrent < 0 {
		*errs = append(*errs, "global.autoblock.max_concurrent cannot be negative (0 disables the check)")
	}
	if ab.BlockDuration.Duration <= 0 {
		*errs = append(*errs, "global.autoblock.block_duration must be greater than 0")
	}
	if ab.MaxBlockDuration.Duration > 0 && ab.MaxBlockDuration.Duration < ab.BlockDuration.Duration {
		*errs = append(*errs, "global.autoblock.max_block_duration cannot be shorter than block_duration")
	}
	for _, entry := range ab.Whitelist {
		if _, err := netip.ParsePrefix(entry); err == nil {
			continue
		}
		if _, err := netip.ParseAddr(entry); err == nil {
			continue
		}
		*errs = append(*errs, fmt.Sprintf("global.autoblock.whitelist: %q is not a valid IP or CIDR", entry))
	}
}

// validateWatchdogConfig keeps the liveness probe from restarting a healthy
// server. A timeout at or above the interval means probes overlap and a slow
// but working server reads as wedged.
// validateBackupSchedule rejects a backup.cron or backup.schedule the scheduler
// can never run. Startup used to skip such a value without a word, so the
// operator believed backups were happening (F31303). A cron expression wins
// over the interval, as it does at startup, so the interval is only checked
// when no cron is set. Field ranges are checked here because the scheduler
// treats an out-of-range value as one that never matches.
func validateBackupSchedule(b BackupConfig, errs *[]string) {
	if cron := strings.TrimSpace(b.Cron); cron != "" {
		fields := strings.Fields(cron)
		if len(fields) != 5 {
			*errs = append(*errs, fmt.Sprintf("global.backup.cron: %q must have 5 fields (minute hour day month weekday)", b.Cron))
			return
		}
		bounds := [5]struct {
			name   string
			lo, hi int
		}{{"minute", 0, 59}, {"hour", 0, 23}, {"day", 1, 31}, {"month", 1, 12}, {"weekday", 0, 7}}
		for i, f := range fields {
			if err := checkCronField(f, bounds[i].lo, bounds[i].hi); err != nil {
				*errs = append(*errs, fmt.Sprintf("global.backup.cron: %s field %q: %v", bounds[i].name, f, err))
			}
		}
		return
	}
	if sched := strings.TrimSpace(b.Schedule); sched != "" {
		if d, err := time.ParseDuration(sched); err != nil || d <= 0 {
			*errs = append(*errs, fmt.Sprintf("global.backup.schedule: %q must be a positive duration such as \"24h\"", b.Schedule))
		}
	}
}

// checkCronField validates one cron field: comma-separated terms of "*", "n",
// "a-b", each with an optional "/step".
func checkCronField(field string, lo, hi int) error {
	for _, term := range strings.Split(field, ",") {
		base, step, hasStep := strings.Cut(term, "/")
		if hasStep {
			if n, err := strconv.Atoi(step); err != nil || n < 1 {
				return fmt.Errorf("step %q must be a positive number", step)
			}
		}
		if base == "*" {
			continue
		}
		from, to, isRange := strings.Cut(base, "-")
		for _, v := range []string{from, to} {
			if v == "" && !isRange {
				return fmt.Errorf("empty value")
			}
			if v == "" {
				return fmt.Errorf("incomplete range %q", base)
			}
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("%q is not a number", v)
			}
			if n < lo || n > hi {
				return fmt.Errorf("%d is outside %d-%d", n, lo, hi)
			}
			if !isRange {
				break
			}
		}
	}
	return nil
}

func validateWatchdogConfig(w WatchdogConfig, errs *[]string) {
	if !w.Enabled {
		return
	}
	if w.Interval.Duration <= 0 {
		*errs = append(*errs, "global.watchdog.interval must be greater than 0")
	}
	if w.Timeout.Duration <= 0 {
		*errs = append(*errs, "global.watchdog.timeout must be greater than 0")
	}
	if w.Interval.Duration > 0 && w.Timeout.Duration >= w.Interval.Duration {
		*errs = append(*errs, "global.watchdog.timeout must be shorter than global.watchdog.interval")
	}
	if w.Failures < 1 {
		*errs = append(*errs, "global.watchdog.failures must be at least 1")
	}
}

// validGeoCode reports whether s is a two-character country code as the geo
// lookup returns it: two ASCII letters or digits, no spaces ("T1" is
// Cloudflare's Tor pseudo-country).
func validGeoCode(s string) bool {
	if len(s) != 2 {
		return false
	}
	for i := 0; i < 2; i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// validACLEntry reports whether an ip_whitelist/ip_blacklist entry is one the
// IP guard can use: a plain address or a CIDR, with no stray spaces.
func validACLEntry(s string) bool {
	if strings.Contains(s, "/") {
		_, _, err := net.ParseCIDR(s)
		return err == nil
	}
	return net.ParseIP(s) != nil
}

// apacheRewriteFlags are the mod_rewrite flag names; a config rewrite flag
// outside this set is a typo the engine would silently ignore.
var apacheRewriteFlags = map[string]bool{
	"B": true, "BCTLS": true, "BNP": true, "C": true, "CO": true, "DPI": true,
	"E": true, "END": true, "F": true, "G": true, "H": true, "L": true,
	"N": true, "NC": true, "NE": true, "NS": true, "NV": true, "P": true,
	"PT": true, "QSA": true, "QSD": true, "QSL": true, "R": true, "S": true,
	"T": true, "UNC": true,
}

// ValidateClientAuthCA reports a mutual-TLS setting that cannot work: "request"
// or "require" with no CA to verify client certificates against. The handshake
// is then not configured and the domain serves anonymous clients while the
// operator believes mutual TLS is on (F13501). A partial (PATCH) validation
// sees only the changed fields, so it cannot judge this: an update that adds
// client_auth may rely on a CA already stored on the domain. A caller that
// merges a patch must therefore run this on the MERGED domain (F16701 made
// MergeDomain keep client_auth, which is what exposed the gap).
func ValidateClientAuthCA(ssl SSLConfig) error {
	switch ssl.ClientAuth {
	case "request", "require":
		if ssl.ClientCA == "" {
			return fmt.Errorf("ssl.client_auth %q needs ssl.client_ca (no client certificate can be verified without a CA)", ssl.ClientAuth)
		}
	}
	return nil
}
