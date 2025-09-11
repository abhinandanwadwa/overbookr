package mail

import (
	"crypto/tls"
	"fmt"

	gomail "gopkg.in/gomail.v2"
)

// Mailer holds SMTP dialer configuration.
type Mailer struct {
	Host     string
	Port     int
	Username string
	Password string

	// Optional: if true, Skip TLS verification (useful for self-signed dev SMTP).
	InsecureSkipVerify bool
}

// NewMailer creates a configured Mailer.
func NewMailer(host string, port int, username, password string) *Mailer {
	return &Mailer{
		Host:     host,
		Port:     port,
		Username: username,
		Password: password,
	}
}

func (m *Mailer) Send(from string, to []string, subject, body string, isHTML bool) error {
	if len(to) == 0 {
		return fmt.Errorf("no recipients provided")
	}

	msg := gomail.NewMessage()
	msg.SetHeader("From", from)
	msg.SetHeader("To", to...)
	msg.SetHeader("Subject", subject)

	if isHTML {
		msg.SetBody("text/html", body)
	} else {
		msg.SetBody("text/plain", body)
	}

	d := gomail.NewDialer(m.Host, m.Port, m.Username, m.Password)

	// Optional TLS config for self-signed certs / local servers.
	if m.InsecureSkipVerify {
		d.TLSConfig = &tls.Config{InsecureSkipVerify: true}
	}

	// Send
	if err := d.DialAndSend(msg); err != nil {
		return fmt.Errorf("failed to send mail: %w", err)
	}
	return nil
}
