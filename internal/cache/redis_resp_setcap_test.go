package cache

// Regression: respClient.Set must reject values larger than the reader's
// bulk cap. readReply refuses any declared bulk over maxBulkLen (64 MiB),
// so before this guard Set could store an entry this client could never
// read back — every Get of that key failed with the over-cap error and the
// entry sat in Redis as unreadable garbage until purged. (Before the
// framing fix, each such read additionally desynced the connection.) The
// writer's cap now matches the reader's: oversized values fail fast at
// write time and the engine logs them as a skipped L3 write.

import (
	"bufio"
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
)

// redisStoreServer is a minimal Redis stand-in: SET stores, GET returns the
// stored value as a bulk string. Returns the store map for assertions.
func redisStoreServer(ln net.Listener) map[string]string {
	stored := make(map[string]string)
	var mu sync.Mutex
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
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
						l, err := strconv.Atoi(strings.TrimPrefix(string(lhdr), "$"))
						if err != nil {
							return
						}
						buf := make([]byte, l+2) // payload + CRLF
						if _, err := io.ReadFull(br, buf); err != nil {
							return
						}
						args = append(args, string(buf[:l]))
					}
					switch {
					case len(args) >= 3 && args[0] == "SET":
						mu.Lock()
						stored[args[1]] = args[2]
						mu.Unlock()
						if _, err := c.Write([]byte("+OK\r\n")); err != nil {
							return
						}
					case len(args) >= 2 && args[0] == "GET":
						mu.Lock()
						v, ok := stored[args[1]]
						mu.Unlock()
						if !ok {
							if _, err := c.Write([]byte("$-1\r\n")); err != nil {
								return
							}
							continue
						}
						if _, err := c.Write([]byte("$" + strconv.Itoa(len(v)) + "\r\n" + v + "\r\n")); err != nil {
							return
						}
					default:
						if _, err := c.Write([]byte("-ERR unknown command\r\n")); err != nil {
							return
						}
					}
				}
			}(c)
		}
	}()
	return stored
}

func TestRespClientSetRejectsUnreadableValue(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	stored := redisStoreServer(ln)

	cl, err := newRespClient(config.RedisConfig{Addr: ln.Addr().String()})
	if err != nil {
		t.Fatalf("FAIL control: dial: %v", err)
	}
	defer cl.Close()
	ctx := context.Background()

	// Control: a small value round-trips through the real client.
	if err := cl.Set(ctx, "small", "hello", time.Minute); err != nil {
		t.Fatalf("FAIL control: small Set: %v", err)
	}
	if v, err := cl.Get(ctx, "small"); err != nil || v != "hello" {
		t.Fatalf("FAIL control: small Get = %q, %v; want \"hello\", nil", v, err)
	}

	// Hostile: one byte over the reader's cap. Set must reject it at write
	// time; a client that stores it can then never read it back.
	big := string(make([]byte, maxBulkLen+1))
	if err := cl.Set(ctx, "big", big, time.Minute); err == nil {
		if _, getErr := cl.Get(ctx, "big"); getErr != nil {
			t.Fatalf("FAIL: Set accepted a %d-byte value that Get cannot read back (%v) — the client stored an entry its own reader rejects (> %d cap): unreadable garbage in Redis until purged, poisoning every reader of that key", len(big), getErr, maxBulkLen)
		}
		if len(stored["big"]) != len(big) {
			t.Fatalf("FAIL: server store mismatch")
		}
		t.Fatalf("FAIL: Set accepted and Get returned a %d-byte value — no write-side cap exists", len(big))
	}
}
