package engine

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattmezza/mrkt/internal/security"
)

func registryFixture(t *testing.T) *Engine {
	t.Helper()
	e, err := Open(Config{DBPath: filepath.Join(t.TempDir(), "app.db"), Initialize: true, AdminToken: "test-token", EncryptionKey: []byte(strings.Repeat("e", 32)), Development: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	for _, p := range []string{"one", "two"} {
		_, err = e.Do(context.Background(), Authority{Admin: true}, Operation{Resource: "projects", Action: "create", Input: raw(map[string]string{"id": p, "name": p})})
		if err != nil {
			t.Fatal(err)
		}
	}
	return e
}
func TestRegistrySecretsAndTenantIsolation(t *testing.T) {
	e := registryFixture(t)
	ctx := context.Background()
	a := Authority{Admin: true}
	created, err := e.Do(ctx, a, Operation{Project: "one", Resource: "webhooks", Action: "create", Input: raw(map[string]any{"name": "synthetic", "config": map[string]any{"url": "https://example.com/receiver"}})})
	if err != nil {
		t.Fatal(err)
	}
	v := created.(map[string]any)
	id := v["id"].(string)
	secret := v["secret"].(string)
	var stored string
	if err = e.db.QueryRow(`SELECT config FROM registry WHERE id=?`, id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, secret) || strings.Contains(stored, "example.com") {
		t.Fatal("registry secrets unencrypted")
	}
	listed, err := e.Do(ctx, Authority{Project: "one", Scopes: []string{"read"}}, Operation{Project: "one", Resource: "webhooks", Action: "get", ID: id})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(listed)
	if strings.Contains(string(b), secret) {
		t.Fatal("secret disclosed after creation")
	}
	if _, err = e.Do(ctx, Authority{Project: "two", Scopes: []string{"read"}}, Operation{Project: "one", Resource: "webhooks", Action: "get", ID: id}); err == nil {
		t.Fatal("cross-project read accepted")
	}
	if _, err = e.Do(ctx, a, Operation{Project: "one", Resource: "webhooks", Action: "create", Input: raw(map[string]any{"name": "private", "config": map[string]any{"url": "https://127.0.0.1/admin"}})}); err == nil {
		t.Fatal("private webhook accepted")
	}
	if _, err = e.Do(ctx, Authority{Project: "one", Scopes: []string{"config"}}, Operation{Project: "one", Resource: "transports", Action: "create", Input: raw(map[string]any{"name": "private", "config": map[string]any{"host": "127.0.0.1"}})}); err == nil {
		t.Fatal("project credential can provision private SMTP")
	}
}
func TestFeedbackDeduplicationAndOrdering(t *testing.T) {
	e := registryFixture(t)
	ctx := context.Background()
	a := Authority{Admin: true}
	result, err := e.Do(ctx, a, Operation{Project: "one", Resource: "transports", Action: "create", Input: raw(map[string]any{"name": "capture", "config": map[string]any{"host": "localhost", "port": 1025, "tls_mode": "plain", "from": "no-reply@example.test", "allow_private": true}})})
	if err != nil {
		t.Fatal(err)
	}
	v := result.(map[string]any)
	tid, secret := v["id"].(string), v["secret"].(string)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = e.db.Exec(`INSERT INTO contacts(id,project_id,email,created_at,updated_at) VALUES('contact','one','reader@example.test',?,?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.db.Exec(`INSERT INTO deliveries(id,project_id,contact_id,release_id,step_id,message_key,message_id,state,created_at,updated_at) VALUES('delivery','one','contact','release','step','hello','message@example.test','accepted',?,?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	ingest := func(id, typ string) (any, error) {
		b := raw(normalizedFeedback{ID: id, Type: typ, MessageID: "message@example.test", Email: "reader@example.test", OccurredAt: now})
		timestamp := time.Now().Unix()
		return e.doFeedback(ctx, Operation{Project: "one", ID: tid, Input: raw(feedbackInput{Body: b, Timestamp: timestamp, Signature: security.SignWebhook([]byte(secret), b, timestamp)})})
	}
	if _, err = ingest("complaint-1", "complaint"); err != nil {
		t.Fatal(err)
	}
	dup, err := ingest("complaint-1", "complaint")
	if err != nil || dup.(map[string]any)["applied"] != 0 {
		t.Fatalf("duplicate: %v %v", dup, err)
	}
	if _, err = ingest("delivery-1", "delivered"); err != nil {
		t.Fatal(err)
	}
	var state string
	_ = e.db.QueryRow(`SELECT state FROM deliveries WHERE id='delivery'`).Scan(&state)
	if state != "complaint" {
		t.Fatal("late delivery cleared complaint")
	}
	var paused int
	_ = e.db.QueryRow(`SELECT paused FROM projects WHERE id='one'`).Scan(&paused)
	if paused != 1 {
		t.Fatal("complaint did not pause project")
	}
	if _, err = ingest("complaint-1", "bounce"); err == nil {
		t.Fatal("same event ID allowed different data")
	}
}

func TestOfflineEncryptionRotation(t *testing.T) {
	e := registryFixture(t)
	ctx := context.Background()
	v, err := e.Do(ctx, Authority{Admin: true}, Operation{Project: "one", Resource: "webhooks", Action: "create", Input: raw(map[string]any{"name": "rotation", "config": map[string]string{"url": "https://example.com/events"}})})
	if err != nil {
		t.Fatal(err)
	}
	id := v.(map[string]any)["id"].(string)
	secret := v.(map[string]any)["secret"].(string)
	old, _ := e.box()
	if err = e.RotateEncryptionKey(ctx, []byte(strings.Repeat("n", 32))); err != nil {
		t.Fatal(err)
	}
	var encoded string
	if err = e.db.QueryRow(`SELECT config FROM registry WHERE id=?`, id).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	if _, err = old.Open(encoded, "one:"+id); err == nil {
		t.Fatal("old key still decrypts new registry")
	}
	var c webhookConfig
	if err = e.registryConfig(ctx, "one", "webhooks", id, &c); err != nil || c.Secret != secret {
		t.Fatalf("secret lost %v", err)
	}
}

func TestOfflineAdministratorRotation(t *testing.T) {
	e := registryFixture(t)
	ctx := context.Background()
	if err := e.RotateAdministratorToken(ctx, strings.Repeat("new", 16)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Authenticate(ctx, "test-token"); err == nil {
		t.Fatal("old admin token still active")
	}
	a, err := e.Authenticate(ctx, strings.Repeat("new", 16))
	if err != nil || !a.Admin {
		t.Fatalf("new admin failed %v", err)
	}
}
