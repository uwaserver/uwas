package notify

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
)

// SMTPRelay sends transactional emails via an SMTP server.
type SMTPRelay struct {
	Host     string // SMTP host (e.g. smtp.gmail.com)
	Port     int    // SMTP port (587 for TLS, 465 for SSL, 25 for plain)
	Username string
	Password string
	From     string // sender address
}

// Send sends an email through the configured SMTP relay.
func (r *SMTPRelay) Send(to, subject, body string) error {
	if r.Host == "" || r.From == "" {
		return fmt.Errorf("SMTP relay not configured (host=%q, from=%q)", r.Host, r.From)
	}

	addr := fmt.Sprintf("%s:%d", r.Host, r.Port)

	msg := "From: " + r.From + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n" +
		"\r\n" +
		body

	recipients := strings.Split(to, ",")
	for i := range recipients {
		recipients[i] = strings.TrimSpace(recipients[i])
	}

	// Port 465: implicit TLS (SMTPS)
	if r.Port == 465 {
		return r.sendTLS(addr, recipients, msg)
	}

	// Port 587/25: STARTTLS or plain
	var auth smtp.Auth
	if r.Username != "" {
		auth = smtp.PlainAuth("", r.Username, r.Password, r.Host)
	}
	return smtp.SendMail(addr, auth, r.From, recipients, []byte(msg))
}

func (r *SMTPRelay) sendTLS(addr string, recipients []string, msg string) error {
	tlsConfig := &tls.Config{ServerName: r.Host}
	conn, err := tls.Dial("tcp", addr, tlsConfig)
	if err != nil {
		return fmt.Errorf("TLS dial: %w", err)
	}
	defer conn.Close()

	host, _, _ := net.SplitHostPort(addr)
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("SMTP client: %w", err)
	}
	defer client.Close()

	if r.Username != "" {
		auth := smtp.PlainAuth("", r.Username, r.Password, r.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP auth: %w", err)
		}
	}

	if err := client.Mail(r.From); err != nil {
		return fmt.Errorf("SMTP MAIL: %w", err)
	}
	for _, rcpt := range recipients {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("SMTP RCPT %s: %w", rcpt, err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA: %w", err)
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return fmt.Errorf("SMTP write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("SMTP close data: %w", err)
	}
	return client.Quit()
}
