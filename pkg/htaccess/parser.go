package htaccess

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

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
		line := strings.TrimSpace(scanner.Text())

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

	return directives, scanner.Err()
}

// parseBlockOpen parses "<IfModule mod_rewrite.c>" into name and args.
func parseBlockOpen(line string) (string, []string) {
	// Strip < and >
	line = strings.TrimPrefix(line, "<")
	line = strings.TrimSuffix(line, ">")
	line = strings.TrimSpace(line)

	parts := splitArgs(line)
	if len(parts) == 0 {
		return "", nil
	}
	return parts[0], parts[1:]
}

// parseDirective parses "RewriteRule ^(.*)$ /index.php [L,QSA]" into Directive.
func parseDirective(line string, lineNum int) Directive {
	parts := splitArgs(line)
	if len(parts) == 0 {
		return Directive{LineNum: lineNum}
	}
	return Directive{
		Name:    parts[0],
		Args:    parts[1:],
		LineNum: lineNum,
	}
}

// splitArgs splits a directive line respecting quoted strings.
func splitArgs(line string) []string {
	var args []string
	var current strings.Builder
	inQuote := false
	quoteChar := byte(0)

	for i := 0; i < len(line); i++ {
		ch := line[i]

		if inQuote {
			if ch == quoteChar {
				inQuote = false
				// Include the content without quotes
			} else {
				current.WriteByte(ch)
			}
			continue
		}

		if ch == '"' || ch == '\'' {
			inQuote = true
			quoteChar = ch
			continue
		}

		if ch == ' ' || ch == '\t' {
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
			continue
		}

		current.WriteByte(ch)
	}

	if current.Len() > 0 {
		args = append(args, current.String())
	}

	return args
}
