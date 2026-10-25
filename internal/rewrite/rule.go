package rewrite

import (
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/uwaserver/uwas/pkg/rxguard"
)

// ErrPatternTooLong is returned when a rewrite rule pattern exceeds the maximum allowed length.
var ErrPatternTooLong = errors.New("rewrite pattern exceeds maximum length of 1024 characters")

// Rule represents a URL rewrite rule (analogous to Apache's RewriteRule).
type Rule struct {
	Pattern    *regexp.Regexp
	Target     string
	Flags      Flags
	Conditions []Condition
	RawPattern string // original pattern string
	Negated    bool   // "!" prefix: the rule applies when Pattern does NOT match
	// Without, when set, vetoes a match: the rule applies only if Without does
	// not match the URI. It carries a leading PCRE negative lookahead
	// "^(?!X)REST" that RE2 cannot compile (F6100).
	Without *regexp.Regexp
}

// Flags represents parsed RewriteRule flags like [L,R=301,QSA,NC].
type Flags struct {
	Last           bool   // [L] — stop processing after match
	Redirect       int    // [R=301] — redirect with status code (0 = no redirect)
	QSAppend       bool   // [QSA] — append query string
	QSDiscard      bool   // [QSD] — discard the incoming query string
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
	// Limit pattern length to prevent ReDoS (catastrophic backtracking)
	if len(pattern) > 1024 {
		return nil, ErrPatternTooLong
	}

	flags := ParseFlags(flagStr)

	// Apache: a pattern prefixed with "!" is negated (no $N backreferences).
	expr, negated := strings.CutPrefix(pattern, "!")

	opts := ""
	if flags.NoCase {
		opts = "(?i)"
	}

	re, err := rxguard.Compile(opts + expr)
	var without *regexp.Regexp
	if err != nil {
		// A leading negative lookahead is the common PCRE idiom RE2 lacks
		// ("^(?!index\\.php$).*\\.php$"). A rule that cannot be compiled is
		// dropped, and a dropped [F] guard serves what it was blocking.
		var ok bool
		if re, without, ok = splitLeadingNegLookahead(opts, expr); !ok {
			return nil, err
		}
	}

	return &Rule{
		Pattern:    re,
		Target:     target,
		Flags:      flags,
		RawPattern: pattern,
		Negated:    negated,
		Without:    without,
	}, nil
}

// splitLeadingNegLookahead compiles "^(?!A)REST" as REST anchored at the start
// plus a veto pattern A. Only a lookahead that has no capture groups is taken,
// so $N numbering is unchanged; anything else is reported as unsupported.
func splitLeadingNegLookahead(opts, expr string) (rest, without *regexp.Regexp, ok bool) {
	const head = "^(?!"
	if !strings.HasPrefix(expr, head) {
		return nil, nil, false
	}
	depth, inClass := 1, false
	end := -1
	for i := len(head); i < len(expr) && end < 0; i++ {
		switch c := expr[i]; {
		case c == '\\':
			i++
		case inClass:
			if c == ']' {
				inClass = false
			}
		case c == '[':
			inClass = true
		case c == '(':
			if i+1 >= len(expr) || expr[i+1] != '?' {
				return nil, nil, false // capture group inside the lookahead
			}
			depth++
		case c == ')':
			if depth--; depth == 0 {
				end = i
			}
		}
	}
	if end < 0 {
		return nil, nil, false
	}
	w, err := rxguard.Compile(opts + "^(?:" + expr[len(head):end] + ")")
	if err != nil {
		return nil, nil, false
	}
	r, err := rxguard.Compile(opts + "^" + expr[end+1:])
	if err != nil {
		return nil, nil, false
	}
	return r, w, true
}

// ruleFlagNames are mod_rewrite's long flag spellings (flags are
// case-insensitive); "[forbidden]" is as good as "[F]" for Apache.
var ruleFlagNames = map[string]string{
	"chain": "C", "cookie": "CO", "discardpath": "DPI", "env": "E",
	"forbidden": "F", "gone": "G", "handler": "H", "last": "L", "next": "N",
	"nocase": "NC", "noescape": "NE", "nosubreq": "NS", "proxy": "P",
	"passthrough": "PT", "qsappend": "QSA", "qsdiscard": "QSD",
	"qslast": "QSL", "redirect": "R", "skip": "S", "type": "T",
}

// canonicalFlag rewrites a long flag name ("redirect=301") to its short form
// ("R=301"); anything else is returned unchanged. The engine only knew the
// short names, so a long-spelled [forbidden] guard forbade nothing (F7001).
func canonicalFlag(part string, names map[string]string) string {
	name, val, hasVal := strings.Cut(part, "=")
	short, ok := names[strings.ToLower(name)]
	if !ok {
		return part
	}
	if hasVal {
		return short + "=" + val
	}
	return short
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
		part = canonicalFlag(strings.TrimSpace(part), ruleFlagNames)
		upper := strings.ToUpper(part)

		switch {
		case upper == "L", upper == "END":
			// [END] also ends the pass and, unlike [L], stops re-processing;
			// Process already returns on Last without a further pass.
			f.Last = true
		case upper == "QSA":
			f.QSAppend = true
		case upper == "QSD":
			f.QSDiscard = true
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
			code, err := strconv.Atoi(part[2:])
			if err != nil {
				// Apache's symbolic statuses (F6402).
				switch strings.ToLower(part[2:]) {
				case "permanent":
					code = 301
				case "seeother":
					code = 303
				}
			}
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
			var err error
			f.Skip, err = strconv.Atoi(part[2:])
			if err != nil {
				f.Skip = 0
			}
		case strings.HasPrefix(upper, "E="):
			f.EnvironmentVar = part[2:]
		}
	}
	return f
}

// redirectStatus returns code when it is a redirect status, 302 otherwise.
// http.Redirect writes whatever it is given, so a 200 or 404 became a
// "redirect" without redirecting and a code below 100 panics the handler.
func redirectStatus(code int) int {
	switch code {
	case 301, 302, 303, 307, 308:
		return code
	}
	return 302
}

// matchesURI reports whether the rule's pattern applies to uri, without
// capturing. Match and Engine.MightMatch both decide through it: MightMatch
// once read only Pattern and Negated, so a negated rule whose pattern also
// carries a Without veto was said not to match where Match said it did, and the
// server then skipped Process, [F] guard and all (F16600).
func (r *Rule) matchesURI(uri string) bool {
	hit := r.Pattern.MatchString(uri) && (r.Without == nil || !r.Without.MatchString(uri))
	return hit != r.Negated
}

// Match tests if the rule matches the given URI and returns backreferences.
func (r *Rule) Match(uri string) (bool, []string) {
	if r.Negated {
		return r.matchesURI(uri), nil
	}
	matches := r.Pattern.FindStringSubmatch(uri)
	if matches == nil || (r.Without != nil && r.Without.MatchString(uri)) {
		return false, nil
	}
	return true, matches
}

// Apply substitutes backreferences ($1.. for the rule, %1.. for conditions) in
// the target string. It does a single left-to-right pass so that a value
// captured by one group can never be re-interpreted as another backreference
// (e.g. a request path captured into $2 that itself contains the text "$1").
// Backreferences are single-digit, matching Apache mod_rewrite.
func (r *Rule) Apply(target string, ruleMatches, condMatches []string) string {
	return r.apply(target, ruleMatches, condMatches, nil)
}

// apply is Apply plus %{NAME} server-variable expansion when vars is non-nil,
// done in the same single pass so captured text is never re-expanded.
func (r *Rule) apply(target string, ruleMatches, condMatches []string, vars *Variables) string {
	out, _ := r.applyLimited(target, ruleMatches, condMatches, vars, 0)
	return out
}

// applyLimited is apply with a bound on the length of the result (0 means
// none). The second result is false, and the output unusable, once the
// substitution would exceed it: a target such as "$1$1$1..." multiplies the
// URI, and rewriting it again on the next pass multiplies it again (F14500).
// The bound is checked after every piece is written, so no more than one
// variable or capture is held beyond it.
func (r *Rule) applyLimited(target string, ruleMatches, condMatches []string, vars *Variables, limit int) (string, bool) {
	var b strings.Builder
	b.Grow(len(target))
	for i := 0; i < len(target); i++ {
		if limit > 0 && b.Len() > limit {
			return "", false
		}
		c := target[i]
		if vars != nil {
			if n := vars.writeVarRef(&b, target, i); n > 0 {
				i += n - 1
				continue
			}
		}
		if (c == '$' || c == '%') && i+1 < len(target) && target[i+1] >= '0' && target[i+1] <= '9' {
			idx := int(target[i+1] - '0')
			matches := ruleMatches
			if c == '%' {
				matches = condMatches
			}
			if idx < len(matches) {
				b.WriteString(matches[idx])
			}
			i++ // consume the digit (an out-of-range backref expands to empty)
			continue
		}
		b.WriteByte(c)
	}
	if limit > 0 && b.Len() > limit {
		return "", false
	}
	return b.String(), true
}
