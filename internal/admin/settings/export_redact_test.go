package settings

import "testing"

func TestStripURLUserinfo(t *testing.T) {
	cases := map[string]string{
		"":                            "",
		"10.0.0.9:8080":               "10.0.0.9:8080",
		"http://10.0.0.9:8080":        "http://10.0.0.9:8080",
		"http://svc:pw@10.0.0.9:8080": "http://10.0.0.9:8080",
		"https://u:p@host/path?q=1#f": "https://host/path?q=1#f",
		"https://u:p@w@host/p@th":     "https://host/p@th",
		"unix:/run/app.sock":          "unix:/run/app.sock",
		"http://host/with@at/in/path": "http://host/with@at/in/path",
		"user:pw@10.0.0.9:8080":       "10.0.0.9:8080",
	}
	for in, want := range cases {
		if got := stripURLUserinfo(in); got != want {
			t.Errorf("stripURLUserinfo(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRedactWebhookURL(t *testing.T) {
	cases := map[string]string{
		"": "",
		"https://hooks.slack.com/services/T/B/tok": "https://hooks.slack.com/********",
		"https://discord.com/api/webhooks/1/tok":   "https://discord.com/********",
		"https://u:p@example.com/hook?token=abc":   "https://example.com/********",
		"https://example.com":                      "https://example.com",
		"https://example.com/":                     "https://example.com/",
		"https://example.com?token=abc":            "https://example.com/********",
		"not a url":                                "********",
	}
	for in, want := range cases {
		if got := redactWebhookURL(in); got != want {
			t.Errorf("redactWebhookURL(%q) = %q, want %q", in, got, want)
		}
	}
}
