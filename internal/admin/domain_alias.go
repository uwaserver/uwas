package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/uwaserver/uwas/internal/config"
)

func removeDomainAlias(aliases []string, host string) []string {
	host = normalizeDomainHostname(host)
	if host == "" {
		return aliases
	}
	out := aliases[:0]
	for _, alias := range aliases {
		if normalizeDomainHostname(alias) == host {
			continue
		}
		out = append(out, alias)
	}
	return out
}

type domainAliasOptions struct {
	redirect         bool
	redirectCode     int
	preservePath     bool
	canonicalHost    string
	canonicalHostSet bool
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
	mode := strings.ToLower(strings.TrimSpace(raw.AliasMode))
	opts := domainAliasOptions{preservePath: true}
	if raw.AliasPreservePath != nil {
		opts.preservePath = *raw.AliasPreservePath
	}
	if raw.CanonicalHost != "" {
		opts.canonicalHostSet = true
		canonicalHost, err := normalizeRequestedCanonicalHost(raw.CanonicalHost)
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

func validateRequestedDomainAliases(host string, aliases []string) error {
	rawHost := normalizeDomainHostname(host)
	host = canonicalDomainHostname(host)
	seen := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		rawAlias := normalizeDomainHostname(alias)
		aliasKey := canonicalDomainHostname(alias)
		if aliasKey == "" {
			continue
		}
		if rawAlias == rawHost {
			return fmt.Errorf("alias %q cannot be the same as the domain host", rawAlias)
		}
		if aliasKey == host {
			continue
		}
		if _, ok := seen[aliasKey]; ok {
			return fmt.Errorf("duplicate alias %q", aliasKey)
		}
		seen[aliasKey] = struct{}{}
	}
	return nil
}

func newCanonicalRedirectAliasDomain(alias, targetHost string, status int, preservePath bool) config.Domain {
	alias = canonicalDomainHostname(alias)
	targetHost = canonicalDomainHostname(targetHost)
	if status == 0 {
		status = http.StatusMovedPermanently
	}
	return config.Domain{
		Host: alias,
		Type: string(config.DomainTypeRedirect),
		SSL:  config.SSLConfig{Mode: "auto"},
		Redirect: config.RedirectConfig{
			Target:       "https://" + targetHost,
			Status:       status,
			PreservePath: preservePath,
		},
	}
}

func autoWWWRedirectHost(d config.Domain) string {
	host := normalizeDomainHostname(d.Host)
	if host == "" || d.Type == string(config.DomainTypeRedirect) {
		return ""
	}
	if strings.HasPrefix(host, "www.") || strings.Contains(host, ":") || strings.HasPrefix(host, "*.") {
		return ""
	}
	if !strings.Contains(host, ".") {
		return ""
	}
	return "www." + host
}

func applyDomainCanonicalPreference(d *config.Domain, opts domainAliasOptions) []string {
	host := normalizeDomainHostname(d.Host)
	if host == "" || d.Type == string(config.DomainTypeRedirect) {
		d.CanonicalHost = ""
		return nil
	}
	apex, _, ok := apexAndWWWHost(host)
	if !ok {
		return nil
	}
	d.Host = apex
	if opts.canonicalHostSet {
		d.CanonicalHost = opts.canonicalHost
	} else {
		d.CanonicalHost = normalizeCanonicalHostPreference(d.CanonicalHost)
	}
	normalizeDomainHostnames(d)
	return nil
}

func normalizeRequestedCanonicalHost(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "apex", "root", "naked", "domain":
		return "apex", nil
	case "www":
		return "www", nil
	case "both", "none", "no-redirect":
		return "apex", nil
	default:
		return "", fmt.Errorf("canonical_host must be apex or www")
	}
}

func normalizeCanonicalHostPreference(value string) string {
	canonical, err := normalizeRequestedCanonicalHost(value)
	if err != nil {
		return "apex"
	}
	return canonical
}

func apexAndWWWHost(host string) (string, string, bool) {
	host = normalizeDomainHostname(host)
	if host == "" || strings.Contains(host, ":") || strings.HasPrefix(host, "*.") || !strings.Contains(host, ".") {
		return "", "", false
	}
	if strings.HasPrefix(host, "www.") {
		apex := strings.TrimPrefix(host, "www.")
		if apex == "" || !strings.Contains(apex, ".") {
			return "", "", false
		}
		return apex, host, true
	}
	return host, "www." + host, true
}

func uniqueNormalizedHostnames(hosts []string) []string {
	seen := make(map[string]struct{}, len(hosts))
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		host = normalizeDomainHostname(host)
		if host == "" {
			continue
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		out = append(out, host)
	}
	return out
}

func upsertCanonicalRedirectAliasDomains(domains *[]config.Domain, skipIndex int, aliases []string, targetHost string, status int, preservePath bool) {
	for _, alias := range aliases {
		alias = canonicalDomainHostname(alias)
		if alias == "" {
			continue
		}
		redirectDomain := newCanonicalRedirectAliasDomain(alias, targetHost, status, preservePath)
		updated := false
		for i := range *domains {
			if i == skipIndex {
				continue
			}
			if canonicalDomainHostname((*domains)[i].Host) == alias {
				(*domains)[i] = redirectDomain
				updated = true
				break
			}
		}
		if !updated {
			*domains = append(*domains, redirectDomain)
		}
	}
}

func removeImplicitWWWRedirectDomains(domains *[]config.Domain, targetHost string, skipIndex int) {
	targetHost = canonicalDomainHostname(targetHost)
	if targetHost == "" {
		return
	}
	out := (*domains)[:0]
	for i, d := range *domains {
		if i == skipIndex || !isCanonicalRedirectAliasDomain(d, implicitWWWHostname(targetHost), targetHost) {
			out = append(out, d)
		}
	}
	*domains = out
}

func isImplicitWWWRedirectForDomains(d config.Domain, domains []config.Domain) bool {
	if d.Type != string(config.DomainTypeRedirect) {
		return false
	}
	host := canonicalDomainHostname(d.Host)
	if host == "" {
		return false
	}
	for _, candidate := range domains {
		if candidate.Type == string(config.DomainTypeRedirect) {
			continue
		}
		if canonicalDomainHostname(candidate.Host) == host && isCanonicalRedirectAliasDomain(d, d.Host, candidate.Host) {
			return true
		}
	}
	return false
}

func publicDomainAliases(d config.Domain) []string {
	host := canonicalDomainHostname(d.Host)
	seen := make(map[string]struct{}, len(d.Aliases))
	out := make([]string, 0, len(d.Aliases))
	for _, alias := range d.Aliases {
		alias = canonicalDomainHostname(alias)
		if alias == "" || alias == host {
			continue
		}
		if _, ok := seen[alias]; ok {
			continue
		}
		seen[alias] = struct{}{}
		out = append(out, alias)
	}
	return out
}

func findDomainHostnameConflictAllowingRedirect(domains []config.Domain, skipIndex int, host, targetHost string) string {
	host = canonicalDomainHostname(host)
	targetHost = canonicalDomainHostname(targetHost)
	if host == "" {
		return ""
	}
	for i, d := range domains {
		if i == skipIndex {
			continue
		}
		if canonicalDomainHostname(d.Host) == host {
			if isCanonicalRedirectAliasDomain(d, host, targetHost) {
				return ""
			}
			return d.Host
		}
		for _, alias := range d.Aliases {
			if canonicalDomainHostname(alias) == host {
				return d.Host
			}
		}
	}
	return ""
}

func isCanonicalRedirectAliasDomain(d config.Domain, host, targetHost string) bool {
	if canonicalDomainHostname(d.Host) != canonicalDomainHostname(host) {
		return false
	}
	if d.Type != string(config.DomainTypeRedirect) {
		return false
	}
	target := strings.TrimRight(strings.ToLower(strings.TrimSpace(d.Redirect.Target)), "/")
	return target == "https://"+canonicalDomainHostname(targetHost) || target == "https://"+implicitWWWHostname(targetHost)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
