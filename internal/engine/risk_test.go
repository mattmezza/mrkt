package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func riskEngine(t *testing.T) (*Engine, Authority) {
	t.Helper()
	e, er := Open(Config{DBPath: filepath.Join(t.TempDir(), "risk.db"), Initialize: true, AdminToken: "admin", Now: func() time.Time { return time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC) }})
	if er != nil {
		t.Fatal(er)
	}
	a := Authority{Admin: true, Scopes: []string{"*"}}
	for _, id := range []string{"p1", "p2"} {
		if _, er = e.Do(context.Background(), a, Operation{Resource: "projects", Action: "create", Input: raw(map[string]string{"id": id, "name": id})}); er != nil {
			t.Fatal(er)
		}
	}
	return e, a
}

func TestIdempotencyAuthorityFingerprintAndConcurrentClaim(t *testing.T) {
	ctx := context.Background()
	e, admin := riskEngine(t)
	defer e.Close()
	op := Operation{Project: "p1", Resource: "contacts", Action: "create", Key: "contact-key", Input: raw(map[string]any{"email": "one@example.test"})}
	if _, er := e.Do(ctx, admin, op); er != nil {
		t.Fatal(er)
	}
	foreign := Authority{Project: "p2", Scopes: []string{"operate"}}
	if _, er := e.Do(ctx, foreign, op); er == nil || er.(*Error).Status != 403 {
		t.Fatalf("foreign idempotency replay exposed response: %v", er)
	}
	changed := op
	changed.ID = "different"
	if _, er := e.Do(ctx, admin, changed); er == nil || er.(*Error).Code != "idempotency_conflict" {
		t.Fatalf("ID omitted from fingerprint: %v", er)
	}
	concurrent := Operation{Project: "p1", Resource: "contacts", Action: "create", Key: "race-key", Input: raw(map[string]any{"email": "race@example.test"})}
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; _, er := e.Do(ctx, admin, concurrent); errs <- er }()
	}
	close(start)
	wg.Wait()
	close(errs)
	success, conflicts := 0, 0
	for er := range errs {
		if er == nil {
			success++
		} else if ee, ok := er.(*Error); ok && (ee.Code == "idempotency_in_progress" || ee.Code == "idempotency_conflict") {
			conflicts++
		} else {
			t.Fatalf("unexpected race result: %v", er)
		}
	}
	if success < 1 || success+conflicts != 2 {
		t.Fatalf("success=%d conflict=%d", success, conflicts)
	}
	var n int
	_ = e.db.QueryRow(`SELECT count(*) FROM contacts WHERE project_id='p1' AND email='race@example.test'`).Scan(&n)
	if n != 1 {
		t.Fatalf("mutations=%d", n)
	}
}

func TestRetentionKeepsEventDeduplicationTombstone(t *testing.T) {
	ctx := context.Background()
	e, admin := riskEngine(t)
	defer e.Close()
	old := e.now().Add(-400 * 24 * time.Hour).Format(time.RFC3339Nano)
	payload, _ := json.Marshal(map[string]any{"private": "value"})
	fingerprintInput, _ := json.Marshal(struct {
		Type       string         `json:"type"`
		ContactID  string         `json:"contact_id,omitempty"`
		Payload    map[string]any `json:"payload"`
		OccurredAt string         `json:"occurred_at,omitempty"`
	}{"project.test", "", map[string]any{"private": "value"}, old})
	sum := sha256.Sum256(fingerprintInput)
	fingerprint := hex.EncodeToString(sum[:])
	_, er := e.db.Exec(`INSERT INTO events(id,project_id,event_key,type,payload,occurred_at,created_at,request_fingerprint) VALUES('event_old','p1','stable-key','project.test',?,?,?,?)`, string(payload), old, old, fingerprint)
	if er != nil {
		t.Fatal(er)
	}
	res, er := e.Do(ctx, admin, Operation{Project: "p1", Resource: "operations", Action: "retention", Input: raw(map[string]any{"event_days": 30, "apply": true, "note": "risk test"})})
	if er != nil {
		t.Fatal(er)
	}
	if res.(map[string]any)["event_payloads_redacted"].(int64) != 1 {
		t.Fatalf("retention=%#v", res)
	}
	var got string
	_ = e.db.QueryRow(`SELECT payload FROM events WHERE id='event_old'`).Scan(&got)
	if got != "{}" {
		t.Fatalf("payload=%s", got)
	}
	duplicate, er := e.Do(ctx, admin, Operation{Project: "p1", Resource: "events", Action: "create", Input: raw(map[string]any{"key": "stable-key", "type": "project.test", "payload": map[string]any{"private": "value"}, "occurred_at": old})})
	if er != nil {
		t.Fatal(er)
	}
	if duplicate.(map[string]any)["duplicate"] != true {
		t.Fatalf("duplicate=%#v", duplicate)
	}
	_, er = e.Do(ctx, admin, Operation{Project: "p1", Resource: "events", Action: "create", Input: raw(map[string]any{"key": "stable-key", "type": "project.test", "payload": map[string]any{"new": true}, "occurred_at": old})})
	if er == nil || er.(*Error).Code != "event_key_conflict" {
		t.Fatalf("changed duplicate=%v", er)
	}
	var count int
	_ = e.db.QueryRow(`SELECT count(*) FROM events WHERE project_id='p1' AND event_key='stable-key'`).Scan(&count)
	if count != 1 {
		t.Fatalf("dedupe tombstones=%d", count)
	}
}

func TestTransportRateReservationIsDurable(t *testing.T) {
	e, _ := riskEngine(t)
	defer e.Close()
	ctx := context.Background()
	if !e.reserveSelectedTransport(ctx, "p1", "transport-a", 1) {
		t.Fatal("first reservation rejected")
	}
	if e.reserveSelectedTransport(ctx, "p1", "transport-a", 1) {
		t.Fatal("second reservation exceeded configured rate")
	}
	var deliveries int
	_ = e.db.QueryRow(`SELECT count(*) FROM deliveries WHERE project_id='p1'`).Scan(&deliveries)
	if deliveries != 0 {
		t.Fatalf("rate rejection created %d delivery intents", deliveries)
	}
}
