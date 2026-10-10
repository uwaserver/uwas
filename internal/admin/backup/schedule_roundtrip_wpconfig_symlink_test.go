package backup

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uwaserver/uwas/internal/backup"
	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/logger"
	"github.com/uwaserver/uwas/internal/webhook"
)

type roundTripDeps struct {
	mgr  *backup.BackupManager
	root string
}

func (d *roundTripDeps) RequireAdmin(http.ResponseWriter, *http.Request) bool { return true }
func (d *roundTripDeps) RequirePin(http.ResponseWriter, *http.Request) bool   { return true }
func (d *roundTripDeps) RecordAudit(*http.Request, string, string, bool)      {}
func (d *roundTripDeps) ParsePagination(*http.Request) (int, int)             { return 50, 0 }
func (d *roundTripDeps) BackupManager() *backup.BackupManager                 { return d.mgr }
func (d *roundTripDeps) WebhookFire(webhook.EventType, map[string]any)        {}
func (d *roundTripDeps) DomainRoot(string) (string, bool)                     { return d.root, true }

func newRoundTripHandler(t *testing.T, store, root string) *Handler {
	t.Helper()
	mgr := backup.New(config.BackupConfig{Enabled: true, Local: config.BackupLocalConfig{Path: store}, Keep: 7}, logger.New("error", "text"))
	t.Cleanup(func() { mgr.ScheduleBackup(0) })
	return New(&roundTripDeps{mgr: mgr, root: root})
}

// The interval ScheduleGet reports ("1d", "2d", "30d") must be accepted back by
// SchedulePut, and a rejected PUT must not change the retention count.
func TestScheduleIntervalRoundTripsAndRejectedPutIsNoOp(t *testing.T) {
	put := func(h *Handler, body string) int {
		rec := httptest.NewRecorder()
		h.SchedulePut(rec, httptest.NewRequest(http.MethodPut, "/api/v1/backups/schedule", strings.NewReader(body)))
		return rec.Code
	}
	get := func(h *Handler) backup.ScheduleDetail {
		rec := httptest.NewRecorder()
		h.ScheduleGet(rec, httptest.NewRequest(http.MethodGet, "/api/v1/backups/schedule", nil))
		var d backup.ScheduleDetail
		if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	for _, iv := range []string{"24h", "48h", "168h", "720h"} {
		h := newRoundTripHandler(t, t.TempDir(), "")
		if c := put(h, `{"enabled":true,"interval":"`+iv+`","keep":5}`); c != http.StatusOK {
			t.Fatalf("set %s: status %d", iv, c)
		}
		reported := get(h).Interval
		if c := put(h, `{"enabled":true,"interval":"`+reported+`","keep":5}`); c != http.StatusOK {
			t.Errorf("set %s, GET reported %q, re-PUT status %d, want 200", iv, reported, c)
		}
	}

	h := newRoundTripHandler(t, t.TempDir(), "")
	before := get(h).Keep
	if c := put(h, `{"enabled":true,"interval":"bogus","keep":2}`); c != http.StatusBadRequest {
		t.Fatalf("bogus interval: status %d, want 400", c)
	}
	if after := get(h).Keep; after != before {
		t.Errorf("rejected PUT changed keep from %d to %d", before, after)
	}
}

// A tenant-planted wp-config.php symlink must not make the domain backup dump
// the database named in the symlink target.
func TestDomainBackupRefusesSymlinkedWPConfig(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\nfor a; do db=$a; done\necho \"-- dump of $db\"\n"
	if err := os.WriteFile(filepath.Join(bin, "mysqldump"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	victim := t.TempDir()
	if err := os.WriteFile(filepath.Join(victim, "wp-config.php"), []byte("<?php\ndefine('DB_NAME', 'victim_db');\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Symlink(filepath.Join(victim, "wp-config.php"), filepath.Join(root, "wp-config.php")); err != nil {
		t.Fatal(err)
	}
	store := t.TempDir()
	h := newRoundTripHandler(t, store, root)
	rec := httptest.NewRecorder()
	h.DomainBackup(rec, httptest.NewRequest(http.MethodPost, "/api/v1/backups/domain", strings.NewReader(`{"domain":"a.com"}`)))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status %d, want 422 (body %s)", rec.Code, rec.Body.String())
	}
	_ = filepath.Walk(store, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() || !strings.HasSuffix(p, ".tar.gz") {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil
		}
		tr := tar.NewReader(gz)
		for {
			hdr, err := tr.Next()
			if err != nil {
				break
			}
			if hdr.Name == "database/victim_db.sql" {
				t.Errorf("archive contains %s from the symlink target", hdr.Name)
			}
			_, _ = io.Copy(io.Discard, tr)
		}
		return nil
	})
}
