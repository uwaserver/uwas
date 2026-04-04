package bench

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uwaserver/uwas/internal/cache"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/handler/static"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/middleware"
	"github.com/uwaserver/uwas/internal/router"
)

// BenchmarkStaticFileServe measures raw static file handler throughput.
func BenchmarkStaticFileServe(b *testing.B) {
	dir := b.TempDir()
	content := make([]byte, 4096)
	for i := range content {
		content[i] = 'A'
	}
	os.WriteFile(filepath.Join(dir, "bench.html"), content, 0644)

	h := static.New()
	domain := &config.Domain{Host: "bench", Root: dir, Type: "static"}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/bench.html", nil)
			ctx := router.AcquireContext(rec, req)
			static.ResolveRequest(ctx, domain)
			h.Serve(ctx)
			router.ReleaseContext(ctx)
		}
	})
}

// BenchmarkVHostLookup measures vhost routing speed.
func BenchmarkVHostLookup(b *testing.B) {
	domains := make([]config.Domain, 100)
	for i := range domains {
		domains[i] = config.Domain{Host: fmt.Sprintf("site%d.example.com", i), Root: "/tmp"}
	}
	r := router.NewVHostRouter(domains)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			r.Lookup(fmt.Sprintf("site%d.example.com", i%100))
			i++
		}
	})
}

// BenchmarkMiddlewareChain measures overhead of the full middleware chain.
func BenchmarkMiddlewareChain(b *testing.B) {
	log := logger.New("error", "text")
	chain := middleware.Chain(
		middleware.Recovery(log),
		middleware.RequestID(),
		middleware.SecurityHeaders(),
	)

	handler := chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/", nil)
			handler.ServeHTTP(rec, req)
		}
	})
}

// BenchmarkCacheGet measures cache lookup speed.
func BenchmarkCacheGet(b *testing.B) {
	mc := cache.NewMemoryCache(256 << 20) // 256MB

	// Pre-populate 1000 entries
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key-%04d", i)
		mc.Set(key, &cache.CachedResponse{
			StatusCode: 200,
			Body:       make([]byte, 512),
			Created:    time.Now(),
			TTL:        5 * time.Minute,
		})
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key-%04d", i%1000)
			mc.Get(key)
			i++
		}
	})
}

// BenchmarkCacheSet measures cache write speed.
func BenchmarkCacheSet(b *testing.B) {
	mc := cache.NewMemoryCache(256 << 20)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("bench-set-%d", i)
			mc.Set(key, &cache.CachedResponse{
				StatusCode: 200,
				Body:       make([]byte, 256),
				Created:    time.Now(),
				TTL:        5 * time.Minute,
			})
			i++
		}
	})
}

// BenchmarkCacheKeyGenerate measures cache key generation speed.
func BenchmarkCacheKeyGenerate(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest("GET", "/page?a=1&b=2&c=3", nil)
			req.Header.Set("Accept-Encoding", "gzip")
			cache.GenerateKey(req, []string{"Accept-Encoding"})
		}
	})
}

// BenchmarkContextAcquireRelease measures context pool acquisition speed.
func BenchmarkContextAcquireRelease(b *testing.B) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test?a=1", nil)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ctx := router.AcquireContext(rec, req)
			router.ReleaseContext(ctx)
		}
	})
}
