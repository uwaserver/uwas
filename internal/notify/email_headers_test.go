package notify

// F30000: the alert e-mail had no Date, Message-ID or MIME headers (RFC 5322
// requires Date; servers and spam filters reject or penalise the message), a
// non-ASCII title went out as a raw 8-bit Subject, and a "Name <addr>" sender
// was used verbatim as the SMTP envelope sender (MAIL FROM:<Name <addr>>).

import (
	"net/mail"
	"net/smtp"
	"strings"
	"testing"
)

func captureEmail(t *testing.T, from, title string) (envFrom string, m *mail.Message) {
	t.Helper()
	orig, origHost := smtpSendMailFn, notifyHostSafetyCheck
	t.Cleanup(func() { smtpSendMailFn, notifyHostSafetyCheck = orig, origHost })
	notifyHostSafetyCheck = func(string) error { return nil }
	var raw []byte
	smtpSendMailFn = func(_ string, _ smtp.Auth, f string, _ []string, msg []byte) error {
		envFrom, raw = f, msg
		return nil
	}
	err := sendEmail(map[string]string{
		"smtp_host": "mail.example.com", "from": from, "to": "admin@example.com",
	}, Message{Level: "critical", Title: title, Body: "body", Source: "uwas"})
	if err != nil {
		t.Fatal(err)
	}
	m, err = mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("message does not parse: %v", err)
	}
	return envFrom, m
}

func TestEmailHasDateAndMessageID(t *testing.T) {
	_, m := captureEmail(t, "alerts@example.com", "Disk full")
	if _, err := m.Header.Date(); err != nil {
		t.Fatalf("PROBLEM CONFIRMED: no valid Date header: %v", err)
	}
	if id := m.Header.Get("Message-Id"); !strings.HasPrefix(id, "<") || !strings.HasSuffix(id, ">") {
		t.Fatalf("PROBLEM CONFIRMED: Message-ID %q", id)
	}
	if ct := m.Header.Get("Content-Type"); !strings.Contains(strings.ToLower(ct), "charset=utf-8") {
		t.Fatalf("PROBLEM CONFIRMED: Content-Type %q", ct)
	}
}

func TestEmailSubjectEncodedWhenNonASCII(t *testing.T) {
	_, m := captureEmail(t, "alerts@example.com", "Disk dolu: sunucu İstanbul")
	subj := m.Header.Get("Subject")
	for _, r := range subj {
		if r > 127 {
			t.Fatalf("PROBLEM CONFIRMED: raw non-ASCII Subject %q", subj)
		}
	}
	// control: ASCII subject is untouched
	_, m = captureEmail(t, "alerts@example.com", "Disk full")
	if got := m.Header.Get("Subject"); got != "[UWAS] CRITICAL: Disk full" {
		t.Fatalf("ascii subject changed: %q", got)
	}
}

func TestEmailEnvelopeSenderIsBareAddress(t *testing.T) {
	env, m := captureEmail(t, "UWAS Alerts <alerts@example.com>", "x")
	if env != "alerts@example.com" {
		t.Fatalf("PROBLEM CONFIRMED: envelope sender %q, want alerts@example.com", env)
	}
	if got := m.Header.Get("From"); got != "UWAS Alerts <alerts@example.com>" {
		t.Fatalf("From header changed: %q", got)
	}
	// control: bare address unchanged
	env, _ = captureEmail(t, "alerts@example.com", "x")
	if env != "alerts@example.com" {
		t.Fatalf("control envelope %q", env)
	}
}
