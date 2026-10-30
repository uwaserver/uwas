package admin

import (
	"strconv"
	"unicode/utf8"
)

// Bounds for the client-controlled text kept in the access-log ring. They match
// the access log's own (F14800): generous for real values — a host name is at
// most 253 bytes, a User-Agent a few hundred — and only shorten the stored
// copy, never the request.
const (
	maxLogEntryHostBytes   = 255
	maxLogEntryMethodBytes = 32
	maxLogEntryPathBytes   = 4096
	maxLogEntryAgentBytes  = 1024
)

// clipEntryField shortens s to at most max bytes, cutting on a UTF-8 boundary
// and marking the cut so a truncated value is not mistaken for the original.
func clipEntryField(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "...[truncated " + strconv.Itoa(len(s)-cut) + " bytes]"
}

// clipped returns e with its client-controlled text bounded (F49300). The ring
// holds 1000 entries and a request may spend its whole 1 MiB header budget on
// one of these fields (a 414 for a long path is recorded with that path), so
// unclipped it pinned about a gigabyte.
func (e LogEntry) clipped() LogEntry {
	e.Host = clipEntryField(e.Host, maxLogEntryHostBytes)
	e.Method = clipEntryField(e.Method, maxLogEntryMethodBytes)
	e.Path = clipEntryField(e.Path, maxLogEntryPathBytes)
	e.UserAgent = clipEntryField(e.UserAgent, maxLogEntryAgentBytes)
	return e
}
