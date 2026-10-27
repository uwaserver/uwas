// Package notify sends alerts via multiple channels (email, Slack, Telegram, webhook).
package notify

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/uwaserver/uwas/internal/config"
	"github.com/uwaserver/uwas/internal/serverip"
)

var (
	telegramAPIBase       = "https://api.telegram.org"
	smtpSendMailFn        = sendMailBounded
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

	// Overridable in tests.
	resolveHostname = os.Hostname
	resolveServerIP = serverip.PrimaryIPv4

	serverIDOnce sync.Once
	serverHost   string
	serverIP     string
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

// redactURLError removes the request URL from err's text. Channel URLs are
// credentials (Slack hook path, Telegram /bot<TOKEN>/, ?token= on webhooks),
// and *url.Error — returned by net/http transport/redirect failures and by
// url.Parse — prints the full URL. Callers log these errors and the admin
// notify test echoes them, so the URL must not survive in the message.
// Unwrap still yields the underlying cause (timeouts, dial errors).
func redactURLError(err error) error {
	var uerr *url.Error
	if err == nil || !errors.As(err, &uerr) {
		return err
	}
	msg := strings.ReplaceAll(err.Error(), strconv.Quote(uerr.URL), `"[redacted URL]"`)
	msg = strings.ReplaceAll(msg, uerr.URL, "[redacted URL]")
	return &redactedError{msg: msg, cause: uerr.Err}
}

type redactedError struct {
	msg   string
	cause error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.cause }

func sendWebhook(url string, msg Message) (err error) {
	defer func() { err = redactURLError(err) }()
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

func sendSlack(webhookURL string, msg Message) (err error) {
	defer func() { err = redactURLError(err) }()
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
		"text": fmt.Sprintf("%s *%s*\n%s\n_%s_", emoji, slackEscape(msg.Title), slackEscape(msg.Body), slackEscape(msg.Source)),
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

// slackEscape neutralises Slack's control sequences. Message text is mrkdwn:
// <!channel>, <@U123> and <https://evil|label> are interpreted unless &, < and >
// are escaped as &amp; &lt; &gt; (https://api.slack.com/reference/surfaces/formatting).
// Alert bodies carry text an operator did not write — a cron job's command —
// so without this a domain's cron command can ping the whole workspace or
// plant a disguised link (F9900).
func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func sendTelegram(botToken, chatID string, msg Message) (err error) {
	defer func() { err = redactURLError(err) }()
	emoji := "ℹ️"
	switch msg.Level {
	case "warning":
		emoji = "⚠️"
	case "critical":
		emoji = "🚨"
	}
	text := fmt.Sprintf("%s <b>%s</b>\n%s\n<i>%s</i>", emoji, html.EscapeString(msg.Title), html.EscapeString(msg.Body), html.EscapeString(msg.Source))
	if id := formatServerIdentity(); id != "" {
		// Identify which UWAS host sent this — useful when one bot/chat
		// receives alerts from multiple servers.
		text += "\n<code>" + html.EscapeString(id) + "</code>"
	}
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
	_, readErr := io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("telegram API returned %d", resp.StatusCode)
	}
	if readErr != nil {
		return fmt.Errorf("read telegram response: %w", readErr)
	}
	return nil
}

// formatServerIdentity returns "hostname · IP" (whichever parts are known).
// Cached for the process lifetime so alerts do not rescan interfaces.
func formatServerIdentity() string {
	serverIDOnce.Do(func() {
		if h, err := resolveHostname(); err == nil {
			serverHost = strings.TrimSpace(h)
		}
		serverIP = strings.TrimSpace(resolveServerIP())
	})
	switch {
	case serverHost != "" && serverIP != "":
		return serverHost + " · " + serverIP
	case serverHost != "":
		return serverHost
	case serverIP != "":
		return serverIP
	default:
		return ""
	}
}

// resetServerIdentityForTest clears the cached identity (tests only).
func resetServerIdentityForTest() {
	serverIDOnce = sync.Once{}
	serverHost = ""
	serverIP = ""
}

// smtpTimeout bounds one whole SMTP exchange (connect, greeting, STARTTLS, AUTH,
// DATA). smtp.SendMail has no deadline of its own, so an SMTP server that
// accepts the connection and then stays silent would pin the sending goroutine
// for good, one per alert (F13600).
var smtpTimeout = 20 * time.Second

// smtpImplicitTLSPort is the submission port that speaks TLS from the first
// byte (SMTPS); smtpTLSConfig builds the client TLS config. Both are
// overridable in tests.
var (
	smtpImplicitTLSPort = "465"
	smtpTLSConfig       = func(host string) *tls.Config { return &tls.Config{ServerName: host} }
)

// sendMailBounded is smtp.SendMail with a deadline on the connection: same
// sequence (EHLO, STARTTLS when offered, AUTH, MAIL, RCPT, DATA, QUIT).
func sendMailBounded(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
	for _, v := range append([]string{from}, to...) {
		if strings.ContainsAny(v, "\r\n") {
			return errors.New("smtp: A line must not contain CR or LF")
		}
	}
	if len(to) == 0 {
		return errors.New("smtp: no recipients")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	d := net.Dialer{Timeout: smtpTimeout}
	var conn net.Conn
	implicitTLS := port == smtpImplicitTLSPort
	if implicitTLS {
		conn, err = tls.DialWithDialer(&d, "tcp", addr, smtpTLSConfig(host))
	} else {
		conn, err = d.Dial("tcp", addr)
	}
	if err != nil {
		return err
	}
	if err := conn.SetDeadline(time.Now().Add(smtpTimeout)); err != nil {
		conn.Close()
		return err
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if err = c.Hello("localhost"); err != nil {
		return err
	}
	if ok, _ := c.Extension("STARTTLS"); ok && !implicitTLS {
		if err = c.StartTLS(smtpTLSConfig(host)); err != nil {
			return err
		}
	}
	if a != nil {
		if ok, _ := c.Extension("AUTH"); !ok {
			return errors.New("smtp: server doesn't support AUTH")
		}
		if err = c.Auth(a); err != nil {
			return err
		}
	}
	if err = c.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err = c.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err = w.Write(msg); err != nil {
		return err
	}
	if err = w.Close(); err != nil {
		return err
	}
	return c.Quit()
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

	// Trim recipients so " b@y.com" (after a ", ") is not sent as a literal
	// RCPT that SMTP servers reject, and drop empties.
	recipients := splitRecipients(to)
	if len(recipients) == 0 {
		return fmt.Errorf("email: no valid recipients")
	}

	// Strip CR/LF from header-interpolated fields to prevent SMTP header
	// injection (an extra header or a smuggled body via a crafted title/from).
	from = stripCRLF(from)
	subject := fmt.Sprintf("[UWAS] %s: %s", strings.ToUpper(msg.Level), stripCRLF(msg.Title))
	// The envelope sender must be a bare address; "Name <addr>" stays in From:.
	envFrom, msgIDHost := from, "localhost"
	if a, err := mail.ParseAddress(from); err == nil {
		envFrom = a.Address
		if i := strings.LastIndexByte(a.Address, '@'); i >= 0 {
			msgIDHost = a.Address[i+1:]
		}
	}
	var rnd [12]byte
	_, _ = rand.Read(rnd[:])
	body := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%s@%s>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s\n\nSource: %s",
		from, strings.Join(recipients, ", "), mime.QEncoding.Encode("utf-8", subject),
		time.Now().Format(time.RFC1123Z), hex.EncodeToString(rnd[:]), msgIDHost, msg.Body, msg.Source)

	addr := net.JoinHostPort(host, port)
	var auth smtp.Auth
	if user != "" {
		auth = smtp.PlainAuth("", user, pass, host)
	}
	return smtpSendMailFn(addr, auth, envFrom, recipients, []byte(body))
}

// stripCRLF removes carriage returns and line feeds so a value cannot break out
// of its SMTP header line.
func stripCRLF(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

// splitRecipients splits a comma-separated address list, trimming whitespace and
// CR/LF from each and dropping empties.
func splitRecipients(to string) []string {
	var out []string
	for _, r := range strings.Split(to, ",") {
		if r = strings.TrimSpace(stripCRLF(r)); r != "" {
			out = append(out, r)
		}
	}
	return out
}
