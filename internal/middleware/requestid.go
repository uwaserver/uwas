package middleware

import (
	"crypto/rand"
	"net/http"
	"time"
)

const requestIDHeader = "X-Request-ID"

// RequestID adds a unique request ID to each request/response.
// Preserves incoming X-Request-ID if present.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(requestIDHeader)
			if id == "" {
				id = generateRequestID()
			}
			w.Header().Set(requestIDHeader, id)
			next.ServeHTTP(w, r)
		})
	}
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
	rand.Read(b[6:])
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
