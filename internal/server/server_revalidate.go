package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/uwaserver/uwas/internal/cache"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/router"
)

// revalidateTimeout bounds a background refresh. It is not the client's
// deadline — nobody is waiting on this — but an origin that never answers
// must not pin a goroutine and an in-flight marker forever.
const revalidateTimeout = 30 * time.Second

// staleJob is what a background refresh needs from the request that found an
// entry stale.
//
// Snapshotted rather than holding the request: by the time the goroutine runs,
// the client's response is written, its context is cancelled and net/http is
// free to reuse the request. Copying the few fields that matter is cheaper
// than cloning the request and cannot go stale underneath us.
type staleJob struct {
	key   string
	host  string
	path  string
	query string

	// vary carries only the headers the cache key is built from. The refresh
	// has to store its result under the same key it is refreshing, and that
	// key includes Accept-Encoding plus whatever vary_by_headers names — send
	// a different set and the entry lands under a key nobody will look up.
	vary http.Header

	ttl   time.Duration
	grace time.Duration
	tags  []string
}

// scheduleRevalidate refreshes a stale entry in the background, at most one
// refresh per cache key at a time.
//
// Single-flight matters more here than it looks: a stale entry on a busy page
// is stale for every concurrent request at once, so without the guard a
// popular URL going stale sends one origin request per in-flight visitor —
// precisely the stampede the cache exists to prevent.
func (s *Server) scheduleRevalidate(domain *config.Domain, job staleJob) {
	if s.cache == nil || job.key == "" {
		return
	}
	if _, busy := s.revalidating.LoadOrStore(job.key, struct{}{}); busy {
		return
	}

	// Not logger.SafeGo: that restarts its function after a panic, which is
	// right for a supervisor and wrong for a one-shot refresh — a panicking
	// revalidation would retry forever.
	go func() {
		defer func() {
			s.revalidating.Delete(job.key)
			if rec := recover(); rec != nil {
				s.logger.Error("stale revalidation panicked",
					"host", job.host, "path", job.path, "panic", rec)
			}
		}()
		s.runRevalidate(domain, job)
	}()
}

func (s *Server) runRevalidate(domain *config.Domain, job staleJob) {
	// Parented to the server context so shutdown cancels refreshes in flight.
	ctx, cancel := context.WithTimeout(s.ctx, revalidateTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	if err != nil {
		return
	}
	req.Host = job.host
	req.URL.Path = job.path
	req.URL.RawQuery = job.query
	req.RemoteAddr = "127.0.0.1:0"
	for k, vals := range job.vary {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}
	// Deliberately no Cookie or Authorization. The entry being refreshed is
	// shared by every visitor, and IsCacheable refuses credentialed requests
	// anyway — forwarding them could only produce a per-user body to store
	// under a shared key.
	req.Header.Set("User-Agent", "UWAS-Revalidate/1.0")

	rec := httptest.NewRecorder()
	rctx := router.AcquireContext(rec, req)
	defer router.ReleaseContext(rctx)
	rctx.VHostName = domain.Host
	rctx.DocumentRoot = domain.Root

	// dispatchHandler rather than handleRequest, following FetchFragment: it
	// skips the middleware chain and the per-request bookkeeping, so a refresh
	// does not appear in the access log, the metrics, the analytics or the
	// bandwidth ledger as if a visitor had asked for it.
	s.dispatchHandler(rctx, domain)

	result := rec.Result()
	body := rec.Body.Bytes()
	if err := result.Body.Close(); err != nil {
		return
	}

	hdrs := result.Header.Clone()
	// Same reasoning as the inline store path: the cached body is canonical
	// uncompressed bytes and the compress middleware re-derives these on
	// every hit.
	hdrs.Del("Content-Encoding")
	hdrs.Del("Content-Length")

	if !cache.IsCacheable(req, result.StatusCode, hdrs) {
		// The resource stopped being cacheable — a 500, a Set-Cookie, a Vary
		// the key cannot express. Drop the stale entry rather than keep
		// serving it until grace runs out.
		s.cache.PurgeKey(job.key)
		return
	}

	s.cache.SetByKey(job.key, &cache.CachedResponse{
		StatusCode: result.StatusCode,
		Headers:    hdrs,
		Body:       body,
		Created:    time.Now(),
		TTL:        job.ttl,
		GraceTTL:   job.grace,
		Tags:       job.tags,
	})
}

// staleJobFor snapshots what a refresh of this request would need. Called on
// the request path, so it does no work beyond copying.
func (s *Server) staleJobFor(key string, r *http.Request, domain *config.Domain, ttl, grace time.Duration) staleJob {
	vary := make(http.Header, 4)
	if ae := r.Header.Get("Accept-Encoding"); ae != "" {
		vary.Set("Accept-Encoding", ae)
	}
	s.configMu.RLock()
	for _, name := range s.config.Global.Cache.VaryByHeaders {
		if v := r.Header.Get(name); v != "" {
			vary.Set(name, v)
		}
	}
	s.configMu.RUnlock()

	return staleJob{
		key:   key,
		host:  r.Host,
		path:  r.URL.Path,
		query: r.URL.RawQuery,
		vary:  vary,
		ttl:   ttl,
		grace: grace,
		tags:  cacheTagsFor(domain, r.Host),
	}
}
