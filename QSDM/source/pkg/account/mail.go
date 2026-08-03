package account

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

type Mailer interface {
	SendMagicLink(ctx context.Context, recipient, link string) error
}

type SMTPMailer struct {
	host        string
	port        int
	username    string
	password    string
	from        string
	useTLS      bool
	dialTimeout time.Duration
}

func NewSMTPMailer(cfg Config) *SMTPMailer {
	return &SMTPMailer{
		host:        cfg.SMTPHost,
		port:        cfg.SMTPPort,
		username:    cfg.SMTPUsername,
		password:    cfg.SMTPPassword,
		from:        cfg.SMTPFrom,
		useTLS:      cfg.SMTPUseTLS,
		dialTimeout: 10 * time.Second,
	}
}

func rejectMailHeader(value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return errors.New("mail header contains a line break")
	}
	return nil
}

func parseMailbox(value string) (string, error) {
	if err := rejectMailHeader(value); err != nil {
		return "", err
	}
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address == "" {
		return "", errors.New("mailbox is invalid")
	}
	return address.Address, nil
}

func (m *SMTPMailer) SendMagicLink(ctx context.Context, recipient, link string) error {
	for _, value := range []string{recipient, m.from, link} {
		if err := rejectMailHeader(value); err != nil {
			return err
		}
	}
	envelopeFrom, err := parseMailbox(m.from)
	if err != nil {
		return fmt.Errorf("parse SMTP sender: %w", err)
	}
	envelopeRecipient, err := parseMailbox(recipient)
	if err != nil {
		return fmt.Errorf("parse SMTP recipient: %w", err)
	}
	address := net.JoinHostPort(m.host, fmt.Sprintf("%d", m.port))
	dialer := &net.Dialer{Timeout: m.dialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("connect to SMTP server: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))

	client, err := smtp.NewClient(conn, m.host)
	if err != nil {
		return fmt.Errorf("start SMTP client: %w", err)
	}
	defer client.Close()
	if m.useTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP server does not advertise STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{MinVersion: tls.VersionTLS12, ServerName: m.host}); err != nil {
			return fmt.Errorf("start SMTP TLS: %w", err)
		}
	}
	if m.username != "" {
		if err := client.Auth(smtp.PlainAuth("", m.username, m.password, m.host)); err != nil {
			return fmt.Errorf("authenticate to SMTP server: %w", err)
		}
	}
	if err := client.Mail(envelopeFrom); err != nil {
		return fmt.Errorf("set SMTP sender: %w", err)
	}
	if err := client.Rcpt(envelopeRecipient); err != nil {
		return fmt.Errorf("set SMTP recipient: %w", err)
	}
	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("open SMTP message: %w", err)
	}
	w := bufio.NewWriter(wc)
	body := "Use this one-time link to sign in to QSDM Account:\r\n\r\n" + link +
		"\r\n\r\nIf you did not request this, ignore this message. QSDM will never ask for your wallet passphrase or keystore.\r\n"
	headers := []string{
		"From: " + m.from,
		"To: " + recipient,
		"Subject: Sign in to QSDM Account",
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: 8bit",
		"",
		body,
	}
	if _, err := w.WriteString(strings.Join(headers, "\r\n")); err != nil {
		_ = wc.Close()
		return fmt.Errorf("write SMTP message: %w", err)
	}
	if err := w.Flush(); err != nil {
		_ = wc.Close()
		return fmt.Errorf("flush SMTP message: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("finish SMTP message: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("finish SMTP session: %w", err)
	}
	return nil
}
