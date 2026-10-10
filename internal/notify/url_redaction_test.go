package notify

import (
	"net"
	"strings"
	"testing"
)

// Channel URLs are credentials (Telegram /bot<TOKEN>/, Slack hook path,
// ?token= on webhooks). A transport failure must not echo them: callers log
// the error and the admin notify test returns it to the client.
func TestChannelErrorsDoNotLeakURL(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := "http://" + ln.Addr().String()
	ln.Close()

	oldBase := telegramAPIBase
	defer func() { telegramAPIBase = oldBase }()
	telegramAPIBase = closed
	resetServerIdentityForTest()

	cases := []struct {
		name, secret string
		ch           Channel
	}{
		{"telegram", "123:TGSECRET", Channel{Type: "telegram", Enabled: true, Config: map[string]string{"bot_token": "123:TGSECRET", "chat_id": "1"}}},
		{"slack", "SLACKSECRET", Channel{Type: "slack", Enabled: true, Config: map[string]string{"webhook_url": closed + "/services/T/B/SLACKSECRET"}}},
		{"webhook", "HOOKSECRET", Channel{Type: "webhook", Enabled: true, Config: map[string]string{"url": closed + "/h?token=HOOKSECRET"}}},
	}
	for _, c := range cases {
		err := Send(c.ch, Message{Title: "t"})
		if err == nil {
			t.Fatalf("%s: expected a dial error", c.name)
		}
		if strings.Contains(err.Error(), c.secret) {
			t.Errorf("%s: error leaks the channel secret: %v", c.name, err)
		}
	}
}
