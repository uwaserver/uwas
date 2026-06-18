package htaccess

import (
	"errors"
	"strings"
	"testing"
)

// TestParseLineContinuationExceedsMax verifies that a line continuation whose
// combined length exceeds MaxLineLength is rejected. Each individual physical
// line stays within the scanner buffer (MaxLineLength), but the accumulated
// continuation overflows the limit (parser.go:64).
func TestParseLineContinuationExceedsMax(t *testing.T) {
	// First physical line: nearly the full limit, ending with a backslash so
	// the parser keeps reading. Second line pushes the total over the limit.
	first := "Header set X-Long " + strings.Repeat("a", MaxLineLength-30) + "\\"
	second := strings.Repeat("b", 100)
	if len(first) > MaxLineLength || len(second) > MaxLineLength {
		t.Fatalf("test setup error: physical lines must each be <= MaxLineLength (first=%d second=%d)", len(first), len(second))
	}
	input := first + "\n" + second + "\n"

	_, err := Parse(strings.NewReader(input))
	if err == nil {
		t.Fatal("expected error for line continuation exceeding max length")
	}
	if !strings.Contains(err.Error(), "line continuation exceeds maximum length") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestParseTooManyDirectives verifies the directive count limit (parser.go:72).
func TestParseTooManyDirectives(t *testing.T) {
	var b strings.Builder
	for i := 0; i < MaxDirectives+5; i++ {
		b.WriteString("Options +FollowSymLinks\n")
	}

	_, err := Parse(strings.NewReader(b.String()))
	if err == nil {
		t.Fatal("expected error for too many directives")
	}
	if !errors.Is(err, ErrTooManyDirectives) {
		t.Errorf("expected ErrTooManyDirectives, got: %v", err)
	}
}

// TestParseExactlyMaxDirectives verifies the boundary just below the limit
// parses without error (directiveCount == MaxDirectives is allowed).
func TestParseExactlyMaxDirectives(t *testing.T) {
	var b strings.Builder
	for i := 0; i < MaxDirectives; i++ {
		b.WriteString("Options +FollowSymLinks\n")
	}

	dirs, err := Parse(strings.NewReader(b.String()))
	if err != nil {
		t.Fatalf("unexpected error at exactly MaxDirectives: %v", err)
	}
	if len(dirs) != MaxDirectives {
		t.Errorf("expected %d directives, got %d", MaxDirectives, len(dirs))
	}
}

// TestConvertRewriteBaseNormalized verifies a RewriteBase without a trailing
// slash gets one appended (converter.go:126).
func TestConvertRewriteBaseNormalized(t *testing.T) {
	dirs, err := Parse(strings.NewReader("RewriteBase /blog\n"))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	rs := Convert(dirs)
	if rs.RewriteBase != "/blog/" {
		t.Errorf("expected RewriteBase '/blog/', got %q", rs.RewriteBase)
	}
}

// TestConvertRewriteBaseRoot verifies that a root RewriteBase ("/") is left
// untouched (the !strings.HasSuffix branch is skipped).
func TestConvertRewriteBaseRoot(t *testing.T) {
	dirs, err := Parse(strings.NewReader("RewriteBase /\n"))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	rs := Convert(dirs)
	if rs.RewriteBase != "/" {
		t.Errorf("expected RewriteBase '/', got %q", rs.RewriteBase)
	}
}

// TestConvertRewriteBaseAlreadyTrailing verifies a RewriteBase that already
// ends with a slash is left untouched.
func TestConvertRewriteBaseAlreadyTrailing(t *testing.T) {
	dirs, err := Parse(strings.NewReader("RewriteBase /app/\n"))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	rs := Convert(dirs)
	if rs.RewriteBase != "/app/" {
		t.Errorf("expected RewriteBase '/app/', got %q", rs.RewriteBase)
	}
}
