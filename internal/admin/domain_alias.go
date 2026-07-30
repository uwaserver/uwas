package admin

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/domainutil"
)

// domainAliasOptions holds alias redirect/canonical configuration parsed from request body.
type domainAliasOptions = domainutilAliasOptions

type domainutilAliasOptions struct {
	redirect         bool
	redirectCode     int
	preservePath     bool
	canonicalHost    string
	canonicalHostSet bool
}

func removeDomainAlias(aliases []string, host string) []string {
	return domainutil.RemoveDomainAlias(aliases, host)
}

func parseDomainAliasOptions(body []byte) (domainAliasOptions, error) {
	var raw struct {
		AliasMode         string `json:"alias_mode,omitempty"`
		AliasRedirectCode int    `json:"alias_redirect_code,omitempty"`
		AliasPreservePath *bool  `json:"alias_preserve_path,omitempty"`
		CanonicalHost     string `json:"canonical_host,omitempty"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return domainAliasOptions{}, fmt.Errorf("invalid JSON")
	}
	mode := normalizeLowerTrim(raw.AliasMode)
	opts := domainAliasOptions{preservePath: true}
	if raw.AliasPreservePath != nil {
		opts.preservePath = *raw.AliasPreservePath
	}
	if raw.CanonicalHost != "" {
		opts.canonicalHostSet = true
		canonicalHost, err := domainutil.NormalizeRequestedCanonicalHost(raw.CanonicalHost)
		if err != nil {
			return domainAliasOptions{}, err
		}
		opts.canonicalHost = canonicalHost
	}
	switch mode {
	case "", "alias", "redirect":
	default:
		return domainAliasOptions{}, fmt.Errorf("alias_mode must be redirect")
	}
	opts.redirect = true
	opts.redirectCode = raw.AliasRedirectCode
	if opts.redirectCode == 0 {
		opts.redirectCode = http.StatusMovedPermanently
	}
	if opts.redirectCode != http.StatusMovedPermanently && opts.redirectCode != http.StatusFound {
		return domainAliasOptions{}, fmt.Errorf("alias_redirect_code must be 301 or 302")
	}
	return opts, nil
}

func normalizeLowerTrim(s string) string {
	// Local helper to avoid importing strings just for this
	lowered := ""
	for _, c := range s {
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		lowered += string(c)
	}
	// trim spaces
	start, end := 0, len(lowered)
	for start < end && lowered[start] == ' ' { start++ }
	for end > start && lowered[end-1] == ' ' { end-- }
	return lowered[start:end]
}

func validateRequestedDomainAliases(host string, aliases []string) error {
	return domainutil.ValidateRequestedDomainAliases(host, aliases)
}

func newCanonicalRedirectAliasDomain(alias, targetHost string, status int, preservePath bool) config.Domain {
	return domainutil.NewCanonicalRedirectAliasDomain(alias, targetHost, status, preservePath)
}

func autoWWWRedirectHost(d config.Domain) string {
	return domainutil.AutoWWWRedirectHost(d)
}

func applyDomainCanonicalPreference(d *config.Domain, opts domainAliasOptions) []string {
	host := domainutil.NormalizeDomainHostname(d.Host)
	if host == "" || d.Type == string(config.DomainTypeRedirect) {
		d.CanonicalHost = ""
		return nil
	}
	apex, _, ok := domainutil.ApexAndWWWHost(host)
	if !ok {
		return nil
	}
	d.Host = apex
	if opts.canonicalHostSet {
		d.CanonicalHost = opts.canonicalHost
	} else {
		d.CanonicalHost = domainutil.NormalizeCanonicalHostPreference(d.CanonicalHost)
	}
	domainutil.NormalizeDomainHostnames(d)
	return nil
}

func normalizeRequestedCanonicalHost(value string) (string, error) {
	return domainutil.NormalizeRequestedCanonicalHost(value)
}

func normalizeCanonicalHostPreference(value string) string {
	return domainutil.NormalizeCanonicalHostPreference(value)
}

func apexAndWWWHost(host string) (string, string, bool) {
	return domainutil.ApexAndWWWHost(host)
}

func uniqueNormalizedHostnames(hosts []string) []string {
	return domainutil.UniqueNormalizedHostnames(hosts)
}

func upsertCanonicalRedirectAliasDomains(domains *[]config.Domain, skipIndex int, aliases []string, targetHost string, status int, preservePath bool) {
	domainutil.UpsertCanonicalRedirectAliasDomains(domains, skipIndex, aliases, targetHost, status, preservePath)
}

func removeImplicitWWWRedirectDomains(domains *[]config.Domain, targetHost string, skipIndex int) {
	domainutil.RemoveImplicitWWWRedirectDomains(domains, targetHost, skipIndex)
}

func isImplicitWWWRedirectForDomains(d config.Domain, domains []config.Domain) bool {
	return domainutil.IsImplicitWWWRedirectForDomains(d, domains)
}

func publicDomainAliases(d config.Domain) []string {
	return domainutil.PublicDomainAliases(d)
}

func findDomainHostnameConflictAllowingRedirect(domains []config.Domain, skipIndex int, host, targetHost string) string {
	return domainutil.FindDomainHostnameConflictAllowingRedirect(domains, skipIndex, host, targetHost)
}

func isCanonicalRedirectAliasDomain(d config.Domain, host, targetHost string) bool {
	return domainutil.IsCanonicalRedirectAliasDomain(d, host, targetHost)
}

func firstNonEmpty(values ...string) string {
	return domainutil.FirstNonEmpty(values...)
}
