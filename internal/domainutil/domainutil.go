// Package domainutil provides pure domain hostname manipulation helpers
// shared across the admin package and its sub-packages. Extracted to avoid
// circular imports between admin and admin/domain.
package domainutil

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/uwaserver/uwas/internal/config"
)

// NormalizeDomainHostname lowercases, trims whitespace, strips trailing dot.
func NormalizeDomainHostname(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}

// CanonicalDomainHostname strips "www." prefix from a normalized hostname.
// Wildcards, bracketed hosts, and port-containing hosts are returned as-is.
func CanonicalDomainHostname(host string) string {
	host = NormalizeDomainHostname(host)
	if host == "" || strings.Contains(host, ":") || strings.HasPrefix(host, "*.") {
		return host
	}
	if strings.HasPrefix(host, "www.") {
		apex := strings.TrimPrefix(host, "www.")
		if apex != "" && strings.Contains(apex, ".") {
			return apex
		}
	}
	return host
}

// ImplicitWWWHostname returns the "www." variant of a host, or "" if not applicable.
func ImplicitWWWHostname(host string) string {
	host = CanonicalDomainHostname(host)
	if host == "" || strings.Contains(host, ":") || strings.HasPrefix(host, "*.") || !strings.Contains(host, ".") {
		return ""
	}
	return "www." + host
}

// NormalizeCanonicalHostPreference normalizes a canonical host value to "apex" or "www".
func NormalizeCanonicalHostPreference(value string) string {
	canonical, err := NormalizeRequestedCanonicalHost(value)
	if err != nil {
		return "apex"
	}
	return canonical
}

// NormalizeRequestedCanonicalHost validates and normalizes a canonical host preference.
func NormalizeRequestedCanonicalHost(value string) (string, error) {
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

// ApexAndWWWHost returns the apex and www forms of a host.
func ApexAndWWWHost(host string) (string, string, bool) {
	host = NormalizeDomainHostname(host)
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

// NormalizeDomainHostnames normalizes a domain's Host, CanonicalHost, and Aliases in place.
func NormalizeDomainHostnames(d *config.Domain) {
	d.Host = CanonicalDomainHostname(d.Host)
	if d.Type == string(config.DomainTypeRedirect) {
		d.CanonicalHost = ""
	} else if d.Host != "" {
		d.CanonicalHost = NormalizeCanonicalHostPreference(d.CanonicalHost)
	}
	seen := make(map[string]struct{}, len(d.Aliases))
	aliases := make([]string, 0, len(d.Aliases))
	for _, alias := range d.Aliases {
		alias = CanonicalDomainHostname(alias)
		if alias == "" || alias == d.Host {
			continue
		}
		if _, ok := seen[alias]; ok {
			continue
		}
		seen[alias] = struct{}{}
		aliases = append(aliases, alias)
	}
	d.Aliases = aliases
}

// DomainHostnames returns all hostnames a domain responds to (host + www + aliases).
func DomainHostnames(d config.Domain) []string {
	seen := make(map[string]struct{}, 2+len(d.Aliases)*2)
	hosts := make([]string, 0, 2+len(d.Aliases)*2)
	for _, host := range append([]string{d.Host}, d.Aliases...) {
		host = CanonicalDomainHostname(host)
		if host == "" {
			continue
		}
		candidates := []string{host, ImplicitWWWHostname(host)}
		if NormalizeCanonicalHostPreference(d.CanonicalHost) == "www" && ImplicitWWWHostname(host) != "" {
			candidates = []string{ImplicitWWWHostname(host), host}
		}
		for _, candidate := range candidates {
			if candidate == "" {
				continue
			}
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			hosts = append(hosts, candidate)
		}
	}
	return hosts
}

// MainDomainHostname returns the primary hostname for a domain (apex or www based on preference).
func MainDomainHostname(d config.Domain) string {
	host := CanonicalDomainHostname(d.Host)
	if host == "" {
		return NormalizeDomainHostname(d.Host)
	}
	if d.Type != string(config.DomainTypeRedirect) && NormalizeCanonicalHostPreference(d.CanonicalHost) == "www" {
		if www := ImplicitWWWHostname(host); www != "" {
			return www
		}
	}
	return host
}

// FindDomainHostnameConflict returns the owning host if the given hostname is already claimed, or "".
func FindDomainHostnameConflict(domains []config.Domain, skipIndex int, host string) string {
	host = CanonicalDomainHostname(host)
	if host == "" {
		return ""
	}
	for i, d := range domains {
		if i == skipIndex {
			continue
		}
		if CanonicalDomainHostname(d.Host) == host {
			return d.Host
		}
		for _, alias := range d.Aliases {
			if CanonicalDomainHostname(alias) == host {
				return d.Host
			}
		}
	}
	return ""
}

// FindDomainHostnameConflictAllowingRedirect is like FindDomainHostnameConflict
// but returns "" if the conflict is a canonical redirect alias targeting targetHost.
func FindDomainHostnameConflictAllowingRedirect(domains []config.Domain, skipIndex int, host, targetHost string) string {
	host = CanonicalDomainHostname(host)
	targetHost = CanonicalDomainHostname(targetHost)
	if host == "" {
		return ""
	}
	for i, d := range domains {
		if i == skipIndex {
			continue
		}
		if CanonicalDomainHostname(d.Host) == host {
			if IsCanonicalRedirectAliasDomain(d, host, targetHost) {
				return ""
			}
			return d.Host
		}
		for _, alias := range d.Aliases {
			if CanonicalDomainHostname(alias) == host {
				return d.Host
			}
		}
	}
	return ""
}

// IsValidHostname delegates to config.IsValidHostname.
func IsValidHostname(s string) bool { return config.IsValidHostname(s) }

// DomainTypeUsesWebRoot reports whether the domain type serves files from a root directory.
func DomainTypeUsesWebRoot(domainType string) bool {
	switch domainType {
	case "static", "php":
		return true
	default:
		return false
	}
}

// PublicDomainAliases returns deduplicated, non-self aliases for a domain.
func PublicDomainAliases(d config.Domain) []string {
	host := CanonicalDomainHostname(d.Host)
	seen := make(map[string]struct{}, len(d.Aliases))
	out := make([]string, 0, len(d.Aliases))
	for _, alias := range d.Aliases {
		alias = CanonicalDomainHostname(alias)
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

// RemoveDomainAlias removes all occurrences of host from aliases.
func RemoveDomainAlias(aliases []string, host string) []string {
	host = NormalizeDomainHostname(host)
	if host == "" {
		return aliases
	}
	out := aliases[:0]
	for _, alias := range aliases {
		if NormalizeDomainHostname(alias) == host {
			continue
		}
		out = append(out, alias)
	}
	return out
}

// AutoWWWRedirectHost returns the www hostname for a domain if it should have an implicit www redirect.
func AutoWWWRedirectHost(d config.Domain) string {
	host := NormalizeDomainHostname(d.Host)
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

// UniqueNormalizedHostnames returns deduplicated normalized hostnames.
func UniqueNormalizedHostnames(hosts []string) []string {
	seen := make(map[string]struct{}, len(hosts))
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		host = NormalizeDomainHostname(host)
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

// NewCanonicalRedirectAliasDomain creates a redirect domain entry for an alias.
func NewCanonicalRedirectAliasDomain(alias, targetHost string, status int, preservePath bool) config.Domain {
	alias = CanonicalDomainHostname(alias)
	targetHost = CanonicalDomainHostname(targetHost)
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

// UpsertCanonicalRedirectAliasDomains adds or updates redirect alias domains.
func UpsertCanonicalRedirectAliasDomains(domains *[]config.Domain, skipIndex int, aliases []string, targetHost string, status int, preservePath bool) {
	for _, alias := range aliases {
		alias = CanonicalDomainHostname(alias)
		if alias == "" {
			continue
		}
		redirectDomain := NewCanonicalRedirectAliasDomain(alias, targetHost, status, preservePath)
		updated := false
		for i := range *domains {
			if i == skipIndex {
				continue
			}
			if CanonicalDomainHostname((*domains)[i].Host) == alias {
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

// RemoveImplicitWWWRedirectDomains removes implicit www redirect domains for a target host.
func RemoveImplicitWWWRedirectDomains(domains *[]config.Domain, targetHost string, skipIndex int) {
	targetHost = CanonicalDomainHostname(targetHost)
	if targetHost == "" {
		return
	}
	out := (*domains)[:0]
	for i, d := range *domains {
		if i == skipIndex || !IsCanonicalRedirectAliasDomain(d, ImplicitWWWHostname(targetHost), targetHost) {
			out = append(out, d)
		}
	}
	*domains = out
}

// IsImplicitWWWRedirectForDomains checks if a domain is an implicit www redirect.
func IsImplicitWWWRedirectForDomains(d config.Domain, domains []config.Domain) bool {
	if d.Type != string(config.DomainTypeRedirect) {
		return false
	}
	host := CanonicalDomainHostname(d.Host)
	if host == "" {
		return false
	}
	for _, candidate := range domains {
		if candidate.Type == string(config.DomainTypeRedirect) {
			continue
		}
		if CanonicalDomainHostname(candidate.Host) == host && IsCanonicalRedirectAliasDomain(d, d.Host, candidate.Host) {
			return true
		}
	}
	return false
}

// IsCanonicalRedirectAliasDomain checks if d is a redirect alias pointing at targetHost.
func IsCanonicalRedirectAliasDomain(d config.Domain, host, targetHost string) bool {
	if CanonicalDomainHostname(d.Host) != CanonicalDomainHostname(host) {
		return false
	}
	if d.Type != string(config.DomainTypeRedirect) {
		return false
	}
	target := strings.TrimRight(strings.ToLower(strings.TrimSpace(d.Redirect.Target)), "/")
	return target == "https://"+CanonicalDomainHostname(targetHost) || target == "https://"+ImplicitWWWHostname(targetHost)
}

// FirstNonEmpty returns the first non-empty string from variadic args.
func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// ValidateRequestedDomainAliases validates that aliases don't conflict with the host.
func ValidateRequestedDomainAliases(host string, aliases []string) error {
	rawHost := NormalizeDomainHostname(host)
	host = CanonicalDomainHostname(host)
	seen := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		rawAlias := NormalizeDomainHostname(alias)
		aliasKey := CanonicalDomainHostname(alias)
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
