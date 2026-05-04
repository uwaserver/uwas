package cache

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/uwaserver/uwas/internal/config"
)

// respClient implements RedisClient using the RESP wire protocol.
// One process-wide TCP connection, serialized through a mutex (Redis is
// single-threaded; pipelining buys little for our cache use case).
// Auto-reconnects on the next call after any I/O error.
type respClient struct {
	addr     string
	password string
	db       int
	tls      *tls.Config

	mu   sync.Mutex
	conn net.Conn
	rd   *bufio.Reader
	wr   *bufio.Writer
}

// ErrRedisNotFound is returned by Get when the key has no value.
var ErrRedisNotFound = errors.New("redis: key not found")

// dialTimeout is short — cache backend should never block request handling.
const (
	dialTimeout = 3 * time.Second
	ioTimeout   = 5 * time.Second
)

func newRespClient(cfg config.RedisConfig) (*respClient, error) {
	c := &respClient{
		addr:     cfg.Addr,
		password: cfg.Password,
		db:       cfg.DB,
	}
	if cfg.TLS {
		c.tls = &tls.Config{ServerName: hostFromAddr(cfg.Addr)}
	}
	if err := c.dial(); err != nil {
		return nil, err
	}
	return c, nil
}

func hostFromAddr(addr string) string {
	if i := strings.LastIndex(addr, ":"); i > 0 {
		return addr[:i]
	}
	return addr
}

// dial opens (or re-opens) the TCP connection and runs AUTH/SELECT if needed.
// Caller must hold c.mu.
func (c *respClient) dial() error {
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}

	d := &net.Dialer{Timeout: dialTimeout}
	var (
		conn net.Conn
		err  error
	)
	if c.tls != nil {
		conn, err = tls.DialWithDialer(d, "tcp", c.addr, c.tls)
	} else {
		conn, err = d.Dial("tcp", c.addr)
	}
	if err != nil {
		return fmt.Errorf("redis dial %s: %w", c.addr, err)
	}

	c.conn = conn
	c.rd = bufio.NewReader(conn)
	c.wr = bufio.NewWriter(conn)

	if c.password != "" {
		if _, err := c.commandLocked("AUTH", c.password); err != nil {
			c.conn.Close()
			c.conn = nil
			return fmt.Errorf("redis AUTH: %w", err)
		}
	}
	if c.db != 0 {
		if _, err := c.commandLocked("SELECT", strconv.Itoa(c.db)); err != nil {
			c.conn.Close()
			c.conn = nil
			return fmt.Errorf("redis SELECT %d: %w", c.db, err)
		}
	}
	return nil
}

// command runs a single RESP command and returns the parsed reply.
// Reconnects once on I/O error.
func (c *respClient) command(args ...string) (any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		if err := c.dial(); err != nil {
			return nil, err
		}
	}
	reply, err := c.commandLocked(args...)
	if err == nil {
		return reply, nil
	}
	// On I/O error try one reconnect.
	if isNetErr(err) {
		if derr := c.dial(); derr != nil {
			return nil, derr
		}
		return c.commandLocked(args...)
	}
	return nil, err
}

func isNetErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne)
}

// commandLocked writes the request and reads one reply. Caller holds c.mu.
func (c *respClient) commandLocked(args ...string) (any, error) {
	if c.conn == nil {
		return nil, errors.New("redis: not connected")
	}
	c.conn.SetDeadline(time.Now().Add(ioTimeout))
	if err := writeArray(c.wr, args); err != nil {
		return nil, err
	}
	if err := c.wr.Flush(); err != nil {
		return nil, err
	}
	return readReply(c.rd)
}

// writeArray writes a RESP array of bulk strings: *N\r\n$len\r\nval\r\n…
func writeArray(w *bufio.Writer, args []string) error {
	if _, err := fmt.Fprintf(w, "*%d\r\n", len(args)); err != nil {
		return err
	}
	for _, a := range args {
		if _, err := fmt.Fprintf(w, "$%d\r\n", len(a)); err != nil {
			return err
		}
		if _, err := w.WriteString(a); err != nil {
			return err
		}
		if _, err := w.WriteString("\r\n"); err != nil {
			return err
		}
	}
	return nil
}

// readReply parses one RESP reply. Returns:
//   - string for + (simple) and $ (bulk)
//   - int64  for :
//   - []any  for * (array, recursively)
//   - nil    for $-1 / *-1 (RESP nil)
//   - error  for - (error reply) and any I/O failure
func readReply(r *bufio.Reader) (any, error) {
	line, err := readLine(r)
	if err != nil {
		return nil, err
	}
	if len(line) == 0 {
		return nil, errors.New("redis: empty reply")
	}
	switch line[0] {
	case '+':
		return string(line[1:]), nil
	case '-':
		return nil, fmt.Errorf("redis: %s", string(line[1:]))
	case ':':
		n, err := strconv.ParseInt(string(line[1:]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("redis: bad integer %q", line[1:])
		}
		return n, nil
	case '$':
		n, err := strconv.Atoi(string(line[1:]))
		if err != nil {
			return nil, fmt.Errorf("redis: bad bulk len %q", line[1:])
		}
		if n < 0 {
			return nil, nil // RESP nil
		}
		buf := make([]byte, n+2) // includes trailing CRLF
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		return string(buf[:n]), nil
	case '*':
		n, err := strconv.Atoi(string(line[1:]))
		if err != nil {
			return nil, fmt.Errorf("redis: bad array len %q", line[1:])
		}
		if n < 0 {
			return nil, nil
		}
		out := make([]any, n)
		for i := 0; i < n; i++ {
			out[i], err = readReply(r)
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("redis: unknown reply type %q", line[0])
	}
}

// readLine reads up to and including \r\n, returning the bytes WITHOUT the CRLF.
func readLine(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	if len(line) < 2 || line[len(line)-2] != '\r' {
		return nil, fmt.Errorf("redis: malformed line")
	}
	return line[:len(line)-2], nil
}

// --- RedisClient interface implementation ---

func (c *respClient) Get(_ context.Context, key string) (string, error) {
	reply, err := c.command("GET", key)
	if err != nil {
		return "", err
	}
	if reply == nil {
		return "", ErrRedisNotFound
	}
	s, ok := reply.(string)
	if !ok {
		return "", fmt.Errorf("redis GET: unexpected reply type %T", reply)
	}
	return s, nil
}

func (c *respClient) Set(_ context.Context, key, value string, ttl time.Duration) error {
	if ttl > 0 {
		secs := int64(ttl / time.Second)
		if secs < 1 {
			secs = 1
		}
		_, err := c.command("SET", key, value, "EX", strconv.FormatInt(secs, 10))
		return err
	}
	_, err := c.command("SET", key, value)
	return err
}

func (c *respClient) Del(_ context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	args := append([]string{"DEL"}, keys...)
	_, err := c.command(args...)
	return err
}

// Keys returns matching keys. WARNING: KEYS scans the entire keyspace and is
// O(N) — fine for sparse cache but avoid on hot paths.
func (c *respClient) Keys(_ context.Context, pattern string) ([]string, error) {
	reply, err := c.command("KEYS", pattern)
	if err != nil {
		return nil, err
	}
	arr, ok := reply.([]any)
	if !ok {
		if reply == nil {
			return nil, nil
		}
		return nil, fmt.Errorf("redis KEYS: unexpected reply type %T", reply)
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out, nil
}

func (c *respClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}
