//go:build integration

package deploy_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/mattmezza/mrkt/internal/artifact"
	"github.com/mattmezza/mrkt/internal/engine"
	"github.com/mattmezza/mrkt/internal/mail"
)

// TestSyntheticWorkload measures a fixed workload against the Compose MinIO and
// Mailpit services. It is an observation, not a capacity benchmark.
func TestSyntheticWorkload(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "mrktdev")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "mrktdev-secret")
	ctx := context.Background()
	store, err := artifact.NewS3(ctx, artifact.S3Config{Region: "us-east-1", Bucket: "mrkt", Prefix: "benchmark", Endpoint: "http://127.0.0.1:9000", PathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	sender, err := mail.NewSMTP(mail.SMTPConfig{Host: "127.0.0.1", Port: 1025, TLSMode: "plain", Development: true})
	if err != nil {
		t.Fatal(err)
	}
	app, err := engine.Open(engine.Config{DBPath: filepath.Join(t.TempDir(), "mrkt.db"), Initialize: true, AdminToken: "benchmark-admin", PublicURL: "http://localhost", From: "mrkt <mrkt@example.test>", Development: true, Artifacts: store, Sender: sender, EncryptionKey: make([]byte, 32)})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	admin := engine.Authority{Admin: true, Scopes: []string{"*"}}
	if _, err = app.Do(ctx, admin, engine.Operation{Resource: "projects", Action: "create", Input: raw(map[string]string{"id": "benchmark", "name": "Benchmark"})}); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	for i := 0; i < 1000; i++ {
		_, err = app.Do(ctx, admin, engine.Operation{Project: "benchmark", Resource: "contacts", Action: "create", Input: raw(map[string]any{"external_id": fmt.Sprintf("c-%04d", i), "email": fmt.Sprintf("synthetic-%04d@example.test", i), "name": "Synthetic", "locale": "en", "attributes": map[string]any{"cohort": i % 10}})})
		if err != nil {
			t.Fatal(err)
		}
	}
	contactsElapsed := time.Since(started)
	started = time.Now()
	for i := 0; i < 100; i++ {
		data := bytes.Repeat([]byte{byte(i)}, 4096)
		sum := sha256.Sum256(data)
		hash := hex.EncodeToString(sum[:])
		if err = store.Put(ctx, "benchmark", hash, "application/octet-stream", int64(len(data)), bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	}
	s3Elapsed := time.Since(started)
	started = time.Now()
	for i := 0; i < 100; i++ {
		r, e := sender.Send(ctx, mail.Message{ID: fmt.Sprintf("benchmark-%d@mrkt", i), From: "mrkt <mrkt@example.test>", To: fmt.Sprintf("synthetic-%04d@example.test", i), Subject: "Synthetic benchmark", Text: "Bounded local capture test", HTML: "<p>Bounded local capture test</p>"})
		if e != nil || r.State != mail.StateAccepted {
			t.Fatalf("SMTP %d: %+v %v", i, r, e)
		}
	}
	smtpElapsed := time.Since(started)
	t.Logf("1000 SQLite contact upserts=%s; 100 x 4KiB S3 puts=%s; 100 SMTP captures=%s", contactsElapsed, s3Elapsed, smtpElapsed)
}
