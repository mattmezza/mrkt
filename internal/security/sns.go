package security

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha1" // SNS SignatureVersion 1 is legacy SHA-1; verification only.
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var snsCertHost = regexp.MustCompile(`^sns\.[a-z0-9-]+\.amazonaws\.com(?:\.cn)?$`)

type SNSMessage struct{ Type, MessageID, TopicARN, Subject, Message, Timestamp, SignatureVersion, Signature, SigningCertURL string }
type SNSVerifier struct {
	TopicARN         string
	Now              func() time.Time
	MaxAge           time.Duration
	FetchCertificate func(context.Context, string) ([]byte, error)
}

func (v SNSVerifier) Verify(ctx context.Context, body []byte) (SNSMessage, error) {
	if len(body) > 1<<20 {
		return SNSMessage{}, fmt.Errorf("SNS body too large")
	}
	var raw struct {
		Type             string
		MessageId        string
		TopicArn         string
		Subject          string
		Message          string
		Timestamp        string
		SignatureVersion string
		Signature        string
		SigningCertURL   string
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return SNSMessage{}, fmt.Errorf("parse SNS: %w", err)
	}
	m := SNSMessage{raw.Type, raw.MessageId, raw.TopicArn, raw.Subject, raw.Message, raw.Timestamp, raw.SignatureVersion, raw.Signature, raw.SigningCertURL}
	if m.Type != "Notification" || m.MessageID == "" || m.TopicARN != v.TopicARN {
		return SNSMessage{}, fmt.Errorf("unexpected SNS notification")
	}
	at, err := time.Parse(time.RFC3339, m.Timestamp)
	if err != nil {
		return SNSMessage{}, fmt.Errorf("invalid SNS timestamp")
	}
	now := time.Now()
	if v.Now != nil {
		now = v.Now()
	}
	age := v.MaxAge
	if age <= 0 {
		age = 15 * time.Minute
	}
	if at.After(now.Add(time.Minute)) || now.Sub(at) > age {
		return SNSMessage{}, fmt.Errorf("stale SNS notification")
	}
	u, err := url.Parse(m.SigningCertURL)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !snsCertHost.MatchString(strings.ToLower(u.Hostname())) {
		return SNSMessage{}, fmt.Errorf("invalid SNS signing certificate URL")
	}
	fetch := v.FetchCertificate
	if fetch == nil {
		fetch = fetchSNSCertificate
	}
	certPEM, err := fetch(ctx, m.SigningCertURL)
	if err != nil {
		return SNSMessage{}, err
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return SNSMessage{}, fmt.Errorf("invalid SNS certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return SNSMessage{}, fmt.Errorf("parse SNS certificate: %w", err)
	}
	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return SNSMessage{}, fmt.Errorf("SNS certificate is not RSA")
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil {
		return SNSMessage{}, fmt.Errorf("invalid SNS signature encoding")
	}
	canonical := "Message\n" + m.Message + "\nMessageId\n" + m.MessageID + "\n"
	if m.Subject != "" {
		canonical += "Subject\n" + m.Subject + "\n"
	}
	canonical += "Timestamp\n" + m.Timestamp + "\nTopicArn\n" + m.TopicARN + "\nType\n" + m.Type + "\n"
	var digest []byte
	var alg crypto.Hash
	switch m.SignatureVersion {
	case "1":
		h := sha1.Sum([]byte(canonical))
		digest = h[:]
		alg = crypto.SHA1
	case "2":
		h := sha256.Sum256([]byte(canonical))
		digest = h[:]
		alg = crypto.SHA256
	default:
		return SNSMessage{}, fmt.Errorf("unsupported SNS signature version")
	}
	if err = rsa.VerifyPKCS1v15(pub, alg, digest, sig); err != nil {
		return SNSMessage{}, fmt.Errorf("invalid SNS signature")
	}
	return m, nil
}
func fetchSNSCertificate(ctx context.Context, raw string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	client := http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch SNS certificate: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch SNS certificate HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	return b, nil
}
