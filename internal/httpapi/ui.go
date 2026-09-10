package httpapi

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mattmezza/mrkt/internal/engine"
)

//go:embed templates/*.html static/*
var uiFiles embed.FS
var templates = template.Must(template.New("").Funcs(template.FuncMap{"title": func(v string) string {
	if v == "" {
		return ""
	}
	return strings.ToUpper(v[:1]) + strings.NewReplacer("-", " ", "_", " ").Replace(v[1:])
}, "json": func(v any) string { b, _ := json.MarshalIndent(v, "", "  "); return string(b) }, "cell": cell, "list": func(v ...string) []string { return v }}).ParseFS(uiFiles, "templates/*.html"))

type navItem struct{ Name, Label string }

var navigation = []navItem{{"overview", "Overview"}, {"contacts", "Contacts"}, {"lists", "Lists & consent"}, {"releases", "Releases"}, {"enrollments", "Sequences"}, {"broadcasts", "Broadcasts"}, {"deliveries", "Deliveries"}, {"events", "Events"}, {"domains", "Domains"}, {"transports", "Transports"}, {"webhooks", "Webhooks"}, {"webhook-deliveries", "Webhook activity"}, {"operations", "Operations"}, {"installation", "Backup & recovery"}}

type pageData struct {
	Title, Resource, Project, CSRF, Error, Success, Cursor, NextCursor, Token, Action, PreviewMessage, PreviewLocale string
	Nav                                                                                                              []navItem
	Projects                                                                                                         []map[string]any
	Items                                                                                                            []map[string]any
	Columns                                                                                                          []string
	Detail                                                                                                           map[string]any
	Plan                                                                                                             map[string]any
	Messages, Locales                                                                                                []string
	Stats                                                                                                            map[string]int
	Recovery                                                                                                         bool
	Development                                                                                                      bool
}

func object(v any) map[string]any {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func cell(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return "—"
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}
func rows(v any) ([]map[string]any, string) {
	b, _ := json.Marshal(v)
	var l struct {
		Items []map[string]any `json:"items"`
		Next  string           `json:"next_cursor"`
	}
	_ = json.Unmarshal(b, &l)
	return l.Items, l.Next
}
func (s *Server) render(w http.ResponseWriter, name string, d pageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := templates.ExecuteTemplate(w, name, d); err != nil {
		fmt.Printf("UI rendering failed: %v\n", err)
	}
}
func (s *Server) registerUI() {
	static, _ := fs.Sub(uiFiles, "static")
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
	s.mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		s.render(w, "login", pageData{Development: s.cfg.Development})
	})
	s.mux.HandleFunc("POST /login", s.login)
	s.mux.HandleFunc("POST /logout", s.logout)
	s.mux.HandleFunc("POST /ui/action", s.uiAction)
	s.mux.HandleFunc("GET /ui/preview", s.uiPreview)
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		s.ui(w, r)
	})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		http.Error(w, "Origin rejected", 403)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", 400)
		return
	}
	token := r.FormValue("token")
	if _, err := s.cfg.App.Authenticate(r.Context(), token); err != nil {
		s.render(w, "login", pageData{Error: "That token was not accepted. Check that it is active and try again."})
		return
	}
	v := session{Token: token, Expires: time.Now().Add(8 * time.Hour).Unix(), CSRF: randomToken()}
	http.SetCookie(w, &http.Cookie{Name: "mrkt_session", Value: s.signSession(v), Path: "/", HttpOnly: true, Secure: !s.cfg.Development, SameSite: http.SameSiteStrictMode, MaxAge: 8 * 3600})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	_, v, err := s.uiAuthority(r)
	if err != nil || !s.sameOrigin(r) {
		http.Error(w, "Session required", 403)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if r.ParseForm() != nil || r.FormValue("csrf") != v.CSRF {
		http.Error(w, "CSRF token required", 403)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "mrkt_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: !s.cfg.Development, SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, "/login", 303)
}
func (s *Server) ui(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	a, v, err := s.uiAuthority(r)
	if err != nil {
		http.Redirect(w, r, "/login", 303)
		return
	}
	d := pageData{Resource: r.URL.Query().Get("view"), Project: r.URL.Query().Get("project"), CSRF: v.CSRF, Nav: navigation, Development: s.cfg.Development, Stats: map[string]int{}, Success: r.URL.Query().Get("success"), Cursor: r.URL.Query().Get("cursor")}
	if d.Resource == "" {
		d.Resource = "overview"
	}
	valid := false
	for _, n := range navigation {
		if n.Name == d.Resource {
			d.Title = n.Label
			valid = true
			break
		}
	}
	if !valid {
		http.NotFound(w, r)
		return
	}
	p, err := s.cfg.App.Do(r.Context(), a, engine.Operation{Resource: "projects", Action: "list"})
	if err == nil {
		d.Projects, _ = rows(p)
	} else {
		d.Error = err.Error()
	}
	if d.Project == "" {
		d.Project = a.Project
	}
	if d.Project == "" && len(d.Projects) > 0 {
		d.Project = cell(d.Projects[0]["id"])
	}
	if d.Resource == "installation" {
		var detail any
		detail, err = s.cfg.App.Do(r.Context(), a, engine.Operation{Resource: "installation", Action: "get"})
		d.Detail = object(detail)
		if err != nil {
			d.Error = err.Error()
		}
	} else if d.Project != "" {
		if d.Resource == "overview" {
			for _, resource := range []string{"contacts", "enrollments", "deliveries", "releases"} {
				v, e := s.cfg.App.Do(r.Context(), a, engine.Operation{Project: d.Project, Resource: resource, Action: "list", Limit: 200})
				if e != nil {
					d.Error = e.Error()
					continue
				}
				items, _ := rows(v)
				d.Stats[resource] = len(items)
				if resource == "deliveries" {
					d.Items = items
					if len(d.Items) > 8 {
						d.Items = d.Items[:8]
					}
				}
			}
			d.Columns = []string{"to_email", "state", "subject", "created_at"}
		} else {
			op := engine.Operation{Project: d.Project, Resource: d.Resource, Action: "list", Cursor: d.Cursor, Limit: 50, Input: json.RawMessage(`{}`)}
			if id := r.URL.Query().Get("id"); id != "" {
				op.ID = id
				op.Action = "get"
				if d.Resource == "enrollments" {
					op.Action = "explain"
				}
			}
			data, e := s.cfg.App.Do(r.Context(), a, op)
			if e != nil {
				d.Error = e.Error()
			} else if op.Action != "list" {
				d.Detail = object(data)
				if d.Resource == "releases" {
					if man, ok := d.Detail["manifest"].(map[string]any); ok {
						if msgs, ok := man["messages"].(map[string]any); ok {
							names := make([]string, 0, len(msgs))
							for name := range msgs {
								names = append(names, name)
							}
							sort.Strings(names)
							d.Messages = names
							d.PreviewMessage = r.URL.Query().Get("message")
							if d.PreviewMessage == "" && len(names) > 0 {
								d.PreviewMessage = names[0]
							}
						}
						if p, ok := man["project"].(map[string]any); ok {
							if raw, ok := p["locales"].([]any); ok {
								for _, v := range raw {
									d.Locales = append(d.Locales, cell(v))
								}
							}
							d.PreviewLocale = r.URL.Query().Get("locale")
							if d.PreviewLocale == "" {
								d.PreviewLocale = cell(p["default_locale"])
							}
						}
						expected := ""
						for _, p := range d.Projects {
							if cell(p["id"]) == d.Project {
								expected = cell(p["active_release_id"])
							}
						}
						body, _ := json.Marshal(map[string]any{"manifest": man, "expected_release": expected, "allow_destructive": false})
						if plan, e := s.cfg.App.Do(r.Context(), a, engine.Operation{Project: d.Project, Resource: "releases", ID: "_", Action: "plan", Input: body}); e == nil {
							d.Plan = object(plan)
						}
					}
				}
			} else {
				d.Items, d.NextCursor = rows(data)
			}
			d.Columns = columns(d.Resource, d.Items)
		}
	}
	s.render(w, "app", d)
}
func columns(resource string, items []map[string]any) []string {
	preferred := map[string][]string{
		"contacts": {"email", "name", "locale", "timezone", "external_id"}, "lists": {"name", "purpose", "policy_version", "release_id"}, "releases": {"id", "digest", "status", "activated_at"}, "enrollments": {"sequence_id", "state", "current_step", "next_at", "release_id"}, "broadcasts": {"definition_id", "state", "audience_frozen_at", "release_id"}, "deliveries": {"message_id", "state", "contact_id", "release_id", "detail"}, "events": {"type", "key", "contact_id", "occurred_at", "created_at"}, "domains": {"name", "enabled", "created_at", "updated_at"}, "transports": {"name", "enabled", "created_at", "updated_at"}, "webhooks": {"name", "enabled", "created_at", "updated_at"}, "webhook-deliveries": {"name", "enabled", "created_at", "updated_at"}, "operations": {"id", "action", "created_at"}}
	if p, ok := preferred[resource]; ok {
		return p
	}
	if len(items) == 0 {
		return nil
	}
	keys := []string{}
	for k := range items[0] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) > 5 {
		keys = keys[:5]
	}
	return keys
}

func (s *Server) uiPreview(w http.ResponseWriter, r *http.Request) {
	a, _, err := s.uiAuthority(r)
	if err != nil {
		http.Error(w, "Session required", 401)
		return
	}
	resource := r.URL.Query().Get("resource")
	if resource != "deliveries" && resource != "releases" {
		http.Error(w, "Preview unavailable", 404)
		return
	}
	input := json.RawMessage(`{}`)
	if resource == "releases" {
		b, _ := json.Marshal(map[string]any{"message": r.URL.Query().Get("message"), "locale": r.URL.Query().Get("locale"), "variables": map[string]any{"Name": "Preview", "Email": "preview@example.test", "Locale": r.URL.Query().Get("locale"), "UnsubscribeURL": "https://example.test/unsubscribe", "ConfirmationURL": "https://example.test/confirm", "Attributes": map[string]any{}, "Event": map[string]any{}}})
		input = b
	}
	v, err := s.cfg.App.Do(r.Context(), a, engine.Operation{Project: r.URL.Query().Get("project"), Resource: resource, ID: r.URL.Query().Get("id"), Action: "preview", Input: input})
	if err != nil {
		fail(w, err)
		return
	}
	m := object(v)
	htmlBody, ok := m["html"].(string)
	if !ok {
		http.Error(w, "HTML preview unavailable", 404)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	_, _ = w.Write([]byte(htmlBody))
}
func (s *Server) uiAction(w http.ResponseWriter, r *http.Request) {
	a, v, err := s.uiAuthority(r)
	if err != nil {
		http.Error(w, "Session required", 401)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	if err = r.ParseForm(); err != nil || !s.sameOrigin(r) || r.FormValue("csrf") != v.CSRF {
		http.Error(w, "CSRF validation failed", 403)
		return
	}
	resource, action := r.FormValue("resource"), r.FormValue("action")
	allowed := map[string][]string{"projects": {"create"}, "operations": {"pause", "resume"}, "enrollments": {"pause", "resume", "cancel"}, "installation": {"pause", "resume"}, "webhook-deliveries": {"replay"}, "contacts": {"delete"}}
	if !slices.Contains(allowed[resource], action) {
		http.Error(w, "Runtime operation not allowed", 403)
		return
	}
	input := json.RawMessage(r.FormValue("input"))
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	if !json.Valid(input) {
		http.Error(w, "Input must be JSON", 400)
		return
	}
	op := engine.Operation{Project: r.FormValue("project"), Resource: resource, Action: action, ID: r.FormValue("id"), Input: input, Key: randomToken()}
	_, err = s.cfg.App.Do(r.Context(), a, op)
	if err != nil {
		fail(w, err)
		return
	}
	dest := "/?project=" + url.QueryEscape(op.Project) + "&view=" + url.QueryEscape(resource) + "&success=" + url.QueryEscape("Operation recorded.")
	if resource == "projects" {
		dest = "/"
	}
	http.Redirect(w, r, dest, 303)
}
