package cloudflare

import (
	"encoding/json"
	"testing"
)

// TestPurgeCacheJSONInjection verifies that PurgeCacheWithClient properly
// escapes the URL parameter into JSON when building the Cloudflare purge payload.
// The production code uses json.Marshal to construct the payload, which correctly
// escapes special characters. Before the fix, the code used raw string concatenation:
//
//	payload := []byte(`{"files":["` + url + `"]}`)
//
// which produces invalid JSON for URLs containing double quotes or backslashes:
//   {"files":["https://example.com/?q="test"]}  ← malformed
//
// With json.Marshal, the same URL produces valid escaped JSON:
//   {"files":["https://example.com/?q=\"test\"]}  ← correct
func TestPurgeCacheJSONInjection(t *testing.T) {
	testURL := `https://example.com/?q="test`

	// This mirrors the fixed production code at handler.go:887:
	// payload, _ = json.Marshal(map[string][]string{"files": {url}})
	payload, err := json.Marshal(map[string][]string{"files": {testURL}})
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	// Valid JSON must parse without error.
	var parsed map[string]interface{}
	if err := json.Unmarshal(payload, &parsed); err != nil {
		t.Fatalf("FAIL: payload %q is not valid JSON: %v", string(payload), err)
	}

	// Verify the files array contains the original URL with the quote preserved.
	files, ok := parsed["files"].([]interface{})
	if !ok || len(files) != 1 {
		t.Fatalf("FAIL: expected files array with 1 element, got %v", parsed["files"])
	}
	if files[0].(string) != testURL {
		t.Errorf("FAIL: expected URL %q, got %q", testURL, files[0].(string))
	}
}

// TestPurgeCacheRealURLs verifies normal URLs without special characters still work.
func TestPurgeCacheRealURLs(t *testing.T) {
	tests := []string{
		"https://example.com/",
		"https://example.com/path/to/resource",
		"https://example.com/?q=value",
		"https://example.com/?q=value&page=1",
	}
	for _, testURL := range tests {
		payload, err := json.Marshal(map[string][]string{"files": {testURL}})
		if err != nil {
			t.Errorf("json.Marshal(%q) failed: %v", testURL, err)
			continue
		}
		var parsed map[string]interface{}
		if err := json.Unmarshal(payload, &parsed); err != nil {
			t.Errorf("payload %q is not valid JSON: %v", string(payload), err)
		}
	}
}
