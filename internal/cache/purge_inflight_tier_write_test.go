package cache

import (
	"context"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// gatedRedis is a RedisClient whose Set (and optionally Get) waits for a gate,
// so a test can hold a tier operation in flight across a purge.
type gatedRedis struct {
	mu      sync.Mutex
	data    map[string]string
	setGate chan struct{}
	setIn   chan struct{}
	setOut  chan struct{}
	getGate chan struct{}
	getIn   chan struct{}
}

func newGatedRedis() *gatedRedis {
	return &gatedRedis{data: map[string]string{}, setIn: make(chan struct{}, 4), setOut: make(chan struct{}, 4), getIn: make(chan struct{}, 4)}
}

func (r *gatedRedis) Get(_ context.Context, k string) (string, error) {
	r.mu.Lock()
	v, ok := r.data[k]
	r.mu.Unlock()
	if r.getGate != nil {
		r.getIn <- struct{}{}
		<-r.getGate
	}
	if !ok {
		return "", ErrRedisNotFound
	}
	return v, nil
}

func (r *gatedRedis) Set(_ context.Context, k, v string, _ time.Duration) error {
	r.setIn <- struct{}{}
	if r.setGate != nil {
		<-r.setGate
	}
	r.mu.Lock()
	r.data[k] = v
	r.mu.Unlock()
	r.setOut <- struct{}{}
	return nil
}

func (r *gatedRedis) Del(_ context.Context, keys ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, k := range keys {
		delete(r.data, k)
	}
	return nil
}

func (r *gatedRedis) Keys(_ context.Context, pattern string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for k := range r.data {
		if strings.HasPrefix(k, strings.TrimSuffix(pattern, "*")) {
			out = append(out, k)
		}
	}
	return out, nil
}

func (r *gatedRedis) Close() error { return nil }

// purgeParkedOrDone waits until the purge finished or a goroutine running an
// Engine purge is blocked on a lock (waiting for the in-flight write).
func purgeParkedOrDone(done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		default:
		}
		buf := make([]byte, 1<<20)
		buf = buf[:runtime.Stack(buf, true)]
		for _, g := range strings.Split(string(buf), "\n\n") {
			header, _, _ := strings.Cut(g, "\n")
			if strings.Contains(g, "cache.(*Engine).Purge") && (strings.Contains(header, "RWMutex") || strings.Contains(header, "semacquire")) {
				return
			}
		}
		runtime.Gosched()
	}
}

// TestPurgeDropsInFlightTierWrite: SetByKey writes the L3 copy from a
// goroutine. A purge that ran while that write was in flight cleared L1 and
// L3, then the write landed and the next request served the purged response
// from L3 (F67900).
func TestPurgeDropsInFlightTierWrite(t *testing.T) {
	const key = "GET|a.example|/page"
	resp := func() *CachedResponse {
		return &CachedResponse{StatusCode: 200, Headers: http.Header{"Content-Type": {"text/html"}},
			Body: []byte("OLD"), Created: time.Now(), TTL: time.Hour, Tags: []string{SiteTag("a.example")}}
	}
	for name, purge := range map[string]func(e *Engine){
		"PurgeAll":   func(e *Engine) { e.PurgeAll() },
		"PurgeByTag": func(e *Engine) { e.PurgeByTag(SiteTag("a.example")) },
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			e := NewEngine(ctx, 1<<20, "", 0, nil)
			rc := newGatedRedis()
			rc.setGate = make(chan struct{})
			e.SetRedis(&RedisCache{client: rc, prefix: "uwas"})

			e.SetByKey(key, resp())
			<-rc.setIn // L3 write in flight
			done := make(chan struct{})
			go func() { purge(e); close(done) }()
			purgeParkedOrDone(done)
			close(rc.setGate)
			<-rc.setOut
			<-done

			if got, status := e.GetByKey(key); got != nil {
				t.Fatalf("purged entry served again after an in-flight L3 write landed: status=%s body=%q", status, got.Body)
			}
		})
	}
}

// TestPurgeDropsInFlightPromotion: an entry read from L3 before a purge must
// not be promoted into L1 after it (F67900).
func TestPurgeDropsInFlightPromotion(t *testing.T) {
	const key = "GET|a.example|/page"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := NewEngine(ctx, 1<<20, "", 0, nil)
	rc := newGatedRedis()
	e.SetRedis(&RedisCache{client: rc, prefix: "uwas"})
	e.SetByKey(key, &CachedResponse{StatusCode: 200, Headers: http.Header{}, Body: []byte("OLD"),
		Created: time.Now(), TTL: time.Hour})
	<-rc.setIn
	<-rc.setOut
	e.memory.PurgeAll() // only L3 holds it

	rc.getGate = make(chan struct{})
	got := make(chan struct{})
	go func() { e.GetByKey(key); close(got) }()
	<-rc.getIn // read from L3, not yet promoted
	e.PurgeAll()
	close(rc.getGate)
	<-got
	rc.getGate = nil

	if resp, status := e.GetByKey(key); resp != nil {
		t.Fatalf("entry read before the purge was promoted after it: status=%s body=%q", status, resp.Body)
	}
}
