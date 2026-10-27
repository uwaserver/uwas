package dnsmanager

import (
	"strconv"
	"strings"
)

// mxValue returns the record value for providers whose API has no priority
// field (Hetzner DNS, Route53): an MX value is "<priority> <host>". A value
// that already starts with a number is left alone.
func mxValue(rec Record) string {
	if rec.Type != "MX" || rec.Priority <= 0 {
		return rec.Content
	}
	if first, _, _ := strings.Cut(strings.TrimSpace(rec.Content), " "); first != "" {
		if _, err := strconv.Atoi(first); err == nil {
			return rec.Content
		}
	}
	return strconv.Itoa(rec.Priority) + " " + strings.TrimSpace(rec.Content)
}
