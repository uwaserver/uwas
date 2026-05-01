package router

import (
	"crypto/rand"
	"net/http"
	"sync"
	"time"
)

type RequestContext struct {
	// Identity
	idCached string         // lazily computed string form of idBytes
	idBytes  [16]byte      // raw UUID bytes — avoids string alloc on hot path
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
	OriginalURI  string // use OriginalURI() method for lazy computation
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
	generateIDBytes(&ctx.idBytes)
	ctx.StartTime = time.Now()
	ctx.Request = r
	ctx.Response = NewResponseWriter(w)
	ctx.OriginalURI = r.URL.RequestURI()
	return ctx
}

func ReleaseContext(ctx *RequestContext) {
	ReleaseResponseWriter(ctx.Response)
	ctx.Request = nil
	ctx.Response = nil
	contextPool.Put(ctx)
}

// ID returns ctx's ID as a string. Provided for API compatibility; code that
// accesses ctx.ID directly should be updated to call ctx.RequestContextID().
func (ctx *RequestContext) ID() string {
	return ctx.RequestContextID()
}

// RequestContextID returns ctx's ID as a string, lazily computed on first call
// and cached for subsequent accesses. Saves 1 alloc/req vs storing ID as string
// directly (string([16]byte) always heap-allocates).
func (ctx *RequestContext) RequestContextID() string {
	if ctx.idCached == "" && ctx.idBytes != [16]byte{} {
		const hex = "0123456789abcdef"
		var sb [36]byte
		for i, j := 0, 0; i < 16; i++ {
			if i == 4 || i == 6 || i == 8 || i == 10 {
				sb[j] = '-'
				j++
			}
			sb[j] = hex[ctx.idBytes[i]>>4]
			sb[j+1] = hex[ctx.idBytes[i]&0xF]
			j += 2
		}
		ctx.idCached = string(sb[:])
	}
	return ctx.idCached
}

// generateIDBytes writes a UUID v7-like ID into buf.
func generateIDBytes(buf *[16]byte) {
	ms := uint64(time.Now().UnixMilli())
	buf[0] = byte(ms >> 40)
	buf[1] = byte(ms >> 32)
	buf[2] = byte(ms >> 24)
	buf[3] = byte(ms >> 16)
	buf[4] = byte(ms >> 8)
	buf[5] = byte(ms)
	rand.Read(buf[6:])
	// Set version (7) and variant (RFC 9562)
	buf[6] = (buf[6] & 0x0F) | 0x70
	buf[8] = (buf[8] & 0x3F) | 0x80
}

// generateID is exported for tests. Applications should use ctx.RequestContextID().
func generateID() string {
	var b [16]byte
	generateIDBytes(&b)
	const hex = "0123456789abcdef"
	var sb [36]byte
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
