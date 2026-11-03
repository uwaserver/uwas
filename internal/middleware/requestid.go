package middleware

import (
	"crypto/rand"
	"encoding/binary"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

// Pre-computed canonical header key — avoids per-request
// textproto.CanonicalMIMEHeaderKey calls in Header.Get/Header.Set.
var reqIDKey = http.CanonicalHeaderKey("X-Request-ID")

// requestIDFallback supplies per-process uniqueness only when the operating
// system entropy source fails. The timestamp prefix keeps restart generations
// distinct in ordinary operation.
var requestIDFallback atomic.Uint64

// RequestID adds a unique request ID to each request/response.
// Preserves incoming X-Request-ID if present.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Direct map access — skips Header.Get's canonicalization.
			var id string
			vals := r.Header[reqIDKey]
			if len(vals) > 0 {
				id = vals[0]
			}
			if !validRequestID(id) {
				id = generateRequestID()
				// Direct map assignment — skips Header.Set's canonicalization.
				r.Header[reqIDKey] = []string{id}
			} else if len(vals) > 1 {
				// Only the first value was validated; the rest would reach the
				// upstream unbounded and contradict the ID stamped on the
				// response (F10301).
				r.Header[reqIDKey] = vals[:1:1]
			}
			// Stamp response header (direct map, no Set canonicalization).
			w.Header()[reqIDKey] = []string{id}
			next.ServeHTTP(w, r)
		})
	}
}

// maxRequestIDLen caps a client-supplied X-Request-ID: it is reflected in the
// response, forwarded upstream and written to every log line (F3101).
const maxRequestIDLen = 128

// validRequestID reports whether a client-supplied ID is safe to propagate:
// non-empty, bounded, and printable ASCII without spaces.
func validRequestID(id string) bool {
	if id == "" || len(id) > maxRequestIDLen {
		return false
	}
	for i := 0; i < len(id); i++ {
		if c := id[i]; c <= ' ' || c >= 0x7f {
			return false
		}
	}
	return true
}

func generateRequestID() string {
	ms := uint64(time.Now().UnixMilli())
	var b [16]byte
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	// crypto/rand.Read terminates the process on entropy failure in current Go
	// releases. Reading Reader directly keeps the fallback reachable.
	if _, err := io.ReadFull(rand.Reader, b[6:]); err != nil {
		binary.BigEndian.PutUint64(b[8:], requestIDFallback.Add(1))
	}
	b[6] = (b[6] & 0x0F) | 0x70
	b[8] = (b[8] & 0x3F) | 0x80

	// Manual hex formatting to avoid fmt.Sprintf overhead
	const hex = "0123456789abcdef"
	var sb [36]byte
	// 8-4-4-4-12 format
	for i, j := 0, 0; i < 16; i++ {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			sb[j] = '-'
			j++
		}
		sb[j] = hex[b[i]>>4]
		sb[j+1] = hex[b[i]&0xF]
		j += 2
	}
	return string(sb[:])
}
