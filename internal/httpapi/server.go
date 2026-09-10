package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mattmezza/mrkt/internal/artifact"
	"github.com/mattmezza/mrkt/internal/engine"
	"github.com/mattmezza/mrkt/internal/manifest"
	"github.com/mattmezza/mrkt/internal/security"
)

type Application interface {
	Authenticate(context.Context, string) (engine.Authority, error)
	Do(context.Context, engine.Authority, engine.Operation) (any, error)
}
type Challenge interface {
	Verify(context.Context, string, string) error
}
type Config struct {
	App            Application
	Artifacts      artifact.Store
	Development    bool
	PublicURL      string
	SessionKey     []byte
	Challenge      Challenge
	TrustedProxies []net.IPNet
}
type Server struct {
	cfg Config
	mux *http.ServeMux
}

func New(cfg Config) *Server {
	s := &Server{cfg: cfg, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	s.mux.HandleFunc("/api/v1/", s.api)
	s.mux.HandleFunc("/public/{project}/{action}", s.public)
	s.mux.HandleFunc("GET /assets/{project}/{hash}/{name}", s.asset)
	s.mux.HandleFunc("POST /feedback/{project}/{transport}", s.feedback)
	s.registerUI()
	return s
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	if strings.HasPrefix(r.URL.Path, "/public/") {
		w.Header().Set("Referrer-Policy", "no-referrer")
	}
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; frame-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	w.Header().Set("Cache-Control", "no-store")
	if !s.cfg.Development {
		w.Header().Set("Strict-Transport-Security", "max-age=31536000")
	}
	s.mux.ServeHTTP(w, r)
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, err error) {
	var e *engine.Error
	if errors.As(err, &e) {
		if e.Status >= 500 {
			writeJSON(w, e.Status, map[string]any{"error": map[string]string{"code": e.Code, "message": "operation failed; inspect server diagnostics"}})
		} else {
			writeJSON(w, e.Status, map[string]any{"error": map[string]string{"code": e.Code, "message": e.Message}})
		}
		return
	}
	writeJSON(w, 500, map[string]any{"error": map[string]string{"code": "internal", "message": "operation failed"}})
}
func bad(status int, code, msg string) error {
	return &engine.Error{Status: status, Code: code, Message: msg}
}
func (s *Server) bearer(r *http.Request) (engine.Authority, error) {
	v := r.Header.Get("Authorization")
	if !strings.HasPrefix(v, "Bearer ") {
		return engine.Authority{}, bad(401, "unauthorized", "Bearer token required")
	}
	return s.cfg.App.Authenticate(r.Context(), strings.TrimPrefix(v, "Bearer "))
}
func body(w http.ResponseWriter, r *http.Request) (json.RawMessage, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, bad(413, "body_too_large", "request exceeds 2 MiB")
	}
	if len(b) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid(b) {
		return nil, bad(400, "invalid_json", "one valid JSON value required")
	}
	return b, nil
}
func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	a, err := s.bearer(r)
	if err != nil {
		fail(w, err)
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/"), "/"), "/")
	op := engine.Operation{Key: r.Header.Get("Idempotency-Key"), Cursor: r.URL.Query().Get("cursor")}
	op.Filters = map[string]string{}
	for key, values := range r.URL.Query() {
		if key == "cursor" || key == "limit" {
			continue
		}
		if !slices.Contains([]string{"state", "contact_id", "list_id", "release_id"}, key) || len(values) != 1 || len(values[0]) > 200 {
			fail(w, bad(400, "invalid_filter", "unsupported or invalid query filter"))
			return
		}
		op.Filters[key] = values[0]
	}
	if len(op.Key) > 200 {
		fail(w, bad(400, "invalid_key", "idempotency key too long"))
		return
	}
	if l := r.URL.Query().Get("limit"); l != "" {
		op.Limit, err = strconv.Atoi(l)
		if err != nil {
			fail(w, bad(400, "invalid_limit", "limit must be an integer"))
			return
		}
	}
	switch {
	case len(parts) >= 1 && parts[0] == "installation" && len(parts) <= 2:
		op.Resource = "installation"
		op.Action = "get"
		if len(parts) == 2 {
			op.Action = parts[1]
		}
	case len(parts) == 1 && parts[0] == "projects":
		op.Resource = "projects"
		op.Action = "list"
		if r.Method == "POST" {
			op.Action = "create"
		}
	case len(parts) >= 3 && len(parts) <= 5 && parts[0] == "projects":
		op.Project = parts[1]
		op.Resource = parts[2]
		op.Action = "list"
		if len(parts) > 3 {
			op.ID = parts[3]
			op.Action = "get"
		}
		if len(parts) == 5 {
			op.Action = parts[4]
		}
		if len(parts) == 3 && r.Method == "POST" {
			op.Action = "create"
		}
		if len(parts) == 4 && r.Method == "DELETE" {
			op.Action = "delete"
		}
	default:
		fail(w, bad(404, "not_found", "unknown API route"))
		return
	}
	if op.Resource == "artifacts" {
		s.artifactAPI(w, r, a, op)
		return
	}
	read := op.Action == "list" || op.Action == "get"
	if r.Method == "DELETE" && (len(parts) != 4 || op.Action != "delete") {
		fail(w, bad(405, "method_not_allowed", "DELETE is only supported for a resource ID"))
		return
	}
	if (read && r.Method != "GET") || (!read && r.Method != "POST" && r.Method != "DELETE") {
		fail(w, bad(405, "method_not_allowed", "incorrect HTTP method"))
		return
	}
	if r.Method == "GET" {
		op.Input = json.RawMessage(`{}`)
	} else {
		op.Input, err = body(w, r)
		if err != nil {
			fail(w, err)
			return
		}
	}
	result, err := s.cfg.App.Do(r.Context(), a, op)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, result)
}
func (s *Server) artifactAPI(w http.ResponseWriter, r *http.Request, a engine.Authority, op engine.Operation) {
	scope := "config"
	if r.Method == "GET" {
		scope = "read"
	}
	if !a.Admin && (a.Project != op.Project || !slices.Contains(a.Scopes, scope) && !slices.Contains(a.Scopes, "*")) {
		fail(w, bad(403, "forbidden", scope+" scope required"))
		return
	}
	// A project ID is not authority: verify its existence and ownership through the service.
	if _, err := s.cfg.App.Do(r.Context(), a, engine.Operation{Resource: "projects", Action: "get", Project: op.Project, ID: op.Project}); err != nil {
		fail(w, err)
		return
	}
	if s.cfg.Artifacts == nil {
		fail(w, bad(503, "storage_unavailable", "S3 storage unavailable"))
		return
	}
	if err := artifact.ValidateRef(op.Project, op.ID); err != nil {
		fail(w, bad(400, "invalid_artifact", err.Error()))
		return
	}
	switch r.Method {
	case "PUT":
		if r.ContentLength < 0 || r.ContentLength > manifest.MaxFileSize {
			fail(w, bad(413, "invalid_size", "Content-Length required, at most 8 MiB"))
			return
		}
		if err := s.cfg.Artifacts.Put(r.Context(), op.Project, op.ID, r.Header.Get("Content-Type"), r.ContentLength, http.MaxBytesReader(w, r.Body, manifest.MaxFileSize)); err != nil {
			fail(w, bad(400, "artifact_upload_failed", err.Error()))
			return
		}
		writeJSON(w, 201, map[string]string{"sha256": op.ID})
	case "GET":
		rd, err := s.cfg.Artifacts.Get(r.Context(), op.Project, op.ID)
		if err != nil {
			fail(w, bad(404, "not_found", "artifact unavailable"))
			return
		}
		defer rd.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", "attachment")
		_, _ = io.Copy(w, io.LimitReader(rd, manifest.MaxFileSize+1))
	default:
		fail(w, bad(405, "method_not_allowed", "use PUT or GET"))
	}
}
func (s *Server) sourceIP(r *http.Request) string {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	trusted := false
	for _, n := range s.cfg.TrustedProxies {
		if n.Contains(ip) {
			trusted = true
			break
		}
	}
	if trusted {
		chain := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		for i := len(chain) - 1; i >= 0; i-- {
			candidate := net.ParseIP(strings.TrimSpace(chain[i]))
			if candidate == nil {
				continue
			}
			host = candidate.String()
			known := false
			for _, n := range s.cfg.TrustedProxies {
				if n.Contains(candidate) {
					known = true
					break
				}
			}
			if !known {
				break
			}
		}
	}
	return host
}
func (s *Server) public(w http.ResponseWriter, r *http.Request) {
	project, action := r.PathValue("project"), r.PathValue("action")
	if !slices.Contains([]string{"subscribe", "confirm", "unsubscribe", "preferences"}, action) {
		http.NotFound(w, r)
		return
	}
	if r.Method == "GET" && action != "subscribe" {
		s.publicPage(w, r, action, project, r.URL.Query().Get("token"))
		return
	}
	if r.Method != "POST" {
		fail(w, bad(405, "method_not_allowed", "POST required"))
		return
	}
	var values map[string]any
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		b, err := body(w, r)
		if err != nil {
			fail(w, err)
			return
		}
		if err = json.Unmarshal(b, &values); err != nil {
			fail(w, bad(400, "invalid_input", "object required"))
			return
		}
	} else {
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		if err := r.ParseForm(); err != nil {
			fail(w, bad(400, "invalid_input", "invalid form"))
			return
		}
		values = map[string]any{}
		for k, v := range r.PostForm {
			if len(v) > 0 {
				values[k] = v[0]
			}
		}
	}
	if values == nil {
		fail(w, bad(400, "invalid_input", "object required"))
		return
	}
	if action == "subscribe" {
		token, _ := values["challenge_token"].(string)
		if s.cfg.Challenge == nil {
			fail(w, bad(503, "challenge_unavailable", "subscription validation unavailable"))
			return
		}
		if err := s.cfg.Challenge.Verify(r.Context(), token, s.sourceIP(r)); err != nil {
			fail(w, bad(400, "challenge_failed", "challenge validation failed"))
			return
		}
		delete(values, "challenge_token")
		values["source_ip"] = s.sourceIP(r)
	}
	delete(values, "csrf")
	delete(values, "List-Unsubscribe")
	if value, ok := values["project_wide"].(string); ok {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			fail(w, bad(400, "invalid_input", "project_wide must be boolean"))
			return
		}
		values["project_wide"] = parsed
	}
	if action == "unsubscribe" && values["token"] == nil {
		values["token"] = r.URL.Query().Get("token")
	}
	b, _ := json.Marshal(values)
	locale := "en"
	formResponse := !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") && (action == "confirm" || action == "unsubscribe")
	if formResponse {
		prefInput, _ := json.Marshal(map[string]any{"token": values["token"]})
		if prefs, er := s.cfg.App.Do(r.Context(), engine.Authority{Public: true, Project: project}, engine.Operation{Project: project, Resource: "consent", Action: "preferences", Input: prefInput}); er == nil {
			data, _ := json.Marshal(prefs)
			var p struct {
				Locale string `json:"locale"`
			}
			_ = json.Unmarshal(data, &p)
			locale = p.Locale
		}
	}
	result, err := s.cfg.App.Do(r.Context(), engine.Authority{Public: true, Project: project}, engine.Operation{Project: project, Resource: "consent", Action: action, Input: b})
	if err != nil {
		fail(w, err)
		return
	}
	if formResponse {
		s.publicResult(w, locale, action)
		return
	}
	writeJSON(w, 200, result)
}
func (s *Server) asset(w http.ResponseWriter, r *http.Request) {
	// Explicit inventory lookup; public authority cannot inspect arbitrary releases.
	a := engine.Authority{Public: true, Project: r.PathValue("project")}
	v, err := s.cfg.App.Do(r.Context(), a, engine.Operation{Project: a.Project, Resource: "assets", Action: "get", ID: r.PathValue("hash"), Input: json.RawMessage(`{}`)})
	if err != nil {
		fail(w, bad(404, "not_found", "public asset unavailable"))
		return
	}
	b, _ := json.Marshal(v)
	var meta struct {
		ContentType string `json:"content_type"`
	}
	_ = json.Unmarshal(b, &meta)
	if !slices.Contains([]string{"image/png", "image/jpeg", "image/gif", "image/webp"}, meta.ContentType) {
		http.NotFound(w, r)
		return
	}
	rd, err := s.cfg.Artifacts.Get(r.Context(), a.Project, r.PathValue("hash"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer rd.Close()
	w.Header().Set("Content-Type", meta.ContentType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	_, _ = io.Copy(w, io.LimitReader(rd, manifest.MaxFileSize+1))
}

type session struct {
	Token   string `json:"token"`
	Expires int64  `json:"expires"`
	CSRF    string `json:"csrf"`
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
func (s *Server) signSession(v session) string {
	b, _ := json.Marshal(v)
	key := sha256.Sum256(append([]byte("mrkt/ui-session/v1:"), s.cfg.SessionKey...))
	box, _ := security.NewSecretBox(key[:])
	sealed, err := box.Seal(b, "mrkt/ui-session/v1")
	if err != nil {
		return ""
	}
	return sealed
}
func (s *Server) readSession(r *http.Request) (session, error) {
	var v session
	c, err := r.Cookie("mrkt_session")
	if err != nil {
		return v, err
	}
	key := sha256.Sum256(append([]byte("mrkt/ui-session/v1:"), s.cfg.SessionKey...))
	box, _ := security.NewSecretBox(key[:])
	b, err := box.Open(c.Value, "mrkt/ui-session/v1")
	if err != nil {
		return v, err
	}
	if err = json.Unmarshal(b, &v); err != nil {
		return v, err
	}
	if v.Expires < time.Now().Unix() {
		return v, errors.New("expired session")
	}
	return v, nil
}
func (s *Server) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(s.cfg.PublicURL)
	if err != nil {
		return false
	}
	return origin == u.Scheme+"://"+u.Host
}
func (s *Server) uiAuthority(r *http.Request) (engine.Authority, session, error) {
	v, err := s.readSession(r)
	if err != nil {
		return engine.Authority{}, v, err
	}
	a, err := s.cfg.App.Authenticate(r.Context(), v.Token)
	return a, v, err
}

var _ = fmt.Sprintf

func (s *Server) feedback(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		fail(w, bad(413, "body_too_large", "feedback exceeds 1 MiB"))
		return
	}
	timestamp, _ := strconv.ParseInt(r.Header.Get("X-Mrkt-Timestamp"), 10, 64)
	payload, _ := json.Marshal(map[string]any{"body": raw, "timestamp": timestamp, "signature": r.Header.Get("X-Mrkt-Signature")})
	project := r.PathValue("project")
	result, err := s.cfg.App.Do(r.Context(), engine.Authority{Public: true, Project: project}, engine.Operation{Project: project, Resource: "feedback", Action: "ingest", ID: r.PathValue("transport"), Input: payload})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, result)
}
