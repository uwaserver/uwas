package rewrite

import (
	"regexp"
	"strconv"
	"strings"
)

// Rule represents a URL rewrite rule (analogous to Apache's RewriteRule).
type Rule struct {
	Pattern    *regexp.Regexp
	Target     string
	Flags      Flags
	Conditions []Condition
	RawPattern string // original pattern string
}

// Flags represents parsed RewriteRule flags like [L,R=301,QSA,NC].
type Flags struct {
	Last           bool   // [L] — stop processing after match
	Redirect       int    // [R=301] — redirect with status code (0 = no redirect)
	QSAppend       bool   // [QSA] — append query string
	NoCase         bool   // [NC] — case-insensitive match
	Forbidden      bool   // [F] — return 403
	Gone           bool   // [G] — return 410
	Chain          bool   // [C] — chain with next rule
	Skip           int    // [S=N] — skip N rules
	PassThrough    bool   // [PT] — pass through to next handler
	NoEscape       bool   // [NE] — don't escape special chars
	EnvironmentVar string // [E=VAR:VAL]
}

// ParseRule parses a rewrite rule from pattern, target, and optional flags string.
func ParseRule(pattern, target, flagStr string) (*Rule, error) {
	flags := ParseFlags(flagStr)

	opts := ""
	if flags.NoCase {
		opts = "(?i)"
	}

	re, err := regexp.Compile(opts + pattern)
	if err != nil {
		return nil, err
	}

	return &Rule{
		Pattern:    re,
		Target:     target,
		Flags:      flags,
		RawPattern: pattern,
	}, nil
}

// ParseFlags parses a flag string like "L,R=301,QSA,NC" into Flags.
func ParseFlags(s string) Flags {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "[]")
	if s == "" {
		return Flags{}
	}

	var f Flags
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		upper := strings.ToUpper(part)

		switch {
		case upper == "L":
			f.Last = true
		case upper == "QSA":
			f.QSAppend = true
		case upper == "NC":
			f.NoCase = true
		case upper == "F":
			f.Forbidden = true
		case upper == "G":
			f.Gone = true
		case upper == "C":
			f.Chain = true
		case upper == "PT":
			f.PassThrough = true
		case upper == "NE":
			f.NoEscape = true
		case strings.HasPrefix(upper, "R="):
			code, _ := strconv.Atoi(part[2:])
			switch code {
			case 301, 302, 303, 307, 308:
				// valid redirect codes
			default:
				code = 302
			}
			f.Redirect = code
		case upper == "R":
			f.Redirect = 302
		case strings.HasPrefix(upper, "S="):
			f.Skip, _ = strconv.Atoi(part[2:])
		case strings.HasPrefix(upper, "E="):
			f.EnvironmentVar = part[2:]
		}
	}
	return f
}

// Match tests if the rule matches the given URI and returns backreferences.
func (r *Rule) Match(uri string) (bool, []string) {
	matches := r.Pattern.FindStringSubmatch(uri)
	if matches == nil {
		return false, nil
	}
	return true, matches
}

// Apply substitutes backreferences ($1, $2, ...) in the target string.
func (r *Rule) Apply(target string, ruleMatches, condMatches []string) string {
	result := target

	// Rule backreferences: $1, $2, ...
	for i := len(ruleMatches) - 1; i >= 0; i-- {
		placeholder := "$" + strconv.Itoa(i)
		result = strings.ReplaceAll(result, placeholder, ruleMatches[i])
	}

	// Condition backreferences: %1, %2, ...
	for i := len(condMatches) - 1; i >= 0; i-- {
		placeholder := "%" + strconv.Itoa(i)
		result = strings.ReplaceAll(result, placeholder, condMatches[i])
	}

	return result
}
