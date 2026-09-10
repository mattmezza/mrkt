//go:build integration

package mail

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestSMTPRealCapture(t *testing.T) {
	cfg := SMTPConfig{Host: "127.0.0.1", Port: 1025, TLSMode: "plain", Development: true}
	if err := ProbeSMTP(context.Background(), cfg); err != nil {
		t.Fatalf("probe: %v", err)
	}
	s, err := NewSMTP(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m := testMessage()
	m.ID = "integration.mrkt.local"
	r, err := s.Send(context.Background(), m)
	if err != nil || r.State != StateAccepted {
		t.Fatalf("send=%+v err=%v", r, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, e := http.Get("http://127.0.0.1:8025/api/v1/messages")
		if e == nil {
			var v struct {
				Messages []struct {
					MessageID string `json:"MessageID"`
				} `json:"messages"`
			}
			e = json.NewDecoder(resp.Body).Decode(&v)
			resp.Body.Close()
			if e == nil {
				for _, msg := range v.Messages {
					if msg.MessageID == "<integration.mrkt.local>" || msg.MessageID == "integration.mrkt.local" {
						return
					}
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("captured message not found")
}
