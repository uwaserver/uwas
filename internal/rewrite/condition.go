package rewrite

import (
	"net/http"
	"os"
	"regexp"
	"strings"
)

// Condition represents a RewriteCond (evaluated before a RewriteRule).
type Condition struct {
	Variable   string         // e.g., "%{REQUEST_URI}", "%{HTTP_HOST}"
	Pattern    *regexp.Regexp // regex pattern (nil for special tests)
	Negated    bool           // "!" prefix
	OrNext     bool           // [OR] flag — OR with next condition
	TestType   string         // "", "-f", "-d", "-l", "-s" (special file tests), "=" (lexical equality)
	RawPattern string
	NoCase     bool   // [NC] flag — case-insensitive pattern / comparison
	Literal    string // comparison string for TestType "="
}

// ParseCondition parses a RewriteCond from variable, pattern, and flags.
func ParseCondition(variable, pattern, flags string) (*Condition, error) {
	// Limit pattern length to prevent ReDoS (catastrophic backtracking)
	if len(pattern) > 1024 {
		return nil, ErrPatternTooLong
	}

	c := &Condition{
		Variable:   variable,
		RawPattern: pattern,
	}

	// Check for [OR] flag
	flags = strings.TrimSpace(flags)
	flags = strings.Trim(flags, "[]")
	for _, f := range strings.Split(flags, ",") {
		switch f = strings.TrimSpace(f); {
		case strings.EqualFold(f, "OR"):
			c.OrNext = true
		case strings.EqualFold(f, "NC"):
			c.NoCase = true
		}
	}

	// Check for negation
	if strings.HasPrefix(pattern, "!") {
		c.Negated = true
		pattern = pattern[1:]
	}

	// Check for special file tests
	switch pattern {
	case "-f":
		c.TestType = "-f"
		return c, nil
	case "-d":
		c.TestType = "-d"
		return c, nil
	case "-l":
		c.TestType = "-l"
		return c, nil
	case "-s":
		c.TestType = "-s"
		return c, nil
	}

	// "=string" is Apache's lexical equality test, not a regex; `=""`
	// compares against the empty string.
	if lit, ok := strings.CutPrefix(pattern, "="); ok {
		if lit == `""` {
			lit = ""
		}
		c.TestType = "="
		c.Literal = lit
		return c, nil
	}

	if c.NoCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	c.Pattern = re

	return c, nil
}

// Evaluate tests the condition against the given variables.
// Returns (matched, backreferences).
func (c *Condition) Evaluate(vars *Variables) (bool, []string) {
	testValue := vars.Expand(c.Variable)

	var matched bool
	var captures []string

	switch c.TestType {
	case "-f":
		info, err := os.Stat(testValue)
		matched = err == nil && info.Mode().IsRegular()
	case "-d":
		info, err := os.Stat(testValue)
		matched = err == nil && info.IsDir()
	case "-l":
		info, err := os.Lstat(testValue)
		matched = err == nil && info.Mode()&os.ModeSymlink != 0
	case "-s":
		info, err := os.Stat(testValue)
		matched = err == nil && info.Mode().IsRegular() && info.Size() > 0
	case "=":
		if c.NoCase {
			matched = strings.EqualFold(testValue, c.Literal)
		} else {
			matched = testValue == c.Literal
		}
	default:
		if c.Pattern != nil {
			matches := c.Pattern.FindStringSubmatch(testValue)
			if matches != nil {
				matched = true
				captures = matches
			}
		}
	}

	if c.Negated {
		matched = !matched
	}

	return matched, captures
}

// Variables holds server variables for rewrite condition evaluation.
type Variables struct {
	RequestURI      string
	RequestFilename string
	QueryString     string
	HTTPHost        string
	HTTPReferer     string
	HTTPUserAgent   string
	RemoteAddr      string
	RequestMethod   string
	ServerPort      string
	HTTPS           string
	DocumentRoot    string
	ServerName      string
	TheRequest      string // "GET /path HTTP/1.1"
	Proto           string // "HTTP/1.1"
	Header          http.Header
}

// Expand resolves a variable reference like %{REQUEST_URI} to its value.
// A composite TestString such as "%{DOCUMENT_ROOT}%{REQUEST_URI}" has every
// %{NAME} reference substituted, as in Apache.
func (v *Variables) Expand(s string) string {
	if !strings.Contains(s, "%{") {
		return v.lookup(s) // bare variable name
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if n := v.writeVarRef(&b, s, i); n > 0 {
			i += n - 1
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// writeVarRef writes the value of a %{NAME} reference starting at s[i] and
// returns the number of bytes consumed, or 0 if s[i:] is not a reference.
func (v *Variables) writeVarRef(b *strings.Builder, s string, i int) int {
	if !strings.HasPrefix(s[i:], "%{") {
		return 0
	}
	end := strings.IndexByte(s[i+2:], '}')
	if end < 0 {
		return 0
	}
	b.WriteString(v.lookup(s[i+2 : i+2+end]))
	return end + 3
}

// lookup returns the value of a bare server variable name.
func (v *Variables) lookup(name string) string {
	switch strings.ToUpper(name) {
	case "REQUEST_URI":
		return v.RequestURI
	case "REQUEST_FILENAME":
		return v.RequestFilename
	case "QUERY_STRING":
		return v.QueryString
	case "HTTP_HOST":
		return v.HTTPHost
	case "HTTP_REFERER":
		return v.HTTPReferer
	case "HTTP_USER_AGENT":
		return v.HTTPUserAgent
	case "REMOTE_ADDR":
		return v.RemoteAddr
	case "REQUEST_METHOD":
		return v.RequestMethod
	case "SERVER_PORT":
		return v.ServerPort
	case "HTTPS":
		return v.HTTPS
	case "DOCUMENT_ROOT":
		return v.DocumentRoot
	case "SERVER_NAME":
		return v.ServerName
	case "THE_REQUEST":
		return v.TheRequest
	case "SERVER_PROTOCOL":
		return v.Proto
	case "REQUEST_SCHEME":
		if v.HTTPS == "on" {
			return "https"
		}
		return "http"
	case "HTTP_COOKIE":
		return v.header("Cookie")
	case "HTTP_ACCEPT":
		return v.header("Accept")
	case "HTTP_FORWARDED":
		return v.header("Forwarded")
	case "HTTP_CONNECTION":
		return v.header("Connection")
	case "HTTP_PROXY_CONNECTION":
		return v.header("Proxy-Connection")
	default:
		// %{HTTP:Header-Name} reads any request header, as in Apache; an
		// unsupported variable used to be silently empty, so a deny rule
		// keyed on it never matched (F2891).
		if name, ok := strings.CutPrefix(strings.ToUpper(name), "HTTP:"); ok && name != "" {
			return v.header(name)
		}
		return ""
	}
}

// header returns the request header's values joined as Apache does.
func (v *Variables) header(name string) string {
	return strings.Join(v.Header.Values(name), ", ")
}
