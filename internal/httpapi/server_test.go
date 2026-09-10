package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattmezza/mrkt/internal/artifact"
	"github.com/mattmezza/mrkt/internal/engine"
)

func TestHTTPAuthorityAndSessions(t *testing.T) {
	app, err := engine.Open(engine.Config{DBPath: filepath.Join(t.TempDir(), "app.db"), Initialize: true, AdminToken: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	admin := engine.Authority{Admin: true}
	ctx := context.Background()
	for _, p := range []string{"one", "two"} {
		b, _ := json.Marshal(map[string]string{"id": p, "name": p})
		if _, err = app.Do(ctx, admin, engine.Operation{Resource: "projects", Action: "create", Input: b}); err != nil {
			t.Fatal(err)
		}
	}
	created, err := app.Do(ctx, admin, engine.Operation{Resource: "tokens", Action: "create", Project: "one", Input: json.RawMessage(`{"name":"reader","scopes":["read"]}`)})
	if err != nil {
		t.Fatal(err)
	}
	token := created.(map[string]any)["token"].(string)
	s := New(Config{App: app, Artifacts: artifact.NewMemory(1 << 20), Development: true, PublicURL: "http://mrkt.test", SessionKey: []byte(strings.Repeat("k", 32))})
	cases := []struct {
		method, path, token string
		status              int
	}{{"GET", "/healthz", "", 200}, {"GET", "/api/v1/projects/one/contacts", "", 401}, {"GET", "/api/v1/projects/two/contacts", token, 403}, {"GET", "/api/v1/projects/one/contacts", token, 200}, {"POST", "/api/v1/projects/one/releases/_/deploy", token, 403}, {"GET", "/assets/two/" + strings.Repeat("0", 64) + "/image.png", "", 404}, {"GET", "/login", "", 200}, {"POST", "/ui/action", "", 401}}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, strings.NewReader(`{}`))
		if c.token != "" {
			r.Header.Set("Authorization", "Bearer "+c.token)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != c.status {
			t.Errorf("%s %s: %d %s", c.method, c.path, w.Code, w.Body.String())
		}
	}
	req := httptest.NewRequest("POST", "/login", strings.NewReader("token="+token))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://attacker.example")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("cross origin login accepted")
	}
	req = httptest.NewRequest("POST", "/login", strings.NewReader("token="+token))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://mrkt.test")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != 303 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("session cookie security missing")
	}
	req = httptest.NewRequest("POST", "/ui/action", strings.NewReader("resource=contacts&action=delete&project=one&id=x"))
	req.AddCookie(cookies[0])
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("missing CSRF accepted")
	}
	req = httptest.NewRequest("GET", "/?project=one", nil)
	req.AddCookie(cookies[0])
	w = httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Overview") {
		t.Fatalf("project UI failed: %d %s", w.Code, w.Body.String())
	}
	req = httptest.NewRequest("POST", "/public/one/subscribe", strings.NewReader(`{"email":"nobody@example.test","list":"news"}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != 503 {
		t.Fatal("missing challenge silently bypassed")
	}
}

func TestEncryptedSessionAndStrictActionMethods(t *testing.T) {
	s := New(Config{SessionKey: []byte(strings.Repeat("k", 32))})
	v := session{Token: "very-secret-api-token", Expires: time.Now().Add(time.Hour).Unix(), CSRF: "csrf"}
	cookie := s.signSession(v)
	if strings.Contains(cookie, v.Token) {
		t.Fatal("plaintext session token")
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "mrkt_session", Value: cookie})
	got, err := s.readSession(r)
	if err != nil || got.Token != v.Token {
		t.Fatalf("session roundtrip %v", err)
	}
	r = httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: "mrkt_session", Value: cookie + "x"})
	if _, err = s.readSession(r); err == nil {
		t.Fatal("tampered session accepted")
	}
}

func TestProjectConfigurationTokenCanUpload(t *testing.T) {
	app, err := engine.Open(engine.Config{DBPath: filepath.Join(t.TempDir(), "app.db"), Initialize: true, AdminToken: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	ctx := context.Background()
	a := engine.Authority{Admin: true}
	_, err = app.Do(ctx, a, engine.Operation{Resource: "projects", Action: "create", Input: json.RawMessage(`{"id":"one","name":"one"}`)})
	if err != nil {
		t.Fatal(err)
	}
	v, err := app.Do(ctx, a, engine.Operation{Project: "one", Resource: "tokens", Action: "create", Input: json.RawMessage(`{"name":"publisher","scopes":["config"]}`)})
	if err != nil {
		t.Fatal(err)
	}
	token := v.(map[string]any)["token"].(string)
	s := New(Config{App: app, Artifacts: artifact.NewMemory(1 << 20)})
	sum := sha256.Sum256([]byte("synthetic"))
	r := httptest.NewRequest("PUT", "/api/v1/projects/one/artifacts/"+hex.EncodeToString(sum[:]), strings.NewReader("synthetic"))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("upload %d %s", w.Code, w.Body.String())
	}
	r = httptest.NewRequest("DELETE", "/api/v1/projects/one/releases/id/rollback", strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 405 {
		t.Fatalf("DELETE action accepted: %d", w.Code)
	}
}
