//go:build integration

package deploy_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mattmezza/mrkt/internal/artifact"
	"github.com/mattmezza/mrkt/internal/engine"
	mrktmail "github.com/mattmezza/mrkt/internal/mail"
	"github.com/mattmezza/mrkt/internal/manifest"
	"github.com/mattmezza/mrkt/internal/security"
	_ "modernc.org/sqlite"
)

func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func mappedPort(t *testing.T, container, port string) string {
	t.Helper()
	s := strings.TrimSpace(docker(t, "port", container, port))
	_, p, ok := strings.Cut(s, ":")
	if !ok {
		t.Fatal(s)
	}
	return p
}

func docker(t *testing.T, args ...string) string {
	t.Helper()
	c := exec.Command("docker", args...)
	b, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, b)
	}
	return string(b)
}
func TestLitestreamRestoreFromRealObjectStore(t *testing.T) {
	if testing.Short() {
		t.Skip("Docker integration")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_ = ctx
	id := fmt.Sprintf("mrkt-recovery-%d", time.Now().UnixNano())
	network := id + "-net"
	minio := id + "-minio"
	mailpit := id + "-mailpit"
	rep := id + "-rep"
	docker(t, "network", "create", network)
	defer exec.Command("docker", "network", "rm", network).Run()
	defer exec.Command("docker", "rm", "-f", minio, mailpit, rep).Run()
	docker(t, "run", "-d", "--name", minio, "--network", network, "-p", "127.0.0.1::9000", "-e", "MINIO_ROOT_USER=testkey", "-e", "MINIO_ROOT_PASSWORD=testsecret", "minio/minio:RELEASE.2025-09-07T16-13-09Z", "server", "/data")
	docker(t, "run", "-d", "--name", mailpit, "--network", network, "-p", "127.0.0.1::1025", "-p", "127.0.0.1::8025", "axllent/mailpit:v1.27.8")
	for i := 0; i < 30; i++ {
		c := exec.Command("docker", "run", "--rm", "--network", network, "--entrypoint", "/bin/sh", "minio/mc:RELEASE.2025-08-13T08-35-41Z", "-c", "mc alias set local http://"+minio+":9000 testkey testsecret && mc mb --ignore-existing local/mrkt")
		if c.Run() == nil {
			break
		}
		time.Sleep(time.Second)
		if i == 29 {
			t.Fatal("MinIO not ready")
		}
	}
	source := t.TempDir()
	restore := t.TempDir()
	webhookHits := 0
	hook := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { webhookHits++ }))
	defer hook.Close()
	hookURL, _ := url.Parse(hook.URL)
	t.Setenv("AWS_ACCESS_KEY_ID", "testkey")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "testsecret")
	store, err := artifact.NewS3(ctx, artifact.S3Config{Region: "us-east-1", Bucket: "mrkt", Prefix: "fixture", Endpoint: "http://127.0.0.1:" + mappedPort(t, minio, "9000/tcp"), PathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	smtpPort, _ := strconv.Atoi(mappedPort(t, mailpit, "1025/tcp"))
	mailpitHTTP := "http://127.0.0.1:" + mappedPort(t, mailpit, "8025/tcp")
	sender, err := mrktmail.NewSMTP(mrktmail.SMTPConfig{Host: "127.0.0.1", Port: smtpPort, TLSMode: "plain", Development: true})
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(source, "mrkt.db")
	app, err := engine.Open(engine.Config{DBPath: dbPath, Initialize: true, AdminToken: "fixture-admin", PublicURL: "http://localhost", From: "mrkt <mrkt@example.test>", Development: true, Artifacts: store, Sender: sender, EncryptionKey: make([]byte, 32), WebhookDevelopmentAllowedHosts: []string{hookURL.Host}})
	if err != nil {
		t.Fatal(err)
	}
	admin := engine.Authority{Admin: true, Scopes: []string{"*"}}
	if _, err = app.Do(ctx, admin, engine.Operation{Resource: "projects", Action: "create", Input: raw(map[string]string{"id": "restore", "name": "Restore"})}); err != nil {
		t.Fatal(err)
	}
	sources := map[string][]byte{"subject.txt": []byte("Welcome {{.Name}}"), "body.html": []byte("<p>Welcome {{.Name}}</p>"), "body.txt": []byte("Welcome {{.Name}} {{.UnsubscribeURL}}")}
	files := []manifest.File{}
	for path, b := range sources {
		sum := sha256.Sum256(b)
		hash := hex.EncodeToString(sum[:])
		ct := "text/plain"
		if strings.HasSuffix(path, ".html") {
			ct = "text/html"
		}
		if err = store.Put(ctx, "restore", hash, ct, int64(len(b)), bytes.NewReader(b)); err != nil {
			t.Fatal(err)
		}
		files = append(files, manifest.File{Path: path, SHA256: hash, ContentType: ct, Size: int64(len(b))})
	}
	m := manifest.Manifest{Version: 1, Project: manifest.Project{Name: "Restore", DefaultLocale: "en", Locales: []string{"en"}}, Lists: []manifest.List{{ID: "news", Name: "News", Purpose: "Updates", PolicyVersion: "v1"}}, Files: files, Messages: map[string]map[string]manifest.Content{"welcome": {"en": {Subject: "subject.txt", HTML: "body.html", Text: "body.txt"}}}, Sequences: []manifest.Sequence{{ID: "welcome", List: "news", Entry: "subscription", Reentry: "once", Steps: []manifest.Step{{ID: "send", Type: "send", Message: "welcome", Next: "wait"}, {ID: "wait", Type: "delay", Delay: "1h", Next: "done"}, {ID: "done", Type: "complete"}}}}}
	dep, err := app.Do(ctx, admin, engine.Operation{Project: "restore", Resource: "releases", Action: "deploy", Input: raw(map[string]any{"manifest": m, "expected_release": ""})})
	if err != nil {
		t.Fatal(err)
	}
	releaseID := dep.(map[string]any)["id"].(string)
	if _, err = app.Do(ctx, engine.Authority{Public: true}, engine.Operation{Project: "restore", Resource: "consent", Action: "subscribe", Input: raw(map[string]string{"email": "recover@example.test", "name": "Recover", "list": "news", "source": "test", "source_ip": "192.0.2.1"})}); err != nil {
		t.Fatal(err)
	}
	if _, err = app.Do(ctx, admin, engine.Operation{Project: "restore", Resource: "webhooks", Action: "create", Input: raw(map[string]any{"name": "recovery-hook", "config": map[string]any{"url": hook.URL, "development": true}})}); err != nil {
		t.Fatal(err)
	}
	transport, err := app.Do(ctx, admin, engine.Operation{Project: "restore", Resource: "transports", Action: "create", Input: raw(map[string]any{"name": "capture", "config": map[string]any{"host": "127.0.0.1", "port": smtpPort, "tls_mode": "plain", "from": "mrkt <mrkt@example.test>", "allow_private": true}})})
	if err != nil {
		t.Fatal(err)
	}
	transportID := transport.(map[string]any)["id"].(string)
	feedbackSecret := transport.(map[string]any)["secret"].(string)
	if err = app.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var listing struct {
		Messages []struct{ ID, Subject string }
	}
	var capturedID string
	var resp *http.Response
	for i := 0; i < 30 && capturedID == ""; i++ {
		resp, err = http.Get(mailpitHTTP + "/api/v1/messages")
		if err == nil {
			listing.Messages = nil
			err = json.NewDecoder(resp.Body).Decode(&listing)
			resp.Body.Close()
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, msg := range listing.Messages {
			if msg.Subject == "Confirm your subscription" {
				capturedID = msg.ID
				break
			}
		}
		if capturedID == "" {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if capturedID == "" {
		t.Fatal("confirmation not captured")
	}
	resp, err = http.Get(mailpitHTTP + "/api/v1/message/" + capturedID)
	if err != nil {
		t.Fatal(err)
	}
	var captured struct{ Text string }
	if err = json.NewDecoder(resp.Body).Decode(&captured); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	match := regexp.MustCompile(`token=([A-Za-z0-9_-]+)`).FindStringSubmatch(captured.Text)
	if len(match) != 2 {
		t.Fatalf("confirmation token missing: %s", captured.Text)
	}
	if _, err = app.Do(ctx, engine.Authority{Public: true}, engine.Operation{Project: "restore", Resource: "consent", Action: "confirm", Input: raw(map[string]string{"token": match[1]})}); err != nil {
		t.Fatal(err)
	}
	if err = app.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	listed, err := app.Do(ctx, admin, engine.Operation{Project: "restore", Resource: "deliveries", Action: "list"})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(listed)
	var deliveryPage struct {
		Items []struct {
			MessageID string `json:"message_id"`
		}
	}
	if err = json.Unmarshal(encoded, &deliveryPage); err != nil || len(deliveryPage.Items) == 0 {
		t.Fatalf("delivery missing: %s %v", encoded, err)
	}
	deliveryMessageID := deliveryPage.Items[0].MessageID
	if _, err = app.Do(ctx, engine.Authority{Public: true}, engine.Operation{Project: "restore", Resource: "consent", Action: "subscribe", Input: raw(map[string]string{"email": "unsubscribed@example.test", "name": "Gone", "list": "news", "source": "test", "source_ip": "192.0.2.2"})}); err != nil {
		t.Fatal(err)
	}
	allConsent, err := app.Do(ctx, admin, engine.Operation{Project: "restore", Resource: "consent", Action: "list"})
	if err != nil {
		t.Fatal(err)
	}
	consentJSON, _ := json.Marshal(allConsent)
	var cp struct {
		Items []struct {
			ContactID string `json:"contact_id"`
			State     string `json:"state"`
		} `json:"items"`
	}
	if err = json.Unmarshal(consentJSON, &cp); err != nil {
		t.Fatal(err)
	}
	var pendingContact string
	for _, c := range cp.Items {
		if c.State == "pending" {
			pendingContact = c.ContactID
		}
	}
	if pendingContact == "" {
		t.Fatal("second pending consent missing")
	}
	if _, err = app.Do(ctx, admin, engine.Operation{Project: "restore", Resource: "consent", Action: "unsubscribe", Input: raw(map[string]string{"contact_id": pendingContact, "list": "news"})}); err != nil {
		t.Fatal(err)
	}
	if _, err = app.Do(ctx, admin, engine.Operation{Project: "restore", Resource: "events", Action: "create", Key: "restore-event", Input: raw(map[string]any{"key": "restore-event", "type": "backup.created", "payload": map[string]any{"source": "integration"}})}); err != nil {
		t.Fatal(err)
	}
	hitsBeforeRecovery := webhookHits
	app.Close()
	config := filepath.Join(source, "litestream.yml")
	cfg := fmt.Sprintf("dbs:\n  - path: /data/mrkt.db\n    monitor-interval: 200ms\n    replica:\n      type: s3\n      bucket: mrkt\n      path: backups/test/sqlite\n      region: us-east-1\n      endpoint: http://%s:9000\n      force-path-style: true\n", minio)
	if err = os.WriteFile(config, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	docker(t, "run", "-d", "--name", rep, "--network", network, "-e", "AWS_ACCESS_KEY_ID=testkey", "-e", "AWS_SECRET_ACCESS_KEY=testsecret", "-v", source+":/data", "-v", config+":/etc/litestream.yml:ro", "litestream/litestream:0.5.14", "replicate", "-config", "/etc/litestream.yml")
	time.Sleep(3 * time.Second)
	docker(t, "stop", "-t", "30", rep)
	docker(t, "run", "--rm", "--network", network, "-e", "AWS_ACCESS_KEY_ID=testkey", "-e", "AWS_SECRET_ACCESS_KEY=testsecret", "-v", restore+":/restore", "-v", config+":/etc/litestream.yml:ro", "litestream/litestream:0.5.14", "restore", "-config", "/etc/litestream.yml", "-integrity-check", "full", "-o", "/restore/mrkt.db", "/data/mrkt.db")
	docker(t, "run", "--rm", "-v", restore+":/restore", "alpine:3.23.3", "chmod", "0666", "/restore/mrkt.db")
	restoredPath := filepath.Join(restore, "mrkt.db")
	if err = os.WriteFile(restoredPath+".recovery-required", []byte("restore\n"), 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := engine.Open(engine.Config{DBPath: restoredPath, PublicURL: "http://localhost", From: "mrkt <mrkt@example.test>", Development: true, Artifacts: store, Sender: sender, EncryptionKey: make([]byte, 32), WebhookDevelopmentAllowedHosts: []string{hookURL.Host}})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	status, err := restored.Do(ctx, admin, engine.Operation{Resource: "installation", Action: "get"})
	if err != nil {
		t.Fatal(err)
	}
	sm := status.(map[string]any)
	if sm["recovery"] != true || sm["outbound_paused"] != true {
		t.Fatalf("marker did not force pause: %#v", sm)
	}
	releases, err := restored.Do(ctx, admin, engine.Operation{Project: "restore", Resource: "releases", Action: "list"})
	if err != nil || !strings.Contains(fmt.Sprint(releases), releaseID) {
		t.Fatalf("release missing: %#v %v", releases, err)
	}
	consents, err := restored.Do(ctx, admin, engine.Operation{Project: "restore", Resource: "consent", Action: "list"})
	if err != nil || !strings.Contains(fmt.Sprint(consents), "confirmed") || !strings.Contains(fmt.Sprint(consents), "unsubscribed") {
		t.Fatalf("consent missing: %#v %v", consents, err)
	}
	usage, err := restored.Do(ctx, admin, engine.Operation{Project: "restore", Resource: "operations", Action: "usage"})
	if err != nil || usage.(map[string]any)["counts"].(map[string]int64)["jobs"] < 1 {
		t.Fatalf("queue missing: %#v %v", usage, err)
	}
	for _, f := range files {
		if _, err = store.Stat(ctx, "restore", f.SHA256); err != nil {
			t.Fatalf("artifact reference %s missing: %v", f.Path, err)
		}
	}
	if err = restored.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if webhookHits != hitsBeforeRecovery {
		t.Fatal("business webhook delivered during recovery")
	}
	feedback := raw(map[string]any{"id": "feedback-after-restore", "type": "bounce", "message_id": deliveryMessageID, "email": "recover@example.test", "occurred_at": time.Now().UTC().Format(time.RFC3339Nano)})
	ts := time.Now().Unix()
	sig := security.SignWebhook([]byte(feedbackSecret), feedback, ts)
	_, err = restored.Do(ctx, engine.Authority{Public: true}, engine.Operation{Project: "restore", Resource: "feedback", Action: "ingest", ID: transportID, Input: raw(map[string]any{"body": []byte(feedback), "timestamp": ts, "signature": "v1=" + sig})})
	if err != nil {
		t.Fatalf("signed inbound feedback was not ingestible during recovery: %v", err)
	}
	deliveriesAfter, err := restored.Do(ctx, admin, engine.Operation{Project: "restore", Resource: "deliveries", Action: "list"})
	if err != nil || !strings.Contains(fmt.Sprint(deliveriesAfter), "state:"+"bounce") {
		t.Fatalf("bounce not applied during recovery: %#v %v", deliveriesAfter, err)
	}
	if err = os.WriteFile(restoredPath+".existing", []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	overwrite := exec.Command("docker", "run", "--rm", "--network", network, "-e", "AWS_ACCESS_KEY_ID=testkey", "-e", "AWS_SECRET_ACCESS_KEY=testsecret", "-v", restore+":/restore", "-v", config+":/etc/litestream.yml:ro", "litestream/litestream:0.5.14", "restore", "-config", "/etc/litestream.yml", "-o", "/restore/mrkt.db", "/data/mrkt.db")
	if err = overwrite.Run(); err == nil {
		t.Fatal("restore overwrote existing database")
	}
	failed := t.TempDir()
	badConfig := filepath.Join(failed, "litestream.yml")
	bad := strings.Replace(cfg, "bucket: mrkt", "bucket: missing-backup", 1)
	if err = os.WriteFile(badConfig, []byte(bad), 0600); err != nil {
		t.Fatal(err)
	}
	attempt := exec.Command("docker", "run", "--rm", "--network", network, "-e", "AWS_ACCESS_KEY_ID=testkey", "-e", "AWS_SECRET_ACCESS_KEY=testsecret", "-v", failed+":/failed", "-v", badConfig+":/etc/litestream.yml:ro", "litestream/litestream:0.5.14", "restore", "-config", "/etc/litestream.yml", "-o", "/failed/mrkt.db", "/data/mrkt.db")
	if err = attempt.Run(); err == nil {
		t.Fatal("missing backup restore unexpectedly succeeded")
	}
	if _, err = os.Stat(filepath.Join(failed, "mrkt.db")); !os.IsNotExist(err) {
		t.Fatalf("failed restore created database: %v", err)
	}
}
