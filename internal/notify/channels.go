// Package notify sends alerts via multiple channels (email, Slack, Telegram, webhook).
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/smtp"
	"strings"
	"time"
)

var (
	telegramAPIBase  = "https://api.telegram.org"
	smtpSendMailFn   = smtp.SendMail
	notifyHTTPClient = &http.Client{
		Timeout: 10 * time.Second,
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
