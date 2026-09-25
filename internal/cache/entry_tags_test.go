package cache

// Regression: Deserialize must bound the tags pre-allocation. The serialized
// tagCount is untrusted (corrupt on-disk .cache files, hostile Redis bulk
// payloads); before the guard it flowed straight into
// make([]string, 0, tagCount), so a 4-byte field of 0xFFFFFFFF requested a
// ~64 GiB zeroed allocation — fatal out-of-memory on the request path
// (DiskCache.Get), the admin purge path (PurgeByTag), and the background
// sweep (cleanExpired). See also redis_resp_hostile_test.go (round-6 class:
// RESP wire lengths) — this file covers the serialized-format layer.

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net/http"
	"runtime"
	"slices"
	"testing"
	"time"
)

// craftTagPayload builds a minimal serialized entry: status=200, a fixed
// Created, 1-minute TTL/grace, zero headers, empty body, ESI flag 0, and a
// tags section that declares tagCount tags but supplies no tag bytes.
func craftTagPayload(tagCount uint32) []byte {
	buf := make([]byte, 0, 41)
	var b4 [4]byte
	var b8 [8]byte
	fixed := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	binary.BigEndian.PutUint32(b4[:], 200)
	buf = append(buf, b4[:]...)
	binary.BigEndian.PutUint64(b8[:], uint64(fixed.UnixNano()))
	buf = append(buf, b8[:]...)
	binary.BigEndian.PutUint64(b8[:], uint64(time.Minute))
	buf = append(buf, b8[:]...)
	binary.BigEndian.PutUint64(b8[:], uint64(time.Minute))
	buf = append(buf, b8[:]...)
	binary.BigEndian.PutUint32(b4[:], 0) // headerCount
	buf = append(buf, b4[:]...)
	binary.BigEndian.PutUint32(b4[:], 0) // bodyLen
	buf = append(buf, b4[:]...)
	buf = append(buf, 0) // ESI flag
	binary.BigEndian.PutUint32(b4[:], tagCount)
	buf = append(buf, b4[:]...)
	return buf
}

func readTotalAlloc(t *testing.T) uint64 {
	t.Helper()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.TotalAlloc
}

// Control 1: a well-formed entry with tags round-trips unchanged.
func TestDeserializeTagsRoundtripControl(t *testing.T) {
	fixed := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	src := &CachedResponse{
		StatusCode: 200,
		Headers:    http.Header{"X-Uwas-Test": {"a", "b"}},
		Body:       []byte("hello"),
		Created:    fixed,
		TTL:        time.Minute,
		GraceTTL:   time.Minute,
		Tags:       []string{"site:example.com", "asset"},
	}
	got, err := Deserialize(src.Serialize())
	if err != nil {
		t.Fatalf("FAIL control: roundtrip returned error: %v", err)
	}
	if got.StatusCode != src.StatusCode || !got.Created.Equal(src.Created) ||
		got.TTL != src.TTL || got.GraceTTL != src.GraceTTL ||
		!bytes.Equal(got.Body, src.Body) || !slices.Equal(got.Tags, src.Tags) ||
		len(got.Headers.Values("X-Uwas-Test")) != 2 {
		t.Fatalf("FAIL control: roundtrip mismatch:\n got %+v\nwant %+v", got, src)
	}
}

// Control 2: a SMALL truncated tags section (declares 2 tags, supplies 0
// bytes) is rejected cleanly with a near-zero allocation — the per-tag
// guards themselves are sound.
func TestDeserializeTruncatedTagsCleanError(t *testing.T) {
	payload := craftTagPayload(2)
	before := readTotalAlloc(t)
	resp, err := Deserialize(payload)
	after := readTotalAlloc(t)
	if err == nil || resp != nil {
		t.Fatalf("FAIL control: expected clean errCorrupt, got resp=%v err=%v", resp, err)
	}
	if after-before > 1<<20 {
		t.Fatalf("FAIL control: tiny truncated tags section allocated %d bytes — should be near-zero", after-before)
	}
}

// Hostile: a 41-byte entry declaring tagCount=0xFFFFFFFF. The parser must
// reject it without attempting a multi-gigabyte pre-allocation. If the guard
// regresses, this dies with a fatal out-of-memory (or, on a very large
// machine, fails the allocation-bound assertion below).
func TestDeserializeTagCountIsBounded(t *testing.T) {
	payload := craftTagPayload(0xFFFFFFFF)
	fmt.Printf("entry_tags: Deserialize on a %d-byte entry declaring tagCount=4294967295 — must be rejected without a multi-gigabyte allocation\n", len(payload))
	before := readTotalAlloc(t)
	resp, err := Deserialize(payload)
	after := readTotalAlloc(t)
	if err == nil || resp != nil {
		t.Fatalf("FAIL: expected errCorrupt for a truncated tags section, got resp=%v err=%v", resp, err)
	}
	if after-before > 1<<30 {
		t.Fatalf("FAIL: Deserialize allocated %d bytes (bound 1 GiB) for a 41-byte corrupt entry — the untrusted tagCount flows straight into make()", after-before)
	}
}
