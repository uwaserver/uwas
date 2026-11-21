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
//
// The handler is built once per Server: its concurrent-session cap (F15202)
// counts sessions per handler, and a fresh one per request counted every
// session against an empty counter, so the cap never applied (F67502).
func (s *Server) terminalHandler() http.Handler {
	s.terminalOnce.Do(func() { s.terminal = terminal.New(s.logger) })
	return s.terminal
}
