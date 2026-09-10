package security

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestSecretBoxContextAndTamper(t *testing.T) {
	b, err := NewSecretBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	v, err := b.Seal([]byte("secret"), "project:a")
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.Open(v, "project:a")
	if err != nil || string(got) != "secret" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := b.Open(v, "project:b"); err == nil {
		t.Fatal("context substitution accepted")
	}
	if _, err := b.Open(v[:len(v)-1]+"A", "project:a"); err == nil {
		t.Fatal("tamper accepted")
	}
}

func TestWebhookDevelopmentExplicitHostAndSignature(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		if r.Header.Get("X-Mrkt-Signature") == "" {
			t.Error("missing signature")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	w := WebhookClient{AllowHTTPDevelopment: true, DevelopmentAllowedHosts: []string{u.Host}}
	status, err := w.Post(context.Background(), srv.URL+"/hook", []byte(`{"safe":true}`), []byte("secret"), "event", "attempt")
	if err != nil || status != http.StatusNoContent || !bytes.Equal(body, []byte(`{"safe":true}`)) {
		t.Fatalf("status=%d body=%s err=%v", status, body, err)
	}
	w.DevelopmentAllowedHosts = nil
	if _, err = w.Post(context.Background(), srv.URL, nil, []byte("secret"), "event", "attempt"); err == nil {
		t.Fatal("private development host accepted without explicit allowlist")
	}
}
func TestWebhookSignature(t *testing.T) {
	now := time.Unix(1700000000, 0)
	p := []byte(`{"x":1}`)
	sig := SignWebhook([]byte("key"), p, now.Unix())
	if !VerifyWebhook([]byte("key"), p, now.Unix(), sig, now, time.Minute) {
		t.Fatal("valid signature rejected")
	}
	if VerifyWebhook([]byte("key"), []byte(`{}`), now.Unix(), sig, now, time.Minute) {
		t.Fatal("changed payload accepted")
	}
	if VerifyWebhook([]byte("key"), p, now.Add(-2*time.Minute).Unix(), sig, now, time.Minute) {
		t.Fatal("stale accepted")
	}
}

type fixedResolver struct{ ips []net.IP }

func (f fixedResolver) LookupIP(context.Context, string, string) ([]net.IP, error) { return f.ips, nil }
func TestWebhookRejectsPrivateResolution(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "::1", "fc00::1"} {
		w := WebhookClient{Resolver: fixedResolver{[]net.IP{net.ParseIP(ip)}}}
		if _, err := w.Post(context.Background(), "https://example.com/hook", nil, []byte("x"), "e", "a"); err == nil {
			t.Errorf("accepted %s", ip)
		}
	}
}
