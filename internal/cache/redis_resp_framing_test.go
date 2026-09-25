package cache

// Regression: respClient.command() must discard a connection whose reply
// framing was broken. The over-cap reply branches (declared bulk/array
// longer than maxBulkLen/maxArrayLen) reject the reply WITHOUT consuming
// the declared payload — the RESP stream position is unknown afterwards.
// Before the fix, those errors were not net.Errors, so command() left the
// desynced connection in place and the NEXT command parsed the leftover
// bytes as its own reply (wrong-value pairing). Reachable from a hostile or
// misconfigured peer, or self-inflicted: Set has no size cap, so a >64 MiB
// stored value poisons every reader of that key. readReply now wraps
// framing-breaking parse failures in errFramingLost and command() redials
// on it exactly as it does on network errors.
//
// Scripted server:
//   conn 1 (poisoned): GET k1 -> "$100000000\r\n" (over cap, no payload);
//                      GET k2 -> "$5\r\nhello\r\n" (the framing tail)
//   conn 2 (fresh):    GET k1 -> "$-1\r\n" (nil); GET k2 -> "$5\r\nworld\r\n"
// A correct client must discard the desynced connection: after the failed
// GET k1, GET k2 must return "world" from the fresh connection.

import (
	"bufio"
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// handleScriptedRedis reads RESP requests (array of bulk strings) and
// replies from the script map keyed by space-joined args ("GET k1").
func handleScriptedRedis(c net.Conn, replies map[string]string) {
	defer c.Close()
	br := bufio.NewReader(c)
	for {
		hdr, err := readLine(br) // "*N"
		if err != nil {
			return
		}
		n, err := strconv.Atoi(strings.TrimPrefix(string(hdr), "*"))
		if err != nil || n <= 0 {
			return
		}
		args := make([]string, 0, n)
		for i := 0; i < n; i++ {
			lhdr, err := readLine(br) // "$len"
			if err != nil {
				return
			}
			ln, err := strconv.Atoi(strings.TrimPrefix(string(lhdr), "$"))
			if err != nil {
				return
			}
			buf := make([]byte, ln+2) // payload + CRLF
			if _, err := io.ReadFull(br, buf); err != nil {
				return
			}
			args = append(args, string(buf[:ln]))
		}
		reply, ok := replies[strings.Join(args, " ")]
		if !ok {
			reply = "-ERR unknown script entry\r\n"
		}
		if _, err := c.Write([]byte(reply)); err != nil {
			return
		}
	}
}

// serveScriptedRedis accepts connections and hands each the i-th script.
func serveScriptedRedis(ln net.Listener, scripts []map[string]string) *atomic.Int32 {
	var conns atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			idx := int(conns.Add(1))
			if idx > len(scripts) {
				c.Close()
				continue
			}
			go handleScriptedRedis(c, scripts[idx-1])
		}
	}()
	return &conns
}

// Control: a well-formed scripted round-trip through the real client.
func TestRespClientWellFormedControl(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	serveScriptedRedis(ln, []map[string]string{
		{"GET ck": "$3\r\nabc\r\n"},
	})

	cl, err := newRespClient(config.RedisConfig{Addr: ln.Addr().String()})
	if err != nil {
		t.Fatalf("FAIL control: dial: %v", err)
	}
	defer cl.Close()

	v, err := cl.Get(context.Background(), "ck")
	if err != nil {
		t.Fatalf("FAIL control: Get: %v", err)
	}
	if v != "abc" {
		t.Fatalf("FAIL control: got %q, want %q", v, "abc")
	}
}

// Hostile: after a framing-broken reply, the client must not reuse the
// desynced connection — GET k2 must come from a fresh connection.
func TestRespClientFramingDesyncPoisonsNextCommand(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	serveScriptedRedis(ln, []map[string]string{
		{ // conn 1: poisoned
			"GET k1": "$100000000\r\n",  // declared bulk > 64 MiB cap, no payload
			"GET k2": "$5\r\nhello\r\n", // framing tail, parses as k2's reply
		},
		{ // conn 2: fresh (only used if the client discards conn 1)
			"GET k1": "$-1\r\n", // nil
			"GET k2": "$5\r\nworld\r\n",
		},
	})

	cl, err := newRespClient(config.RedisConfig{Addr: ln.Addr().String()})
	if err != nil {
		t.Fatalf("FAIL: dial: %v", err)
	}
	defer cl.Close()

	ctx := context.Background()
	if _, err := cl.Get(ctx, "k1"); err == nil {
		t.Fatalf("FAIL: expected an error from the over-cap bulk declaration")
	}
	v, err := cl.Get(ctx, "k2")
	if err != nil {
		t.Fatalf("FAIL: Get(k2) errored: %v", err)
	}
	if v != "world" {
		t.Fatalf("FAIL: Get(k2)=%q after a framing-broken reply — the desynced connection was reused and the poisoned framing tail was served as k2's value (want \"world\" from a fresh connection)", v)
	}
}
