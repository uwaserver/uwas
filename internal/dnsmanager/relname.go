package dnsmanager

import "strings"

// zoneRelativeName turns a fully-qualified record name inside zone into the
// zone-relative spelling Hetzner DNS and DigitalOcean expect ("blog.example.com"
// in zone "example.com" is "blog", the apex is "@"). Those APIs append the zone
// to whatever they are given, so an FQDN would be stored as
// "blog.example.com.example.com" (F55300). A name that is already relative, or
// that is not inside the zone, is returned unchanged. An empty zone name (not
// known) leaves the name alone.
func zoneRelativeName(name, zone string) string {
	z := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(zone), "."))
	n := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
	if z == "" || n == "" {
		return name
	}
	if n == z {
		return "@"
	}
	if rel, ok := strings.CutSuffix(n, "."+z); ok && rel != "" {
		return rel
	}
	return name
}
