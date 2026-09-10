package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDoRoutesAndHeaders(t *testing.T) {
	var gotPath, gotAuth, gotKey string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotKey = r.Header.Get("Idempotency-Key")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	defer s.Close()
	c, _ := New(s.URL, "secret")
	_, err := c.Do(context.Background(), Operation{Project: "demo", Resource: "broadcasts", ID: "spring", Action: "pause", Key: "one", Input: map[string]any{"reason": "test"}})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/projects/demo/broadcasts/spring/pause" || gotAuth != "Bearer secret" || gotKey != "one" {
		t.Fatalf("unexpected request: %s %s %s", gotPath, gotAuth, gotKey)
	}
}
func TestErrorEnvelope(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(409)
		io.WriteString(w, `{"error":{"code":"conflict","message":"stale"}}`)
	}))
	defer s.Close()
	c, _ := New(s.URL, "")
	_, err := c.Do(context.Background(), Operation{Resource: "projects"})
	apiErr, ok := err.(*Error)
	if !ok || apiErr.Code != "conflict" || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("unexpected error: %v", err)
	}
}
