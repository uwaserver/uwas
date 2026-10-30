package middleware

// RateBucket is the bucket identity the rate limiters use for a client
// address: an IPv6 client is counted by its /64 (it owns at least that much
// address space), IPv4 and IPv4-mapped addresses are unchanged. It is exported
// for limiters that live outside this package (per-location limits in the
// server) so that they cannot disagree with the domain and global limiters
// about who one client is.
func RateBucket(ip string) string { return rateAddr(ip) }
