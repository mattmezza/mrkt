package security

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Resolver interface {
	LookupIP(context.Context, string, string) ([]net.IP, error)
}
type WebhookClient struct {
	Resolver                Resolver
	Timeout                 time.Duration
	AllowHTTPDevelopment    bool
	DevelopmentAllowedHosts []string
	MaxResponseBytes        int64
}

func SignWebhook(secret, payload []byte, timestamp int64) string {
	mac := hmac.New(sha256.New, secret)
	fmt.Fprintf(mac, "%d.", timestamp)
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}
func VerifyWebhook(secret, payload []byte, timestamp int64, signature string, now time.Time, maxAge time.Duration) bool {
	if maxAge <= 0 || timestamp > now.Add(time.Minute).Unix() || now.Sub(time.Unix(timestamp, 0)) > maxAge {
		return false
	}
	want := SignWebhook(secret, payload, timestamp)
	got, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	expected, _ := hex.DecodeString(want)
	return hmac.Equal(got, expected)
}
func (w WebhookClient) Post(ctx context.Context, rawURL string, payload, secret []byte, eventID, attemptID string) (int, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" || u.User != nil {
		return 0, fmt.Errorf("invalid webhook URL")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && w.AllowHTTPDevelopment) {
		return 0, fmt.Errorf("webhook URL requires HTTPS")
	}
	if u.Fragment != "" {
		return 0, fmt.Errorf("webhook URL cannot contain fragment")
	}
	r := w.Resolver
	if r == nil {
		r = net.DefaultResolver
	}
	ips, err := r.LookupIP(ctx, "ip", u.Hostname())
	if err != nil || len(ips) == 0 {
		return 0, fmt.Errorf("resolve webhook host: %w", err)
	}
	devAllowed := false
	if w.AllowHTTPDevelopment && u.Scheme == "http" {
		for _, host := range w.DevelopmentAllowedHosts {
			if u.Host == host {
				devAllowed = true
				break
			}
		}
	}
	for _, ip := range ips {
		if !publicIP(ip) && !devAllowed {
			return 0, fmt.Errorf("webhook host resolves to prohibited address")
		}
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	target := net.JoinHostPort(ips[0].String(), port)
	timeout := w.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	dialer := net.Dialer{Timeout: timeout}
	transport := &http.Transport{Proxy: nil, DialContext: func(c context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(c, network, target)
	}, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: timeout}
	client := &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	ts := time.Now().Unix()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Mrkt-Timestamp", strconv.FormatInt(ts, 10))
	req.Header.Set("X-Mrkt-Signature", "v1="+SignWebhook(secret, payload, ts))
	req.Header.Set("X-Mrkt-Event-ID", eventID)
	req.Header.Set("X-Mrkt-Attempt-ID", attemptID)
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("deliver webhook: %w", err)
	}
	defer resp.Body.Close()
	limit := w.MaxResponseBytes
	if limit <= 0 {
		limit = 64 << 10
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, limit))
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return resp.StatusCode, fmt.Errorf("webhook redirects are prohibited")
	}
	return resp.StatusCode, nil
}
func publicIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 0 || ip4[0] >= 224 || ip4[0] == 169 && ip4[1] == 254 || ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return false
		}
	} else {
		lower := strings.ToLower(ip.String())
		if strings.HasPrefix(lower, "2001:db8:") {
			return false
		}
	}
	return true
}
