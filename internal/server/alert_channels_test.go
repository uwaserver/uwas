package server

import (
	"testing"

	"github.com/uwaserver/uwas/internal/config"
)

// TestAlertChannelsFromConfig is the wiring test for alert delivery. The panel
// offered Slack, Telegram and SMTP fields under Alerting, the settings API
// stored them, and nothing ever delivered to any of them: internal/alerting
// only knew about the generic webhook and never imported internal/notify,
// where all three are implemented.
func TestAlertChannelsFromConfig(t *testing.T) {
	tests := []struct {
		name  string
		cfg   config.AlertingConfig
		types []string
	}{
		{"nothing configured", config.AlertingConfig{}, nil},
		{"slack", config.AlertingConfig{SlackURL: "https://hooks.slack.com/x"}, []string{"slack"}},
		{
			"telegram needs both halves",
			config.AlertingConfig{TelegramToken: "123:abc"},
			nil,
		},
		{
			"telegram",
			config.AlertingConfig{TelegramToken: "123:abc", TelegramChatID: "-100"},
			[]string{"telegram"},
		},
		{
			"email needs a recipient",
			config.AlertingConfig{EmailSMTP: "smtp.example.com:587"},
			nil,
		},
		{
			"email",
			config.AlertingConfig{EmailSMTP: "smtp.example.com:587", EmailTo: "a@b.c"},
			[]string{"email"},
		},
		{
			"all three",
			config.AlertingConfig{
				SlackURL:       "https://hooks.slack.com/x",
				TelegramToken:  "123:abc",
				TelegramChatID: "-100",
				EmailSMTP:      "smtp.example.com:587",
				EmailTo:        "a@b.c",
			},
			[]string{"slack", "telegram", "email"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := alertChannels(tt.cfg)
			if len(got) != len(tt.types) {
				t.Fatalf("got %d channels, want %d: %+v", len(got), len(tt.types), got)
			}
			for i, want := range tt.types {
				if got[i].Type != want {
					t.Errorf("channel %d type = %q, want %q", i, got[i].Type, want)
				}
				if !got[i].Enabled {
					t.Errorf("channel %q is not enabled", got[i].Type)
				}
			}
		})
	}
}

// TestAlertChannelsSplitsSMTPHostPort covers the panel's "smtp.gmail.com:587"
// shape, and the bare-host fallback so a missing port is not a hard failure.
func TestAlertChannelsSplitsSMTPHostPort(t *testing.T) {
	ch := alertChannels(config.AlertingConfig{EmailSMTP: "smtp.gmail.com:2525", EmailTo: "a@b.c"})
	if len(ch) != 1 {
		t.Fatalf("got %d channels, want 1", len(ch))
	}
	if ch[0].Config["smtp_host"] != "smtp.gmail.com" || ch[0].Config["smtp_port"] != "2525" {
		t.Errorf("host/port = %q/%q, want smtp.gmail.com/2525",
			ch[0].Config["smtp_host"], ch[0].Config["smtp_port"])
	}

	ch = alertChannels(config.AlertingConfig{EmailSMTP: "smtp.gmail.com", EmailTo: "a@b.c"})
	if ch[0].Config["smtp_host"] != "smtp.gmail.com" || ch[0].Config["smtp_port"] != "587" {
		t.Errorf("bare host = %q/%q, want smtp.gmail.com/587",
			ch[0].Config["smtp_host"], ch[0].Config["smtp_port"])
	}
}
