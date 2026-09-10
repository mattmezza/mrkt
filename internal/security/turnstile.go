package security

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type ChallengeVerifier interface {
	Verify(context.Context, string, string) error
}
type Turnstile struct {
	Secret, Hostname, Action string
	Client                   *http.Client
	Endpoint                 string
}
type turnstileResponse struct {
	Success  bool     `json:"success"`
	Hostname string   `json:"hostname"`
	Action   string   `json:"action"`
	Errors   []string `json:"error-codes"`
}

func (t *Turnstile) Verify(ctx context.Context, token, remoteIP string) error {
	if t.Secret == "" {
		return fmt.Errorf("turnstile secret is required")
	}
	if token == "" || len(token) > 2048 {
		return fmt.Errorf("invalid turnstile token")
	}
	endpoint := t.Endpoint
	if endpoint == "" {
		endpoint = "https://challenges.cloudflare.com/turnstile/v0/siteverify"
	}
	body, _ := json.Marshal(map[string]string{"secret": t.Secret, "response": token, "remoteip": remoteIP})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("turnstile verification: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("turnstile verification returned HTTP %d", resp.StatusCode)
	}
	var result turnstileResponse
	if err = json.NewDecoder(io.LimitReader(resp.Body, 16<<10)).Decode(&result); err != nil {
		return fmt.Errorf("turnstile response: %w", err)
	}
	if !result.Success {
		return fmt.Errorf("turnstile rejected token: %v", result.Errors)
	}
	if t.Hostname != "" && result.Hostname != t.Hostname {
		return fmt.Errorf("turnstile hostname mismatch")
	}
	if t.Action != "" && result.Action != t.Action {
		return fmt.Errorf("turnstile action mismatch")
	}
	return nil
}

type DevelopmentBypass struct{ Enabled bool }

func (d DevelopmentBypass) Verify(context.Context, string, string) error {
	if !d.Enabled {
		return fmt.Errorf("challenge bypass disabled")
	}
	return nil
}
