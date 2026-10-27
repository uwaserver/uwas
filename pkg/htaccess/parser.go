package htaccess

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Limits for .htaccess parsing — prevent ReDoS and resource exhaustion.
const (
	// MaxDirectives is the maximum number of directives allowed in an .htaccess file.
	// Prevents ReDoS by limiting rule count.
	MaxDirectives = 200
	// MaxLineLength is the maximum length of a single line in .htaccess.
	MaxLineLength = 4096
	// MaxPatternLength is the maximum length of a regex pattern.
	MaxPatternLength = 1024
)

// ErrTooManyDirectives is returned when .htaccess has more than MaxDirectives.
var ErrTooManyDirectives = errors.New("too many directives, maximum is 200")

// Directive represents a parsed .htaccess directive.
type Directive struct {
	Name    string      // "RewriteRule", "Redirect", "Header", etc.
	Args    []string    // directive arguments
	Block   []Directive // inner directives for block types (<IfModule>, etc.)
	LineNum int
	// Quoted[i] reports whether Args[i] was written inside double quotes. Apache
	// reads a double-quoted ErrorDocument argument as message text, not a path.
	Quoted []bool
}

// Parse reads an Apache .htaccess file and returns parsed directives.
func Parse(reader io.Reader) ([]Directive, error) {
	scanner := bufio.NewScanner(reader)
	// Increase buffer size for long lines (e.g., long regex patterns)
	scanner.Buffer(make([]byte, 256), MaxLineLength)

	var directives []Directive
	var blockStack []*Directive
	lineNum := 0
	directiveCount := 0

	for scanner.Scan() {
		lineNum++
		raw := scanner.Text()
		// A UTF-8 byte order mark (Windows editors write one) must not glue
		// itself onto the first directive or section name, or that line is
		// silently dropped and with it any access rule it carried (F7300).
		if lineNum == 1 {
			raw = strings.TrimPrefix(raw, "\ufeff")
		}
		// A NUL byte means the file is not text we understand (UTF-16, binary).
		// Every directive name would be garbage and be skipped, dropping all
		// access rules; refuse the file instead, as Apache does (F7301).
		if strings.IndexByte(raw, 0) >= 0 {
			return nil, fmt.Errorf("line %d contains a NUL byte; the file is not plain text (UTF-16?)", lineNum)
		}
		line := strings.TrimSpace(raw)

		// Skip comments and empty lines
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Reject overly long lines (potential ReDoS vector)
		if len(line) > MaxLineLength {
			return nil, fmt.Errorf("line %d exceeds maximum length %d", lineNum, MaxLineLength)
		}

		// Handle line continuation (\)
		for strings.HasSuffix(line, "\\") {
			if !scanner.Scan() {
				break
			}
			lineNum++
			nextLine := scanner.Text()
			if len(line)+len(nextLine) > MaxLineLength {
				return nil, fmt.Errorf("line continuation exceeds maximum length %d", MaxLineLength)
			}
			line = line[:len(line)-1] + " " + strings.TrimSpace(nextLine)
		}

		// Enforce directive count limit
		directiveCount++
		if directiveCount > MaxDirectives {
			return nil, ErrTooManyDirectives
		}

		// Block open: <IfModule mod_rewrite.c>
		if strings.HasPrefix(line, "<") && !strings.HasPrefix(line, "</") {
			name, args := parseBlockOpen(line)
			block := &Directive{Name: name, Args: args, LineNum: lineNum}
			blockStack = append(blockStack, block)
			continue
		}

		// Block close: </IfModule>
		if strings.HasPrefix(line, "</") {
			if len(blockStack) > 0 {
				closed := blockStack[len(blockStack)-1]
				blockStack = blockStack[:len(blockStack)-1]
				if len(blockStack) > 0 {
					parent := blockStack[len(blockStack)-1]
					parent.Block = append(parent.Block, *closed)
				} else {
					directives = append(directives, *closed)
				}
			}
			continue
		}

		// Normal directive
		d := parseDirective(line, lineNum)
		if len(blockStack) > 0 {
			current := blockStack[len(blockStack)-1]
			current.Block = append(current.Block, d)
		} else {
			directives = append(directives, d)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// An unclosed block at EOF must be an error, not silently discarded:
	// deny/auth directives inside it would vanish (fail-open) where Apache
	// would refuse to start.
	if len(blockStack) > 0 {
		open := blockStack[len(blockStack)-1]
		return nil, fmt.Errorf("unclosed <%s> block opened at line %d", open.Name, open.LineNum)
	}

	return directives, nil
}

// parseBlockOpen parses "<IfModule mod_rewrite.c>" into name and args.
func parseBlockOpen(line string) (string, []string) {
	// Strip < and the closing >. Like Apache, keep only the text up to the
	// section's last '>' — anything after it (e.g. a trailing "# comment")
	// must not be glued onto the last argument.
	line = strings.TrimPrefix(line, "<")
	if i := strings.LastIndexByte(line, '>'); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)

	parts := splitArgs(line)
	if len(parts) == 0 {
		return "", nil
	}
	return parts[0], parts[1:]
}

// parseDirective parses "RewriteRule ^(.*)$ /index.php [L,QSA]" into Directive.
func parseDirective(line string, lineNum int) Directive {
	parts, quoted := splitArgsQuoted(line)
	if len(parts) == 0 {
		return Directive{LineNum: lineNum}
	}
	return Directive{
		Name:    parts[0],
		Args:    parts[1:],
		LineNum: lineNum,
		Quoted:  quoted[1:],
	}
}

// splitArgs splits a directive line respecting quoted strings.
func splitArgs(line string) []string {
	args, _ := splitArgsQuoted(line)
	return args
}

// splitArgsQuoted is splitArgs that also reports, per argument, whether any
// part of it was written inside double quotes.
func splitArgsQuoted(line string) ([]string, []bool) {
	var args []string
	var quoted []bool
	var current strings.Builder
	inQuote := false
	quoteChar := byte(0)
	argStarted := false
	argQuoted := false

	for i := 0; i < len(line); i++ {
		ch := line[i]

		if inQuote {
			// Like Apache, a backslash before the quote character or another
			// backslash inside a quoted argument is an escape: "{\"a\":1}" is
			// {"a":1}, and the escaped quote does not end the argument (F27500).
			if ch == '\\' && i+1 < len(line) && (line[i+1] == quoteChar || line[i+1] == '\\') {
				i++
				current.WriteByte(line[i])
				continue
			}
			if ch == quoteChar {
				inQuote = false
				// Include the content without quotes
			} else {
				current.WriteByte(ch)
			}
			continue
		}

		if ch == '"' || ch == '\'' {
			argStarted = true
			inQuote = true
			quoteChar = ch
			if ch == '"' {
				argQuoted = true
			}
			continue
		}

		if ch == ' ' || ch == '\t' {
			if argStarted {
				args = append(args, current.String())
				quoted = append(quoted, argQuoted)
				current.Reset()
				argStarted = false
				argQuoted = false
			}
			continue
		}

		argStarted = true
		current.WriteByte(ch)
	}

	if argStarted {
		args = append(args, current.String())
		quoted = append(quoted, argQuoted)
	}

	return args, quoted
}
