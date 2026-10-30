package notify

import (
	"strings"
	"testing"
	"time"
)

// F49303: the e-mail channel checks smtp_host against the SSRF policy before
// sending (IsHostSafe resolves the name), then dialled with a plain net.Dialer:
// a name that answered with a public address for the check and with
// 169.254.169.254 (or any other blocked address) a moment later — DNS
// rebinding — was connected to. Webhook, Slack, Telegram, the alerter and the
// monitor already apply the policy to the address being dialled
// (SafeDialControl); the SMTP dial did not. The dial now refuses blocked
// addresses; loopback and private relays stay allowed, as IsHostSafe allows.

func TestSMTPDialRefusesABlockedAddressEvenWhenThePreflightPassed(t *testing.T) {
	origCheck, origTimeout := notifyHostSafetyCheck, smtpTimeout
	t.Cleanup(func() { notifyHostSafetyCheck, smtpTimeout = origCheck, origTimeout })
	// The pre-flight "passed": the name resolved to a public address then.
	notifyHostSafetyCheck = func(string) error { return nil }
	smtpTimeout = 400 * time.Millisecond // a missing fix ends by timing out; never waited on

	// A link-local address stands in for what the name resolves to at dial
	// time. Nothing is expected to answer there.
	err := sendEmail(map[string]string{
		"smtp_host": "169.254.1.1",
		"smtp_port": "25",
		"from":      "uwas@example.com",
		"to":        "ops@example.com",
	}, Message{Level: "critical", Title: "t", Body: "b", Source: "s"})

	t.Logf("EXPECTED: the dial is refused by the SSRF policy\nACTUAL: %v", err)
	if err == nil || !strings.Contains(err.Error(), "SSRF") {
		t.Fatalf("PROBLEM CONFIRMED: dialled a link-local address after the pre-flight passed (err = %v)", err)
	}
	t.Log("PROBLEM NOT REPRODUCED")
}

func TestSMTPDialRefusesUnspecifiedAndMetadataAddresses(t *testing.T) {
	origTimeout := smtpTimeout
	t.Cleanup(func() { smtpTimeout = origTimeout })
	smtpTimeout = 400 * time.Millisecond

	for _, addr := range []string{"0.0.0.0:25", "169.254.169.254:25", "[fe80::1]:25"} {
		err := sendMailBounded(addr, nil, "a@example.com", []string{"b@example.com"}, []byte("x"))
		if err == nil || !strings.Contains(err.Error(), "SSRF") {
			t.Errorf("%s: err = %v, want an SSRF refusal", addr, err)
		}
	}
}

// Control: a loopback relay (localhost:25 is a normal configuration) still
// receives the message.
func TestSMTPDialStillReachesALoopbackRelay(t *testing.T) {
	origTimeout := smtpTimeout
	t.Cleanup(func() { smtpTimeout = origTimeout })
	smtpTimeout = 2 * time.Second // deadlock detector only

	port, delivered := smtpServer(t, nil)
	if err := sendMailBounded("127.0.0.1:"+port, nil, "a@example.com", []string{"b@example.com"}, []byte("Subject: x\r\n\r\nloopback-relay\r\n")); err != nil {
		t.Fatalf("control: %v", err)
	}
	if got := <-delivered; !strings.Contains(got, "loopback-relay") {
		t.Fatalf("control message not delivered, got %q", got)
	}
}
