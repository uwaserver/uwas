// Package apps owns the lifecycle of standalone application processes —
// PM2-style supervision for Node/Python/Ruby/Go binaries, plus Docker
// containers. Apps are first-class objects identified by name, persisted
// under /etc/uwas/apps.d/<name>.yaml, and completely independent of any
// domain config. A domain that wants to expose an app on a public hostname
// does so by reverse-proxying to the app's local port — never by
// declaring the app inline as part of the domain definition.
//
// This package is the v0.6.0 successor to the domain-keyed appmanager.
// The two coexist during the deprecation window: legacy `type=app`
// domains continue to spawn via internal/appmanager, while new
// deployments land here.
package apps

import (
	"fmt"
	"strings"
	"time"
)

// Runtime identifies how the app process is started and supervised.
type Runtime string

const (
	RuntimeNode   Runtime = "node"
	RuntimePython Runtime = "python"
	RuntimeRuby   Runtime = "ruby"
	RuntimeGo     Runtime = "go"
	RuntimeCustom Runtime = "custom"
	RuntimeDocker Runtime = "docker"
)

// App is the persisted on-disk definition. One file per app under
// /etc/uwas/apps.d/<name>.yaml.
type App struct {
	// Name is the primary key. Must match the file's basename (without
	// extension) and is the value the proxy dropdown picks when an
	// operator links a domain to this app. ASCII alphanumerics, dashes,
	// underscores only — no dots, slashes, spaces. Validated on load.
	Name string `yaml:"name" json:"name"`

	// Description is a short free-text label shown in the dashboard. Not
	// used by anything operational.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`

	// Runtime selects which spawn path the supervisor takes.
	Runtime Runtime `yaml:"runtime" json:"runtime"`

	// Command is the process command line for native runtimes. Ignored
	// for runtime=docker. Empty = auto-detect from workdir contents.
	Command string `yaml:"command,omitempty" json:"command,omitempty"`

	// WorkDir is the cwd for the spawned process and the location of
	// the app's source files. Defaults to /var/lib/uwas/apps/<name>/
	// on first save if blank.
	WorkDir string `yaml:"work_dir,omitempty" json:"work_dir,omitempty"`

	// Port the app listens on locally. The supervisor injects this as
	// the PORT env var so the app can pick it up without hardcoding.
	// Zero on input = auto-assign by the manager; the assigned value is
	// written back to the file.
	Port int `yaml:"port,omitempty" json:"port,omitempty"`

	// Env are extra environment variables passed to the process.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`

	// AutoRestart enables the supervisor to restart the process on
	// crash. Defaults true unless Disabled.
	AutoRestart bool `yaml:"auto_restart" json:"auto_restart"`

	// Disabled means "the operator has explicitly stopped this app; do
	// not auto-start on boot". A Disabled app stays unscheduled until
	// the operator hits Start.
	Disabled bool `yaml:"disabled,omitempty" json:"disabled,omitempty"`

	// Docker is populated only when Runtime == RuntimeDocker.
	Docker DockerSpec `yaml:"docker,omitempty" json:"docker,omitempty"`

	// Deploy carries the git source + webhook auto-deploy config.
	// Populated on first manual /deploy call and reused for
	// subsequent webhook-triggered deploys so the operator doesn't
	// have to re-enter git URL / build command on every push.
	Deploy DeployConfig `yaml:"deploy,omitempty" json:"deploy,omitempty"`

	// CreatedAt / UpdatedAt are bookkeeping timestamps written on
	// create / update respectively. Operator UI can sort by these.
	CreatedAt time.Time `yaml:"created_at,omitempty" json:"created_at,omitempty"`
	UpdatedAt time.Time `yaml:"updated_at,omitempty" json:"updated_at,omitempty"`
}

// DeployConfig holds per-app git-deploy settings, including the
// webhook auto-deploy hook. None of these are required — an app with
// an empty DeployConfig simply has no automation; operators upload
// source via SFTP and click Start.
type DeployConfig struct {
	// GitURL is the repo to clone/fetch from. https://, ssh://, and
	// git@ schemes are accepted.
	GitURL string `yaml:"git_url,omitempty" json:"git_url,omitempty"`

	// GitBranch is the branch to track. Empty = repo default.
	GitBranch string `yaml:"git_branch,omitempty" json:"git_branch,omitempty"`

	// BuildCmd runs after every clone/pull, inside the workdir.
	// Typical: "npm ci && npm run build". Empty = no build step.
	BuildCmd string `yaml:"build_cmd,omitempty" json:"build_cmd,omitempty"`

	// WebhookSecret is the shared secret used to verify push-event
	// signatures from GitHub (X-Hub-Signature-256) or GitLab
	// (X-Gitlab-Token). Empty = webhooks are disabled for this app.
	// Stored in plain text since the YAML is operator-owned and
	// already 0600; future hardening could encrypt at rest.
	WebhookSecret string `yaml:"webhook_secret,omitempty" json:"webhook_secret,omitempty"`

	// BranchFilter, when set, makes the webhook ONLY trigger a deploy
	// if the push event's ref ends in this branch name. A typo-prone
	// alternative to GitBranch: the latter is "what to check out",
	// the former is "what to deploy on". Most operators only need
	// one — leave both blank and any push triggers a deploy of the
	// default branch.
	BranchFilter string `yaml:"branch_filter,omitempty" json:"branch_filter,omitempty"`
}

// DockerSpec describes how to build and run a docker-based app. Build is
// optional; if Image is set and Build is empty, the supervisor pulls and
// runs the image as-is.
type DockerSpec struct {
	// Image is the container image to run. If Build is configured the
	// supervisor builds and tags this name; otherwise it's the upstream
	// reference to pull.
	Image string `yaml:"image,omitempty" json:"image,omitempty"`

	// Build, when non-empty, drives a BuildKit build of the app's
	// source before run. Output is tagged as Image.
	Build DockerBuild `yaml:"build,omitempty" json:"build,omitempty"`

	// ContainerPort is the port the app listens on INSIDE the
	// container. The supervisor maps it to App.Port on 127.0.0.1.
	ContainerPort int `yaml:"container_port,omitempty" json:"container_port,omitempty"`

	// Volumes are extra bind mounts (host:container[:mode]).
	Volumes []string `yaml:"volumes,omitempty" json:"volumes,omitempty"`

	// ExtraArgs are appended verbatim to the `docker run` command —
	// escape hatch for capabilities the schema doesn't expose. Use
	// sparingly.
	ExtraArgs []string `yaml:"extra_args,omitempty" json:"extra_args,omitempty"`
}

// DockerBuild describes the BuildKit build inputs.
type DockerBuild struct {
	// Context is the build context directory (defaults to App.WorkDir).
	Context string `yaml:"context,omitempty" json:"context,omitempty"`

	// Dockerfile is the dockerfile path within Context (default
	// "Dockerfile").
	Dockerfile string `yaml:"dockerfile,omitempty" json:"dockerfile,omitempty"`

	// Args are --build-arg key=value pairs.
	Args map[string]string `yaml:"args,omitempty" json:"args,omitempty"`

	// Target is the multi-stage build target.
	Target string `yaml:"target,omitempty" json:"target,omitempty"`
}

// Validate checks the schema invariants that must hold before write or
// supervision. Caller should reject anything that fails this.
func (a *App) Validate() error {
	if a.Name == "" {
		return fmt.Errorf("app name is required")
	}
	if !isValidAppName(a.Name) {
		return fmt.Errorf("app name %q invalid: must be alphanumeric + dash/underscore, max 64 chars", a.Name)
	}
	switch a.Runtime {
	case RuntimeNode, RuntimePython, RuntimeRuby, RuntimeGo, RuntimeCustom:
		// native — Command or workdir contents will drive spawn
	case RuntimeDocker:
		if a.Docker.Image == "" && a.Docker.Build.Context == "" {
			return fmt.Errorf("docker app %q: either docker.image or docker.build.context is required", a.Name)
		}
		if a.Docker.ContainerPort == 0 {
			return fmt.Errorf("docker app %q: docker.container_port is required", a.Name)
		}
	case "":
		return fmt.Errorf("app %q: runtime is required", a.Name)
	default:
		return fmt.Errorf("app %q: unknown runtime %q", a.Name, a.Runtime)
	}
	if a.Port < 0 || a.Port > 65535 {
		return fmt.Errorf("app %q: port %d out of range", a.Name, a.Port)
	}
	return nil
}

// isValidAppName enforces the filename-safe / shell-safe / URL-safe
// character set. Same set used by domain hostname validation, minus
// dots (apps are not DNS names).
func isValidAppName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// SanitizeName lowercases and replaces unsafe chars in a user-supplied
// label so the result passes isValidAppName. Used by import flows that
// take an arbitrary string (e.g., a legacy hostname being migrated to
// an app name).
func SanitizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b = append(b, c)
		case c == '.', c == ':', c == '/', c == ' ':
			b = append(b, '-')
		case c == '-', c == '_':
			b = append(b, c)
		default:
			// drop
		}
	}
	out := strings.Trim(string(b), "-_")
	if len(out) > 64 {
		out = out[:64]
	}
	if out == "" {
		out = "app"
	}
	return out
}
