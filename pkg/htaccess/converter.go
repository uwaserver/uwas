package htaccess

import (
	"path/filepath"
	"strconv"
	"strings"
)

// RuleSet represents the converted internal rules from .htaccess directives.
type RuleSet struct {
	RewriteEnabled   bool
	Rewrites         []RewriteRule
	Redirects        []RedirectRule
	ErrorDocuments   map[int]string
	DirectoryIndex   []string
	Headers          []HeaderRule
	ExpiresActive    bool
	ExpiresByType    map[string]string
	DirectoryListing *bool // nil = not set, true/false = explicit
	FollowSymlinks   *bool
	AuthType         string
	AuthName         string
	AuthUserFile     string
	Require          string
	FilesMatch       []FilesMatchBlock
	PHPValues        map[string]string // php_value directives
	PHPFlags         map[string]string // php_flag directives (on/off → 1/0)
}

// RewriteRule is a converted rewrite rule.
type RewriteRule struct {
	Pattern    string
	Target     string
	Flags      string
	Conditions []RewriteCondition
}

// RewriteCondition is a converted rewrite condition.
type RewriteCondition struct {
	Variable string
	Pattern  string
	Flags    string
}

// RedirectRule is a converted redirect directive.
type RedirectRule struct {
	Status  int
	Pattern string
	Target  string
	IsRegex bool // RedirectMatch uses regex
}

// HeaderRule represents a Header directive.
type HeaderRule struct {
	Action string // "set", "unset", "append", "add"
	Name   string
	Value  string
}

// FilesMatchBlock represents a <FilesMatch> block.
type FilesMatchBlock struct {
	Pattern    string
	Directives []Directive
}

// NewRuleSet creates an empty RuleSet with initialized maps.
func NewRuleSet() *RuleSet {
	return &RuleSet{
		ErrorDocuments: make(map[int]string),
		ExpiresByType:  make(map[string]string),
		PHPValues:      make(map[string]string),
		PHPFlags:       make(map[string]string),
	}
}

// Convert transforms parsed .htaccess directives into an internal RuleSet.
func Convert(directives []Directive) *RuleSet {
	rules := NewRuleSet()
	var pendingConds []RewriteCondition

	for _, d := range directives {
		name := strings.ToLower(d.Name)

		switch name {
		case "rewriteengine":
			if len(d.Args) > 0 {
				rules.RewriteEnabled = strings.EqualFold(d.Args[0], "on")
			}

		case "rewritecond":
			if len(d.Args) >= 2 {
				cond := RewriteCondition{
					Variable: d.Args[0],
					Pattern:  d.Args[1],
				}
				if len(d.Args) >= 3 {
					cond.Flags = d.Args[2]
				}
				pendingConds = append(pendingConds, cond)
			}

		case "rewriterule":
			if len(d.Args) >= 2 {
				rr := RewriteRule{
					Pattern:    d.Args[0],
					Target:     d.Args[1],
					Conditions: pendingConds,
				}
				if len(d.Args) >= 3 {
					rr.Flags = d.Args[2]
				}
				pendingConds = nil
				rules.Rewrites = append(rules.Rewrites, rr)
			}

		case "redirect":
			rules.Redirects = append(rules.Redirects, parseRedirect(d, false))

		case "redirectmatch":
			rules.Redirects = append(rules.Redirects, parseRedirect(d, true))

		case "errordocument":
			if len(d.Args) >= 2 {
				code, _ := strconv.Atoi(d.Args[0])
				if code > 0 {
					rules.ErrorDocuments[code] = strings.Join(d.Args[1:], " ")
				}
			}

		case "directoryindex":
			rules.DirectoryIndex = append(rules.DirectoryIndex, d.Args...)

		case "header":
			if len(d.Args) >= 2 {
				hr := HeaderRule{Action: strings.ToLower(d.Args[0])}
				if hr.Action == "unset" {
					hr.Name = d.Args[1]
				} else if len(d.Args) >= 3 {
					hr.Name = d.Args[1]
					hr.Value = d.Args[2]
				}
				rules.Headers = append(rules.Headers, hr)
			}

		case "expiresactive":
			if len(d.Args) > 0 {
				rules.ExpiresActive = strings.EqualFold(d.Args[0], "on")
			}

		case "expiresbytype":
			if len(d.Args) >= 2 {
				rules.ExpiresByType[d.Args[0]] = d.Args[1]
			}

		case "options":
			for _, opt := range d.Args {
				switch strings.ToLower(opt) {
				case "-indexes":
					f := false
					rules.DirectoryListing = &f
				case "+indexes", "indexes":
					t := true
					rules.DirectoryListing = &t
				case "-followsymlinks":
					f := false
					rules.FollowSymlinks = &f
				case "+followsymlinks", "followsymlinks":
					t := true
					rules.FollowSymlinks = &t
				}
			}

		case "authtype":
			if len(d.Args) > 0 {
				rules.AuthType = d.Args[0]
			}

		case "authname":
			if len(d.Args) > 0 {
				rules.AuthName = d.Args[0]
			}

		case "authuserfile":
			if len(d.Args) > 0 {
				// Reject absolute paths and traversal to prevent reading arbitrary files.
				f := d.Args[0]
				if !filepath.IsAbs(f) && !strings.Contains(f, "..") {
					rules.AuthUserFile = f
				}
			}

		case "require":
			rules.Require = strings.Join(d.Args, " ")

		case "php_value":
			if len(d.Args) >= 2 {
				rules.PHPValues[d.Args[0]] = strings.Join(d.Args[1:], " ")
			}

		case "php_flag":
			if len(d.Args) >= 2 {
				val := strings.ToLower(d.Args[1])
				if val == "on" || val == "1" || val == "true" {
					rules.PHPFlags[d.Args[0]] = "1"
				} else {
					rules.PHPFlags[d.Args[0]] = "0"
				}
			}

		default:
			// Block directives: <IfModule>, <FilesMatch>, etc.
			if len(d.Block) > 0 {
				if strings.EqualFold(d.Name, "IfModule") {
					// IfModule always evaluates to true in UWAS
					blockRules := Convert(d.Block)
					rules.Merge(blockRules)
				} else if strings.EqualFold(d.Name, "FilesMatch") || strings.EqualFold(d.Name, "Files") {
					pattern := ""
					if len(d.Args) > 0 {
						pattern = d.Args[0]
					}
					rules.FilesMatch = append(rules.FilesMatch, FilesMatchBlock{
						Pattern:    pattern,
						Directives: d.Block,
					})
				}
			}
		}
	}

	return rules
}

// Merge combines another RuleSet into this one.
func (rs *RuleSet) Merge(other *RuleSet) {
	if other == nil {
		return
	}
	if other.RewriteEnabled {
		rs.RewriteEnabled = true
	}
	rs.Rewrites = append(rs.Rewrites, other.Rewrites...)
	rs.Redirects = append(rs.Redirects, other.Redirects...)
	rs.Headers = append(rs.Headers, other.Headers...)
	for k, v := range other.ErrorDocuments {
		rs.ErrorDocuments[k] = v
	}
	if len(other.DirectoryIndex) > 0 {
		rs.DirectoryIndex = other.DirectoryIndex
	}
	if other.DirectoryListing != nil {
		rs.DirectoryListing = other.DirectoryListing
	}
	if other.FollowSymlinks != nil {
		rs.FollowSymlinks = other.FollowSymlinks
	}
	if other.AuthType != "" {
		rs.AuthType = other.AuthType
		rs.AuthName = other.AuthName
		rs.AuthUserFile = other.AuthUserFile
		rs.Require = other.Require
	}
	if other.ExpiresActive {
		rs.ExpiresActive = true
	}
	for k, v := range other.ExpiresByType {
		rs.ExpiresByType[k] = v
	}
	rs.FilesMatch = append(rs.FilesMatch, other.FilesMatch...)
	for k, v := range other.PHPValues {
		rs.PHPValues[k] = v
	}
	for k, v := range other.PHPFlags {
		rs.PHPFlags[k] = v
	}
}

func parseRedirect(d Directive, isRegex bool) RedirectRule {
	r := RedirectRule{
		Status:  302,
		IsRegex: isRegex,
	}

	switch len(d.Args) {
	case 2:
		// Redirect /old /new (default 302)
		r.Pattern = d.Args[0]
		r.Target = d.Args[1]
	case 3:
		// Redirect 301 /old /new
		code, err := strconv.Atoi(d.Args[0])
		if err == nil {
			r.Status = code
			r.Pattern = d.Args[1]
			r.Target = d.Args[2]
		} else {
			// Redirect permanent /old /new
			switch strings.ToLower(d.Args[0]) {
			case "permanent":
				r.Status = 301
			case "temp":
				r.Status = 302
			case "seeother":
				r.Status = 303
			case "gone":
				r.Status = 410
			}
			r.Pattern = d.Args[1]
			r.Target = d.Args[2]
		}
	}

	return r
}
