package settings

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// F30301: GenRecoveryCodes and UseRecoveryCode changed the live config before
// persisting and answered 500 on a persist failure without undoing it. A failed
// rotation left the in-memory codes replaced by ones the caller never saw
// (locking them out of recovery until a restart), and a failed recovery left
// the code consumed and TOTP cleared in memory while the disk still required
// both. The live config must match what is on disk when the call fails.

type failPersistDeps struct{ stubDeps }

func (d *failPersistDeps) PersistConfig() error { return errors.New("disk full") }

func TestRecoveryCodesRolledBackWhenPersistFails(t *testing.T) {
	t.Run("generate", func(t *testing.T) {
		d := &failPersistDeps{}
		old := []string{hashRecoveryCode("a"), hashRecoveryCode("b")}
		d.cfg.Global.Admin.RecoveryCodes = append([]string(nil), old...)
		rec := httptest.NewRecorder()
		New(d).GenRecoveryCodes(rec, httptest.NewRequest("POST", "/", nil))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		if got := d.cfg.Global.Admin.RecoveryCodes; !reflect.DeepEqual(got, old) {
			t.Errorf("EXPECTED: old codes kept\nACTUAL: %v", got)
		}
	})
	t.Run("use", func(t *testing.T) {
		d := &failPersistDeps{}
		old := []string{hashRecoveryCode("a"), hashRecoveryCode("b")}
		d.cfg.Global.Admin.RecoveryCodes = append([]string(nil), old...)
		d.cfg.Global.Admin.TOTPSecret = "SECRET"
		rec := httptest.NewRecorder()
		New(d).UseRecoveryCode(rec, httptest.NewRequest("POST", "/", strings.NewReader(`{"code":"a"}`)))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		if got := d.cfg.Global.Admin.RecoveryCodes; !reflect.DeepEqual(got, old) {
			t.Errorf("EXPECTED: code not consumed\nACTUAL: %v", got)
		}
		if d.cfg.Global.Admin.TOTPSecret != "SECRET" {
			t.Errorf("EXPECTED: TOTP secret kept\nACTUAL: %q", d.cfg.Global.Admin.TOTPSecret)
		}
	})
	t.Run("control: successful persist applies", func(t *testing.T) {
		d := &stubDeps{}
		d.cfg.Global.Admin.RecoveryCodes = []string{hashRecoveryCode("a"), hashRecoveryCode("b")}
		d.cfg.Global.Admin.TOTPSecret = "SECRET"
		rec := httptest.NewRecorder()
		New(d).UseRecoveryCode(rec, httptest.NewRequest("POST", "/", strings.NewReader(`{"code":"a"}`)))
		if rec.Code != http.StatusOK || len(d.cfg.Global.Admin.RecoveryCodes) != 1 || d.cfg.Global.Admin.TOTPSecret != "" {
			t.Fatalf("control broken: %d codes=%d totp=%q", rec.Code, len(d.cfg.Global.Admin.RecoveryCodes), d.cfg.Global.Admin.TOTPSecret)
		}
	})
}
