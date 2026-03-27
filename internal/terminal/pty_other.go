//go:build !linux

package terminal

import (
	"net/http"
)

func defaultShell() string { return "" }

// ServeHTTP returns 501 on non-Linux platforms.
func (h *Handler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "terminal is only available on Linux", http.StatusNotImplemented)
}

// Ensure Handler satisfies http.Handler.
var _ http.Handler = (*Handler)(nil)
