package php

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/uwaserver/uwas/internal/phpmanager"
)

// installGate runs the shared "one installer at a time" check. It returns the
// unlock func and false when another install task is active (409 written).
func (h *Handler) installGate(w http.ResponseWriter) (unlock func(), ok bool) {
	unlock = func() {}
	if l, has := h.deps.(interface{ InstallLock() (unlock func()) }); has {
		unlock = l.InstallLock()
	}
	if active := h.deps.TaskActive(); active != nil {
		unlock()
		jsonError(w, fmt.Sprintf("another installation in progress: %s (%s)", active.Name, active.ID), http.StatusConflict)
		return func() {}, false
	}
	return unlock, true
}

// Update upgrades one installed PHP branch to the newest patch release the
// distro repository offers, or every supported branch with version "all".
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	mgr := h.deps.PHPManager()
	if mgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	var targets []string
	if req.Version == "" || req.Version == "all" {
		for _, b := range mgr.Branches() {
			if !b.EOL {
				targets = append(targets, b.Short)
			}
		}
		if len(targets) == 0 {
			jsonError(w, "no supported PHP version is installed", http.StatusNotFound)
			return
		}
	} else {
		if !validPHPVersion(req.Version) {
			jsonError(w, "invalid PHP version (expected N.N, e.g. 8.3)", http.StatusBadRequest)
			return
		}
		if !phpmanager.SupportedBranch(req.Version) {
			jsonError(w, "PHP "+req.Version+" is end-of-life and gets no updates; install 8.2 or newer and move the sites to it", http.StatusBadRequest)
			return
		}
		found := false
		for _, b := range mgr.Branches() {
			if b.Short == req.Version {
				found = true
			}
		}
		if !found {
			jsonError(w, "PHP "+req.Version+" is not installed", http.StatusNotFound)
			return
		}
		targets = []string{req.Version}
	}

	unlock, ok := h.installGate(w)
	if !ok {
		return
	}
	defer unlock()

	name := strings.Join(targets, ",")
	h.deps.RecordAudit(r, "php.update", name, true)
	h.deps.LogInfo("starting PHP update", "versions", name)
	task := h.deps.TaskSubmit("php", name, "update", func(appendOutput func(string)) error {
		var firstErr error
		for _, v := range targets {
			appendOutput("== PHP " + v + " ==\n")
			output, err := h.deps.PhpRunUpdate(v)
			appendOutput(output)
			if err != nil {
				h.deps.LogError("PHP update failed", "version", v, "error", err)
				if firstErr == nil {
					firstErr = fmt.Errorf("PHP %s: %w", v, err)
				}
			}
		}
		_ = mgr.Detect() // re-probe so versions and advisories reflect the new binaries
		return firstErr
	})
	jsonResponse(w, map[string]any{"status": "started", "task_id": task.ID, "versions": targets})
}

// Uninstall removes one installed PHP branch. Branches that still serve
// domains are refused so a click cannot take sites offline.
func (h *Handler) Uninstall(w http.ResponseWriter, r *http.Request) {
	if !h.deps.RequireAdmin(w, r) {
		return
	}
	mgr := h.deps.PHPManager()
	if mgr == nil {
		jsonError(w, "PHP manager not enabled", http.StatusNotImplemented)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Version string `json:"version"`
		Purge   bool   `json:"purge"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if !validPHPVersion(req.Version) {
		jsonError(w, "invalid PHP version (expected N.N, e.g. 8.3)", http.StatusBadRequest)
		return
	}
	var branch *phpmanager.Branch
	for _, b := range mgr.Branches() {
		if b.Short == req.Version {
			b := b
			branch = &b
		}
	}
	if branch == nil {
		jsonError(w, "PHP "+req.Version+" is not installed", http.StatusNotFound)
		return
	}
	if len(branch.Domains) > 0 {
		jsonError(w, fmt.Sprintf("PHP %s is used by %d domain(s): %s — move them to another version first",
			req.Version, len(branch.Domains), strings.Join(branch.Domains, ", ")), http.StatusConflict)
		return
	}

	unlock, ok := h.installGate(w)
	if !ok {
		return
	}
	defer unlock()

	h.deps.RecordAudit(r, "php.uninstall", req.Version, true)
	h.deps.LogInfo("starting PHP uninstall", "version", req.Version, "purge", req.Purge)
	task := h.deps.TaskSubmit("php", req.Version, "uninstall", func(appendOutput func(string)) error {
		_ = mgr.StopFPM(branch.Version) // UWAS-managed process, if any
		output, err := h.deps.PhpRunUninstall(req.Version, req.Purge)
		appendOutput(output)
		_ = mgr.Detect()
		if err != nil {
			h.deps.LogError("PHP uninstall failed", "version", req.Version, "error", err)
		}
		return err
	})
	jsonResponse(w, map[string]any{"status": "started", "task_id": task.ID, "version": req.Version, "purge": req.Purge})
}
