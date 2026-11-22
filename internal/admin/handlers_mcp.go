package admin

import (
	"encoding/json"
	"net/http"

	"github.com/uwaserver/uwas/internal/mcp"
)

// SetMCP sets the MCP server for AI tool management endpoints; nil disables
// them. Once the API is serving, the caller must hold configMu for writing
// (the server's reload does): the handlers read it under configMu (F68400).
func (s *Server) SetMCP(m *mcp.Server) { s.mcpSrv = m }

// liveMCP returns the MCP server, or nil while MCP is disabled.
func (s *Server) liveMCP() *mcp.Server {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return s.mcpSrv
}

func (s *Server) handleMCPTools(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	srv := s.liveMCP()
	if srv == nil {
		jsonError(w, "MCP not enabled", http.StatusServiceUnavailable)
		return
	}
	jsonResponse(w, srv.ListTools())
}

func (s *Server) handleMCPCall(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.liveMCP() == nil {
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
	srv := s.mcpSrv // re-read: a reload may have disabled MCP meanwhile
	if srv == nil {
		s.configMu.RUnlock()
		jsonError(w, "MCP not enabled", http.StatusServiceUnavailable)
		return
	}
	result, err := srv.CallTool(req.Name, req.Input)
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
		s.recordAuditR(r, "mcp.call", req.Name+": "+err.Error(), false)
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.recordAuditR(r, "mcp.call", req.Name, true)
	jsonResponse(w, json.RawMessage(body))
}
