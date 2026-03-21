package cache

import (
	"hash/fnv"
	"net/http"
	"sort"
	"strings"
)

// GenerateKey creates a cache key from the request.
// Key = FNV-1a(method + host + path + sorted_query + vary_headers)
func GenerateKey(r *http.Request, varyHeaders []string) string {
	h := fnv.New64a()
	h.Write([]byte(r.Method))
	h.Write([]byte("|"))
	h.Write([]byte(r.Host))
	h.Write([]byte("|"))
	h.Write([]byte(r.URL.Path))
	h.Write([]byte("|"))

	// Sorted query params for consistency
	if r.URL.RawQuery != "" {
		params := strings.Split(r.URL.RawQuery, "&")
		sort.Strings(params)
		h.Write([]byte(strings.Join(params, "&")))
	}

	// Vary headers
	for _, name := range varyHeaders {
		h.Write([]byte("|"))
		h.Write([]byte(name))
		h.Write([]byte("="))
		h.Write([]byte(r.Header.Get(name)))
	}

	return formatKey(h.Sum64())
}

func formatKey(n uint64) string {
	const hex = "0123456789abcdef"
	var buf [16]byte
	for i := 15; i >= 0; i-- {
		buf[i] = hex[n&0xF]
		n >>= 4
	}
	return string(buf[:])
}

// KeyPrefix returns first 4 chars for disk cache directory sharding.
func KeyPrefix(key string) (dir1, dir2 string) {
	if len(key) < 4 {
		return "00", "00"
	}
	return key[:2], key[2:4]
}
