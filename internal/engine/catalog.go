package engine

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

type listResult struct {
	Items      any    `json:"items"`
	NextCursor string `json:"next_cursor"`
}

func (e *Engine) doProjects(ctx context.Context, a Authority, op Operation) (any, error) {
	if !a.Admin && (op.Action != "get" && op.Action != "list" || !hasAnyScope(a.Scopes, "read", "config", "operate", "send")) {
		return nil, forbidden()
	}
	if !a.Admin {
		if op.Project == "" {
			op.Project = a.Project
		}
		if op.Project != a.Project || (op.ID != "" && op.ID != a.Project) {
			return nil, forbidden()
		}
	}
	switch op.Action {
	case "list":
		q := `SELECT id,name,COALESCE(active_release_id,''),paused,pause_reason,created_at FROM projects WHERE id>?`
		args := []any{op.Cursor}
		if !a.Admin {
			q += ` AND id=?`
			args = append(args, a.Project)
		}
		q += ` ORDER BY id LIMIT ?`
		args = append(args, op.Limit+1)
		rows, er := e.db.QueryContext(ctx, q, args...)
		if er != nil {
			return nil, internal(er)
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var id, n, ar, pr, ca string
			var paused int
			if er = rows.Scan(&id, &n, &ar, &paused, &pr, &ca); er != nil {
				return nil, internal(er)
			}
			items = append(items, map[string]any{"id": id, "name": n, "active_release_id": ar, "paused": paused == 1, "pause_reason": pr, "created_at": ca})
		}
		next := ""
		if len(items) > op.Limit {
			next = items[op.Limit-1]["id"].(string)
			items = items[:op.Limit]
		}
		return listResult{items, next}, nil
	case "get":
		var id, n, ar, pr, ca string
		var paused int
		er := e.db.QueryRowContext(ctx, `SELECT id,name,COALESCE(active_release_id,''),paused,pause_reason,created_at FROM projects WHERE id=?`, op.ID).Scan(&id, &n, &ar, &paused, &pr, &ca)
		if er == sql.ErrNoRows {
			return nil, notFound("project not found")
		}
		if er != nil {
			return nil, internal(er)
		}
		return map[string]any{"id": id, "name": n, "active_release_id": ar, "paused": paused == 1, "pause_reason": pr, "created_at": ca}, nil
	case "create":
		var in struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		if er := decode(op.Input, &in); er != nil {
			return nil, er
		}
		in.ID = strings.TrimSpace(in.ID)
		if in.ID == "" || in.Name == "" || !validProjectID(in.ID) {
			return nil, bad("invalid_input", "id and name are required")
		}
		now := e.now().UTC().Format(time.RFC3339Nano)
		_, er := e.db.ExecContext(ctx, `INSERT INTO projects(id,name,created_at,updated_at) VALUES(?,?,?,?)`, in.ID, in.Name, now, now)
		if isConstraint(er) {
			return nil, conflict("already_exists", "project already exists")
		}
		if er != nil {
			return nil, internal(er)
		}
		return map[string]any{"id": in.ID, "name": in.Name}, nil
	default:
		return nil, notFound("unknown projects action")
	}
}
func hasAnyScope(scopes []string, wants ...string) bool {
	for _, w := range wants {
		if hasScope(scopes, w) {
			return true
		}
	}
	return false
}

func validProjectID(v string) bool {
	if len(v) > 63 {
		return false
	}
	for i, c := range v {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || (c == '-' && i > 0 && i < len(v)-1) {
			continue
		}
		return false
	}
	return v != ""
}

func (e *Engine) doTokens(ctx context.Context, a Authority, op Operation) (any, error) {
	if !a.Admin && op.Action != "list" {
		return nil, forbidden()
	}
	switch op.Action {
	case "create":
		var in struct {
			Name   string   `json:"name"`
			Scopes []string `json:"scopes"`
		}
		if er := decode(op.Input, &in); er != nil {
			return nil, er
		}
		if in.Name == "" || len(in.Scopes) == 0 {
			return nil, bad("invalid_input", "name and scopes are required")
		}
		for _, s := range in.Scopes {
			if s != "read" && s != "config" && s != "operate" && s != "send" {
				return nil, bad("invalid_scope", "unknown scope")
			}
		}
		secret := randomToken()
		id := newID("tok")
		raw, _ := json.Marshal(in.Scopes)
		now := e.now().UTC().Format(time.RFC3339Nano)
		_, er := e.db.ExecContext(ctx, `INSERT INTO api_tokens(id,project_id,name,token_hash,scopes,created_at) VALUES(?,?,?,?,?,?)`, id, op.Project, in.Name, tokenHash(secret), raw, now)
		if er != nil {
			return nil, internal(er)
		}
		return map[string]any{"id": id, "token": secret, "name": in.Name, "scopes": in.Scopes}, nil
	case "list":
		rows, er := e.db.QueryContext(ctx, `SELECT id,name,scopes,created_at,revoked_at FROM api_tokens WHERE project_id=? AND id>? ORDER BY id LIMIT ?`, op.Project, op.Cursor, op.Limit+1)
		if er != nil {
			return nil, internal(er)
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var id, n, s, ca string
			var rev sql.NullString
			if er = rows.Scan(&id, &n, &s, &ca, &rev); er != nil {
				return nil, internal(er)
			}
			items = append(items, map[string]any{"id": id, "name": n, "scopes": scanJSON[[]string](s), "created_at": ca, "revoked": rev.Valid})
		}
		return paged(items, op.Limit), nil
	case "delete":
		r, er := e.db.ExecContext(ctx, `UPDATE api_tokens SET revoked_at=? WHERE id=? AND project_id=? AND revoked_at IS NULL`, e.now().UTC().Format(time.RFC3339Nano), op.ID, op.Project)
		if er != nil {
			return nil, internal(er)
		}
		return affected(r, "token not found")
	default:
		return nil, notFound("unknown tokens action")
	}
}

func (e *Engine) doContacts(ctx context.Context, a Authority, op Operation) (any, error) {
	if op.Action == "preview-import" || op.Action == "import" || op.Action == "export" {
		return e.doContactsCSV(ctx, op)
	}
	switch op.Action {
	case "create":
		var in struct {
			ExternalID string         `json:"external_id"`
			Email      string         `json:"email"`
			Name       string         `json:"name"`
			Locale     string         `json:"locale"`
			Timezone   string         `json:"timezone"`
			Attributes map[string]any `json:"attributes"`
		}
		if er := decode(op.Input, &in); er != nil {
			return nil, er
		}
		email, er := normalizeEmail(in.Email)
		if er != nil {
			return nil, er
		}
		attrs, _ := json.Marshal(in.Attributes)
		now := e.now().UTC().Format(time.RFC3339Nano)
		id := newID("con")
		_, er = e.db.ExecContext(ctx, `INSERT INTO contacts(id,project_id,external_id,email,name,locale,timezone,attributes,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(project_id,email) DO UPDATE SET external_id=COALESCE(excluded.external_id,contacts.external_id),name=excluded.name,locale=excluded.locale,timezone=excluded.timezone,attributes=excluded.attributes,updated_at=excluded.updated_at`, id, op.Project, nullable(in.ExternalID), email, in.Name, in.Locale, in.Timezone, string(attrs), now, now)
		if er != nil {
			return nil, internal(er)
		}
		_ = e.db.QueryRowContext(ctx, `SELECT id FROM contacts WHERE project_id=? AND email=?`, op.Project, email).Scan(&id)
		return e.contact(ctx, op.Project, id)
	case "get":
		return e.contact(ctx, op.Project, op.ID)
	case "list":
		if er := validateFilters(op.Filters, "contact_id"); er != nil {
			return nil, er
		}
		q := `SELECT id,email,name,locale,timezone,attributes,external_id,created_at FROM contacts WHERE project_id=? AND deleted_at IS NULL AND id>?`
		args := []any{op.Project, op.Cursor}
		if v := op.Filters["contact_id"]; v != "" {
			q += ` AND id=?`
			args = append(args, v)
		}
		q += ` ORDER BY id LIMIT ?`
		args = append(args, op.Limit+1)
		rows, er := e.db.QueryContext(ctx, q, args...)
		if er != nil {
			return nil, internal(er)
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			v, er := scanContact(rows)
			if er != nil {
				return nil, internal(er)
			}
			items = append(items, v)
		}
		return paged(items, op.Limit), nil
	case "delete":
		now := e.now().UTC().Format(time.RFC3339Nano)
		tx, er := e.db.BeginTx(ctx, nil)
		if er != nil {
			return nil, internal(er)
		}
		defer tx.Rollback()
		var email string
		if er = tx.QueryRowContext(ctx, `SELECT email FROM contacts WHERE id=? AND project_id=? AND deleted_at IS NULL`, op.ID, op.Project).Scan(&email); er == sql.ErrNoRows {
			return nil, notFound("contact not found")
		}
		if er != nil {
			return nil, internal(er)
		}
		if _, er = tx.ExecContext(ctx, `INSERT INTO suppressions(project_id,email,reason,created_at) VALUES(?,?, 'contact_deleted',?) ON CONFLICT(project_id,email) DO UPDATE SET reason='contact_deleted'`, op.Project, email, now); er != nil {
			return nil, internal(er)
		}
		if _, er = tx.ExecContext(ctx, `DELETE FROM consent_tokens WHERE project_id=? AND contact_id=?`, op.Project, op.ID); er != nil {
			return nil, internal(er)
		}
		if _, er = tx.ExecContext(ctx, `UPDATE enrollments SET state='cancelled',next_at=NULL,updated_at=? WHERE project_id=? AND contact_id=? AND state IN ('active','waiting','paused')`, now, op.Project, op.ID); er != nil {
			return nil, internal(er)
		}
		if _, er = tx.ExecContext(ctx, `UPDATE jobs SET state=CASE WHEN state='pending' THEN 'cancelled' ELSE state END,payload=json_remove(payload,'$.email','$.name','$.token'),updated_at=? WHERE project_id=? AND json_extract(payload,'$.contact_id')=?`, now, op.Project, op.ID); er != nil {
			return nil, internal(er)
		}
		if _, er = tx.ExecContext(ctx, `UPDATE idempotency SET response='{"redacted":true}' WHERE project_id=? AND resource='contacts' AND json_valid(response) AND json_extract(response,'$.id')=?`, op.Project, op.ID); er != nil {
			return nil, internal(er)
		}
		r, er := tx.ExecContext(ctx, `UPDATE contacts SET external_id=NULL,email='deleted-'||id||'@invalid.invalid',name='',locale='',timezone='',attributes='{}',deleted_at=?,updated_at=? WHERE id=? AND project_id=? AND deleted_at IS NULL`, now, now, op.ID, op.Project)
		if er != nil {
			return nil, internal(er)
		}
		if er = insertOutbox(ctx, tx, op.Project, "contact.deleted", map[string]any{"contact_id": op.ID}, op.ID, now); er != nil {
			return nil, internal(er)
		}
		if er = tx.Commit(); er != nil {
			return nil, internal(er)
		}
		return affected(r, "contact not found")
	default:
		return nil, notFound("unknown contacts action")
	}
}
func validateFilters(filters map[string]string, allowed ...string) error {
	ok := map[string]bool{}
	for _, v := range allowed {
		ok[v] = true
	}
	for k := range filters {
		if !ok[k] {
			return bad("invalid_filter", "unsupported filter: "+k)
		}
	}
	return nil
}

type rowScanner interface{ Scan(...any) error }

func scanContact(r rowScanner) (map[string]any, error) {
	var id, email, name, locale, tz, attrs, created string
	var ext sql.NullString
	if er := r.Scan(&id, &email, &name, &locale, &tz, &attrs, &ext, &created); er != nil {
		return nil, er
	}
	return map[string]any{"id": id, "email": email, "name": name, "locale": locale, "timezone": tz, "attributes": scanJSON[map[string]any](attrs), "external_id": ext.String, "created_at": created}, nil
}
func (e *Engine) contact(ctx context.Context, p, id string) (any, error) {
	r := e.db.QueryRowContext(ctx, `SELECT id,email,name,locale,timezone,attributes,external_id,created_at FROM contacts WHERE project_id=? AND id=? AND deleted_at IS NULL`, p, id)
	v, er := scanContact(r)
	if er == sql.ErrNoRows {
		return nil, notFound("contact not found")
	}
	if er != nil {
		return nil, internal(er)
	}
	return v, nil
}

func normalizeEmail(v string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	at := strings.LastIndexByte(v, '@')
	if at < 1 || at == len(v)-1 || strings.ContainsAny(v, "\r\n") || len(v) > 320 {
		return "", bad("invalid_email", "invalid email address")
	}
	return v, nil
}
func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
func paged(items []map[string]any, limit int) listResult {
	next := ""
	if len(items) > limit {
		next = items[limit-1]["id"].(string)
		items = items[:limit]
	}
	return listResult{items, next}
}
func affected(r sql.Result, msg string) (any, error) {
	n, e := r.RowsAffected()
	if e != nil {
		return nil, internal(e)
	}
	if n == 0 {
		return nil, notFound(msg)
	}
	return map[string]any{"ok": true}, nil
}
func isConstraint(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "constraint")
}
