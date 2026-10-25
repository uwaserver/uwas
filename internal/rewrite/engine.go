package rewrite

import (
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

const maxRewrites = 10 // loop detection

// rewriteTimeout is the wall-clock time limit for rewrite processing.
// Prevents ReDoS when user-controlled .htaccess contains malicious regexes.
const rewriteTimeout = 50 * time.Millisecond

// maxRewriteString bounds every string rewrite processing works on: the URI
// a regex is matched against, a condition's test value, and the URI a
// substitution builds. Apache and nginx refuse request lines and header fields
// beyond ~8 KiB, so no well-formed request is affected. Without it a pattern
// near the program-size limit costs O(program x input) per match, and a
// target such as "$1$1$1..." multiplies the URI on every pass (F14500,
// F14502).
const maxRewriteString = 16 << 10

// rewriteNow is the clock the rewrite budget reads (a seam for tests).
var rewriteNow = time.Now

// budget is the wall-clock allowance for one Process or MightMatch call.
type budget struct{ deadline time.Time }

func newBudget() budget { return budget{deadline: rewriteNow().Add(rewriteTimeout)} }

func (b budget) expired() bool { return rewriteNow().After(b.deadline) }

// Result is the outcome of rewrite processing.
type Result struct {
	URI        string // rewritten URI
	Query      string // rewritten query string
	Redirect   bool   // should redirect?
	StatusCode int    // redirect status code
	Forbidden  bool   // 403
	Gone       bool   // 410
	Modified   bool   // was URI actually changed?
}

// Engine processes URL rewrite rules.
type Engine struct {
	rules []*Rule
}

// NewEngine creates a rewrite engine with the given rules.
func NewEngine(rules []*Rule) *Engine {
	return &Engine{rules: rules}
}

// MightMatch is a cheap pre-check: returns true if at least one rule's
// pattern regex matches the URI. Callers use it to skip the Variables
// allocation + Process() entirely for domains that have rewrites
// configured but whose request paths never match.
//
// False positives (returning true when the rule would not actually fire
// after condition evaluation) are acceptable — they just mean we fall
// through to the full Process() like before. False negatives must not
// happen, so this only inspects rule.Pattern, never conditions.
//
// Refs: refactor.md P12.
// deadline protects MightMatch against ReDoS — same guard Process() uses.
func (e *Engine) MightMatch(uri string) bool {
	if len(e.rules) == 0 {
		return false
	}
	if len(uri) > maxRewriteString {
		return true // too long to pre-check: let Process decide
	}
	b := newBudget()
	for _, rule := range e.rules {
		if b.expired() {
			// "No match" here skips Process entirely, and with it every
			// rule not yet looked at, [F] guards included. Out of time is
			// "cannot rule a match out" (F14501).
			return true
		}
		if rule.Pattern != nil && rule.matchesURI(uri) {
			return true
		}
	}
	return false
}

// Process evaluates all rules against the request URI.
// Uses a wall-clock timeout to prevent ReDoS from malicious regexes.
func (e *Engine) Process(uri, queryString string, vars *Variables) *Result {
	result := &Result{
		URI:   uri,
		Query: queryString,
	}

	if len(uri) > maxRewriteString {
		return e.overload(result)
	}
	b := newBudget()
	for iteration := 0; iteration < maxRewrites; iteration++ {
		if b.expired() {
			return e.overload(result)
		}
		changed := false

		for i := 0; i < len(e.rules); i++ {
			// The budget is read before every rule, not only between
			// passes: one pass costs rules x one match (F14502).
			if b.expired() {
				return e.overload(result)
			}
			rule := e.rules[i]

			// Evaluate conditions
			condMatch, condCaptures, ok := e.evalConditionsBudget(rule.Conditions, vars, b)
			if !ok {
				return e.overload(result)
			}
			if !condMatch {
				// If this is a chain rule and didn't match, skip until non-chain
				if rule.Flags.Chain {
					i = e.skipChain(i)
				}
				continue
			}

			// Match rule pattern against current URI
			matched, ruleCaptures := rule.Match(result.URI)
			if !matched {
				if rule.Flags.Chain {
					i = e.skipChain(i)
				}
				continue
			}

			// Apply substitution
			newURI, ok := rule.applyLimited(rule.Target, ruleCaptures, condCaptures, vars, maxRewriteString)
			if !ok {
				return e.overload(result) // substitution grew past the bound
			}

			// Handle special targets
			if newURI == "-" {
				// "-" means no substitution, just apply flags
			} else {
				// Handle query string
				if qIdx := strings.Index(newURI, "?"); qIdx != -1 {
					if rule.Flags.QSAppend && !rule.Flags.QSDiscard && result.Query != "" {
						result.Query = newURI[qIdx+1:] + "&" + result.Query
					} else {
						result.Query = newURI[qIdx+1:]
					}
					newURI = newURI[:qIdx]
				} else if rule.Flags.QSDiscard {
					// A substitution without its own query drops the incoming
					// one under [QSD]; otherwise the original is kept (F6701).
					result.Query = ""
				}

				if len(result.Query) > maxRewriteString {
					return e.overload(result)
				}
				result.URI = newURI
				changed = true
				result.Modified = true

				// Update variables for subsequent rules
				vars.RequestURI = newURI
				vars.QueryString = result.Query
			}

			// Handle flags
			if rule.Flags.Forbidden {
				result.Forbidden = true
				return result
			}
			if rule.Flags.Gone {
				result.Gone = true
				return result
			}
			if rule.Flags.Redirect > 0 {
				result.Redirect = true
				result.StatusCode = rule.Flags.Redirect
				result.URI = sameHostRedirectPath(result.URI)
				if result.Query != "" {
					result.URI = result.URI + "?" + result.Query
				}
				return result
			}
			if rule.Flags.Skip > 0 {
				i += min(rule.Flags.Skip, len(e.rules)-i-1)
			}
			if rule.Flags.Last {
				return result
			}
		}

		// If no rule changed the URI this iteration, we're done
		if !changed {
			break
		}
	}

	return result
}

// overload ends processing once the work allowance is spent: the wall-clock
// budget ran out, or a string was too long to match or to build. The rules
// not yet evaluated are lost. Losing an ordinary rewrite only loses the
// rewrite, but losing an [F]/[G] rule serves what it was written to block, so
// a rule set that carries one refuses the request instead (F14501).
func (e *Engine) overload(result *Result) *Result {
	for _, r := range e.rules {
		if r.Flags.Forbidden || r.Flags.Gone {
			result.Forbidden = true
			break
		}
	}
	return result
}

// sameHostRedirectPath collapses the leading run of '/' and '\\' in a
// scheme-less redirect target to a single '/'. Substitutions are built from
// the decoded request path, so a request for "//evil.com/" or "/%2Fevil.com/"
// would otherwise yield the protocol-relative Location "//evil.com" (browsers
// treat "/\" the same way) — an off-site redirect. Apache avoids this by
// merging slashes before matching.
func sameHostRedirectPath(uri string) string {
	if uri == "" || (uri[0] != '/' && uri[0] != '\\') {
		return uri
	}
	return "/" + strings.TrimLeft(uri, "/\\")
}

// evalConditionsBudget evaluates a list of conditions with Apache RewriteCond
// semantics: conditions are AND-ed by default, and a run of conditions linked
// by [OR] forms an OR-group whose members are OR-ed together. So
// "A [OR] B  C" means "(A OR B) AND C". The overall result is the AND of every
// group's OR result.
//
// The third result is false when the work allowance ran out first (the
// wall-clock budget, or a test value too long to match); the other results
// are then meaningless and the caller must stop (F14502).
func (e *Engine) evalConditionsBudget(conditions []Condition, vars *Variables, b budget) (bool, []string, bool) {
	if len(conditions) == 0 {
		return true, nil, true
	}

	var lastCaptures []string
	overall := true       // AND accumulator across groups
	groupMatched := false // OR accumulator within the current group
	inGroup := false      // a multi-member OR-group is open

	for _, cond := range conditions {
		if b.expired() {
			return false, nil, false
		}
		matched, captures, ok := cond.evaluate(vars, maxRewriteString)
		if !ok {
			return false, nil, false
		}
		if matched {
			// %N refers to the last matched regex condition; a negated or
			// file-test condition has no captures and must not clear them.
			if captures != nil {
				lastCaptures = captures
			}
			groupMatched = true
		}

		if cond.OrNext {
			// More alternatives follow — keep OR-accumulating, don't close
			// the group yet. (A failed alternative must not poison a later
			// matching one — the bug this replaces.)
			inGroup = true
			continue
		}

		// End of the current group (this condition is its last member):
		// fold the group's OR result into the overall AND, then reset.
		overall = overall && groupMatched
		groupMatched = false
		inGroup = false
	}

	// Fold a trailing, never-closed OR-group (malformed input ending in [OR]).
	if inGroup {
		overall = overall && groupMatched
	}

	return overall, lastCaptures, true
}

// skipChain skips rules that are chained ([C] flag) after a non-matching rule.
func (e *Engine) skipChain(current int) int {
	for current < len(e.rules)-1 && e.rules[current].Flags.Chain {
		current++
	}
	return current
}

// BuildVariables creates a Variables struct from an HTTP request.
func BuildVariables(r *http.Request, docRoot, resolvedPath string, isHTTPS bool) *Variables {
	port := "80"
	httpsVal := "off"
	if isHTTPS {
		port = "443"
		httpsVal = "on"
	}

	theRequest := r.Method + " " + r.URL.RequestURI() + " " + r.Proto

	host := r.Host
	if idx := strings.LastIndex(host, ":"); idx != -1 && !strings.HasSuffix(host, "]") {
		host = host[:idx]
	}

	// REMOTE_ADDR is the bare client address; the socket form carries a port,
	// which made anchored IP conditions ("^203\.0\.113\.9$") never match
	// (F2890).
	remote := r.RemoteAddr
	if h, _, err := net.SplitHostPort(remote); err == nil {
		remote = h
	}

	// A rewrite that runs before the static handler has resolved the file has
	// no resolved path yet. REQUEST_FILENAME then stays "", every "-f"/"-d"
	// test fails, and "!-f" matches for existing files too: a "-f" + [F] guard
	// never fires and an SPA fallback swallows real assets (F11200). The file
	// the request would map to is the document root plus the cleaned path.
	if resolvedPath == "" && docRoot != "" {
		resolvedPath = filepath.Join(docRoot, filepath.Clean("/"+r.URL.Path))
	}

	return &Variables{
		RequestURI:      r.URL.Path,
		RequestFilename: resolvedPath,
		QueryString:     r.URL.RawQuery,
		HTTPHost:        r.Host,
		HTTPReferer:     r.Header.Get("Referer"),
		HTTPUserAgent:   r.Header.Get("User-Agent"),
		RemoteAddr:      remote,
		RequestMethod:   r.Method,
		ServerPort:      port,
		HTTPS:           httpsVal,
		DocumentRoot:    docRoot,
		ServerName:      host,
		TheRequest:      theRequest,
		Proto:           r.Proto,
		Header:          r.Header,
	}
}

// ConvertConfigRewrites converts config rewrite rules to engine rules.
func ConvertConfigRewrites(rewrites []ConfigRewrite) []*Rule {
	var rules []*Rule
	for _, rw := range rewrites {
		rule, err := ParseRule(rw.Match, rw.To, strings.Join(rw.Flags, ","))
		if err != nil {
			continue
		}

		// Convert simple conditions
		for _, condStr := range rw.Conditions {
			var variable, pattern string

			switch condStr {
			case "!is_file", "!-f":
				variable = "%{REQUEST_FILENAME}"
				pattern = "!-f"
			case "is_file", "-f":
				variable = "%{REQUEST_FILENAME}"
				pattern = "-f"
			case "!is_dir", "!-d":
				variable = "%{REQUEST_FILENAME}"
				pattern = "!-d"
			case "is_dir", "-d":
				variable = "%{REQUEST_FILENAME}"
				pattern = "-d"
			default:
				continue
			}

			cond, err := ParseCondition(variable, pattern, "")
			if err != nil {
				continue
			}
			rule.Conditions = append(rule.Conditions, *cond)
		}

		// Set redirect flag from config status
		if rw.Status > 0 {
			rule.Flags.Redirect = redirectStatus(rw.Status)
		}

		// Default: last flag
		if !rule.Flags.Chain && rule.Flags.Redirect == 0 {
			rule.Flags.Last = true
		}

		rules = append(rules, rule)
	}
	return rules
}

// ConfigRewrite matches the config.RewriteRule struct shape.
type ConfigRewrite struct {
	Match      string
	To         string
	Status     int
	Conditions []string
	Flags      []string
}
