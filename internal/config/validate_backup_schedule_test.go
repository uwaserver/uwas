package config

// F31303: backup.schedule and backup.cron were never validated. An unusable
// value ("daily", "24", "0 25 * * *") loaded fine and the scheduler quietly
// never ran, so the operator believed backups were happening. Startup does not
// even log the skip.

import (
	"strings"
	"testing"
)

func backupScheduleCfg(schedule, cron string) *Config {
	cfg := &Config{}
	cfg.Global.LogLevel, cfg.Global.LogFormat = "info", "text"
	cfg.Global.Backup = BackupConfig{
		Enabled: true, Provider: "local", Keep: 3, Schedule: schedule, Cron: cron,
	}
	return cfg
}

func TestBackupScheduleAndCronAreValidated(t *testing.T) {
	cases := []struct {
		name           string
		schedule, cron string
		wantErr        string // "" = must be accepted
	}{
		{"no schedule", "", "", ""},
		{"interval", "24h", "", ""},
		{"interval minutes", "90m", "", ""},
		{"cron nightly", "", "0 2 * * *", ""},
		{"cron steps and lists", "", "*/15 1-5,22 1,15 * 1-5", ""},
		{"cron sunday as 7", "", "30 3 * * 7", ""},
		{"cron wins, schedule ignored", "garbage", "0 2 * * *", ""},
		{"interval without unit", "24", "", "global.backup.schedule"},
		{"interval word", "daily", "", "global.backup.schedule"},
		{"interval zero", "0s", "", "global.backup.schedule"},
		{"interval negative", "-1h", "", "global.backup.schedule"},
		{"cron four fields", "", "0 2 * *", "global.backup.cron"},
		{"cron hour 25", "", "0 25 * * *", "global.backup.cron"},
		{"cron minute 60", "", "60 2 * * *", "global.backup.cron"},
		{"cron month 13", "", "0 2 1 13 *", "global.backup.cron"},
		{"cron day 0", "", "0 2 0 * *", "global.backup.cron"},
		{"cron weekday name", "", "0 2 * * mon", "global.backup.cron"},
		{"cron zero step", "", "*/0 * * * *", "global.backup.cron"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Validate(backupScheduleCfg(c.schedule, c.cron))
			t.Logf("EXPECTED: %q\nACTUAL: %v", c.wantErr, err)
			switch {
			case c.wantErr == "" && err != nil:
				t.Fatalf("rejected a usable schedule: %v", err)
			case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
				t.Fatalf("PROBLEM CONFIRMED: %q/%q accepted or wrong error: %v", c.schedule, c.cron, err)
			}
		})
	}
}

// A disabled backup block is not scheduled, so its leftovers must not block a
// load.
func TestBackupScheduleIgnoredWhenDisabled(t *testing.T) {
	cfg := backupScheduleCfg("daily", "0 25 * * *")
	cfg.Global.Backup.Enabled = false
	if err := Validate(cfg); err != nil {
		t.Fatalf("disabled backup validated its schedule: %v", err)
	}
}
