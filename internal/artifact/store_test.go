package artifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"testing"
)

func TestMemoryPutVerifiesAndIsTenantScoped(t *testing.T) {
	ctx := context.Background()
	s := NewMemory(8)
	data := []byte("hello")
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	if err := s.Put(ctx, "Proj_1", hash, "text/plain", 5, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Stat(ctx, "other", hash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-project stat = %v", err)
	}
	r, err := s.Get(ctx, "Proj_1", hash)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got, _ := io.ReadAll(r)
	if !bytes.Equal(got, data) {
		t.Fatal("content differs")
	}
}
func TestMemoryRejectsDigestSizeAndTraversal(t *testing.T) {
	s := NewMemory(4)
	bad := string(make([]byte, 64))
	for _, tc := range []struct {
		project, hash string
		size          int64
		body          string
	}{{"../x", bad, 1, "x"}, {"ok", bad, 5, "hello"}, {"ok", bad, 1, "x"}} {
		if err := s.Put(context.Background(), tc.project, tc.hash, "text/plain", tc.size, bytes.NewBufferString(tc.body)); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
}
