package smtp

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kungfusheep/mail/provider"
)

type Config struct {
	Server   string // e.g. "smtp.gmail.com:587"
	Email    string
	Password string
}

type SMTP struct {
	config Config
}

func New(cfg Config) *SMTP {
	return &SMTP{config: cfg}
}

// Send sends a message via SMTP. It generates a Message-ID and sets it on msg
// before sending, so the caller can use msg.MessageID to track the sent message.
func (s *SMTP) Send(msg *provider.Message) error {
	// generate Message-ID so we can track this message
	msg.MessageID = generateMessageID(s.config.Email)
	host, _, err := net.SplitHostPort(s.config.Server)
	if err != nil {
		return fmt.Errorf("parsing server address: %w", err)
	}

	auth := smtp.PlainAuth("", s.config.Email, s.config.Password, host)

	// build recipients list
	var to []string
	for _, a := range msg.To {
		to = append(to, a.Email)
	}
	for _, a := range msg.CC {
		to = append(to, a.Email)
	}
	for _, a := range msg.BCC {
		to = append(to, a.Email)
	}

	if len(to) == 0 {
		return fmt.Errorf("no recipients")
	}

	raw, err := buildMessage(s.config.Email, *msg)
	if err != nil {
		return err
	}

	// connect with STARTTLS
	conn, err := net.DialTimeout("tcp", s.config.Server, 10*time.Second)
	if err != nil {
		return fmt.Errorf("connecting: %w", err)
	}

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("creating client: %w", err)
	}
	defer c.Close()

	// starttls
	tlsConfig := &tls.Config{ServerName: host}
	if err := c.StartTLS(tlsConfig); err != nil {
		return fmt.Errorf("starttls: %w", err)
	}

	if err := c.Auth(auth); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	if err := c.Mail(s.config.Email); err != nil {
		return fmt.Errorf("mail from: %w", err)
	}

	for _, addr := range to {
		if err := c.Rcpt(addr); err != nil {
			return fmt.Errorf("rcpt to %s: %w", addr, err)
		}
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("data: %w", err)
	}

	if _, err := w.Write([]byte(raw)); err != nil {
		return fmt.Errorf("writing message: %w", err)
	}

	if err := w.Close(); err != nil {
		return fmt.Errorf("closing data: %w", err)
	}

	return c.Quit()
}

func buildMessage(from string, msg provider.Message) (string, error) {
	var b strings.Builder

	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + formatAddresses(msg.To) + "\r\n")
	if len(msg.CC) > 0 {
		b.WriteString("Cc: " + formatAddresses(msg.CC) + "\r\n")
	}
	b.WriteString("Subject: " + msg.Subject + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	if msg.MessageID != "" {
		b.WriteString("Message-ID: <" + msg.MessageID + ">\r\n")
	}

	if msg.InReplyTo != "" {
		b.WriteString("In-Reply-To: " + msg.InReplyTo + "\r\n")
	}
	if len(msg.References) > 0 {
		b.WriteString("References: " + strings.Join(msg.References, " ") + "\r\n")
	}

	b.WriteString("MIME-Version: 1.0\r\n")

	if len(msg.Attachments) > 0 {
		boundary := fmt.Sprintf("mixed_%d", time.Now().UnixNano())
		b.WriteString("Content-Type: multipart/mixed; boundary=" + boundary + "\r\n\r\n")
		b.WriteString("--" + boundary + "\r\n")
		writeMessageBody(&b, msg)
		for _, attachment := range msg.Attachments {
			if err := writeAttachmentPart(&b, boundary, attachment); err != nil {
				return "", err
			}
		}
		b.WriteString("--" + boundary + "--\r\n")
		return b.String(), nil
	}

	writeMessageBody(&b, msg)
	return b.String(), nil
}

func writeMessageBody(b *strings.Builder, msg provider.Message) {
	if msg.HTMLBody != "" {
		boundary := fmt.Sprintf("alternative_%d", time.Now().UnixNano())
		b.WriteString("Content-Type: multipart/alternative; boundary=" + boundary + "\r\n\r\n")

		if msg.TextBody != "" {
			b.WriteString("--" + boundary + "\r\n")
			b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
			b.WriteString(msg.TextBody + "\r\n")
		}

		b.WriteString("--" + boundary + "\r\n")
		b.WriteString("Content-Type: text/html; charset=UTF-8\r\n\r\n")
		b.WriteString(msg.HTMLBody + "\r\n")
		b.WriteString("--" + boundary + "--\r\n")
	} else {
		b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
		b.WriteString(msg.TextBody + "\r\n")
	}
}

func writeAttachmentPart(b *strings.Builder, boundary string, attachment provider.Attachment) error {
	if attachment.LocalPath == "" {
		return fmt.Errorf("attachment %q has no local path", attachment.Filename)
	}
	data, err := os.ReadFile(attachment.LocalPath)
	if err != nil {
		return fmt.Errorf("read attachment %s: %w", attachment.LocalPath, err)
	}
	filename := attachment.Filename
	if filename == "" {
		filename = filepath.Base(attachment.LocalPath)
	}
	contentType := attachment.ContentType
	if contentType == "" {
		contentType = mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: " + contentType + "; name=" + quoteHeader(filename) + "\r\n")
	b.WriteString("Content-Disposition: attachment; filename=" + quoteHeader(filename) + "\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	writeBase64Lines(b, data)
	b.WriteString("\r\n")
	return nil
}

func writeBase64Lines(b *strings.Builder, data []byte) {
	encoded := base64.StdEncoding.EncodeToString(data)
	for len(encoded) > 76 {
		b.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	if encoded != "" {
		b.WriteString(encoded + "\r\n")
	}
}

func quoteHeader(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func formatAddresses(addrs []provider.Address) string {
	var parts []string
	for _, a := range addrs {
		parts = append(parts, a.String())
	}
	return strings.Join(parts, ", ")
}

func generateMessageID(email string) string {
	b := make([]byte, 16)
	rand.Read(b)
	domain := email
	if at := strings.LastIndex(email, "@"); at >= 0 {
		domain = email[at+1:]
	}
	return fmt.Sprintf("%x.%x@%s", b[:8], b[8:], domain)
}
