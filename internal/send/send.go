// Package send sends mail through the SMTP server of an account. It has no
// dependency on the UI.
package send

import (
	"errors"
	"fmt"
	"net"
	"slices"
	"unicode/utf8"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/nomad-software/apemail/internal/account"
	"github.com/nomad-software/apemail/internal/remote"
)

// ErrUnreachable is the error of mail that could not be sent because the
// server could not be reached, which can be tried again later.
var ErrUnreachable = errors.New("the outgoing server could not be reached")

// dial connects to the SMTP server and logs in, if the account has a user
// name to log in with.
func dial(s account.Server) (*smtp.Client, error) {
	if s.Host == "" {
		return nil, errors.New("the account has no outgoing server")
	}
	config, err := remote.TLSConfig(s)
	if err != nil {
		return nil, err
	}

	var c *smtp.Client
	switch s.Security {
	case account.SecurityTLS:
		c, err = smtp.DialTLS(s.Address(), config)
	case account.SecurityStartTLS:
		c, err = smtp.DialStartTLS(s.Address(), config)
	case account.SecurityNone:
		c, err = smtp.Dial(s.Address())
	default:
		return nil, fmt.Errorf("%q is not a kind of security", s.Security)
	}
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) {
			return nil, fmt.Errorf("%w: connecting to %s: %w", ErrUnreachable, s.Address(), err)
		}
		return nil, fmt.Errorf("connecting to %s: %w", s.Address(), err)
	}
	// The greeting is checked before logging in, so that a server that
	// turns the client away says so rather than seeming to lack every way
	// of logging in.
	if err := c.Noop(); err != nil {
		c.Close()
		return nil, fmt.Errorf("greeting %s: %w", s.Address(), err)
	}

	if err := login(c, s); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// login authenticates with the server as the account says to.
func login(c *smtp.Client, s account.Server) error {
	var a sasl.Client
	switch s.Auth {
	case account.AuthPassword:
		if s.Username == "" {
			// The server is used without logging in, as a relay that
			// trusts where it is reached from.
			return nil
		}
		switch {
		case c.SupportsAuth("PLAIN"):
			a = sasl.NewPlainClient("", s.Username, s.Password)
		case c.SupportsAuth("LOGIN"):
			a = sasl.NewLoginClient(s.Username, s.Password)
		default:
			return errors.New("the server does not support logging in with a password; it may need SSL/TLS or STARTTLS")
		}
	case account.AuthEncryptedPassword:
		if !c.SupportsAuth("CRAM-MD5") {
			return errors.New("the server does not support encrypted passwords (CRAM-MD5)")
		}
		a = remote.NewCramMD5Client(s.Username, s.Password)
	case account.AuthTLSCert:
		if !c.SupportsAuth("EXTERNAL") {
			// The certificate was given in the handshake, which some
			// servers take as enough.
			return nil
		}
		a = sasl.NewExternalClient("")
	default:
		return fmt.Errorf("%q is not a kind of authentication that is supported", s.Auth)
	}
	if err := c.Auth(a); err != nil {
		return fmt.Errorf("logging in: %w", err)
	}
	return nil
}

// Send sends the message from the address to the recipients. dsn asks the
// server for a delivery status notification (RFC 3461) for each recipient,
// when the message is delivered or cannot be, with the headers of the
// message. A server without DSN sends the message without one.
func Send(s account.Server, from string, to []string, raw []byte, dsn bool) error {
	if len(to) == 0 {
		return errors.New("the message has no recipients")
	}
	c, err := dial(s)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := sendMail(c, from, to, raw, dsn); err != nil {
		return fmt.Errorf("sending: %w", err)
	}
	return c.Quit()
}

// sendMail sends the message, as smtp.Client.SendMail does, asking for
// delivery status notifications if dsn is true. The library leaves out what
// is asked for of a server without DSN.
func sendMail(c *smtp.Client, from string, to []string, raw []byte, dsn bool) error {
	mailOptions := &smtp.MailOptions{UTF8: !isASCII(from) || slices.ContainsFunc(to, func(a string) bool { return !isASCII(a) })}
	var rcptOptions *smtp.RcptOptions
	if dsn {
		mailOptions.Return = smtp.DSNReturnHeaders
		rcptOptions = &smtp.RcptOptions{Notify: []smtp.DSNNotify{smtp.DSNNotifySuccess, smtp.DSNNotifyFailure}}
	}
	if err := c.Mail(from, mailOptions); err != nil {
		return err
	}
	for _, addr := range to {
		if err := c.Rcpt(addr, rcptOptions); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(raw); err != nil {
		return err
	}
	return w.Close()
}

// isASCII says if s is all ASCII, which an address must be for a server
// without SMTPUTF8.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// Test connects to the server and logs in, and then leaves without sending
// anything.
func Test(s account.Server) error {
	c, err := dial(s)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Quit()
}
