package proxy

import (
	"net/url"
	"sync"
	"sync/atomic"
)

// BackendState represents the health state of a backend.
type BackendState int

const (
	StateHealthy BackendState = iota
	StateUnhealthy
	StateDraining
)

// Backend is a single upstream server.
type Backend struct {
	URL         *url.URL
	Weight      int
	State       atomic.Int32 // BackendState
	ActiveConns atomic.Int64
	TotalReqs   atomic.Int64
	TotalFails  atomic.Int64
}

func (b *Backend) GetState() BackendState {
	return BackendState(b.State.Load())
}

func (b *Backend) SetState(s BackendState) {
	b.State.Store(int32(s))
}

func (b *Backend) IsHealthy() bool {
	return b.GetState() == StateHealthy
}

// UpstreamPool manages a set of backends.
type UpstreamPool struct {
	mu       sync.RWMutex
	backends []*Backend
}

// NewUpstreamPool creates a pool from address/weight pairs.
func NewUpstreamPool(upstreams []UpstreamConfig) *UpstreamPool {
	pool := &UpstreamPool{}
	for _, u := range upstreams {
		parsed, err := url.Parse(u.Address)
		if err != nil {
			continue
		}
		b := &Backend{
			URL:    parsed,
			Weight: u.Weight,
		}
		if b.Weight <= 0 {
			b.Weight = 1
		}
		pool.backends = append(pool.backends, b)
	}
	return pool
}

// UpstreamConfig is the config input for an upstream.
type UpstreamConfig struct {
	Address string
	Weight  int
}

// Healthy returns only healthy backends.
func (p *UpstreamPool) Healthy() []*Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()

	var healthy []*Backend
	for _, b := range p.backends {
		if b.IsHealthy() {
			healthy = append(healthy, b)
		}
	}
	return healthy
}

// All returns all backends regardless of state.
func (p *UpstreamPool) All() []*Backend {
	p.mu.RLock()
	defer p.mu.RUnlock()
	result := make([]*Backend, len(p.backends))
	copy(result, p.backends)
	return result
}

// Len returns total backend count.
func (p *UpstreamPool) Len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.backends)
}
