// Package admin provides the admin API server for uwas.
package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
)

// jsonEncode writes v as JSON to w, logging on write failure so truncated
// responses never silently corrupt client state.
//
// Use this instead of json.NewEncoder(w).Encode(v) in all HTTP response
// handlers to ensure connection-drop errors are surfaced to operators.
func jsonEncode(w http.ResponseWriter, v any) {
	if err := json.NewEncoder(w).Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "[WARN] admin: JSON write failed (client disconnect?): %v\n", err)
	}
}
