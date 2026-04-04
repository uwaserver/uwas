package router

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"sync"
	"time"
)

type RequestContext struct {
	// Identity
	ID        string
	StartTime time.Time

	// HTTP
	Request  *http.Request
	Response *ResponseWriter

	// Routing
	VHostName  string
	RemoteIP   string
	RemotePort string
	ServerPort string
	IsHTTPS    bool

	// Path Resolution
	OriginalURI  string
	RewrittenURI string
	DocumentRoot string
	ResolvedPath string
	ScriptName   string
	PathInfo     string

	// State
	CacheStatus   string
	Upstream      string
	PHPEnvOverride map[string]string // htaccess-derived PHP_VALUE override (per-request, not mutated on domain)

	// Metrics
	BytesSent int64
	Duration  time.Duration
	TTFBDur   time.Duration
}

var contextPool = sync.Pool{
	New: func() any {
		return &RequestContext{}
	},
}

func AcquireContext(w http.ResponseWriter, r *http.Request) *RequestContext {
	ctx := contextPool.Get().(*RequestContext)
	// Zero the entire struct to prevent data leaking between pooled uses.
	*ctx = RequestContext{}
	ctx.ID = generateID()
	ctx.StartTime = time.Now()
	ctx.Request = r
	ctx.Response = NewResponseWriter(w)
	ctx.OriginalURI = r.URL.RequestURI()
	return ctx
}

func ReleaseContext(ctx *RequestContext) {
	ctx.Request = nil
	ctx.Response = nil
	contextPool.Put(ctx)
}

// generateID creates a UUID v7-like sortable ID.
func generateID() string {
	ms := uint64(time.Now().UnixMilli())

	var b [16]byte
	// 48-bit timestamp in big-endian
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	rand.Read(b[6:])

	// Set version (7) and variant (RFC 9562)
	b[6] = (b[6] & 0x0F) | 0x70
	b[8] = (b[8] & 0x3F) | 0x80

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
