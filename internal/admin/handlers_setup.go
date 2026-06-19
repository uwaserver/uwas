package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/uwaserver/uwas/internal/install"
)

// ── Setup wizard ────────────────────────────────────────────
//
// The setup wizard lets an operator select a batch of system components
// (PHP versions + apt packages) and install them in one go. It reuses the
// serial install queue (s.taskMgr) so the components install one-at-a-time
// and progress is watchable via GET /api/v1/tasks. Already-installed items
// are skipped so the wizard is safe to re-run from the menu.

// packageInstalled reports whether a known package is present, and its
// version string when detectable. Shared by handlePackageList and the setup
// wizard so both agree on "installed".
func packageInstalled(kp knownPkg) (bool, string) {
	if kp.id == "docker-compose" {
		return detectDockerComposePackage()
	}
	for _, bin := range kp.binaries {
		if p, err := exec.LookPath(bin); err == nil {
			version := ""
			if out, err := newSystemExecCommand(p, "--version").CombinedOutput(); err == nil {
				version = packageVersionLine(out)
			}
			return true, version
		}
	}
	return false, ""
}

// phpVersionInstalled reports whether the given major.minor PHP version
// (e.g. "8.3") is already installed.
func (s *Server) phpVersionInstalled(version string) bool {
	if s.phpMgr == nil {
		return false
	}
	for _, st := range s.phpMgr.Status() {
		if st.Version == version || strings.HasPrefix(st.Version, version+".") {
			return true
		}
	}
	return false
}

// packageTaskFn builds the install/remove work for a known package. Extracted
// from handlePackageInstall so the single-package endpoint and the setup
// wizard run identical logic.
func (s *Server) packageTaskFn(pkg knownPkg, action string) install.TaskFunc {
	pkgName := pkg.name
	pkgID := pkg.id
	aptPkgs := pkg.aptPkgs
	aptRemove := pkg.aptRemove
	return func(appendOutput func(string)) error {
		var cmd *exec.Cmd
		if action == "remove" {
			if pkgID == "wp-cli" {
				cmd = newSystemExecCommand("rm", "-f", "/usr/local/bin/wp")
			} else {
				newSystemExecCommand("systemctl", "stop", pkgID).Run()
				args := append([]string{"remove", "-y", "--purge"}, aptRemove...)
				cmd = newSystemExecCommand("apt", args...)
			}
		} else {
			switch {
			case pkgID == "wp-cli":
				cmd = newSystemExecCommand("bash", "-c", "curl -fsSL -o /usr/local/bin/wp https://raw.githubusercontent.com/wp-cli/builds/gh-pages/phar/wp-cli.phar && chmod +x /usr/local/bin/wp")
			case pkgID == "nodejs":
				// Distro nodejs is typically too old to run modern apps.
				cmd = newSystemExecCommand("bash", "-c", "curl -fsSL https://deb.nodesource.com/setup_lts.x | bash - && apt install -y nodejs")
			case pkgID == "docker-compose":
				cmd = newSystemExecCommand("bash", "-c", strings.Join([]string{
					"apt-get update",
					"(apt-get install -y docker.io docker-compose-plugin || apt-get install -y docker.io docker-compose)",
					"(systemctl enable --now docker >/dev/null 2>&1 || service docker start >/dev/null 2>&1 || true)",
				}, " && "))
			case len(aptPkgs) > 0:
				args := append([]string{"install", "-y"}, aptPkgs...)
				cmd = newSystemExecCommand("apt", args...)
			default:
				return fmt.Errorf("no install method for %s", pkgName)
			}
		}

		cmd.Env = append(os.Environ(),
			"DEBIAN_FRONTEND=noninteractive",
			"NEEDRESTART_MODE=a",
			"APT_LISTCHANGES_FRONTEND=none",
			"DEBIAN_PRIORITY=critical",
		)
		out, err := cmd.CombinedOutput()
		appendOutput(string(out))
		if err != nil {
			s.logger.Error("package "+action+" failed", "package", pkgName, "error", err)
			return err
		}
		s.logger.Info("package "+action+" complete", "package", pkgName)
		return nil
	}
}

// phpInstallTaskFn builds the install work for a PHP version. Extracted from
// handlePHPInstall for reuse by the setup wizard.
func (s *Server) phpInstallTaskFn(version string) install.TaskFunc {
	phpMgr := s.phpMgr
	return func(appendOutput func(string)) error {
		output, err := phpRunInstall(version)
		appendOutput(output)
		if err != nil {
			s.logger.Error("PHP install failed", "version", version, "error", err)
			return err
		}
		s.logger.Info("PHP install complete", "version", version)
		if phpMgr != nil {
			phpMgr.Detect()
		}
		return nil
	}
}

// setupInstallItem is one component the wizard wants installed.
type setupInstallItem struct {
	Type string `json:"type"` // "package" or "php"
	ID   string `json:"id"`   // package id (e.g. "redis") or PHP version (e.g. "8.3")
}

// setupInstallResult reports what happened to one requested item.
type setupInstallResult struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	TaskID  string `json:"task_id,omitempty"`
	Skipped bool   `json:"skipped,omitempty"`
	Reason  string `json:"reason,omitempty"` // why skipped / failed to queue
}

// handleSetupInstall queues a batch of components for installation. Unlike the
// single-item endpoints it does NOT reject when another task is active — the
// queue serializes everything — so the wizard can submit the whole selection
// at once and watch it drain via GET /api/v1/tasks.
func (s *Server) handleSetupInstall(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Items []setupInstallItem `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if len(req.Items) == 0 {
		jsonError(w, "no items selected", http.StatusBadRequest)
		return
	}
	if len(req.Items) > 64 {
		jsonError(w, "too many items", http.StatusBadRequest)
		return
	}

	results := make([]setupInstallResult, 0, len(req.Items))
	seen := make(map[string]bool, len(req.Items))
	queued := 0

	for _, item := range req.Items {
		key := item.Type + ":" + item.ID
		if seen[key] {
			continue
		}
		seen[key] = true

		switch item.Type {
		case "php":
			res := setupInstallResult{Type: "php", ID: item.ID, Name: "PHP " + item.ID}
			if !validPHPVersion(item.ID) {
				res.Skipped, res.Reason = true, "invalid PHP version"
			} else if s.phpMgr == nil {
				res.Skipped, res.Reason = true, "PHP manager not enabled"
			} else if s.phpVersionInstalled(item.ID) {
				res.Skipped, res.Reason = true, "already installed"
			} else {
				task := s.taskMgr.Submit("php", item.ID, "install", s.phpInstallTaskFn(item.ID))
				res.TaskID = task.ID
				queued++
			}
			results = append(results, res)

		case "package":
			pkg := findPkg(item.ID)
			res := setupInstallResult{Type: "package", ID: item.ID}
			if pkg == nil {
				res.Skipped, res.Reason = true, "unknown package"
				results = append(results, res)
				continue
			}
			res.Name = pkg.name
			if ok, _ := packageInstalled(*pkg); ok {
				res.Skipped, res.Reason = true, "already installed"
			} else {
				task := s.taskMgr.Submit("package", pkg.name, "install", s.packageTaskFn(*pkg, "install"))
				res.TaskID = task.ID
				queued++
			}
			results = append(results, res)

		default:
			results = append(results, setupInstallResult{Type: item.Type, ID: item.ID, Skipped: true, Reason: "unknown type"})
		}
	}

	s.recordAuditR(r, "setup.install", fmt.Sprintf("%d queued", queued), true)
	jsonResponse(w, map[string]any{"items": results, "queued": queued})
}
