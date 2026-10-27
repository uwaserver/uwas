package mcp

import (
	"encoding/json"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/metrics"
)

// F33101: list tools built their result with `var x []T`, so a server with no
// domains answered `null` instead of `[]`. MCP clients iterate the result; null
// breaks them while an empty array does not.
func TestListToolsReturnEmptyArrayWithoutDomains(t *testing.T) {
	for _, tool := range []string{"domain_list", "ssl_status", "security_overview"} {
		t.Run(tool+"/empty", func(t *testing.T) {
			s := New(&config.Config{}, logger.New("error", "text"), metrics.New())
			out, err := s.CallTool(tool, json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(out)
			t.Logf("EXPECTED: []\nACTUAL: %s", b)
			if string(b) != "[]" {
				t.Fatalf("PROBLEM CONFIRMED: %s returned %s, want []", tool, b)
			}
		})
		t.Run(tool+"/one domain (control)", func(t *testing.T) {
			s := New(&config.Config{Domains: []config.Domain{{Host: "a.test", Type: "static"}}}, logger.New("error", "text"), metrics.New())
			out, err := s.CallTool(tool, json.RawMessage(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(out)
			if len(b) < 3 || b[0] != '[' || string(b) == "[]" {
				t.Fatalf("one domain returned %s", b)
			}
		})
	}
}
