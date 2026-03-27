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
	// ESIMaxIncludes is the maximum number of includes per page.
	ESIMaxIncludes = 50
)

var (
	esiCommentRe = regexp.MustCompile(`(?s)<!--esi\s+(.*?)\s*-->`)
	esiIncludeRe = regexp.MustCompile(`<esi:include\s+src="([^"]+)"\s*/>`)
	esiRemoveRe  = regexp.MustCompile(`(?s)<esi:remove>.*?</esi:remove>`)
)

// ESIFragmentFetcher makes internal sub-requests for ESI fragments.
type ESIFragmentFetcher interface {
	FetchFragment(host, path string, parentReq *http.Request) (body []byte, statusCode int, headers http.Header, err error)
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
	if depth >= p.maxDepth {
		return body, nil
	}

	// Strip <esi:remove> blocks (fallback content removed when ESI is active)
	body = esiRemoveRe.ReplaceAll(body, nil)

	if !bytes.Contains(body, []byte("<!--esi")) {
		return body, nil
	}

	includeCount := 0
	var lastErr error

	result := esiCommentRe.ReplaceAllFunc(body, func(match []byte) []byte {
		inner := esiCommentRe.FindSubmatch(match)
		if len(inner) < 2 {
			return match
		}

		return esiIncludeRe.ReplaceAllFunc(inner[1], func(incMatch []byte) []byte {
			if includeCount >= ESIMaxIncludes {
				return []byte("<!-- ESI: max includes exceeded -->")
			}
			includeCount++

			srcMatch := esiIncludeRe.FindSubmatch(incMatch)
			if len(srcMatch) < 2 {
				return incMatch
			}
			src := string(srcMatch[1])

			fragmentBody, err := p.fetchFragment(host, src, parentReq, tags, depth)
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
	key := esiFragmentKey(host, path)

	// Check cache first
	if cached, status := p.cache.GetByKey(key); cached != nil && (status == StatusHit || status == StatusStale) {
		body := cached.Body
		if ContainsESI(body) {
			var err error
			body, err = p.Process(body, host, parentReq, tags, depth+1)
			if err != nil {
				return nil, err
			}
		}
		return body, nil
	}

	// Cache miss: sub-request
	body, statusCode, headers, err := p.fetcher.FetchFragment(host, path, parentReq)
	if err != nil {
		return nil, err
	}
	if statusCode != http.StatusOK {
		return nil, fmt.Errorf("fragment %s returned %d", path, statusCode)
	}

	// Cache the fragment
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

	// Recursive ESI processing on fragment
	if ContainsESI(body) {
		body, err = p.Process(body, host, parentReq, tags, depth+1)
		if err != nil {
			return nil, err
		}
	}

	return body, nil
}

func esiFragmentKey(host, path string) string {
	return "esi|" + host + "|" + path
}

func parseFragmentTTL(headers http.Header) time.Duration {
	cc := headers.Get("Cache-Control")
	if cc == "" {
		return 0
	}
	for _, part := range strings.Split(cc, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "max-age=") {
			var secs int
			fmt.Sscanf(part, "max-age=%d", &secs)
			if secs > 0 {
				return time.Duration(secs) * time.Second
			}
		}
	}
	return 0
}
