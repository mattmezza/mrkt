package security

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func TestSNSVerifierRejectsForgedCallback(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	now := time.Unix(1700000000, 0).UTC()
	tpl := x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "sns.amazonaws.com"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, &tpl, &tpl, &key.PublicKey, key)
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	m := map[string]string{"Type": "Notification", "MessageId": "id", "TopicArn": "arn:aws:sns:eu-west-1:1:topic", "Message": "payload", "Timestamp": now.Format(time.RFC3339), "SignatureVersion": "2", "SigningCertURL": "https://sns.eu-west-1.amazonaws.com/cert.pem"}
	canonical := "Message\npayload\nMessageId\nid\nTimestamp\n" + m["Timestamp"] + "\nTopicArn\n" + m["TopicArn"] + "\nType\nNotification\n"
	h := sha256.Sum256([]byte(canonical))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, key, 0, h[:])
	m["Signature"] = base64.StdEncoding.EncodeToString(sig)
	body, _ := json.Marshal(m)
	v := SNSVerifier{TopicARN: m["TopicArn"], Now: func() time.Time { return now }, FetchCertificate: func(context.Context, string) ([]byte, error) { return cert, nil }}
	if _, err := v.Verify(context.Background(), body); err == nil {
		t.Fatal("signature with wrong hash identifier accepted")
	}
	sig, _ = rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
	m["Signature"] = base64.StdEncoding.EncodeToString(sig)
	body, _ = json.Marshal(m)
	if _, err := v.Verify(context.Background(), body); err != nil {
		t.Fatal(err)
	}
	m["Message"] = "forged"
	body, _ = json.Marshal(m)
	if _, err := v.Verify(context.Background(), body); err == nil {
		t.Fatal("forged callback accepted")
	}
}
