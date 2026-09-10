package mail

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"
)

const DefaultMaxEncodedSize int64 = 20 << 20

func BuildMIME(m Message, max int64) ([]byte, error) {
	if max <= 0 {
		max = DefaultMaxEncodedSize
	}
	from, err := mail.ParseAddress(m.From)
	if err != nil {
		return nil, fmt.Errorf("invalid from: %w", err)
	}
	to, err := mail.ParseAddress(m.To)
	if err != nil {
		return nil, fmt.Errorf("invalid to: %w", err)
	}
	if !validHeader(m.ID) || !validHeader(m.Subject) || !validHeader(m.UnsubscribeURL) {
		return nil, fmt.Errorf("header contains newline")
	}
	if m.ID == "" || m.Subject == "" {
		return nil, fmt.Errorf("id and subject are required")
	}
	boundary := randomBoundary()
	alt := randomBoundary()
	var b bytes.Buffer
	w := func(s string) error {
		if int64(b.Len()+len(s)) > max {
			return fmt.Errorf("encoded message exceeds %d bytes", max)
		}
		_, e := b.WriteString(s)
		return e
	}
	date := time.Now().UTC().Format(time.RFC1123Z)
	for _, h := range []string{"From: " + from.String() + "\r\n", "To: " + to.String() + "\r\n", "Subject: " + mime.QEncoding.Encode("utf-8", m.Subject) + "\r\n", "Message-ID: <" + m.ID + ">\r\n", "Date: " + date + "\r\n", "MIME-Version: 1.0\r\n"} {
		if err := w(h); err != nil {
			return nil, err
		}
	}
	if m.UnsubscribeURL != "" {
		if err := w("List-Unsubscribe: <" + m.UnsubscribeURL + ">\r\nList-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n"); err != nil {
			return nil, err
		}
	}
	if err := w("Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n\r\n--" + boundary + "\r\nContent-Type: multipart/alternative; boundary=\"" + alt + "\"\r\n"); err != nil {
		return nil, err
	}
	for _, p := range []struct{ ct, body string }{{"text/plain; charset=utf-8", m.Text}, {"text/html; charset=utf-8", m.HTML}} {
		if err := w("\r\n--" + alt + "\r\nContent-Type: " + p.ct + "\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n"); err != nil {
			return nil, err
		}
		var q bytes.Buffer
		qw := quotedprintable.NewWriter(&q)
		_, _ = qw.Write([]byte(p.body))
		_ = qw.Close()
		if err := w(q.String()); err != nil {
			return nil, err
		}
	}
	if err := w("\r\n--" + alt + "--\r\n"); err != nil {
		return nil, err
	}
	for _, a := range m.Attachments {
		if a.Name == "" || !validHeader(a.Name) || !validHeader(a.ContentType) {
			return nil, fmt.Errorf("invalid attachment metadata")
		}
		ct := a.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		enc := base64.StdEncoding.EncodeToString(a.Data)
		if err := w("\r\n--" + boundary + "\r\nContent-Type: " + ct + "\r\nContent-Disposition: attachment; filename*=utf-8''" + urlEncode(a.Name) + "\r\nContent-Transfer-Encoding: base64\r\n\r\n"); err != nil {
			return nil, err
		}
		for len(enc) > 76 {
			if err := w(enc[:76] + "\r\n"); err != nil {
				return nil, err
			}
			enc = enc[76:]
		}
		if err := w(enc + "\r\n"); err != nil {
			return nil, err
		}
	}
	if err := w("\r\n--" + boundary + "--\r\n"); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
func validHeader(v string) bool { return !strings.ContainsAny(v, "\r\n\x00") }
func randomBoundary() string {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
func urlEncode(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._", rune(c)) {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
