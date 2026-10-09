package admin

import (
	"encoding/json"
	"net/http"

	"github.com/uwaserver/uwas/internal/mcp"
)

// SetMCP sets the MCP server for AI tool management endpoints.
func (s *Server) SetMCP(m *mcp.Server) { s.mcpSrv = m }

func (s *Server) handleMCPTools(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.mcpSrv == nil {
		jsonError(w, "MCP not enabled", http.StatusServiceUnavailable)
		return
	}
	jsonResponse(w, s.mcpSrv.ListTools())
}

func (s *Server) handleMCPCall(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.mcpSrv == nil {
		jsonError(w, "MCP not enabled", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	// The MCP tools read the same *config.Config the admin handlers mutate
	// under configMu, so run the tool and encode its result under the read
	// lock. The response is written after unlocking so a slow client cannot
	// hold the lock.
	s.configMu.RLock()
	result, err := s.mcpSrv.CallTool(req.Name, req.Input)
	var body []byte
	if err == nil {
		body, err = json.Marshal(result)
		if err != nil {
			s.configMu.RUnlock()
			jsonError(w, "failed to encode MCP result", http.StatusInternalServerError)
			return
		}
	}
	s.configMu.RUnlock()
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonResponse(w, json.RawMessage(body))
}
