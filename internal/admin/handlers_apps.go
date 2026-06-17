package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uwaserver/uwas/internal/apps"
)

// listeningProbeTimeout is how long Create/Update/Start handlers wait
// for the app's port to become connectable before reporting "started
// but not yet listening". 3s catches normal startup; longer-warming
// apps trip the warning but don't fail the deploy.
const listeningProbeTimeout = 3 * time.Second

// blockedEnvVars are system-critical environment variables apps must
// not override. PATH / LD_PRELOAD / etc. are well-known privilege-
// escalation vectors; HOME / USER would break the supervisor's own
// expectations about the process's identity.
var blockedEnvVars = map[string]bool{
	"PATH": true, "LD_PRELOAD": true, "LD_LIBRARY_PATH": true, "LD_AUDIT": true,
	"LD_PROFILE": true, "SHELL": true, "IFS": true, "ENV": true, "BASH_ENV": true,
	"PS4": true, "PROMPT_COMMAND": true, "HOME": true, "USER": true, "LOGNAME": true,
}

// validEnvName checks that a string matches the POSIX env-var name
// grammar: leading [A-Za-z_], then [A-Za-z0-9_]. Apps that try to
// inject names with `=` or other shell-meaningful characters get
// rejected at the API boundary.
func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, c := range name {
		if i == 0 {
			if c != '_' && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') {
				return false
			}
		} else if c != '_' && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func validateAppEnvMap(env map[string]string) error {
	for k := range env {
		if blockedEnvVars[k] {
			return fmt.Errorf("env var %s is reserved", k)
		}
		if !validEnvName(k) {
			return fmt.Errorf("invalid env name: %s", k)
		}
	}
	return nil
}

// v0.6.0 apps API. Apps are first-class objects keyed by name (not by
// domain) and persisted under /etc/uwas/apps.d/<name>.yaml. A domain
// that wants to expose an app uses `type: proxy` targeting the app's
// local listen port — there is no `type: app` value anymore.
//
// Routes live under /api/v1/apps/. The pre-v0.6 domain-keyed handlers
// at the same path were removed in v0.6.0 (see CHANGELOG breaking-
// change notice).

// handleAppsList returns every registered standalone app
// merged with its current runtime state.
func (s *Server) handleAppsList(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.appsMgr == nil {
		jsonResponse(w, []any{})
		return
	}
	instances := s.appsMgr.Instances()
	out := make([]any, 0, len(instances))
	for _, inst := range instances {
		out = append(out, inst)
	}
	limit, offset := parsePagination(r)
	items, total := paginateSlice(out, limit, offset)
	jsonResponse(w, map[string]any{
		"items":  items,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// handleAppGet returns one app: its on-disk definition AND
// its current runtime instance. Separating "definition" from "live"
// lets the dashboard render a Disabled app whose process is stopped
// without confusing the two.
func (s *Server) handleAppGet(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.appsMgr == nil {
		jsonError(w, "apps manager not enabled", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	def, err := s.appsMgr.Store().Get(name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if def == nil {
		jsonError(w, "app not found: "+name, http.StatusNotFound)
		return
	}
	jsonResponse(w, map[string]any{
		"app":      appDefinitionForResponse(def),
		"instance": s.appsMgr.Get(name),
	})
}

func appDefinitionForResponse(a *apps.App) *apps.App {
	if a == nil {
		return nil
	}
	out := *a
	out.Deploy.GitToken = ""
	return &out
}

// handleAppCreate registers a new app and — by default —
// starts it immediately. The auto-start behavior is the key UX
// difference from the legacy domain-keyed appmanager: operators
// asked for "create → ready to use" instead of "create → click Start".
//
// Query params:
//
//	start=false    Skip the start attempt (only persist the definition).
//	               Useful when uploading source via SFTP first.
//
// Response shape:
//
//	{
//	  "app":          {...},        // resolved definition with port/workdir
//	  "started":      true|false,   // outcome of the start attempt
//	  "start_error":  "..."         // present iff started=false and a start
//	                                 // was attempted; carries the supervisor's
//	                                 // diagnostic (incl. log tail).
//	}
//
// On start failure the HTTP status is still 201 (the definition
// persisted) — the client uses the start_error field to render a
// "saved but not running, click for logs" prompt. Returning 5xx here
// would force the dashboard to roll back the definition, which is
// the opposite of what we want for a debuggable deploy.
func (s *Server) handleAppCreate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.appsMgr == nil {
		jsonError(w, "apps manager not enabled", http.StatusNotImplemented)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var a apps.App
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Sanitize a user-supplied name. If the caller sent something with
	// dots / spaces / mixed case (common when migrating a hostname),
	// coerce to the apps name format rather than erroring out.
	a.Name = apps.SanitizeName(a.Name)

	if s.appsMgr.Store().Exists(a.Name) {
		jsonError(w, "app already exists: "+a.Name, http.StatusConflict)
		return
	}

	if a.WorkDir == "" {
		a.WorkDir = s.appsMgr.Store().DefaultWorkDir(a.Name)
	}
	if err := a.Validate(); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	scaffolded := false
	if a.Deploy.GitURL == "" {
		var err error
		scaffolded, err = apps.ScaffoldDemo(&a)
		if err != nil {
			jsonError(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if err := s.appsMgr.Register(&a); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.recordAuditR(r, "app.create", a.Name, true)

	// Resolved definition (with assigned port + filled workdir +
	// timestamps) goes back to the client regardless of start outcome.
	def, _ := s.appsMgr.Store().Get(a.Name)

	startMode := r.URL.Query().Get("start")
	wantStart := startMode != "false" && !a.Disabled

	result := map[string]any{
		"app":        appDefinitionForResponse(def),
		"started":    false,
		"scaffolded": scaffolded,
	}

	if wantStart {
		if err := s.appsMgr.Start(a.Name); err != nil {
			// Keep the definition — operator needs to see the error
			// to fix the source or config. Reload still happens so
			// any waiting proxy clients can re-resolve once the
			// operator does fix the issue.
			result["start_error"] = err.Error()
		} else {
			result["started"] = true
			// Process is alive (liveness probe inside startNative
			// already verified). Now check that it has bound to its
			// port — without this, the proxy 502s during the
			// app's startup window. Probe is non-fatal: a slow-warming
			// app still reports started=true, but listening=false
			// tells the dashboard to render an "app is starting" hint
			// rather than implying full readiness.
			if err := s.appsMgr.WaitListening(a.Name, listeningProbeTimeout); err != nil {
				result["listening"] = false
				result["listening_warning"] = err.Error()
			} else {
				result["listening"] = true
			}
		}
	}

	s.maybeReloadForApps()

	w.WriteHeader(http.StatusCreated)
	jsonResponse(w, result)
}

// maybeReloadForApps triggers a config reload so the server's proxy
// pools re-resolve `apps://<name>` upstream URLs against the latest
// apps.Manager state. Best-effort — a reload failure is logged but
// doesn't fail the operator's API call. The state change itself
// (registered/started/stopped) is already persisted.
func (s *Server) maybeReloadForApps() {
	if s.reloadFn == nil {
		return
	}
	if err := s.reloadFn(); err != nil && s.logger != nil {
		s.logger.Warn("post-app-change reload failed", "error", err)
	}
}

// handleAppUpdate is a partial-update. Empty/zero fields in
// the patch are LEFT ALONE; non-zero fields overwrite. This matches
// the v0.5.5 fix for MergeDomain — never wipe a structured field just
// because the client didn't bother sending it.
//
// The supervisor stops the running process before re-saving so port
// or command changes take effect on the next Start. Auto-restart is
// not implicit here — explicit operator action keeps surprises low.
func (s *Server) handleAppUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.appsMgr == nil {
		jsonError(w, "apps manager not enabled", http.StatusNotImplemented)
		return
	}

	name := r.PathValue("name")
	existing, err := s.appsMgr.Store().Get(name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if existing == nil {
		jsonError(w, "app not found: "+name, http.StatusNotFound)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var patch apps.App
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	hasDeployPatch := patch.Deploy.GitURL != "" ||
		patch.Deploy.GitBranch != "" ||
		patch.Deploy.BuildCmd != "" ||
		patch.Deploy.HealthPath != "" ||
		patch.Deploy.SSHKeyPath != "" ||
		patch.Deploy.GitToken != "" ||
		patch.Deploy.WebhookSecret != "" ||
		patch.Deploy.BranchFilter != ""
	hasOperationalPatch := patch.Description != "" ||
		patch.Runtime != "" ||
		patch.Command != "" ||
		patch.WorkDir != "" ||
		patch.Port > 0 ||
		patch.Env != nil ||
		patch.Docker.Image != "" ||
		patch.Docker.ContainerPort > 0 ||
		patch.Docker.Volumes != nil ||
		patch.Docker.ExtraArgs != nil ||
		patch.Docker.Build.Context != ""

	// Field-by-field merge. Name CANNOT change (rename = delete+create).
	if patch.Name != "" && patch.Name != name {
		jsonError(w, "renaming via PUT is not supported — delete and recreate", http.StatusBadRequest)
		return
	}
	if patch.Description != "" {
		existing.Description = patch.Description
	}
	if patch.Runtime != "" {
		existing.Runtime = patch.Runtime
	}
	if patch.Command != "" {
		existing.Command = patch.Command
	}
	if patch.WorkDir != "" {
		existing.WorkDir = patch.WorkDir
	}
	if patch.Port > 0 {
		existing.Port = patch.Port
	}
	if patch.Env != nil {
		// env is a wholesale-replace within update — operator sends
		// the full desired map, same semantics as Kubernetes envFrom.
		// Validate keys aren't system-critical (PATH/LD_PRELOAD/etc).
		if err := validateAppEnvMap(patch.Env); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		existing.Env = patch.Env
	}
	if patch.Deploy.GitURL != "" {
		existing.Deploy.GitURL = patch.Deploy.GitURL
	}
	if patch.Deploy.GitBranch != "" {
		existing.Deploy.GitBranch = patch.Deploy.GitBranch
	}
	if patch.Deploy.BuildCmd != "" {
		existing.Deploy.BuildCmd = patch.Deploy.BuildCmd
	}
	if patch.Deploy.HealthPath != "" {
		existing.Deploy.HealthPath = patch.Deploy.HealthPath
	}
	if patch.Deploy.SSHKeyPath != "" {
		existing.Deploy.SSHKeyPath = patch.Deploy.SSHKeyPath
	}
	if patch.Deploy.GitToken != "" {
		existing.Deploy.GitToken = patch.Deploy.GitToken
	}
	if patch.Deploy.WebhookSecret != "" {
		existing.Deploy.WebhookSecret = patch.Deploy.WebhookSecret
	}
	if patch.Deploy.BranchFilter != "" {
		existing.Deploy.BranchFilter = patch.Deploy.BranchFilter
	}
	if hasDeployPatch {
		if err := validateDeployConfig(existing); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	// Docker subblock: replace wholesale when Runtime == docker and
	// the patch carries any docker fields. Partial merge of nested
	// build args is more complexity than the current dashboard needs.
	if patch.Runtime == apps.RuntimeDocker {
		if patch.Docker.Image != "" {
			existing.Docker.Image = patch.Docker.Image
		}
		if patch.Docker.ContainerPort > 0 {
			existing.Docker.ContainerPort = patch.Docker.ContainerPort
		}
		if patch.Docker.Volumes != nil {
			existing.Docker.Volumes = patch.Docker.Volumes
		}
		if patch.Docker.ExtraArgs != nil {
			existing.Docker.ExtraArgs = patch.Docker.ExtraArgs
		}
		if patch.Docker.Build.Context != "" {
			existing.Docker.Build = patch.Docker.Build
		}
	}
	if hasDeployPatch && !hasOperationalPatch {
		if err := s.appsMgr.Store().Save(existing); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.recordAuditR(r, "app.deploy_config", name, true)
		def, _ := s.appsMgr.Store().Get(name)
		running := false
		if inst := s.appsMgr.Get(name); inst != nil {
			running = inst.Running
		}
		jsonResponse(w, map[string]any{
			"app":       appDefinitionForResponse(def),
			"started":   running,
			"listening": running,
		})
		return
	}

	// Bool fields: we can't distinguish "unset" from "false" in JSON
	// without pointer types, so we accept the patch value directly.
	// Callers that want to leave them alone should omit them and the
	// client library should clone-and-modify a fetched object.
	existing.AutoRestart = patch.AutoRestart
	existing.Disabled = patch.Disabled

	// Stop any running instance before swapping the definition so the
	// in-memory port/command can't desync from the live process.
	_ = s.appsMgr.Stop(name)

	if err := s.appsMgr.Register(existing); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Response shape mirrors Create: {app, started, start_error?} so
	// the dashboard can surface a failed restart-after-edit without
	// a second round-trip. Pre-hardening Update silently logged the
	// start error and returned the (now stale) definition, which is
	// exactly the silent-failure mode the user got burned by before.
	result := map[string]any{
		"started": false,
	}
	if !existing.Disabled {
		if err := s.appsMgr.Start(name); err != nil {
			result["start_error"] = err.Error()
		} else {
			result["started"] = true
			if err := s.appsMgr.WaitListening(name, listeningProbeTimeout); err != nil {
				result["listening"] = false
				result["listening_warning"] = err.Error()
			} else {
				result["listening"] = true
			}
		}
	} else {
		// Disabled: update succeeded, intentionally not started.
		result["started"] = false
		result["start_error"] = "app is disabled — clear the disabled flag and click Start to launch"
	}

	s.recordAuditR(r, "app.update", name, true)
	s.maybeReloadForApps()
	def, _ := s.appsMgr.Store().Get(name)
	result["app"] = appDefinitionForResponse(def)
	jsonResponse(w, result)
}

// handleAppDelete removes the app's YAML, stops the process,
// and clears the in-memory registration. WorkDir is left in place —
// users may want to inspect logs or recover source from there. A
// future endpoint can offer a `?purge=true` to also wipe the workdir.
func (s *Server) handleAppDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) || !s.requirePin(w, r) {
		return
	}
	if s.appsMgr == nil {
		jsonError(w, "apps manager not enabled", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	if !s.appsMgr.Store().Exists(name) {
		jsonError(w, "app not found: "+name, http.StatusNotFound)
		return
	}
	if err := s.appsMgr.Unregister(name); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.recordAuditR(r, "app.delete", name, true)
	s.maybeReloadForApps()
	jsonResponse(w, map[string]string{"status": "deleted", "name": name})
}

// handleAppStart launches the app's process / container. If
// the app was Disabled, clear the flag so the operator's intent is
// persisted across daemon restarts.
func (s *Server) handleAppStart(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.appsMgr == nil {
		jsonError(w, "apps manager not enabled", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	existing, err := s.appsMgr.Store().Get(name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if existing == nil {
		jsonError(w, "app not found: "+name, http.StatusNotFound)
		return
	}
	// Clear Disabled if set — Start implies operator intent for it
	// to be running.
	if existing.Disabled {
		existing.Disabled = false
		if err := s.appsMgr.Register(existing); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	if err := s.appsMgr.Start(name); err != nil {
		jsonError(w, err.Error(), http.StatusConflict)
		return
	}
	s.recordAuditR(r, "app.start", name, true)
	s.maybeReloadForApps()

	resp := map[string]any{"status": "started", "name": name}
	if err := s.appsMgr.WaitListening(name, listeningProbeTimeout); err != nil {
		resp["listening"] = false
		resp["listening_warning"] = err.Error()
	} else {
		resp["listening"] = true
	}
	jsonResponse(w, resp)
}

// handleAppStop terminates the process and persists Disabled
// so it doesn't auto-restart on the next daemon boot.
func (s *Server) handleAppStop(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.appsMgr == nil {
		jsonError(w, "apps manager not enabled", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	existing, err := s.appsMgr.Store().Get(name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if existing == nil {
		jsonError(w, "app not found: "+name, http.StatusNotFound)
		return
	}

	if err := s.appsMgr.Stop(name); err != nil {
		// Stop returns an error for "already stopped" — surface as
		// 200 with status so the dashboard's button is idempotent.
		if strings.Contains(err.Error(), "not running") ||
			strings.Contains(err.Error(), "not registered") {
			jsonResponse(w, map[string]string{"status": "already stopped", "name": name})
			return
		}
		jsonError(w, err.Error(), http.StatusConflict)
		return
	}

	// Persist Disabled so the next StartAll() doesn't relaunch.
	if !existing.Disabled {
		existing.Disabled = true
		_ = s.appsMgr.Store().Save(existing)
	}

	s.recordAuditR(r, "app.stop", name, true)
	s.maybeReloadForApps()
	jsonResponse(w, map[string]string{"status": "stopped", "name": name})
}

// handleAppRestart is a stop+start. Clears Disabled (mirror
// of Start) so a Restart on a disabled app brings it up.
func (s *Server) handleAppRestart(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.appsMgr == nil {
		jsonError(w, "apps manager not enabled", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	existing, err := s.appsMgr.Store().Get(name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if existing == nil {
		jsonError(w, "app not found: "+name, http.StatusNotFound)
		return
	}
	if existing.Disabled {
		existing.Disabled = false
		_ = s.appsMgr.Store().Save(existing)
	}
	if err := s.appsMgr.Restart(name); err != nil {
		jsonError(w, err.Error(), http.StatusConflict)
		return
	}
	s.recordAuditR(r, "app.restart", name, true)
	s.maybeReloadForApps()

	resp := map[string]any{"status": "restarted", "name": name}
	if err := s.appsMgr.WaitListening(name, listeningProbeTimeout); err != nil {
		resp["listening"] = false
		resp["listening_warning"] = err.Error()
	} else {
		resp["listening"] = true
	}
	jsonResponse(w, resp)
}

// handleAppStats returns CPU% / memory / uptime for a
// running app. Stopped apps get a Running:false response with zeroes
// rather than a 404 — the dashboard polls this endpoint for live
// resource charts and a 404 mid-poll would be noisy to handle.
func (s *Server) handleAppStats(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.appsMgr == nil {
		jsonError(w, "apps manager not enabled", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	stats := s.appsMgr.Stats(name)
	if stats == nil {
		jsonError(w, "app not found: "+name, http.StatusNotFound)
		return
	}
	jsonResponse(w, stats)
}

// handleAppLogs tails the supervisor-managed log file under
// <workdir>/../logs/<name>.log. Returns up to the last 100 KB so the
// response stays bounded — the dashboard renders this in a scroll
// container and operators who want full logs SSH in.
func (s *Server) handleAppLogs(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.appsMgr == nil {
		jsonError(w, "apps manager not enabled", http.StatusNotImplemented)
		return
	}
	name := r.PathValue("name")
	def, err := s.appsMgr.Store().Get(name)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if def == nil {
		jsonError(w, "app not found: "+name, http.StatusNotFound)
		return
	}

	logPath := filepath.Join(filepath.Dir(def.WorkDir), "logs", name+".log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		// Build log fallback — show this for docker apps that haven't
		// had a runtime log yet but have a build log.
		buildLogPath := filepath.Join(filepath.Dir(def.WorkDir), "logs", name+"-build.log")
		if bdata, berr := os.ReadFile(buildLogPath); berr == nil {
			if len(bdata) > 100*1024 {
				bdata = bdata[len(bdata)-100*1024:]
			}
			jsonResponse(w, map[string]string{
				"log":  string(bdata),
				"kind": "build",
			})
			return
		}
		if errors.Is(err, os.ErrNotExist) {
			jsonResponse(w, map[string]string{"log": "", "kind": "runtime"})
			return
		}
		jsonError(w, fmt.Sprintf("read log: %v", err), http.StatusInternalServerError)
		return
	}
	if len(data) > 100*1024 {
		data = data[len(data)-100*1024:]
	}
	jsonResponse(w, map[string]string{"log": string(data), "kind": "runtime"})
}
