package cache

import (
	"hash/fnv"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// stackParamsCap bounds the stack-allocated params array used by
// writeSortedQuery. 16 covers 99%+ of real-world query strings; longer
// queries fall back to a heap slice.
const stackParamsCap = 16

// pool for strings.Builder to reduce allocations.
var builderPool = sync.Pool{
	New: func() interface{} {
		b := new(strings.Builder)
		return b
	},
}

// GenerateKey creates a cache key from the request.
// The key is the full canonical string (method|host|path|query|vary) so that
// collisions are impossible. The FNV-1a hash is only used for disk sharding
// via HashKey.
//
// Host is normalized: lowercase + port stripped to ensure Example.com:80
// and example.com produce the same cache key.
func GenerateKey(r *http.Request, varyHeaders []string) string {
	b := builderPool.Get().(*strings.Builder)
	b.Reset()
	defer builderPool.Put(b)

	// Normalize host: lowercase + strip port
	host := r.Host
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	host = strings.ToLower(host)

	// Pre-allocate based on typical URL lengths
	b.Grow(300 + len(r.URL.RawQuery) + len(host) + len(r.URL.Path))

	b.WriteString(r.Method)
	b.WriteByte('|')
	if r.TLS != nil {
		b.WriteString("https|")
	} else {
		b.WriteString("http|")
	}
	b.WriteString(host)
	b.WriteByte('|')
	b.WriteString(r.URL.Path)
	b.WriteByte('|')

	// Sorted query params for consistency (key=a&b and key=b&a → same key).
	if r.URL.RawQuery != "" {
		writeSortedQuery(b, r.URL.RawQuery)
	}

	// Vary headers
	for _, name := range varyHeaders {
		b.WriteByte('|')
		b.WriteString(name)
		b.WriteByte('=')
		b.WriteString(r.Header.Get(name))
	}

	return b.String()
}

// writeSortedQuery splits raw on '&', sorts the parts lexicographically, and
// writes them to b separated by '&'. Avoids strings.Split's slice + per-part
// allocation for the common case of ≤16 params by using a stack array.
func writeSortedQuery(b *strings.Builder, raw string) {
	// Fast path: no '&' means a single param — write as-is.
	if !strings.Contains(raw, "&") {
		b.WriteString(raw)
		return
	}

	var stack [stackParamsCap]string
	parts := stack[:0]
	overflow := false

	start := 0
	for i := 0; i < len(raw); i++ {
		if raw[i] != '&' {
			continue
		}
		if len(parts) == stackParamsCap {
			overflow = true
			break
		}
		parts = append(parts, raw[start:i])
		start = i + 1
	}
	if !overflow {
		if len(parts) == stackParamsCap {
			overflow = true
		} else {
			parts = append(parts, raw[start:])
		}
	}

	if overflow {
		// Rare: >16 params. Fall back to strings.Split + sort.Strings.
		full := strings.Split(raw, "&")
		sort.Strings(full)
		for i, p := range full {
			if i > 0 {
				b.WriteByte('&')
			}
			b.WriteString(p)
		}
		return
	}

	// Insertion sort — O(n²) but n≤16, no allocation, branch-predictable.
	for i := 1; i < len(parts); i++ {
		v := parts[i]
		j := i - 1
		for j >= 0 && parts[j] > v {
			parts[j+1] = parts[j]
			j--
		}
		parts[j+1] = v
	}

	for i, p := range parts {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(p)
	}
}

// HashKey returns a hex-encoded FNV-1a hash of the key, used for disk
// directory sharding.
func HashKey(key string) string {
	h := fnv.New64a()
	h.Write([]byte(key))
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

// KeyPrefix returns first 4 chars of the hash for disk cache directory sharding.
func KeyPrefix(key string) (dir1, dir2 string) {
	h := HashKey(key)
	return h[:2], h[2:4]
}
