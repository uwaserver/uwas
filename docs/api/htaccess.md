---
title: UWAS .htaccess Parser Package API
generated: true
---

# UWAS .htaccess Parser Package API

<!-- Auto-generated from `go doc -all`. Do not edit manually. -->

```
package htaccess // import "github.com/uwaserver/uwas/pkg/htaccess"


CONSTANTS

const (
	// MaxDirectives is the maximum number of directives allowed in an .htaccess file.
	// Prevents ReDoS by limiting rule count.
	MaxDirectives = 200
	// MaxLineLength is the maximum length of a single line in .htaccess.
	MaxLineLength = 4096
	// MaxPatternLength is the maximum length of a regex pattern.
	MaxPatternLength = 1024
)
    Limits for .htaccess parsing — prevent ReDoS and resource exhaustion.


VARIABLES

var ErrTooManyDirectives = errors.New("too many directives, maximum is 200")
    ErrTooManyDirectives is returned when .htaccess has more than MaxDirectives.


FUNCTIONS

func IsModuleLoaded(module string) bool
    IsModuleLoaded returns true if the given module is loaded.


TYPES

type Directive struct {
	Name    string      // "RewriteRule", "Redirect", "Header", etc.
	Args    []string    // directive arguments
	Block   []Directive // inner directives for block types (<IfModule>, etc.)
	LineNum int
}
    Directive represents a parsed .htaccess directive.

func Parse(reader io.Reader) ([]Directive, error)
    Parse reads an Apache .htaccess file and returns parsed directives.

type FilesMatchBlock struct {
	Pattern    string
	Directives []Directive
}
    FilesMatchBlock represents a <FilesMatch> block.

type HeaderRule struct {
	Action string // "set", "unset", "append", "add"
	Name   string
	Value  string
}
    HeaderRule represents a Header directive.

type RedirectRule struct {
	Status  int
	Pattern string
	Target  string
	IsRegex bool // RedirectMatch uses regex
}
    RedirectRule is a converted redirect directive.

type RewriteCondition struct {
	Variable string
	Pattern  string
	Flags    string
}
    RewriteCondition is a converted rewrite condition.

type RewriteRule struct {
	Pattern    string
	Target     string
	Flags      string
	Conditions []RewriteCondition
}
    RewriteRule is a converted rewrite rule.

type RuleSet struct {
	RewriteEnabled   bool
	RewriteBase      string // base path for rewrites (e.g. "/" or "/subdir/")
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
    RuleSet represents the converted internal rules from .htaccess directives.

func Convert(directives []Directive) *RuleSet
    Convert transforms parsed .htaccess directives into an internal RuleSet.

func NewRuleSet() *RuleSet
    NewRuleSet creates an empty RuleSet with initialized maps.

func (rs *RuleSet) Merge(other *RuleSet)
    Merge combines another RuleSet into this one.

```
