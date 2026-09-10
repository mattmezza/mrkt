//go:build integration

package artifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"
)

func TestS3RealCompatibleStore(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "mrktdev")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "mrktdev-secret")
	ctx := context.Background()
	s, err := NewS3(ctx, S3Config{Region: "us-east-1", Bucket: "mrkt", Prefix: "integration", Endpoint: "http://127.0.0.1:9000", PathStyle: true, MaxObjectSize: 1024})
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("real object storage")
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	if err = s.Put(ctx, "Project_A", hash, "text/plain", int64(len(data)), bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if err = s.Put(ctx, "Project_A", hash, "text/plain", int64(len(data)), bytes.NewReader(data)); err != nil {
		t.Fatalf("idempotent put: %v", err)
	}
	info, err := s.Stat(ctx, "Project_A", hash)
	if err != nil || info.Size != int64(len(data)) || info.ContentType != "text/plain" {
		t.Fatalf("stat=%+v err=%v", info, err)
	}
	r, err := s.Get(ctx, "Project_A", hash)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got, _ := io.ReadAll(r)
	if !bytes.Equal(got, data) {
		t.Fatalf("got %q", got)
	}
	if _, err = s.Get(ctx, "Project_B", hash); err == nil {
		t.Fatal("cross-project object visible")
	}
}
