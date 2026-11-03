package cache

import (
	"bufio"
	"strings"
	"testing"
)

func TestReadReplyRejectsMalformedBulkFraming(t *testing.T) {
	tests := map[string]string{
		"invalid terminator": "$3\r\nabcXX",
		"invalid nil length": "$-2\r\n",
	}
	for name, wire := range tests {
		t.Run(name, func(t *testing.T) {
			if value, err := readReply(bufio.NewReader(strings.NewReader(wire))); err == nil {
				t.Fatalf("readReply(%q) = %#v, nil; want framing error", wire, value)
			}
		})
	}
}
