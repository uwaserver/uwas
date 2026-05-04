package cache

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/config"
)

// fakeRedis is a minimal in-process Redis-compatible TCP server for tests.
// Supports the commands respClient actually issues: PING, AUTH, SELECT,
// GET, SET (with optional EX), DEL, KEYS (only "*" returns all keys).
type fakeRedis struct {
	listener net.Listener
	mu       sync.Mutex
	store    map[string]string
}

func newFakeRedis(t *testing.T) *fakeRedis {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("fake redis listen: %v", err)
	}
	f := &fakeRedis{listener: l, store: map[string]string{}}
	go f.serve()
	t.Cleanup(func() { l.Close() })
	return f
}

func (f *fakeRedis) addr() string { return f.listener.Addr().String() }

func (f *fakeRedis) serve() {
	for {
		c, err := f.listener.Accept()
		if err != nil {
			return
		}
		go f.handle(c)
	}
}

func (f *fakeRedis) handle(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	w := bufio.NewWriter(c)
	for {
		args, err := readArrayCmd(r)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return
			}
			return
		}
		if len(args) == 0 {
			continue
		}
		switch strings.ToUpper(args[0]) {
		case "PING":
			w.WriteString("+PONG\r\n")
		case "AUTH", "SELECT":
			w.WriteString("+OK\r\n")
		case "SET":
			if len(args) < 3 {
				w.WriteString("-ERR wrong args\r\n")
				break
			}
			f.mu.Lock()
			f.store[args[1]] = args[2]
			f.mu.Unlock()
			w.WriteString("+OK\r\n")
		case "GET":
			f.mu.Lock()
			v, ok := f.store[args[1]]
			f.mu.Unlock()
			if !ok {
				w.WriteString("$-1\r\n")
			} else {
				w.WriteString("$" + strconv.Itoa(len(v)) + "\r\n" + v + "\r\n")
			}
		case "DEL":
			f.mu.Lock()
			n := 0
			for _, k := range args[1:] {
				if _, ok := f.store[k]; ok {
					delete(f.store, k)
					n++
				}
			}
			f.mu.Unlock()
			w.WriteString(":" + strconv.Itoa(n) + "\r\n")
		case "KEYS":
			f.mu.Lock()
			keys := make([]string, 0, len(f.store))
			for k := range f.store {
				keys = append(keys, k)
			}
			f.mu.Unlock()
			w.WriteString("*" + strconv.Itoa(len(keys)) + "\r\n")
			for _, k := range keys {
				w.WriteString("$" + strconv.Itoa(len(k)) + "\r\n" + k + "\r\n")
			}
		default:
			w.WriteString("-ERR unknown command\r\n")
		}
		w.Flush()
	}
}

// readArrayCmd reads one RESP array of bulk strings sent by the client.
func readArrayCmd(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if len(line) < 2 || line[0] != '*' {
		return nil, errors.New("expected array header")
	}
	n, err := strconv.Atoi(line[1:])
	if err != nil {
		return nil, err
	}
	args := make([]string, n)
	for i := 0; i < n; i++ {
		head, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		head = strings.TrimRight(head, "\r\n")
		if len(head) < 2 || head[0] != '$' {
			return nil, errors.New("expected bulk header")
		}
		ln, err := strconv.Atoi(head[1:])
		if err != nil {
			return nil, err
		}
		buf := make([]byte, ln+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		args[i] = string(buf[:ln])
	}
	return args, nil
}

func TestRespClient_Roundtrip(t *testing.T) {
	srv := newFakeRedis(t)

	c, err := newRespClient(config.RedisConfig{Enabled: true, Addr: srv.addr()})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	ctx := context.Background()

	// SET with TTL
	if err := c.Set(ctx, "k1", "hello", 60*time.Second); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := c.Get(ctx, "k1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}

	// Missing key returns ErrRedisNotFound
	if _, err := c.Get(ctx, "missing"); !errors.Is(err, ErrRedisNotFound) {
		t.Fatalf("expected ErrRedisNotFound, got %v", err)
	}

	// DEL
	if err := c.Del(ctx, "k1"); err != nil {
		t.Fatalf("Del: %v", err)
	}
	if _, err := c.Get(ctx, "k1"); !errors.Is(err, ErrRedisNotFound) {
		t.Fatalf("expected miss after delete, got %v", err)
	}

	// KEYS
	c.Set(ctx, "a", "1", 0)
	c.Set(ctx, "b", "2", 0)
	keys, err := c.Keys(ctx, "*")
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %v", keys)
	}
}

func TestRespClient_AuthFailure(t *testing.T) {
	// Listener that always returns an error on the first reply (simulates AUTH fail).
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		// Drain the AUTH command
		r := bufio.NewReader(c)
		_, _ = readArrayCmd(r)
		c.Write([]byte("-ERR invalid password\r\n"))
	}()

	_, err = newRespClient(config.RedisConfig{Enabled: true, Addr: l.Addr().String(), Password: "x"})
	if err == nil {
		t.Fatal("expected AUTH failure error")
	}
	if !strings.Contains(err.Error(), "AUTH") {
		t.Fatalf("expected AUTH in error, got %v", err)
	}
}

func TestRespClient_DialFailure(t *testing.T) {
	// Connect to an address that's almost certainly closed.
	_, err := newRespClient(config.RedisConfig{Enabled: true, Addr: "127.0.0.1:1"})
	if err == nil {
		t.Fatal("expected dial failure")
	}
}
