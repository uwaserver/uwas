package config

import (
	"reflect"
	"testing"
)

// MergeDomain treats compression as a unit: any patch that touches the block
// replaces the whole thing rather than merging field by field. That is the
// documented two-mode contract, and it means a caller sending a partial
// compression block deletes the fields it left out.
//
// It became load-bearing when the block started being read. Before that,
// losing `types` changed nothing; now it changes which content is compressed.
// The dashboard therefore has to round-trip every field, including ones its
// form does not let the operator edit.

func compressionPatch(min int, algorithms, types []string) Domain {
	return Domain{
		Host: "dgn.test",
		Compression: CompressionConfig{
			Enabled:    BoolPtr(true),
			MinSize:    min,
			Algorithms: algorithms,
			Types:      types,
		},
	}
}

// The behaviour a caller has to know about: omit a field, lose it.
func TestMergeCompressionReplacesTheWholeBlock(t *testing.T) {
	existing := compressionPatch(1024, []string{"br"}, []string{"text/html", "image/svg+xml"})

	// A patch that changes only min_size, with types left out.
	patch := Domain{
		Host:        "dgn.test",
		Compression: CompressionConfig{Enabled: BoolPtr(true), MinSize: 2048},
	}
	merged := MergeDomain(existing, patch, DomainPatchFields{}, false)

	if merged.Compression.MinSize != 2048 {
		t.Errorf("min_size = %d, want 2048", merged.Compression.MinSize)
	}
	if len(merged.Compression.Types) != 0 {
		t.Errorf("types = %v — this test documents that they are dropped; if the "+
			"merge now preserves them, the dashboard workaround can go", merged.Compression.Types)
	}
	if len(merged.Compression.Algorithms) != 0 {
		t.Errorf("algorithms = %v — same contract as types", merged.Compression.Algorithms)
	}
}

// What the dashboard must send: everything it loaded, every time.
func TestMergeCompressionKeepsAFullBlock(t *testing.T) {
	existing := compressionPatch(1024, []string{"br"}, []string{"text/html", "image/svg+xml"})
	patch := compressionPatch(2048, []string{"br"}, []string{"text/html", "image/svg+xml"})

	merged := MergeDomain(existing, patch, DomainPatchFields{}, false)

	if merged.Compression.MinSize != 2048 {
		t.Errorf("min_size = %d, want 2048", merged.Compression.MinSize)
	}
	want := []string{"text/html", "image/svg+xml"}
	if !reflect.DeepEqual(merged.Compression.Types, want) {
		t.Errorf("types = %v, want %v", merged.Compression.Types, want)
	}
	if !reflect.DeepEqual(merged.Compression.Algorithms, []string{"br"}) {
		t.Errorf("algorithms = %v, want [br]", merged.Compression.Algorithms)
	}
}

// A patch that does not mention compression at all must leave it alone —
// otherwise editing an unrelated field would clear the block.
func TestMergeUntouchedCompressionSurvives(t *testing.T) {
	existing := compressionPatch(1024, []string{"br"}, []string{"text/html"})
	patch := Domain{Host: "dgn.test", Root: "/srv/www/new"}

	merged := MergeDomain(existing, patch, DomainPatchFields{}, false)

	if merged.Compression.MinSize != 1024 {
		t.Errorf("min_size = %d, want 1024 — an unrelated edit cleared the block", merged.Compression.MinSize)
	}
	if !reflect.DeepEqual(merged.Compression.Types, []string{"text/html"}) {
		t.Errorf("types = %v, want [text/html]", merged.Compression.Types)
	}
}
