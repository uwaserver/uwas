package cronjob

import (
	"testing"
)

func TestParseCronLine(t *testing.T) {
	tests := []struct {
		input        string
		wantSchedule string
		wantCommand  string
	}{
		{
			"*/5 * * * * /usr/bin/php /var/www/cron.php",
			"*/5 * * * *",
			"/usr/bin/php /var/www/cron.php",
		},
		{
			"0 2 * * * /usr/local/bin/backup.sh --full",
			"0 2 * * *",
			"/usr/local/bin/backup.sh --full",
		},
		{
			"30 4 1 * * /bin/cleanup",
			"30 4 1 * *",
			"/bin/cleanup",
		},
		{
			// Too few fields — entire line becomes Command
			"short",
			"",
			"short",
		},
		{
			"",
			"",
			"",
		},
	}

	for _, tt := range tests {
		got := parseCronLine(tt.input)
		if got.Schedule != tt.wantSchedule {
			t.Errorf("parseCronLine(%q).Schedule = %q, want %q",
				tt.input, got.Schedule, tt.wantSchedule)
		}
		if got.Command != tt.wantCommand {
			t.Errorf("parseCronLine(%q).Command = %q, want %q",
				tt.input, got.Command, tt.wantCommand)
		}
	}
}

func TestUwasMarkerConstant(t *testing.T) {
	if uwasMarker == "" {
		t.Error("uwasMarker should not be empty")
	}
	if uwasMarker != "# UWAS managed" {
		t.Errorf("uwasMarker = %q, want %q", uwasMarker, "# UWAS managed")
	}
}

func TestJobStruct(t *testing.T) {
	job := Job{
		Schedule: "*/5 * * * *",
		Command:  "/usr/bin/php /var/www/cron.php",
		Domain:   "example.com",
		Comment:  "WordPress cron",
	}

	if job.Schedule != "*/5 * * * *" {
		t.Errorf("expected Schedule '*/5 * * * *', got %q", job.Schedule)
	}
	if job.Command != "/usr/bin/php /var/www/cron.php" {
		t.Errorf("expected Command, got %q", job.Command)
	}
	if job.Domain != "example.com" {
		t.Errorf("expected Domain 'example.com', got %q", job.Domain)
	}
	if job.Comment != "WordPress cron" {
		t.Errorf("expected Comment 'WordPress cron', got %q", job.Comment)
	}
}

func TestParseCronLineWithExtraSpaces(t *testing.T) {
	line := "  0  3  *  *  *  /usr/bin/run task  "
	got := parseCronLine(line)
	if got.Schedule != "0 3 * * *" {
		t.Errorf("parseCronLine with spaces: Schedule = %q, want %q", got.Schedule, "0 3 * * *")
	}
	if got.Command != "/usr/bin/run task" {
		t.Errorf("parseCronLine with spaces: Command = %q, want %q", got.Command, "/usr/bin/run task")
	}
}
