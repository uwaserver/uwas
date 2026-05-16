package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/uwaserver/uwas/internal/apps"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/cronjob"
	"github.com/uwaserver/uwas/internal/domainroot"
	"github.com/uwaserver/uwas/internal/filemanager"
)

// ============ File Manager ============

func (s *Server) domainRoot(domain string) string {
	root, _ := s.domainRootForFiles(domain)
	return root
}

func (s *Server) domainRootForFiles(domain string) (string, error) {
	s.configMu.RLock()
	var found *config.Domain
	for _, d := range s.config.Domains {
		if d.Host == domain {
			dd := d
			found = &dd
			break
		}
	}
	webRoot := s.config.Global.WebRoot
	s.configMu.RUnlock()

	if found != nil {
		var store = (*apps.Store)(nil)
		if s.appsMgr != nil {
			store = s.appsMgr.Store()
		}
		return domainroot.ForDomain(*found, store)
	}
	return domainroot.Fallback(webRoot, domain), nil
}

func (s *Server) authorizedDomainRoot(w http.ResponseWriter, r *http.Request, domain, action string) (string, bool) {
	if !s.requireDomainAccess(w, r, domain, action) {
		return "", false
	}
	root, err := s.domainRootForFiles(domain)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return "", false
	}
	if root == "" {
		jsonError(w, "domain not found", http.StatusNotFound)
		return "", false
	}
	return root, true
}

func (s *Server) handleFileList(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := s.authorizedDomainRoot(w, r, domain, "file.list")
	if !ok {
		return
	}
	// Auto-create root if it doesn't exist
	os.MkdirAll(root, 0755)
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "."
	}
	entries, err := filemanager.List(root, path)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if entries == nil {
		entries = []filemanager.Entry{}
	}
	limit, offset := parsePagination(r)
	items, total := paginateSlice(entries, limit, offset)
	jsonResponse(w, map[string]any{
		"items":  items,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (s *Server) handleFileRead(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := s.authorizedDomainRoot(w, r, domain, "file.read")
	if !ok {
		return
	}
	path := r.URL.Query().Get("path")
	data, err := filemanager.ReadFile(root, path)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Check if file is an image - serve binary with proper content type
	lowerPath := strings.ToLower(path)
	if strings.HasSuffix(lowerPath, ".png") ||
		strings.HasSuffix(lowerPath, ".jpg") ||
		strings.HasSuffix(lowerPath, ".jpeg") ||
		strings.HasSuffix(lowerPath, ".gif") ||
		strings.HasSuffix(lowerPath, ".webp") ||
		strings.HasSuffix(lowerPath, ".svg") ||
		strings.HasSuffix(lowerPath, ".ico") {

		// Set appropriate content type
		contentType := "application/octet-stream"
		switch {
		case strings.HasSuffix(lowerPath, ".png"):
			contentType = "image/png"
		case strings.HasSuffix(lowerPath, ".jpg") || strings.HasSuffix(lowerPath, ".jpeg"):
			contentType = "image/jpeg"
		case strings.HasSuffix(lowerPath, ".gif"):
			contentType = "image/gif"
		case strings.HasSuffix(lowerPath, ".webp"):
			contentType = "image/webp"
		case strings.HasSuffix(lowerPath, ".svg"):
			contentType = "image/svg+xml"
		case strings.HasSuffix(lowerPath, ".ico"):
			contentType = "image/x-icon"
		}

		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
		w.Write(data)
		return
	}

	// For text files, return as JSON
	jsonResponse(w, map[string]string{"content": string(data), "path": path})
}

func (s *Server) handleFileWrite(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := s.authorizedDomainRoot(w, r, domain, "file.write")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := filemanager.WriteFile(root, req.Path, []byte(req.Content)); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonResponse(w, map[string]string{"status": "written", "path": req.Path})
}

func (s *Server) handleFileDelete(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := s.authorizedDomainRoot(w, r, domain, "file.delete")
	if !ok {
		return
	}
	path := r.URL.Query().Get("path")
	if err := filemanager.Delete(root, path); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonResponse(w, map[string]string{"status": "deleted", "path": path})
}

func (s *Server) handleFileMkdir(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := s.authorizedDomainRoot(w, r, domain, "file.mkdir")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := filemanager.CreateDir(root, req.Path); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonResponse(w, map[string]string{"status": "created", "path": req.Path})
}

func (s *Server) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := s.authorizedDomainRoot(w, r, domain, "file.upload")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 100<<20) // 100MB total request limit
	if err := r.ParseMultipartForm(100 << 20); err != nil {
		jsonError(w, "parse form: "+err.Error(), http.StatusBadRequest)
		return
	}
	dir := r.FormValue("path")
	if dir == "" {
		dir = "."
	}

	// Enforce per-file size limit (50MB per file)
	const maxFileSize = 50 << 20

	var uploaded []string
	for _, fHeaders := range r.MultipartForm.File {
		for _, fh := range fHeaders {
			// Reject files that exceed per-file limit
			if fh.Size > maxFileSize {
				jsonError(w, fmt.Sprintf("file %q exceeds maximum size of %d MB", fh.Filename, maxFileSize>>20), http.StatusBadRequest)
				return
			}
			src, err := fh.Open()
			if err != nil {
				continue
			}
			// Use filepath.Base to strip directory components from uploaded filename
			// (prevents path traversal via filenames like "../../etc/cron.d/x")
			relPath := filepath.Join(dir, filepath.Base(fh.Filename))
			_, err = filemanager.SaveUpload(root, relPath, src)
			src.Close()
			if err == nil {
				uploaded = append(uploaded, relPath)
			}
		}
	}
	jsonResponse(w, map[string]any{"status": "uploaded", "files": uploaded})
}

func (s *Server) handleDiskUsage(w http.ResponseWriter, r *http.Request) {
	domain := r.PathValue("domain")
	root, ok := s.authorizedDomainRoot(w, r, domain, "file.disk_usage")
	if !ok {
		return
	}
	bytes, err := filemanager.DiskUsage(root)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]any{
		"domain": domain,
		"bytes":  bytes,
		"human":  formatBytes(bytes),
		"root":   root,
	})
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// ============ Cron Jobs ============

func (s *Server) handleCronList(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	jobs, err := cronjob.List()
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if jobs == nil {
		jobs = []cronjob.Job{}
	}
	limit, offset := parsePagination(r)
	items, total := paginateSlice(jobs, limit, offset)
	jsonResponse(w, map[string]any{
		"items":  items,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

func (s *Server) handleCronAdd(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var job cronjob.Job
	if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if job.Schedule == "" || job.Command == "" {
		jsonError(w, "schedule and command are required", http.StatusBadRequest)
		return
	}
	if err := cronjob.Add(job); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.logger.Info("cron job added", "schedule", job.Schedule, "command", job.Command)
	jsonResponse(w, map[string]string{"status": "added"})
}

func (s *Server) handleCronDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) || !s.requirePin(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var req struct {
		Schedule string `json:"schedule"`
		Command  string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := cronjob.Remove(req.Schedule, req.Command); err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, map[string]string{"status": "removed"})
}
