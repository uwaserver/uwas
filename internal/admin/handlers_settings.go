package admin

import (
	"net/http"
	"os"
	"strings"

	"github.com/uwaserver/uwas/internal/admin/settings"
	"github.com/uwaserver/uwas/internal/config"
)

// settingsDeps adapts admin.Server to the settings.Deps interface.
type settingsDeps struct {
	s *Server
}

func (d *settingsDeps) RequireAdmin(w http.ResponseWriter, r *http.Request) bool {
	return d.s.requireAdmin(w, r)
}
func (d *settingsDeps) RequirePin(w http.ResponseWriter, r *http.Request) bool {
	return d.s.requirePin(w, r)
}
func (d *settingsDeps) RecordAudit(r *http.Request, action, detail string, success bool) {
	d.s.recordAuditR(r, action, detail, success)
}
func (d *settingsDeps) LogError(msg string, args ...any) { d.s.logger.Error(msg, args...) }
func (d *settingsDeps) ConfigPtr() *config.Config        { return d.s.config }
func (d *settingsDeps) LockConfig()                      { d.s.configMu.Lock() }
func (d *settingsDeps) UnlockConfig()                    { d.s.configMu.Unlock() }
func (d *settingsDeps) RLockConfig()                     { d.s.configMu.RLock() }
func (d *settingsDeps) RUnlockConfig()                   { d.s.configMu.RUnlock() }
func (d *settingsDeps) PersistConfig()                   { d.s.persistConfig() }
func (d *settingsDeps) ConfigPath() string               { return d.s.configPath }
func (d *settingsDeps) EnsureAuthManagerFromConfig()     { d.s.ensureAuthManagerFromConfig() }
func (d *settingsDeps) AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	return atomicWriteFile(path, data, perm)
}
func (d *settingsDeps) LockPersist()   { d.s.persistMu.Lock() }
func (d *settingsDeps) UnlockPersist() { d.s.persistMu.Unlock() }
func (d *settingsDeps) Reload() error {
	if d.s.reloadFn == nil {
		return nil
	}
	return d.s.reloadFn()
}
func (d *settingsDeps) ToInt(v any) int                      { return toInt(v) }
func (d *settingsDeps) ParseDur(s string) config.Duration    { return parseDur(s) }
func (d *settingsDeps) ByteSizeStr(b config.ByteSize) string { return byteSizeStr(b) }
func (d *settingsDeps) ParseBS(s string) config.ByteSize     { return parseBS(s) }

// settingsHandler holds the settings admin handler instance.
var settingsHandler *settings.Handler

func (s *Server) initSettingsHandler() {
	settingsHandler = settings.New(&settingsDeps{s: s})
}

// ── Thin wrappers ──

func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	settingsHandler.NotifyTest(w, r)
}
func (s *Server) handleGenRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	settingsHandler.GenRecoveryCodes(w, r)
}
func (s *Server) handleUseRecoveryCode(w http.ResponseWriter, r *http.Request) {
	settingsHandler.UseRecoveryCode(w, r)
}
func (s *Server) handleNotifyPrefsGet(w http.ResponseWriter, r *http.Request) {
	settingsHandler.NotifyPrefsGet(w, r)
}
func (s *Server) handleNotifyPrefsPut(w http.ResponseWriter, r *http.Request) {
	settingsHandler.NotifyPrefsPut(w, r)
}
func (s *Server) handleBrandingGet(w http.ResponseWriter, r *http.Request) {
	settingsHandler.BrandingGet(w, r)
}
func (s *Server) handleBrandingPut(w http.ResponseWriter, r *http.Request) {
	settingsHandler.BrandingPut(w, r)
}
func (s *Server) handleConfigExport(w http.ResponseWriter, r *http.Request) {
	settingsHandler.ConfigExport(w, r)
}
func (s *Server) handleConfigRawGet(w http.ResponseWriter, r *http.Request) {
	settingsHandler.ConfigRawGet(w, r)
}
func (s *Server) handleConfigRawPut(w http.ResponseWriter, r *http.Request) {
	settingsHandler.ConfigRawPut(w, r)
}
func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	settingsHandler.SettingsGet(w, r)
}
func (s *Server) handleSettingsPut(w http.ResponseWriter, r *http.Request) {
	settingsHandler.SettingsPut(w, r)
}

// Compile-time check.
var _ settings.Deps = (*settingsDeps)(nil)

// Retained helpers referenced by other admin files.
func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return "****"
	}
	return "****" + s[len(s)-4:]
}

func hashRecoveryCode(code string) string {
	return settings.HashRecoveryCode(code)
}

func maskYAMLListValue(content, key string) string {
	// Delegate to the sub-package implementation via inline copy.
	var result strings.Builder
	inList := false
	keyIndent := 0
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if !inList {
			if strings.HasPrefix(trimmed, key+":") {
				idx := strings.Index(line, key+":")
				rest := strings.TrimSpace(line[idx+len(key)+1:])
				if rest == "" {
					inList = true
					keyIndent = idx
					result.WriteString(line)
				} else {
					result.WriteString(line[:idx] + key + `: "********"`)
				}
				result.WriteByte('\n')
				continue
			}
			result.WriteString(line)
			result.WriteByte('\n')
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if trimmed != "" && indent > keyIndent && strings.HasPrefix(trimmed, "-") {
			result.WriteString(line[:indent] + `- "********"`)
			result.WriteByte('\n')
			continue
		}
		inList = false
		result.WriteString(line)
		result.WriteByte('\n')
	}
	return strings.TrimSuffix(result.String(), "\n")
}
