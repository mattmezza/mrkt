package mail

import (
	"context"
	"errors"
	smtp "github.com/emersion/go-smtp"
	"strings"
	"testing"
)

func testMessage() Message {
	return Message{ID: "msg.example", From: "Sender <sender@example.com>", To: "User <user@example.net>", Subject: "Héllo", Text: "plain", HTML: "<p>html</p>", UnsubscribeURL: "https://example.com/u/1", Attachments: []Attachment{{Name: "report €.txt", ContentType: "text/plain", Data: []byte("abc")}}}
}
func TestBuildMIMEHeadersAndBounds(t *testing.T) {
	b, err := BuildMIME(testMessage(), 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"multipart/alternative", "List-Unsubscribe-Post: List-Unsubscribe=One-Click", "filename*=utf-8''report%20%E2%82%AC.txt"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q", want)
		}
	}
	m := testMessage()
	m.Subject = "bad\r\nBcc: victim@example.org"
	if _, err := BuildMIME(m, 1<<20); err == nil {
		t.Fatal("accepted injected header")
	}
	if _, err := BuildMIME(testMessage(), 100); err == nil {
		t.Fatal("accepted oversized encoded message")
	}
}
func TestSMTPClassification(t *testing.T) {
	r, _ := classified(&smtp.SMTPError{Code: 550, Message: "no"}, false)
	if r.State != StatePermanent {
		t.Fatal(r)
	}
	r, _ = classified(errors.New("timeout"), false)
	if r.State != StateTransient {
		t.Fatal(r)
	}
	r, _ = classified(&smtp.SMTPError{Code: 550}, true)
	if r.State != StateUncertain {
		t.Fatal(r)
	}
}
func TestPlainSMTPRequiresDevelopment(t *testing.T) {
	if _, err := NewSMTP(SMTPConfig{Host: "localhost", Port: 1025, TLSMode: "plain"}); err == nil {
		t.Fatal("plain production SMTP accepted")
	}
}

func TestProbeRejectsUnsafeConfigWithoutDial(t *testing.T) {
	if err := ProbeSMTP(context.Background(), SMTPConfig{Host: "localhost", Port: 1025, TLSMode: "plain"}); err == nil {
		t.Fatal("unsafe probe config accepted")
	}
}
