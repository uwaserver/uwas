package cache

import (
	"hash/fnv"
	"net/http"
	"sort"
	"strings"
	"sync"
)

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
func GenerateKey(r *http.Request, varyHeaders []string) string {
	b := builderPool.Get().(*strings.Builder)
	b.Reset()
	defer builderPool.Put(b)

	// Pre-allocate based on typical URL lengths
	b.Grow(300 + len(r.URL.RawQuery) + len(r.Host) + len(r.URL.Path))

	b.WriteString(r.Method)
	b.WriteByte('|')
	if r.TLS != nil {
		b.WriteString("https|")
	} else {
		b.WriteString("http|")
	}
	b.WriteString(r.Host)
	b.WriteByte('|')
	b.WriteString(r.URL.Path)
	b.WriteByte('|')

	// Sorted query params for consistency
	if r.URL.RawQuery != "" {
		// Split and sort for cache key consistency (key=a&b vs key=b&a)
		params := strings.Split(r.URL.RawQuery, "&")
		sort.Strings(params)
		for i, p := range params {
			if i > 0 {
				b.WriteByte('&')
			}
			b.WriteString(p)
		}
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
