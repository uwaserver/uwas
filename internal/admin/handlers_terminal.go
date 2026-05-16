package admin

import (
	"net/http"

	"github.com/uwaserver/uwas/internal/terminal"
)

// terminalHandler returns the WebSocket → PTY bridge used by the
// browser terminal. Lives in its own file so the apps refactor that
// removed the legacy domain-keyed app handlers didn't take the
// terminal with it — they shared a source file historically but are
// unrelated subsystems.
func (s *Server) terminalHandler() http.Handler {
	return terminal.New(s.logger)
}
