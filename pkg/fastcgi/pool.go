package fastcgi

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Pool manages a pool of FastCGI connections.
type Pool struct {
	network string // "unix" or "tcp"
	address string // socket path or host:port
	maxIdle int
	maxOpen int
	maxLife time.Duration
	idle    chan *conn
	active  atomic.Int32
	mu      sync.Mutex
	closed  bool
}

type conn struct {
	netConn   net.Conn
	createdAt time.Time
	usedAt    time.Time
}

// PoolConfig configures a connection pool.
type PoolConfig struct {
	Address     string        // "unix:/var/run/php-fpm.sock" or "tcp:127.0.0.1:9000"
	MaxIdle     int           // max idle connections (default 10)
	MaxOpen     int           // max total connections (default 64)
	MaxLifetime time.Duration // max connection lifetime (default 5m)
}

// NewPool creates a new connection pool.
func NewPool(cfg PoolConfig) *Pool {
	network, address := parseAddress(cfg.Address)

	maxIdle := cfg.MaxIdle
	if maxIdle <= 0 {
		maxIdle = 10
	}
	maxOpen := cfg.MaxOpen
	if maxOpen <= 0 {
		maxOpen = 64
	}
	maxLife := cfg.MaxLifetime
	if maxLife <= 0 {
		maxLife = 5 * time.Minute
	}

	return &Pool{
		network: network,
		address: address,
		maxIdle: maxIdle,
		maxOpen: maxOpen,
		maxLife: maxLife,
		idle:    make(chan *conn, maxIdle),
	}
}

// Get returns an idle connection or creates a new one.
func (p *Pool) Get(ctx context.Context) (*conn, error) {
	// 1. Try idle connection
	for {
		select {
		case c := <-p.idle:
			// Check if stale
			if time.Since(c.usedAt) > 30*time.Second || time.Since(c.createdAt) > p.maxLife {
				c.netConn.Close()
				p.active.Add(-1)
				continue
			}
			c.usedAt = time.Now()
			return c, nil
		default:
			goto create
		}
	}

create:
	// 2. Create new if under limit
	if int(p.active.Load()) < p.maxOpen {
		return p.create(ctx)
	}

	// 3. Wait for idle connection or context cancel
	select {
	case c := <-p.idle:
		c.usedAt = time.Now()
		return c, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Put returns a connection to the pool.
func (p *Pool) Put(c *conn) {
	if c == nil {
		return
	}
	c.usedAt = time.Now()
	select {
	case p.idle <- c:
	default:
		// Pool full
		c.netConn.Close()
		p.active.Add(-1)
	}
}

// Discard closes a connection without returning it to the pool.
func (p *Pool) Discard(c *conn) {
	if c == nil {
		return
	}
	c.netConn.Close()
	p.active.Add(-1)
}

// Close drains and closes all connections.
func (p *Pool) Close() {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()

	close(p.idle)
	for c := range p.idle {
		c.netConn.Close()
	}
}

// Stats returns current pool statistics.
func (p *Pool) Stats() (active, idle int) {
	return int(p.active.Load()), len(p.idle)
}

func (p *Pool) create(ctx context.Context) (*conn, error) {
	p.active.Add(1)

	d := net.Dialer{Timeout: 5 * time.Second}
	nc, err := d.DialContext(ctx, p.network, p.address)
	if err != nil {
		p.active.Add(-1)
		return nil, fmt.Errorf("dial %s %s: %w", p.network, p.address, err)
	}

	return &conn{
		netConn:   nc,
		createdAt: time.Now(),
		usedAt:    time.Now(),
	}, nil
}

// parseAddress splits "unix:/path" or "tcp:host:port" into network and address.
func parseAddress(addr string) (network, address string) {
	if strings.HasPrefix(addr, "unix:") {
		return "unix", addr[5:]
	}
	if strings.HasPrefix(addr, "tcp:") {
		return "tcp", addr[4:]
	}
	// Default: if it starts with / it's unix, otherwise tcp
	if strings.HasPrefix(addr, "/") {
		return "unix", addr
	}
	return "tcp", addr
}
