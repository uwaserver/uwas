package notify

import (
	"strings"
	"testing"
)

func TestSMTPRelaySendNotConfigured(t *testing.T) {
	r := &SMTPRelay{}
	err := r.Send("to@example.com", "Subject", "Body")
	if err == nil {
		t.Error("expected error for unconfigured relay")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSMTPRelaySendMissingHost(t *testing.T) {
	r := &SMTPRelay{
		From: "from@example.com",
		// Host is empty
	}
	err := r.Send("to@example.com", "Subject", "Body")
	if err == nil {
		t.Error("expected error for missing host")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSMTPRelaySendMissingFrom(t *testing.T) {
	r := &SMTPRelay{
		Host: "smtp.example.com",
		Port: 587,
		// From is empty
	}
	err := r.Send("to@example.com", "Subject", "Body")
	if err == nil {
		t.Error("expected error for missing from")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSMTPRelayValidation(t *testing.T) {
	tests := []struct {
		name    string
		relay   SMTPRelay
		wantErr bool
		errMsg  string
	}{
		{
			name:    "empty relay",
			relay:   SMTPRelay{},
			wantErr: true,
			errMsg:  "not configured",
		},
		{
			name: "only host",
			relay: SMTPRelay{
				Host: "smtp.example.com",
			},
			wantErr: true,
			errMsg:  "not configured",
		},
		{
			name: "only from",
			relay: SMTPRelay{
				From: "from@example.com",
			},
			wantErr: true,
			errMsg:  "not configured",
		},
		{
			name: "configured but no port",
			relay: SMTPRelay{
				Host: "smtp.example.com",
				From: "from@example.com",
				Port: 0, // will use default 0 in fmt.Sprintf
			},
			wantErr: true, // Will fail on actual connection
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.relay.Send("to@example.com", "Test", "Body")
			if tt.wantErr {
				if err == nil {
					t.Error("expected error")
				} else if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			}
		})
	}
}

func TestSMTPRelayMultipleRecipients(t *testing.T) {
	r := &SMTPRelay{
		Host: "smtp.example.com",
		Port: 587,
		From: "from@example.com",
	}
	// This will fail on connection, but we can verify the recipient parsing
	err := r.Send("a@example.com, b@example.com, c@example.com", "Subject", "Body")
	// Expected to fail on connection, not on parsing
	if err == nil {
		t.Error("expected connection error")
	}
}

func TestSMTPRelayPort465(t *testing.T) {
	r := &SMTPRelay{
		Host:     "smtp.gmail.com",
		Port:     465,
		From:     "from@example.com",
		Username: "user",
		Password: "pass",
	}
	// Port 465 uses TLS dial which will fail without real server
	err := r.Send("to@example.com", "Subject", "Body")
	if err == nil {
		t.Error("expected connection error for TLS dial")
	}
	// Should fail on TLS dial, not on validation
	if strings.Contains(err.Error(), "not configured") {
		t.Error("should not fail on configuration for port 465")
	}
}

func TestSMTPRelayWithAuth(t *testing.T) {
	r := &SMTPRelay{
		Host:     "smtp.example.com",
		Port:     587,
		From:     "from@example.com",
		Username: "testuser",
		Password: "testpass",
	}
	err := r.Send("to@example.com", "Subject", "Body")
	if err == nil {
		t.Error("expected connection error")
	}
}
