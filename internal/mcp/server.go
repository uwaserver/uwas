package mcp

import (
	"encoding/json"
	"fmt"

	"github.com/uwaserver/uwas/internal/cache"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
)

// Server implements the Model Context Protocol (MCP) for AI-driven management.
type Server struct {
	config  *config.Config
	logger  *logger.Logger
	metrics *metrics.Collector
	cache   *cache.Engine
	tools   map[string]Tool
}

// SetCache sets the cache engine for purge operations.
func (s *Server) SetCache(c *cache.Engine) { s.cache = c }

// Tool is an MCP tool that can be invoked.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Handler     func(input json.RawMessage) (any, error)
}

type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

func New(cfg *config.Config, log *logger.Logger, m *metrics.Collector) *Server {
	s := &Server{
		config:  cfg,
		logger:  log,
		metrics: m,
		tools:   make(map[string]Tool),
	}
	s.registerTools()
	return s
}

func (s *Server) registerTools() {
	s.tools["domain_list"] = Tool{
		Name:        "domain_list",
		Description: "List all configured domains with their type and SSL mode",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(_ json.RawMessage) (any, error) {
			type info struct {
				Host string `json:"host"`
				Type string `json:"type"`
				SSL  string `json:"ssl"`
			}
			var domains []info
			for _, d := range s.config.Domains {
				domains = append(domains, info{Host: d.Host, Type: d.Type, SSL: d.SSL.Mode})
			}
			return domains, nil
		},
	}

	s.tools["stats"] = Tool{
		Name:        "stats",
		Description: "Get server statistics including requests, cache, and connections",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(_ json.RawMessage) (any, error) {
			return map[string]any{
				"requests_total": s.metrics.RequestsTotal.Load(),
				"cache_hits":     s.metrics.CacheHits.Load(),
				"cache_misses":   s.metrics.CacheMisses.Load(),
				"active_conns":   s.metrics.ActiveConns.Load(),
				"bytes_sent":     s.metrics.BytesSent.Load(),
			}, nil
		},
	}

	s.tools["config_show"] = Tool{
		Name:        "config_show",
		Description: "Show current server configuration (sanitized, no secrets)",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(_ json.RawMessage) (any, error) {
			return map[string]any{
				"global": map[string]any{
					"worker_count":    s.config.Global.WorkerCount,
					"max_connections": s.config.Global.MaxConnections,
					"log_level":       s.config.Global.LogLevel,
				},
				"domain_count": len(s.config.Domains),
			}, nil
		},
	}

	s.tools["cache_purge"] = Tool{
		Name:        "cache_purge",
		Description: "Purge cache entries by tag or purge all",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"tag":{"type":"string","description":"Tag to purge (empty = purge all)"}}}`),
		Handler: func(input json.RawMessage) (any, error) {
			var params struct {
				Tag string `json:"tag"`
			}
			json.Unmarshal(input, &params)
			if s.cache == nil {
				return map[string]string{"status": "cache not enabled"}, nil
			}
			if params.Tag != "" {
				count := s.cache.PurgeByTag(params.Tag)
				return map[string]any{"status": "purged", "tag": params.Tag, "count": count}, nil
			}
			s.cache.PurgeAll()
			return map[string]string{"status": "all purged"}, nil
		},
	}

	s.tools["domain_get"] = Tool{
		Name:        "domain_get",
		Description: "Get detailed configuration for a specific domain",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"host":{"type":"string","description":"Domain hostname"}},"required":["host"]}`),
		Handler: func(input json.RawMessage) (any, error) {
			var params struct {
				Host string `json:"host"`
			}
			if err := json.Unmarshal(input, &params); err != nil {
				return nil, fmt.Errorf("invalid input: %w", err)
			}
			for _, d := range s.config.Domains {
				if d.Host == params.Host {
					return d, nil
				}
			}
			return nil, fmt.Errorf("domain not found: %s", params.Host)
		},
	}

	s.tools["domain_types"] = Tool{
		Name:        "domain_types",
		Description: "Count domains by type (static, php, proxy, app, redirect)",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(_ json.RawMessage) (any, error) {
			counts := map[string]int{}
			for _, d := range s.config.Domains {
				counts[d.Type]++
			}
			counts["total"] = len(s.config.Domains)
			return counts, nil
		},
	}

	s.tools["ssl_status"] = Tool{
		Name:        "ssl_status",
		Description: "Show SSL/TLS configuration status for all domains",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(_ json.RawMessage) (any, error) {
			type info struct {
				Host    string `json:"host"`
				SSLMode string `json:"ssl_mode"`
			}
			var result []info
			for _, d := range s.config.Domains {
				result = append(result, info{Host: d.Host, SSLMode: d.SSL.Mode})
			}
			return result, nil
		},
	}

	s.tools["security_overview"] = Tool{
		Name:        "security_overview",
		Description: "Show security configuration overview: WAF, rate limiting, IP ACLs across all domains",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(_ json.RawMessage) (any, error) {
			type domSec struct {
				Host      string `json:"host"`
				WAF       bool   `json:"waf"`
				RateLimit int    `json:"rate_limit_rps"`
				IPWhite   int    `json:"ip_whitelist_count"`
				IPBlack   int    `json:"ip_blacklist_count"`
			}
			var result []domSec
			for _, d := range s.config.Domains {
				ds := domSec{
					Host:      d.Host,
					WAF:       d.Security.WAF.Enabled,
					RateLimit: d.Security.RateLimit.Requests,
					IPWhite:   len(d.Security.IPWhitelist),
					IPBlack:   len(d.Security.IPBlacklist),
				}
				result = append(result, ds)
			}
			return result, nil
		},
	}

	s.tools["cache_stats"] = Tool{
		Name:        "cache_stats",
		Description: "Get cache hit/miss statistics and memory usage",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(_ json.RawMessage) (any, error) {
			hits := s.metrics.CacheHits.Load()
			misses := s.metrics.CacheMisses.Load()
			total := hits + misses
			hitRate := float64(0)
			if total > 0 {
				hitRate = float64(hits) / float64(total) * 100
			}
			return map[string]any{
				"hits":     hits,
				"misses":   misses,
				"total":    total,
				"hit_rate": fmt.Sprintf("%.1f%%", hitRate),
			}, nil
		},
	}

	s.tools["error_summary"] = Tool{
		Name:        "error_summary",
		Description: "Get request error rate summary by status code class",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(_ json.RawMessage) (any, error) {
			return map[string]any{
				"requests_total": s.metrics.RequestsTotal.Load(),
				"status_1xx":     s.metrics.RequestsByCode[0].Load(),
				"status_2xx":     s.metrics.RequestsByCode[1].Load(),
				"status_3xx":     s.metrics.RequestsByCode[2].Load(),
				"status_4xx":     s.metrics.RequestsByCode[3].Load(),
				"status_5xx":     s.metrics.RequestsByCode[4].Load(),
				"bytes_sent":     s.metrics.BytesSent.Load(),
				"active_conns":   s.metrics.ActiveConns.Load(),
			}, nil
		},
	}

	s.tools["config_summary"] = Tool{
		Name:        "config_summary",
		Description: "Show server configuration summary: listeners, features enabled, domain count",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(_ json.RawMessage) (any, error) {
			return map[string]any{
				"http_listen":     s.config.Global.HTTPListen,
				"https_listen":    s.config.Global.HTTPSListen,
				"http3":           s.config.Global.HTTP3Enabled,
				"admin_enabled":   s.config.Global.Admin.Enabled,
				"cache_enabled":   s.config.Global.Cache.Enabled,
				"backup_enabled":  s.config.Global.Backup.Enabled,
				"max_connections": s.config.Global.MaxConnections,
				"domain_count":    len(s.config.Domains),
				"log_level":       s.config.Global.LogLevel,
			}, nil
		},
	}

	s.tools["performance"] = Tool{
		Name:        "performance",
		Description: "Get performance metrics: request rate, latency percentiles, throughput",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(_ json.RawMessage) (any, error) {
			return map[string]any{
				"requests_total":   s.metrics.RequestsTotal.Load(),
				"active_conns":     s.metrics.ActiveConns.Load(),
				"bytes_sent":       s.metrics.BytesSent.Load(),
				"cache_hits":       s.metrics.CacheHits.Load(),
				"cache_misses":     s.metrics.CacheMisses.Load(),
				"slow_requests":    s.metrics.SlowRequests.Load(),
				"static_requests":  s.metrics.StaticRequests.Load(),
				"php_requests":     s.metrics.PHPRequests.Load(),
				"proxy_requests":   s.metrics.ProxyRequests.Load(),
			}, nil
		},
	}
}

// ListTools returns all available tools.
func (s *Server) ListTools() []ToolInfo {
	var tools []ToolInfo
	for _, t := range s.tools {
		tools = append(tools, ToolInfo{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}
	return tools
}

// CallTool invokes a tool by name.
func (s *Server) CallTool(name string, input json.RawMessage) (any, error) {
	tool, ok := s.tools[name]
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	return tool.Handler(input)
}
