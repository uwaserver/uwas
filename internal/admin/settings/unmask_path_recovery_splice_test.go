package settings

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

type stubDeps struct {
	mu  sync.RWMutex
	cfg config.Config
}

func (d *stubDeps) RequireAdmin(http.ResponseWriter, *http.Request) bool { return true }
func (d *stubDeps) RequirePin(http.ResponseWriter, *http.Request) bool   { return true }
func (d *stubDeps) GetTOTPSecret() string                                { return "" }
func (d *stubDeps) ValidateTOTPCode(string) bool                         { return false }
func (d *stubDeps) RecordAudit(*http.Request, string, string, bool)      {}
func (d *stubDeps) LogError(string, ...any)                              {}
func (d *stubDeps) ConfigPtr() *config.Config                            { return &d.cfg }
func (d *stubDeps) LockConfig()                                          { d.mu.Lock() }
func (d *stubDeps) UnlockConfig()                                        { d.mu.Unlock() }
func (d *stubDeps) RLockConfig()                                         { d.mu.RLock() }
func (d *stubDeps) RUnlockConfig()                                       { d.mu.RUnlock() }
func (d *stubDeps) PersistConfig() error                                 { return nil }
func (d *stubDeps) ConfigPath() string                                   { return "" }
func (d *stubDeps) EnsureAuthManagerFromConfig()                         {}
func (d *stubDeps) AtomicWriteFile(string, []byte, os.FileMode) error    { return nil }
func (d *stubDeps) LockPersist()                                         {}
func (d *stubDeps) UnlockPersist()                                       {}
func (d *stubDeps) Reload() error                                        { return nil }
func (d *stubDeps) ToInt(any) int                                        { return 0 }
func (d *stubDeps) ParseDur(string) config.Duration                      { return config.Duration{} }
func (d *stubDeps) ByteSizeStr(config.ByteSize) string                   { return "" }
func (d *stubDeps) ParseBS(string) config.ByteSize                       { return 0 }

// Masked secrets restore from their own section. Adding a secret line of the
// same key in another section must not shift the mapping (which wrote the
// literal mask over the sftp password), and deleting one section's secret
// must not hand its value to another section.
func TestUnmaskYAMLValueMatchesBySection(t *testing.T) {
	old := "global:\n  cache:\n    enabled: true\n  backup:\n    sftp:\n      password: SFTPPASS\n"
	neu := "global:\n  cache:\n    enabled: true\n    redis:\n      password: NEWREDIS\n  backup:\n    sftp:\n      password: \"********\"\n"
	out := unmaskYAMLValue(neu, old, "password")
	if !strings.Contains(out, "      password: NEWREDIS\n") || !strings.Contains(out, "      password: SFTPPASS") {
		t.Errorf("added secret shifted another section's restore:\n%s", out)
	}

	old = "global:\n  cache:\n    redis:\n      password: REDISPASS\n  backup:\n    sftp:\n      password: SFTPPASS\n"
	neu = "global:\n  cache:\n    redis:\n      addr: x\n  backup:\n    sftp:\n      password: \"********\"\n"
	out = unmaskYAMLValue(neu, old, "password")
	if !strings.Contains(out, "password: SFTPPASS") || strings.Contains(out, "REDISPASS") {
		t.Errorf("deleted secret crossed into another section:\n%s", out)
	}
}

// UseRecoveryCode must replace the slice, not splice it in place: the admin
// persist path marshals a snapshot of the old slice header after RUnlock.
func TestUseRecoveryCodeDoesNotRewritePersistSnapshot(t *testing.T) {
	d := &stubDeps{}
	d.cfg.Global.Admin.RecoveryCodes = []string{hashRecoveryCode("a"), hashRecoveryCode("b"), hashRecoveryCode("c")}
	want := append([]string(nil), d.cfg.Global.Admin.RecoveryCodes...)
	d.RLockConfig()
	snap := d.cfg // what persistConfig holds after RUnlock
	d.RUnlockConfig()

	rec := httptest.NewRecorder()
	New(d).UseRecoveryCode(rec, httptest.NewRequest("POST", "/", strings.NewReader(`{"code":"a"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("UseRecoveryCode = %d: %s", rec.Code, rec.Body.String())
	}
	for i := range want {
		if snap.Global.Admin.RecoveryCodes[i] != want[i] {
			t.Fatalf("persist snapshot rewritten at index %d", i)
		}
	}
	if got := d.cfg.Global.Admin.RecoveryCodes; len(got) != 2 || got[0] != want[1] || got[1] != want[2] {
		t.Fatalf("live codes = %v, want [b c]", got)
	}
}
