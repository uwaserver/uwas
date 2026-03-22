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
