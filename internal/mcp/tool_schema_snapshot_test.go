package mcp

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestToolListOwnsSchemaBytes(t *testing.T) {
	original := json.RawMessage(`{"type":"object"}`)
	s := &Server{tools: map[string]Tool{"x": {Name: "x", InputSchema: original}, "nil": {Name: "nil"}, "empty": {Name: "empty", InputSchema: json.RawMessage{}}}}
	first, second := s.ListTools(), s.ListTools()
	if len(first) != 3 || first[0].Name != "empty" || first[1].Name != "nil" || first[2].Name != "x" {
		t.Fatal("order/control", first)
	}
	release, done := make(chan struct{}), make(chan struct{})
	go func() { <-release; first[2].InputSchema[0] = '!'; first[2].Name = "changed"; close(done) }()
	close(release)
	<-done
	third := s.ListTools()
	for _, snapshot := range [][]ToolInfo{second, third} {
		if !bytes.Equal(snapshot[2].InputSchema, original) || !json.Valid(snapshot[2].InputSchema) || snapshot[2].Name != "x" {
			t.Fatal("schema aliases caller", snapshot)
		}
		if snapshot[0].InputSchema == nil || snapshot[1].InputSchema != nil {
			t.Fatal("nil/empty changed")
		}
	}
	empty := (&Server{}).ListTools()
	if len(empty) != 0 {
		t.Fatal("unexpected tools")
	}
}
