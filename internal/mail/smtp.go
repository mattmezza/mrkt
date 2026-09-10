package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"strconv"
	"time"

	"github.com/emersion/go-sasl"
	smtp "github.com/emersion/go-smtp"
)

type SMTPConfig struct {
	Host               string
	Port               int
	Username, Password string
	TLSMode            string
	InsecureSkipVerify bool
	Development        bool
	MaxEncodedSize     int64
	Timeout            time.Duration
}
type SMTP struct{ cfg SMTPConfig }

// ProbeSMTP establishes the configured TLS session, authenticates, and issues
// NOOP. It never submits MAIL, RCPT, or DATA and therefore cannot send mail.
func ProbeSMTP(ctx context.Context, cfg SMTPConfig) error {
	s, err := NewSMTP(cfg)
	if err != nil {
		return err
	}
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	d := net.Dialer{Timeout: s.cfg.Timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("SMTP connect: %w", err)
	}
	defer conn.Close()
	deadline := time.Now().Add(s.cfg.Timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)
	tlsCfg := &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12, InsecureSkipVerify: s.cfg.InsecureSkipVerify}
	var c *smtp.Client
	switch s.cfg.TLSMode {
	case "tls":
		tlsConn := tls.Client(conn, tlsCfg)
		if err = tlsConn.HandshakeContext(ctx); err == nil {
			c = smtp.NewClient(tlsConn)
			err = c.Hello("localhost")
		}
	case "starttls":
		c, err = smtp.NewClientStartTLS(conn, tlsCfg)
	default:
		c = smtp.NewClient(conn)
		err = c.Hello("localhost")
	}
	if err != nil {
		return fmt.Errorf("SMTP handshake: %w", err)
	}
	defer c.Close()
	c.CommandTimeout = s.cfg.Timeout
	c.SubmissionTimeout = s.cfg.Timeout
	if s.cfg.Username != "" {
		if err = c.Auth(sasl.NewPlainClient("", s.cfg.Username, s.cfg.Password)); err != nil {
			return fmt.Errorf("SMTP authentication: %w", err)
		}
	}
	if err = c.Noop(); err != nil {
		return fmt.Errorf("SMTP NOOP: %w", err)
	}
	_ = c.Quit()
	return nil
}

func NewSMTP(cfg SMTPConfig) (*SMTP, error) {
	if cfg.Host == "" || cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("valid SMTP host and port required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.MaxEncodedSize <= 0 {
		cfg.MaxEncodedSize = DefaultMaxEncodedSize
	}
	switch cfg.TLSMode {
	case "tls", "starttls":
	case "plain":
		if !cfg.Development {
			return nil, fmt.Errorf("plain SMTP is development-only")
		}
	default:
		return nil, fmt.Errorf("tls_mode must be tls, starttls, or plain")
	}
	if cfg.InsecureSkipVerify && !cfg.Development {
		return nil, fmt.Errorf("insecure TLS verification is development-only")
	}
	return &SMTP{cfg}, nil
}
func (s *SMTP) Send(ctx context.Context, m Message) (Result, error) {
	raw, err := BuildMIME(m, s.cfg.MaxEncodedSize)
	if err != nil {
		return Result{StatePermanent, err.Error()}, err
	}
	from, _ := mail.ParseAddress(m.From)
	to, _ := mail.ParseAddress(m.To)
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	d := net.Dialer{Timeout: s.cfg.Timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return classified(err, false)
	}
	defer conn.Close()
	deadline := time.Now().Add(s.cfg.Timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)
	tlsCfg := &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12, InsecureSkipVerify: s.cfg.InsecureSkipVerify} // explicit dev-only above
	var c *smtp.Client
	switch s.cfg.TLSMode {
	case "tls":
		tlsConn := tls.Client(conn, tlsCfg)
		if err = tlsConn.HandshakeContext(ctx); err == nil {
			c = smtp.NewClient(tlsConn)
			err = c.Hello("localhost")
		}
	case "starttls":
		c, err = smtp.NewClientStartTLS(conn, tlsCfg)
	default:
		c = smtp.NewClient(conn)
		err = c.Hello("localhost")
	}
	if err != nil {
		return classified(err, false)
	}
	defer c.Close()
	c.CommandTimeout = s.cfg.Timeout
	c.SubmissionTimeout = s.cfg.Timeout
	if s.cfg.Username != "" {
		if err = c.Auth(sasl.NewPlainClient("", s.cfg.Username, s.cfg.Password)); err != nil {
			return classified(err, false)
		}
	}
	if err = c.Mail(from.Address, nil); err != nil {
		return classified(err, false)
	}
	if err = c.Rcpt(to.Address, nil); err != nil {
		return classified(err, false)
	}
	dc, err := c.Data()
	if err != nil {
		return classified(err, false)
	}
	if _, err = dc.Write(bytes.TrimSpace(raw)); err != nil {
		return classified(err, true)
	}
	if err = dc.Close(); err != nil {
		return classified(err, true)
	}
	_ = c.Quit()
	return Result{State: StateAccepted, Detail: "accepted by next SMTP server"}, nil
}
func classified(err error, dataStarted bool) (Result, error) {
	state := StateTransient
	if dataStarted {
		state = StateUncertain
	} else {
		var se *smtp.SMTPError
		if errors.As(err, &se) && se.Code >= 500 {
			state = StatePermanent
		}
	}
	return Result{State: state, Detail: err.Error()}, err
}
