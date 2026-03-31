package cache

import (
	"encoding/binary"
	"net/http"
	"time"
)

// Status constants for X-Cache header.
const (
	StatusHit   = "HIT"
	StatusMiss  = "MISS"
	StatusStale = "STALE"
)

// CachedResponse holds a complete HTTP response for caching.
type CachedResponse struct {
	StatusCode  int
	Headers     http.Header
	Body        []byte
	Created     time.Time
	TTL         time.Duration
	GraceTTL    time.Duration
	Tags        []string
	ESITemplate bool // true if Body contains ESI tags needing assembly on serve
}

// Size returns the approximate memory size of this entry in bytes.
func (r *CachedResponse) Size() int64 {
	size := int64(len(r.Body))
	for k, vals := range r.Headers {
		size += int64(len(k))
		for _, v := range vals {
			size += int64(len(v))
		}
	}
	size += 64 // struct overhead
	return size
}

// Age returns how long ago this response was cached.
func (r *CachedResponse) Age() time.Duration {
	return time.Since(r.Created)
}

// IsFresh returns true if the entry is within its TTL.
func (r *CachedResponse) IsFresh() bool {
	return time.Since(r.Created) < r.TTL
}

// IsStale returns true if expired but within grace period.
func (r *CachedResponse) IsStale() bool {
	age := time.Since(r.Created)
	return age >= r.TTL && age < r.TTL+r.GraceTTL
}

// IsExpired returns true if beyond TTL + grace.
func (r *CachedResponse) IsExpired() bool {
	return time.Since(r.Created) >= r.TTL+r.GraceTTL
}

// Serialize encodes the response for disk storage.
func (r *CachedResponse) Serialize() []byte {
	// Simple binary format:
	// [4] status code
	// [8] created unix nano
	// [8] ttl nanoseconds
	// [8] grace ttl nanoseconds
	// [4] header count
	// for each header: [2]keyLen [2]valLen key val
	// [4] body length
	// body

	var buf []byte
	b4 := make([]byte, 4)
	b8 := make([]byte, 8)

	binary.BigEndian.PutUint32(b4, uint32(r.StatusCode))
	buf = append(buf, b4...)

	binary.BigEndian.PutUint64(b8, uint64(r.Created.UnixNano()))
	buf = append(buf, b8...)

	binary.BigEndian.PutUint64(b8, uint64(r.TTL))
	buf = append(buf, b8...)

	binary.BigEndian.PutUint64(b8, uint64(r.GraceTTL))
	buf = append(buf, b8...)

	// Headers
	headerCount := 0
	for _, vals := range r.Headers {
		headerCount += len(vals)
	}
	binary.BigEndian.PutUint32(b4, uint32(headerCount))
	buf = append(buf, b4...)

	for k, vals := range r.Headers {
		for _, v := range vals {
			binary.BigEndian.PutUint32(b4, uint32(len(k)))
			buf = append(buf, b4...)
			binary.BigEndian.PutUint32(b4, uint32(len(v)))
			buf = append(buf, b4...)
			buf = append(buf, k...)
			buf = append(buf, v...)
		}
	}

	// Body
	binary.BigEndian.PutUint32(b4, uint32(len(r.Body)))
	buf = append(buf, b4...)
	buf = append(buf, r.Body...)

	// ESI flag (1 byte, backward compatible extension)
	if r.ESITemplate {
		buf = append(buf, 1)
	} else {
		buf = append(buf, 0)
	}

	return buf
}

// Deserialize decodes a cached response from disk storage.
func Deserialize(data []byte) (*CachedResponse, error) {
	if len(data) < 32 {
		return nil, errCorrupt
	}

	r := &CachedResponse{}
	pos := 0

	r.StatusCode = int(binary.BigEndian.Uint32(data[pos:]))
	pos += 4

	r.Created = time.Unix(0, int64(binary.BigEndian.Uint64(data[pos:])))
	pos += 8

	r.TTL = time.Duration(binary.BigEndian.Uint64(data[pos:]))
	pos += 8

	r.GraceTTL = time.Duration(binary.BigEndian.Uint64(data[pos:]))
	pos += 8

	headerCount := int(binary.BigEndian.Uint32(data[pos:]))
	pos += 4

	r.Headers = make(http.Header)
	for i := 0; i < headerCount; i++ {
		if pos+8 > len(data) {
			return nil, errCorrupt
		}
		keyLen := int(binary.BigEndian.Uint32(data[pos:]))
		pos += 4
		valLen := int(binary.BigEndian.Uint32(data[pos:]))
		pos += 4
		if pos+keyLen+valLen > len(data) {
			return nil, errCorrupt
		}
		key := string(data[pos : pos+keyLen])
		pos += keyLen
		val := string(data[pos : pos+valLen])
		pos += valLen
		r.Headers.Add(key, val)
	}

	if pos+4 > len(data) {
		return nil, errCorrupt
	}
	bodyLen := int(binary.BigEndian.Uint32(data[pos:]))
	pos += 4
	if pos+bodyLen > len(data) {
		return nil, errCorrupt
	}
	r.Body = append([]byte(nil), data[pos:pos+bodyLen]...)
	pos += bodyLen

	// ESI flag (backward compatible: missing = false)
	if pos < len(data) {
		r.ESITemplate = data[pos] == 1
	}

	return r, nil
}

var errCorrupt = &CacheError{Message: "corrupt cache entry"}

type CacheError struct {
	Message string
}

func (e *CacheError) Error() string { return e.Message }
