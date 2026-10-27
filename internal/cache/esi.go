package cache

import (
	"bytes"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/uwaserver/uwas/internal/logger"
)

const (
	// ESIMaxDepthDefault is the maximum nesting depth for ESI includes.
	ESIMaxDepthDefault = 3
	// ESIMaxIncludes is the maximum number of includes per page. It counts the
	// whole page: includes inside fragments draw on the same allowance. Counted
	// per body instead, 50 includes of a fragment with 50 includes of a fragment
	// with 50 includes expanded to 125,000 leaf copies (F14404).
	ESIMaxIncludes = 50
	// ESIMaxOutputBytes bounds the fragment bytes one page may pull in, so a
	// few includes of large fragments cannot assemble a response of hundreds
	// of MiB in memory (F14404).
	ESIMaxOutputBytes = 8 << 20
)

// errESIOutputLimit is returned for a fragment that would take a page past
// ESIMaxOutputBytes.
var errESIOutputLimit = fmt.Errorf("ESI: output limit of %d bytes reached", ESIMaxOutputBytes)

// esiBudget is what is left of the allowance of one page; it is shared by every
// level of its includes.
type esiBudget struct {
	includes int
	bytes    int
}

var (
	esiCommentRe = regexp.MustCompile(`(?s)<!--esi\s+(.*?)\s*-->`)
	esiIncludeRe = regexp.MustCompile(`<esi:include\s+src="([^"]+)"\s*/>`)
	esiRemoveRe  = regexp.MustCompile(`(?s)<esi:remove>.*?</esi:remove>`)
)

// ESIFragmentFetcher makes internal sub-requests for ESI fragments.
type ESIFragmentFetcher interface {
	FetchFragment(host, path string, parentReq *http.Request) (body []byte, statusCode int, headers http.Header, err error)
}

// ESIFragmentAuthorizer is implemented by a fetcher that can say, without
// rendering the fragment, whether the parent's client may read it. A cached
// fragment is replayed only when it says yes: the cache is filled by whoever
// the access rules allowed and must not answer a client they deny (F17402).
type ESIFragmentAuthorizer interface {
	FragmentAllowed(host, path string, parentReq *http.Request) bool
}

// ESIProcessor handles ESI tag scanning, fragment fetching, and assembly.
type ESIProcessor struct {
	cache    *Engine
	fetcher  ESIFragmentFetcher
	logger   *logger.Logger
	maxDepth int
}

// NewESIProcessor creates an ESI processor.
func NewESIProcessor(cache *Engine, fetcher ESIFragmentFetcher, log *logger.Logger, maxDepth int) *ESIProcessor {
	if maxDepth <= 0 {
		maxDepth = ESIMaxDepthDefault
	}
	return &ESIProcessor{
		cache:    cache,
		fetcher:  fetcher,
		logger:   log,
		maxDepth: maxDepth,
	}
}

// ContainsESI checks if body contains ESI markers.
func ContainsESI(body []byte) bool {
	return bytes.Contains(body, []byte("<!--esi"))
}

// Process scans body for ESI tags, fetches fragments, and returns assembled output.
func (p *ESIProcessor) Process(body []byte, host string, parentReq *http.Request, tags []string, depth int) ([]byte, error) {
	return p.process(body, host, parentReq, tags, depth, &esiBudget{includes: ESIMaxIncludes, bytes: ESIMaxOutputBytes})
}

func (p *ESIProcessor) process(body []byte, host string, parentReq *http.Request, tags []string, depth int, budget *esiBudget) ([]byte, error) {
	if depth >= p.maxDepth {
		return body, nil
	}

	// Strip <esi:remove> blocks (fallback content removed when ESI is active)
	body = esiRemoveRe.ReplaceAll(body, nil)

	if !bytes.Contains(body, []byte("<!--esi")) {
		return body, nil
	}

	var lastErr error

	result := esiCommentRe.ReplaceAllFunc(body, func(match []byte) []byte {
		inner := esiCommentRe.FindSubmatch(match)
		if len(inner) < 2 {
			return match
		}

		return esiIncludeRe.ReplaceAllFunc(inner[1], func(incMatch []byte) []byte {
			if budget.includes <= 0 {
				return []byte("<!-- ESI: max includes exceeded -->")
			}
			budget.includes--

			srcMatch := esiIncludeRe.FindSubmatch(incMatch)
			if len(srcMatch) < 2 {
				return incMatch
			}
			src := string(srcMatch[1])

			fragmentBody, err := p.fetchFragmentWithin(host, src, parentReq, tags, depth, budget)
			if err != nil {
				lastErr = err
				if p.logger != nil {
					p.logger.Warn("ESI fragment fetch failed", "host", host, "src", src, "error", err)
				}
				return []byte(fmt.Sprintf("<!-- ESI error: %s -->", src))
			}
			return fragmentBody
		})
	})

	_ = lastErr // errors are logged inline, assembled result is best-effort
	return result, nil
}

func (p *ESIProcessor) fetchFragment(host, path string, parentReq *http.Request, tags []string, depth int) ([]byte, error) {
	return p.fetchFragmentWithin(host, path, parentReq, tags, depth, &esiBudget{includes: ESIMaxIncludes, bytes: ESIMaxOutputBytes})
}

func (p *ESIProcessor) fetchFragmentWithin(host, path string, parentReq *http.Request, tags []string, depth int, budget *esiBudget) ([]byte, error) {
	key := esiFragmentKey(host, path)

	// The sub-request forwards the parent's Cookie header, so a fragment
	// rendered for a cookie-carrying request may contain per-user content.
	// The fragment cache key is cookie-blind — such fragments must neither
	// be served from nor stored into the shared cache.
	shareable := parentReq == nil || parentReq.Header.Get("Cookie") == ""

	// Check cache first
	if shareable && p.fragmentAllowed(host, path, parentReq) {
		if cached, status := p.cache.GetByKey(key); cached != nil && (status == StatusHit || status == StatusStale) {
			body := cached.Body
			if len(body) > budget.bytes {
				return nil, errESIOutputLimit
			}
			budget.bytes -= len(body)
			if ContainsESI(body) {
				var err error
				body, err = p.process(body, host, parentReq, tags, depth+1, budget)
				if err != nil {
					return nil, err
				}
			}
			return body, nil
		}
	}

	// Cache miss: sub-request
	body, statusCode, headers, err := p.fetcher.FetchFragment(host, path, parentReq)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("fragment %s returned %d", path, statusCode)
	}
	if len(body) > budget.bytes {
		return nil, errESIOutputLimit
	}
	budget.bytes -= len(body)

	// Cache the fragment (only when rendered without user cookies, and only if
	// the origin allows sharing it: no-store/private/Set-Cookie are not
	// overridden by a default TTL, F3401).
	if shareable && sharedCacheableHeaders(headers) {
		ttl := parseFragmentTTL(headers)
		if ttl <= 0 {
			ttl = 60 * time.Second
		}
		p.cache.SetByKey(key, &CachedResponse{
			StatusCode: statusCode,
			Headers:    headers,
			Body:       body,
			Created:    time.Now(),
			TTL:        ttl,
			Tags:       tags,
		})
	}

	// Recursive ESI processing on fragment
	if ContainsESI(body) {
		body, err = p.process(body, host, parentReq, tags, depth+1, budget)
		if err != nil {
			return nil, err
		}
	}

	return body, nil
}

// fragmentAllowed reports whether a cached copy of the fragment may be replayed
// to parentReq's client; a fetcher without an opinion allows it.
func (p *ESIProcessor) fragmentAllowed(host, path string, parentReq *http.Request) bool {
	if a, ok := p.fetcher.(ESIFragmentAuthorizer); ok && parentReq != nil {
		return a.FragmentAllowed(host, path, parentReq)
	}
	return true
}

func esiFragmentKey(host, path string) string {
	return "esi|" + host + "|" + path
}

func parseFragmentTTL(headers http.Header) time.Duration {
	// Same reading as the page cache: lines joined, s-maxage before max-age.
	if origin, ok := originMaxAge(strings.Join(headers.Values("Cache-Control"), ",")); ok && origin > 0 {
		return origin
	}
	return 0
}
