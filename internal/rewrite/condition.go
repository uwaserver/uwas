package rewrite

import (
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
	TestType   string         // "", "-f", "-d", "-l", "-s" (special file tests)
	RawPattern string
}

// ParseCondition parses a RewriteCond from variable, pattern, and flags.
func ParseCondition(variable, pattern, flags string) (*Condition, error) {
	c := &Condition{
		Variable:   variable,
		RawPattern: pattern,
	}

	// Check for [OR] flag
	flags = strings.TrimSpace(flags)
	flags = strings.Trim(flags, "[]")
	for _, f := range strings.Split(flags, ",") {
		if strings.EqualFold(strings.TrimSpace(f), "OR") {
			c.OrNext = true
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
		matched = err == nil && !info.IsDir()
	case "-d":
		info, err := os.Stat(testValue)
		matched = err == nil && info.IsDir()
	case "-l":
		info, err := os.Lstat(testValue)
		matched = err == nil && info.Mode()&os.ModeSymlink != 0
	case "-s":
		info, err := os.Stat(testValue)
		matched = err == nil && info.Size() > 0
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
}

// Expand resolves a variable reference like %{REQUEST_URI} to its value.
func (v *Variables) Expand(s string) string {
	// Strip %{ and } if present
	name := s
	if strings.HasPrefix(s, "%{") && strings.HasSuffix(s, "}") {
		name = s[2 : len(s)-1]
	}

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
	default:
		return ""
	}
}
