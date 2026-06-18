// Package notify sends alerts via multiple channels (email, Slack, Telegram, webhook).
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"strings"
	"syscall"
	"time"

	"github.com/uwaserver/uwas/internal/config"
)

var (
	telegramAPIBase       = "https://api.telegram.org"
	smtpSendMailFn        = smtp.SendMail
	notifyURLSafetyCheck  = config.IsWebhookURLSafe
	notifyHostSafetyCheck = config.IsHostSafe
	// notifyDialControl validates the IP at dial time (DNS-rebinding protection).
	// Tests that target loopback servers set this to nil.
	notifyDialControl = config.SafeDialControl
	notifyHTTPClient  = &http.Client{
		Timeout: 10 * time.Second,
		// Re-validate every redirect hop against the SSRF policy so a
		// 302 Location: http://169.254.169.254/ can't smuggle the request to an
		// internal target after the initial URL passed the check.
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			return notifyURLSafetyCheck(req.URL.String())
		},
		// Validate the IP at dial time too — closes the DNS-rebinding window
		// where the hostname re-resolves to an internal address between the
		// pre-flight check and the actual connection. The indirection is
		// evaluated per-dial so notifyDialControl can be overridden in tests.
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: 10 * time.Second,
				Control: func(network, address string, c syscall.RawConn) error {
					if notifyDialControl == nil {
						return nil
					}
					return notifyDialControl(network, address, c)
				},
			}).DialContext,
		},
	}
)

// Channel is a notification destination.
type Channel struct {
	Type    string            `json:"type" yaml:"type"` // "webhook", "slack", "telegram", "email"
	Enabled bool              `json:"enabled" yaml:"enabled"`
	Config  map[string]string `json:"config" yaml:"config"`
}

// Message is the notification payload.
type Message struct {
	Level  string `json:"level"` // "info", "warning", "critical"
	Title  string `json:"title"`
	Body   string `json:"body"`
	Source string `json:"source"` // "cert_expiry", "domain_down", "backup_failed", etc.
}

// Send dispatches a message to a channel.
func Send(ch Channel, msg Message) error {
	if !ch.Enabled {
		return nil
	}
	switch ch.Type {
	case "webhook":
		return sendWebhook(ch.Config["url"], msg)
	case "slack":
		return sendSlack(ch.Config["webhook_url"], msg)
	case "telegram":
		return sendTelegram(ch.Config["bot_token"], ch.Config["chat_id"], msg)
	case "email":
		return sendEmail(ch.Config, msg)
	default:
		return fmt.Errorf("unknown channel type: %s", ch.Type)
	}
}

func sendWebhook(url string, msg Message) error {
	if strings.TrimSpace(url) == "" {
		return fmt.Errorf("webhook url is required")
	}

	// SSRF check
	if err := notifyURLSafetyCheck(url); err != nil {
		return fmt.Errorf("webhook URL not allowed: %w", err)
	}

	data, _ := json.Marshal(msg)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := notifyHTTPClient.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}

func sendSlack(webhookURL string, msg Message) error {
	if strings.TrimSpace(webhookURL) == "" {
		return fmt.Errorf("slack webhook_url is required")
	}

	// SSRF check
	if err := notifyURLSafetyCheck(webhookURL); err != nil {
		return fmt.Errorf("slack webhook URL not allowed: %w", err)
	}

	emoji := "ℹ️"
	switch msg.Level {
	case "warning":
		emoji = "⚠️"
	case "critical":
		emoji = "🚨"
	}
	payload := map[string]string{
		"text": fmt.Sprintf("%s *%s*\n%s\n_%s_", emoji, msg.Title, msg.Body, msg.Source),
	}
	data, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, webhookURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := notifyHTTPClient.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("slack webhook returned %d", resp.StatusCode)
	}
	return nil
}

func sendTelegram(botToken, chatID string, msg Message) error {
	emoji := "ℹ️"
	switch msg.Level {
	case "warning":
		emoji = "⚠️"
	case "critical":
		emoji = "🚨"
	}
	text := fmt.Sprintf("%s <b>%s</b>\n%s\n<i>%s</i>", emoji, msg.Title, msg.Body, msg.Source)
	url := fmt.Sprintf("%s/bot%s/sendMessage", telegramAPIBase, botToken)
	// Match the SSRF policy applied to the webhook and Slack channels.
	// telegramAPIBase is a package-level var (overridable by tests and any
	// future caller) so we cannot assume it is always api.telegram.org —
	// without this check a misconfiguration could turn the notify pipeline
	// into an internal-network probe.
	if err := notifyURLSafetyCheck(url); err != nil {
		return fmt.Errorf("telegram URL not allowed: %w", err)
	}
	payload := map[string]string{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
	}
	data, _ := json.Marshal(payload)
	resp, err := notifyHTTPClient.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("telegram API returned %d", resp.StatusCode)
	}
	return nil
}

func sendEmail(cfg map[string]string, msg Message) error {
	host := cfg["smtp_host"]
	port := cfg["smtp_port"]
	user := cfg["smtp_user"]
	pass := cfg["smtp_pass"]
	from := cfg["from"]
	to := cfg["to"]

	if host == "" || to == "" {
		return fmt.Errorf("email: smtp_host and to are required")
	}
	if err := notifyHostSafetyCheck(host); err != nil {
		return fmt.Errorf("email: %w", err)
	}
	if port == "" {
		port = "587"
	}
	if from == "" {
		from = user
	}

	subject := fmt.Sprintf("[UWAS] %s: %s", strings.ToUpper(msg.Level), msg.Title)
	body := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s\n\nSource: %s",
		from, to, subject, msg.Body, msg.Source)

	addr := host + ":" + port
	var auth smtp.Auth
	if user != "" {
		auth = smtp.PlainAuth("", user, pass, host)
	}
	return smtpSendMailFn(addr, auth, from, strings.Split(to, ","), []byte(body))
}
