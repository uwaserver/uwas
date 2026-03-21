package rewrite

import (
	"net/http"
	"net/url"
	"strings"
)

const maxRewrites = 10 // loop detection

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

// Process evaluates all rules against the request URI.
func (e *Engine) Process(uri, queryString string, vars *Variables) *Result {
	result := &Result{
		URI:   uri,
		Query: queryString,
	}

	for iteration := 0; iteration < maxRewrites; iteration++ {
		changed := false

		for i := 0; i < len(e.rules); i++ {
			rule := e.rules[i]

			// Evaluate conditions
			condMatch, condCaptures := e.evalConditions(rule.Conditions, vars)
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
			newURI := rule.Apply(rule.Target, ruleCaptures, condCaptures)

			// Handle special targets
			if newURI == "-" {
				// "-" means no substitution, just apply flags
			} else {
				// Handle query string
				if qIdx := strings.Index(newURI, "?"); qIdx != -1 {
					if rule.Flags.QSAppend && result.Query != "" {
						result.Query = newURI[qIdx+1:] + "&" + result.Query
					} else {
						result.Query = newURI[qIdx+1:]
					}
					newURI = newURI[:qIdx]
				} else if rule.Flags.QSAppend {
					// Keep original query string
				}

				result.URI = newURI
				changed = true
				result.Modified = true

				// Update variables for subsequent rules
				vars.RequestURI = newURI
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
				if result.Query != "" {
					result.URI = result.URI + "?" + result.Query
				}
				return result
			}
			if rule.Flags.Skip > 0 {
				i += rule.Flags.Skip
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

// evalConditions evaluates a list of conditions with AND/OR logic.
func (e *Engine) evalConditions(conditions []Condition, vars *Variables) (bool, []string) {
	if len(conditions) == 0 {
		return true, nil
	}

	var lastCaptures []string
	overallResult := false
	groupResult := true // current AND-group result

	for i, cond := range conditions {
		matched, captures := cond.Evaluate(vars)

		if matched {
			lastCaptures = captures
		}

		if cond.OrNext {
			// OR logic: if any in the OR group matches, the group is true
			if matched {
				overallResult = true
				// Skip remaining OR conditions in this group
				for i+1 < len(conditions) && conditions[i].OrNext {
					i++
				}
				continue
			}
			groupResult = groupResult && matched
		} else {
			// AND logic (default)
			groupResult = groupResult && matched
			if !groupResult && !overallResult {
				return false, nil
			}
			overallResult = overallResult || groupResult
			groupResult = true
		}
	}

	return overallResult || groupResult, lastCaptures
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
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}

	return &Variables{
		RequestURI:      r.URL.Path,
		RequestFilename: resolvedPath,
		QueryString:     r.URL.RawQuery,
		HTTPHost:        r.Host,
		HTTPReferer:     r.Header.Get("Referer"),
		HTTPUserAgent:   r.Header.Get("User-Agent"),
		RemoteAddr:      r.RemoteAddr,
		RequestMethod:   r.Method,
		ServerPort:      port,
		HTTPS:           httpsVal,
		DocumentRoot:    docRoot,
		ServerName:      host,
		TheRequest:      theRequest,
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
			negated := false

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
			_ = negated
			rule.Conditions = append(rule.Conditions, *cond)
		}

		// Set redirect flag from config status
		if rw.Status > 0 {
			rule.Flags.Redirect = rw.Status
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

// ResolvedURI returns the final URI with query string.
func (r *Result) ResolvedURI() string {
	if r.Query != "" {
		return r.URI + "?" + url.PathEscape(r.Query)
	}
	return r.URI
}
