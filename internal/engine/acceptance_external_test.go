package engine_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/mattmezza/mrkt/internal/artifact"
	"github.com/mattmezza/mrkt/internal/engine"
	"github.com/mattmezza/mrkt/internal/mail"
	"github.com/mattmezza/mrkt/internal/manifest"
)

func input(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

type captureSender struct {
	mu       sync.Mutex
	messages []mail.Message
}

func (s *captureSender) Send(_ context.Context, m mail.Message) (mail.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, m)
	return mail.Result{State: mail.StateAccepted}, nil
}
func (s *captureSender) all() []mail.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]mail.Message(nil), s.messages...)
}

type blockingStore struct {
	artifact.Store
	mu      sync.Mutex
	armed   bool
	started chan struct{}
	release chan struct{}
}

func (s *blockingStore) arm() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.armed = true
	s.started = make(chan struct{})
	s.release = make(chan struct{})
}
func (s *blockingStore) Put(ctx context.Context, p, h, ct string, n int64, r io.Reader) error {
	s.mu.Lock()
	block := s.armed && ct == "application/json"
	started, release := s.started, s.release
	if block {
		s.armed = false
	}
	s.mu.Unlock()
	if block {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.Store.Put(ctx, p, h, ct, n, r)
}

func TestBroadcastEventIdempotencyRejectsChangedFingerprint(t *testing.T) {
	ctx := context.Background()
	store := artifact.NewMemory(1 << 20)
	app, err := engine.Open(engine.Config{DBPath: filepath.Join(t.TempDir(), "mrkt.db"), Initialize: true, AdminToken: "admin", Artifacts: store, EncryptionKey: make([]byte, 32)})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	admin := engine.Authority{Admin: true, Scopes: []string{"*"}}
	if _, err = app.Do(ctx, admin, engine.Operation{Resource: "projects", Action: "create", Input: input(map[string]string{"id": "p", "name": "P"})}); err != nil {
		t.Fatal(err)
	}
	sources := map[string][]byte{"subject": []byte("Hello"), "html": []byte("<p>Hello</p>"), "text": []byte("Hello")}
	files := []manifest.File{}
	for path, data := range sources {
		sum := sha256.Sum256(data)
		hash := hex.EncodeToString(sum[:])
		ct := "text/plain"
		if path == "html" {
			ct = "text/html"
		}
		if err = store.Put(ctx, "p", hash, ct, int64(len(data)), bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
		files = append(files, manifest.File{Path: path, SHA256: hash, ContentType: ct, Size: int64(len(data))})
	}
	m := manifest.Manifest{Version: 1, Project: manifest.Project{Name: "P", DefaultLocale: "en", Locales: []string{"en"}}, Lists: []manifest.List{{ID: "news", Name: "News", Purpose: "News", PolicyVersion: "v1"}}, Files: files, Messages: map[string]map[string]manifest.Content{"announcement": {"en": {Subject: "subject", HTML: "html", Text: "text"}}}, Broadcasts: []manifest.Broadcast{{ID: "launch", Event: "launch.created", List: "news", Message: "announcement"}}}
	deployed, err := app.Do(ctx, admin, engine.Operation{Project: "p", Resource: "releases", Action: "deploy", Input: input(map[string]any{"manifest": m, "expected_release": ""})})
	if err != nil {
		t.Fatal(err)
	}
	active := deployed.(map[string]any)["id"].(string)
	stale := m
	stale.Project.Name = "stale"
	if _, err = app.Do(ctx, admin, engine.Operation{Project: "p", Resource: "releases", Action: "deploy", Input: input(map[string]any{"manifest": stale, "expected_release": "wrong"})}); err == nil {
		t.Fatal("stale deployment activated")
	}
	releases, err := app.Do(ctx, admin, engine.Operation{Project: "p", Resource: "releases", Action: "list"})
	if err != nil || !strings.Contains(fmt.Sprint(releases), active) {
		t.Fatalf("active release lost after stale deploy: %#v %v", releases, err)
	}
	op := engine.Operation{Project: "p", Resource: "events", Action: "create", Input: input(map[string]any{"key": "source-event-1", "type": "launch.created", "payload": map[string]any{"version": 1}})}
	_, err = app.Do(ctx, admin, op)
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.Do(ctx, admin, op)
	if err != nil {
		t.Fatalf("identical producer retry failed: %v", err)
	}
	op.Input = input(map[string]any{"key": "source-event-1", "type": "launch.created", "payload": map[string]any{"version": 2}})
	if _, err = app.Do(ctx, admin, op); err == nil {
		t.Fatal("changed idempotency fingerprint accepted")
	}
	listed, err := app.Do(ctx, admin, engine.Operation{Project: "p", Resource: "broadcasts", Action: "list"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(listed)
	var page struct {
		Items []any `json:"items"`
	}
	if err = json.Unmarshal(b, &page); err != nil || len(page.Items) != 1 {
		t.Fatalf("broadcast count != 1: %s %v", b, err)
	}
}

func TestConcurrentUnsubscribeWhileRenderArtifactBlockedPreventsSMTP(t *testing.T) {
	ctx := context.Background()
	memory := artifact.NewMemory(1 << 20)
	store := &blockingStore{Store: memory}
	sender := &captureSender{}
	app, err := engine.Open(engine.Config{DBPath: filepath.Join(t.TempDir(), "mrkt.db"), Initialize: true, AdminToken: "admin", PublicURL: "https://mrkt.example", From: "mrkt <mrkt@example.test>", Artifacts: store, Sender: sender, EncryptionKey: make([]byte, 32)})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	admin := engine.Authority{Admin: true, Scopes: []string{"*"}}
	_, err = app.Do(ctx, admin, engine.Operation{Resource: "projects", Action: "create", Input: input(map[string]string{"id": "p", "name": "P"})})
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string][]byte{"subject": []byte("Welcome"), "html": []byte("<p>Welcome</p>"), "text": []byte("Welcome {{.UnsubscribeURL}}")}
	files := []manifest.File{}
	for path, data := range sources {
		sum := sha256.Sum256(data)
		hash := hex.EncodeToString(sum[:])
		ct := "text/plain"
		if path == "html" {
			ct = "text/html"
		}
		if err = store.Put(ctx, "p", hash, ct, int64(len(data)), bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
		files = append(files, manifest.File{Path: path, SHA256: hash, ContentType: ct, Size: int64(len(data))})
	}
	m := manifest.Manifest{Version: 1, Project: manifest.Project{Name: "P", DefaultLocale: "en", Locales: []string{"en"}}, Lists: []manifest.List{{ID: "news", Name: "News", Purpose: "News", PolicyVersion: "v1"}}, Files: files, Messages: map[string]map[string]manifest.Content{"welcome": {"en": {Subject: "subject", HTML: "html", Text: "text"}}}, Sequences: []manifest.Sequence{{ID: "welcome", List: "news", Entry: "subscription", Reentry: "once", Steps: []manifest.Step{{ID: "send", Type: "send", Message: "welcome", Next: "done"}, {ID: "done", Type: "complete"}}}}}
	deployed, err := app.Do(ctx, admin, engine.Operation{Project: "p", Resource: "releases", Action: "deploy", Input: input(map[string]any{"manifest": m, "expected_release": ""})})
	if err != nil {
		t.Fatal(err)
	}
	_, err = app.Do(ctx, engine.Authority{Public: true}, engine.Operation{Project: "p", Resource: "consent", Action: "subscribe", Input: input(map[string]string{"email": "race@example.test", "list": "news", "source": "test", "source_ip": "192.0.2.1"})})
	if err != nil {
		t.Fatal(err)
	}
	if err = app.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	messages := sender.all()
	match := regexp.MustCompile(`token=([A-Za-z0-9_-]+)`).FindStringSubmatch(messages[0].Text)
	if len(match) != 2 {
		t.Fatal("confirmation token missing")
	}
	_, err = app.Do(ctx, engine.Authority{Public: true}, engine.Operation{Project: "p", Resource: "consent", Action: "confirm", Input: input(map[string]string{"token": match[1]})})
	if err != nil {
		t.Fatal(err)
	}
	consent, err := app.Do(ctx, admin, engine.Operation{Project: "p", Resource: "consent", Action: "list"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(consent)
	var page struct {
		Items []struct {
			ContactID string `json:"contact_id"`
		} `json:"items"`
	}
	_ = json.Unmarshal(encoded, &page)
	store.arm()
	done := make(chan error, 1)
	go func() { done <- app.Tick(ctx) }()
	<-store.started
	_, err = app.Do(ctx, admin, engine.Operation{Project: "p", Resource: "consent", Action: "unsubscribe", Input: input(map[string]string{"contact_id": page.Items[0].ContactID, "list": "news"})})
	if err != nil {
		t.Fatal(err)
	}
	close(store.release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if got := len(sender.all()); got != 1 {
		t.Fatalf("SMTP called after concurrent unsubscribe; sends=%d", got)
	}
	consent, err = app.Do(ctx, admin, engine.Operation{Project: "p", Resource: "consent", Action: "list"})
	if err != nil || !strings.Contains(fmt.Sprint(consent), "unsubscribed") {
		t.Fatalf("unsubscribe not durable: %#v %v", consent, err)
	}
	release1 := deployed.(map[string]any)["id"].(string)
	m.Project.Name = "P v2"
	deployed, err = app.Do(ctx, admin, engine.Operation{Project: "p", Resource: "releases", Action: "deploy", Input: input(map[string]any{"manifest": m, "expected_release": release1})})
	if err != nil {
		t.Fatal(err)
	}
	release2 := deployed.(map[string]any)["id"].(string)
	if _, err = app.Do(ctx, admin, engine.Operation{Project: "p", Resource: "releases", Action: "rollback", Input: input(map[string]string{"release_id": release1, "expected_release": release2})}); err != nil {
		t.Fatal(err)
	}
	enrollments, err := app.Do(ctx, admin, engine.Operation{Project: "p", Resource: "enrollments", Action: "list"})
	if err != nil || !strings.Contains(fmt.Sprint(enrollments), release1) {
		t.Fatalf("enrollment lost pinned release: %#v %v", enrollments, err)
	}
}
