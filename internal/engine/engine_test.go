package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattmezza/mrkt/internal/artifact"
	"github.com/mattmezza/mrkt/internal/mail"
	"github.com/mattmezza/mrkt/internal/manifest"
)

type captureSender struct {
	mu       sync.Mutex
	messages []mail.Message
	states   []string
}

func (s *captureSender) Send(_ context.Context, m mail.Message) (mail.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, m)
	state := mail.StateAccepted
	if len(s.states) > 0 {
		state = s.states[0]
		s.states = s.states[1:]
	}
	return mail.Result{State: state}, nil
}
func (s *captureSender) all() []mail.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]mail.Message(nil), s.messages...)
}
func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

func TestInitializationAuthenticationAndIsolation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mrkt.db")
	if _, er := Open(Config{DBPath: path}); er == nil {
		t.Fatal("missing explicit initialization accepted")
	}
	if _, er := Open(Config{DBPath: path, Initialize: true}); er == nil {
		t.Fatal("initialization without token accepted")
	}
	e, er := Open(Config{DBPath: path, Initialize: true, AdminToken: "admin-secret"})
	if er != nil {
		t.Fatal(er)
	}
	defer e.Close()
	a, er := e.Authenticate(ctx, "admin-secret")
	if er != nil || !a.Admin {
		t.Fatalf("authenticate: %#v %v", a, er)
	}
	_, er = e.Do(ctx, a, Operation{Resource: "projects", Action: "create", Input: raw(map[string]any{"id": "one", "name": "One"})})
	if er != nil {
		t.Fatal(er)
	}
	_, er = e.Do(ctx, a, Operation{Resource: "projects", Action: "create", Input: raw(map[string]any{"id": "two", "name": "Two"})})
	if er != nil {
		t.Fatal(er)
	}
	idem := Operation{Resource: "projects", Action: "create", Key: "create-three", Input: raw(map[string]any{"id": "three", "name": "Three"})}
	if _, er = e.Do(ctx, a, idem); er != nil {
		t.Fatal(er)
	}
	if _, er = e.Do(ctx, a, idem); er != nil {
		t.Fatalf("idempotent replay: %v", er)
	}
	idem.Input = raw(map[string]any{"id": "four", "name": "Four"})
	if _, er = e.Do(ctx, a, idem); er == nil || er.(*Error).Code != "idempotency_conflict" {
		t.Fatalf("idempotency conflict=%v", er)
	}
	created, er := e.Do(ctx, a, Operation{Project: "one", Resource: "tokens", Action: "create", Input: raw(map[string]any{"name": "reader", "scopes": []string{"read"}})})
	if er != nil {
		t.Fatal(er)
	}
	tok := created.(map[string]any)["token"].(string)
	reader, er := e.Authenticate(ctx, tok)
	if er != nil {
		t.Fatal(er)
	}
	if _, er = e.Do(ctx, reader, Operation{Project: "two", Resource: "contacts", Action: "list"}); er == nil || er.(*Error).Status != 403 {
		t.Fatalf("cross project access = %v", er)
	}
}

func TestConsentPinnedSequenceAndUnsubscribe(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	store := artifact.NewMemory(1 << 20)
	sender := &captureSender{}
	e, er := Open(Config{DBPath: filepath.Join(t.TempDir(), "mrkt.db"), Initialize: true, AdminToken: "admin", Artifacts: store, Sender: sender, PublicURL: "https://mail.example", Now: func() time.Time { return now }})
	if er != nil {
		t.Fatal(er)
	}
	defer e.Close()
	admin := Authority{Admin: true, Scopes: []string{"*"}}
	if _, er = e.Do(ctx, admin, Operation{Resource: "projects", Action: "create", Input: raw(map[string]string{"id": "p", "name": "P"})}); er != nil {
		t.Fatal(er)
	}
	if _, er = e.Do(ctx, admin, Operation{Project: "p", Resource: "events", Action: "create", Input: raw(map[string]any{"key": "foreign", "type": "booking.completed", "contact_id": "missing", "payload": map[string]any{}})}); er == nil || er.(*Error).Code != "invalid_contact" {
		t.Fatalf("foreign contact event accepted: %v", er)
	}
	sources := map[string][]byte{"subject.txt": []byte("Hello {{.Name}}\n"), "body.html": []byte("<p>Hello {{.Name}}</p>"), "body.txt": []byte("Hello {{.Name}} {{.UnsubscribeURL}}")}
	files := []manifest.File{}
	for _, p := range []string{"subject.txt", "body.html", "body.txt"} {
		b := sources[p]
		h := sha256.Sum256(b)
		hs := hex.EncodeToString(h[:])
		ct := "text/plain"
		if p == "body.html" {
			ct = "text/html"
		}
		if er = store.Put(ctx, "p", hs, ct, int64(len(b)), bytes.NewReader(b)); er != nil {
			t.Fatal(er)
		}
		files = append(files, manifest.File{Path: p, SHA256: hs, Size: int64(len(b)), ContentType: ct})
	}
	m := manifest.Manifest{Version: 1, Project: manifest.Project{Name: "P", DefaultLocale: "en", Locales: []string{"en"}}, Lists: []manifest.List{{ID: "news", Name: "News", Purpose: "Updates", PolicyVersion: "v1"}}, Files: files, Messages: map[string]map[string]manifest.Content{"welcome": {"en": {Subject: "subject.txt", HTML: "body.html", Text: "body.txt"}}}, Sequences: []manifest.Sequence{{ID: "welcome", List: "news", Entry: "subscription", Reentry: "once", Steps: []manifest.Step{{ID: "send", Type: "send", Message: "welcome", Next: "wait"}, {ID: "wait", Type: "delay", Delay: "1h", Next: "done"}, {ID: "done", Type: "complete"}}}}}
	dep, er := e.Do(ctx, admin, Operation{Project: "p", Resource: "releases", Action: "deploy", Input: raw(deployInput{Manifest: m})})
	if er != nil {
		t.Fatal(er)
	}
	rid := dep.(map[string]any)["id"].(string)
	got, er := e.Do(ctx, Authority{Public: true}, Operation{Project: "p", Resource: "consent", Action: "subscribe", Input: raw(map[string]string{"email": "Person@Example.com", "name": "Pat", "list": "news", "source": "form", "source_ip": "192.0.2.2"})})
	if er != nil || got.(map[string]any)["accepted"] != true {
		t.Fatalf("subscribe %v %v", got, er)
	}
	if er = e.Tick(ctx); er != nil {
		t.Fatal(er)
	}
	msgs := sender.all()
	if len(msgs) != 1 {
		t.Fatalf("confirmation messages=%d", len(msgs))
	}
	re := regexp.MustCompile(`token=([A-Za-z0-9_-]+)`)
	match := re.FindStringSubmatch(msgs[0].Text)
	if len(match) != 2 {
		t.Fatalf("token absent: %s", msgs[0].Text)
	}
	if _, er = e.Do(ctx, Authority{Public: true}, Operation{Project: "p", Resource: "consent", Action: "confirm", Input: raw(map[string]string{"token": match[1]})}); er != nil {
		t.Fatal(er)
	}
	sender.mu.Lock()
	sender.states = []string{mail.StateTransient, mail.StateAccepted}
	sender.mu.Unlock()
	if er = e.Tick(ctx); er != nil {
		t.Fatal(er)
	}
	msgs = sender.all()
	if len(msgs) != 2 || msgs[1].Subject != "Hello Pat" {
		t.Fatalf("welcome=%#v", msgs)
	}
	now = now.Add(time.Minute)
	if er = e.Tick(ctx); er != nil {
		t.Fatal(er)
	}
	msgs = sender.all()
	if len(msgs) != 3 || msgs[1].ID != msgs[2].ID {
		t.Fatalf("transient retry did not preserve message id: %#v", msgs)
	}
	var eid, cid string
	if er = e.db.QueryRow(`SELECT id,contact_id FROM enrollments WHERE release_id=?`, rid).Scan(&eid, &cid); er != nil {
		t.Fatal(er)
	}
	if _, er = e.Do(ctx, admin, Operation{Project: "p", Resource: "consent", Action: "unsubscribe", Input: raw(map[string]any{"contact_id": cid, "list": "news"})}); er != nil {
		t.Fatal(er)
	}
	now = now.Add(2 * time.Hour)
	if er = e.Tick(ctx); er != nil {
		t.Fatal(er)
	}
	if len(sender.all()) != 3 {
		t.Fatal("message sent after unsubscribe")
	}
	var state string
	_ = e.db.QueryRow(`SELECT state FROM enrollments WHERE id=?`, eid).Scan(&state)
	if state != "cancelled" {
		t.Fatalf("state=%s", state)
	}
}

func TestRecoveryRequiresAuditedResume(t *testing.T) {
	ctx := context.Background()
	e, er := Open(Config{DBPath: filepath.Join(t.TempDir(), "db"), Initialize: true, Recovery: true, AdminToken: "x"})
	if er != nil {
		t.Fatal(er)
	}
	defer e.Close()
	a := Authority{Admin: true}
	if _, er = e.Do(ctx, a, Operation{Resource: "installation", Action: "resume", Input: raw(map[string]any{"reconciled": false, "note": "checked"})}); er == nil {
		t.Fatal("unsafe resume accepted")
	}
	if _, er = e.Do(ctx, a, Operation{Resource: "installation", Action: "resume", Input: raw(map[string]any{"reconciled": true, "note": "checked upstream suppressions and ambiguous sends"})}); er != nil {
		t.Fatal(er)
	}
	var n int
	if er = e.db.QueryRow(`SELECT count(*) FROM audit WHERE action='recovery.resume'`).Scan(&n); er != nil || n != 1 {
		t.Fatalf("audit=%d %v", n, er)
	}
}

func TestRecoveryMarkerAndCSVConsentSafety(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	e, er := Open(Config{DBPath: path, Initialize: true, AdminToken: "x"})
	if er != nil {
		t.Fatal(er)
	}
	a := Authority{Admin: true, Scopes: []string{"*"}}
	_, _ = e.Do(ctx, a, Operation{Resource: "projects", Action: "create", Input: raw(map[string]string{"id": "csv", "name": "CSV"})})
	_, _ = e.db.Exec(`INSERT INTO lists(project_id,id,name,purpose,policy_version,release_id) VALUES('csv','news','News','Updates','v1','fixture')`)
	e.Close()
	if er = os.WriteFile(path+".recovery-required", []byte("restore\n"), 0600); er != nil {
		t.Fatal(er)
	}
	e, er = Open(Config{DBPath: path})
	if er != nil {
		t.Fatal(er)
	}
	defer e.Close()
	status, er := e.Do(ctx, a, Operation{Resource: "installation", Action: "get"})
	if er != nil || status.(map[string]any)["recovery"] != true {
		t.Fatalf("marker did not force recovery: %#v %v", status, er)
	}
	preview, er := e.Do(ctx, a, Operation{Project: "csv", Resource: "contacts", Action: "preview-import", Input: raw(csvInput{CSV: "email,name,unknown\na@example.test,A,x\n"})})
	if er == nil {
		t.Fatalf("unknown CSV header accepted: %#v", preview)
	}
	result, er := e.Do(ctx, a, Operation{Project: "csv", Resource: "contacts", Action: "import", Input: raw(csvInput{CSV: "email,name,attributes\na@example.test,A,{}\n", List: "news", Source: "operator_import", ConsentState: "pending"})})
	if er != nil {
		t.Fatal(er)
	}
	if result.(map[string]any)["confirmed_consent_granted"] != false {
		t.Fatal("import granted consent")
	}
	var state string
	if er = e.db.QueryRow(`SELECT state FROM consent WHERE project_id='csv'`).Scan(&state); er != nil || state != "pending" {
		t.Fatalf("state=%q %v", state, er)
	}
	exported, er := e.Do(ctx, a, Operation{Project: "csv", Resource: "contacts", Action: "export"})
	if er != nil || !strings.Contains(exported.(map[string]any)["csv"].(string), "a@example.test") {
		t.Fatalf("export %#v %v", exported, er)
	}
}
