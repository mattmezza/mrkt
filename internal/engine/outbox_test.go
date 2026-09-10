package engine

import (
	"context"
	"github.com/mattmezza/mrkt/internal/security"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSignedOutboxRetryReplayAndRecoveryPause(t *testing.T) {
	e := registryFixture(t)
	ctx := context.Background()
	a := Authority{Admin: true}
	now := time.Now().UTC()
	e.now = func() time.Time { return now }
	var secret string
	var events, attempts, payloads []string
	status := 503
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		ts, _ := strconv.ParseInt(r.Header.Get("X-Mrkt-Timestamp"), 10, 64)
		if !security.VerifyWebhook([]byte(secret), b, ts, strings.TrimPrefix(r.Header.Get("X-Mrkt-Signature"), "v1="), time.Now(), time.Minute) {
			t.Error("invalid outbound signature")
		}
		events = append(events, r.Header.Get("X-Mrkt-Event-ID"))
		attempts = append(attempts, r.Header.Get("X-Mrkt-Attempt-ID"))
		payloads = append(payloads, string(b))
		w.WriteHeader(status)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	e.cfg.WebhookDevelopmentAllowedHosts = []string{u.Host}
	v, err := e.Do(ctx, a, Operation{Project: "one", Resource: "webhooks", Action: "create", Input: raw(map[string]any{"name": "local capture", "config": map[string]any{"url": server.URL, "development": true}})})
	if err != nil {
		t.Fatal(err)
	}
	secret = v.(map[string]any)["secret"].(string)
	stamp := now.Format(time.RFC3339Nano)
	_, err = e.db.Exec(`INSERT INTO outbox(id,project_id,type,payload,correlation_id,next_at,created_at) VALUES('event-1','one','subscription.confirmed','{"contact_id":"synthetic"}','contact-1',?,?)`, stamp, stamp)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.tickOutbox(ctx); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("calls %d", len(events))
	}
	_, err = e.db.Exec(`UPDATE installation SET recovery=1,outbound_paused=1`)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	if err = e.tickOutbox(ctx); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatal("sent during recovery")
	}
	_, err = e.db.Exec(`UPDATE installation SET recovery=0,outbound_paused=0`)
	if err != nil {
		t.Fatal(err)
	}
	// Success on the last allowed attempt must stay delivered, not dead-letter.
	_, err = e.db.Exec(`UPDATE webhook_deliveries SET attempts=9`)
	if err != nil {
		t.Fatal(err)
	}
	status = 204
	if err = e.tickOutbox(ctx); err != nil {
		t.Fatal(err)
	}
	var id, state string
	if err = e.db.QueryRow(`SELECT id,state FROM webhook_deliveries WHERE event_id='event-1'`).Scan(&id, &state); err != nil {
		t.Fatal(err)
	}
	if state != "delivered" {
		t.Fatalf("state %s", state)
	}
	if _, err = e.Do(ctx, a, Operation{Project: "one", Resource: "webhook-deliveries", Action: "replay", ID: id}); err != nil {
		t.Fatal(err)
	}
	if err = e.tickOutbox(ctx); err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0] != events[1] || events[1] != events[2] || attempts[0] == attempts[1] || attempts[1] == attempts[2] || payloads[0] != payloads[2] {
		t.Fatal("replay identity or frozen payload violated")
	}
	got, err := e.Do(ctx, a, Operation{Project: "one", Resource: "webhook-deliveries", Action: "get", ID: id})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.(map[string]any)["history"].([]map[string]any)) != 3 {
		t.Fatal("attempt history missing")
	}
}
