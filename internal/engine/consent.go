package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/mattmezza/mrkt/internal/mail"
)

func (e *Engine) doLists(ctx context.Context, a Authority, op Operation) (any, error) {
	switch op.Action {
	case "list":
		rows, er := e.db.QueryContext(ctx, `SELECT id,name,purpose,policy_version,release_id FROM lists WHERE project_id=? AND id>? ORDER BY id LIMIT ?`, op.Project, op.Cursor, op.Limit+1)
		if er != nil {
			return nil, internal(er)
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var id, n, p, pv, r string
			if er = rows.Scan(&id, &n, &p, &pv, &r); er != nil {
				return nil, internal(er)
			}
			items = append(items, map[string]any{"id": id, "name": n, "purpose": p, "policy_version": pv, "release_id": r})
		}
		return paged(items, op.Limit), nil
	case "get":
		var id, n, p, pv, r string
		er := e.db.QueryRowContext(ctx, `SELECT id,name,purpose,policy_version,release_id FROM lists WHERE project_id=? AND id=?`, op.Project, op.ID).Scan(&id, &n, &p, &pv, &r)
		if er == sql.ErrNoRows {
			return nil, notFound("list not found")
		}
		if er != nil {
			return nil, internal(er)
		}
		return map[string]any{"id": id, "name": n, "purpose": p, "policy_version": pv, "release_id": r}, nil
	default:
		return nil, notFound("unknown lists action")
	}
}

func (e *Engine) doConsent(ctx context.Context, a Authority, op Operation) (any, error) {
	switch op.Action {
	case "subscribe":
		var in struct {
			Email    string `json:"email"`
			Name     string `json:"name"`
			Locale   string `json:"locale"`
			List     string `json:"list"`
			Source   string `json:"source"`
			SourceIP string `json:"source_ip"`
		}
		if er := decode(op.Input, &in); er != nil {
			return nil, er
		}
		email, er := normalizeEmail(in.Email)
		if er != nil {
			return genericSubscribe(), nil
		}
		if in.List == "" {
			return genericSubscribe(), nil
		}
		if !e.allowSubscribe(ctx, op.Project, in.SourceIP, email) {
			return genericSubscribe(), nil
		}
		now := e.now().UTC()
		var policy string
		er = e.db.QueryRowContext(ctx, `SELECT policy_version FROM lists WHERE project_id=? AND id=? AND retired=0`, op.Project, in.List).Scan(&policy)
		if er != nil {
			return genericSubscribe(), nil
		}
		var suppressed int
		_ = e.db.QueryRowContext(ctx, `SELECT count(*) FROM suppressions WHERE project_id=? AND email=?`, op.Project, email).Scan(&suppressed)
		if suppressed > 0 {
			return genericSubscribe(), nil
		}
		token := randomToken()
		expiry := now.Add(24 * time.Hour).Format(time.RFC3339Nano)
		payload, er := withTx(ctx, e.db, func(tx *sql.Tx) (map[string]string, error) {
			id := newID("con")
			attrs := "{}"
			_, er := tx.ExecContext(ctx, `INSERT INTO contacts(id,project_id,email,name,locale,attributes,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(project_id,email) DO UPDATE SET name=excluded.name,locale=excluded.locale,updated_at=excluded.updated_at`, id, op.Project, email, in.Name, in.Locale, attrs, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
			if er != nil {
				return nil, er
			}
			if er = tx.QueryRowContext(ctx, `SELECT id FROM contacts WHERE project_id=? AND email=?`, op.Project, email).Scan(&id); er != nil {
				return nil, er
			}
			var state string
			var updated string
			er = tx.QueryRowContext(ctx, `SELECT state,updated_at FROM consent WHERE project_id=? AND contact_id=? AND list_id=?`, op.Project, id, in.List).Scan(&state, &updated)
			if er == nil && state == "confirmed" {
				return map[string]string{}, nil
			}
			if er == nil {
				t, _ := time.Parse(time.RFC3339Nano, updated)
				if now.Sub(t) < 10*time.Minute {
					return map[string]string{}, nil
				}
			} else if er != sql.ErrNoRows {
				return nil, er
			}
			cid := newID("cns")
			_, er = tx.ExecContext(ctx, `INSERT INTO consent(id,project_id,contact_id,list_id,state,source,policy_version,requested_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(project_id,contact_id,list_id) DO UPDATE SET state='pending',source=excluded.source,policy_version=excluded.policy_version,requested_at=excluded.requested_at,confirmed_at=NULL,unsubscribed_at=NULL,updated_at=excluded.updated_at`, cid, op.Project, id, in.List, "pending", in.Source, policy, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
			if er != nil {
				return nil, er
			}
			_, er = tx.ExecContext(ctx, `INSERT INTO consent_history(id,project_id,contact_id,list_id,state,source,policy_version,occurred_at,detail) VALUES(?,?,?,?,?,?,?,?,?)`, newID("cnh"), op.Project, id, in.List, "pending", in.Source, policy, now.Format(time.RFC3339Nano), `{"double_opt_in_required":true}`)
			if er != nil {
				return nil, er
			}
			_, er = tx.ExecContext(ctx, `DELETE FROM consent_tokens WHERE project_id=? AND contact_id=? AND list_id=? AND kind='confirm' AND used_at IS NULL`, op.Project, id, in.List)
			if er != nil {
				return nil, er
			}
			_, er = tx.ExecContext(ctx, `INSERT INTO consent_tokens(token_hash,project_id,contact_id,list_id,kind,expires_at,created_at) VALUES(?,?,?,?,?,?,?)`, tokenHash(token), op.Project, id, in.List, "confirm", expiry, now.Format(time.RFC3339Nano))
			if er != nil {
				return nil, er
			}
			payload := map[string]string{"contact_id": id, "email": email, "name": in.Name, "locale": in.Locale, "token": token, "message_id": newID("confirm") + "@mrkt"}
			raw, _ := json.Marshal(payload)
			_, er = tx.ExecContext(ctx, `INSERT INTO jobs(id,project_id,kind,payload,run_at,state,created_at,updated_at) VALUES(?,?,?,?,?,'pending',?,?)`, newID("job"), op.Project, "confirmation", string(raw), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
			if er != nil {
				return nil, er
			}
			return payload, nil
		})
		if er != nil {
			return nil, internal(er)
		}
		_ = payload
		return genericSubscribe(), nil
	case "confirm":
		var in struct {
			Token string `json:"token"`
		}
		if er := decode(op.Input, &in); er != nil {
			return nil, er
		}
		now := e.now().UTC().Format(time.RFC3339Nano)
		res, er := withTx(ctx, e.db, func(tx *sql.Tx) (any, error) {
			var p, c, l string
			er := tx.QueryRowContext(ctx, `SELECT project_id,contact_id,list_id FROM consent_tokens WHERE token_hash=? AND project_id=? AND kind='confirm' AND used_at IS NULL AND expires_at>?`, tokenHash(in.Token), op.Project, now).Scan(&p, &c, &l)
			if er == sql.ErrNoRows {
				return nil, bad("invalid_token", "invalid or expired confirmation token")
			}
			if er != nil {
				return nil, er
			}
			r, er := tx.ExecContext(ctx, `UPDATE consent_tokens SET used_at=? WHERE token_hash=? AND used_at IS NULL`, now, tokenHash(in.Token))
			if er != nil {
				return nil, er
			}
			n, _ := r.RowsAffected()
			if n != 1 {
				return nil, bad("invalid_token", "confirmation token already used")
			}
			_, er = tx.ExecContext(ctx, `UPDATE consent SET state='confirmed',confirmed_at=?,unsubscribed_at=NULL,updated_at=? WHERE project_id=? AND contact_id=? AND list_id=?`, now, now, p, c, l)
			if er != nil {
				return nil, er
			}
			var source, policy string
			_ = tx.QueryRowContext(ctx, `SELECT source,policy_version FROM consent WHERE project_id=? AND contact_id=? AND list_id=?`, p, c, l).Scan(&source, &policy)
			if _, er = tx.ExecContext(ctx, `INSERT INTO consent_history(id,project_id,contact_id,list_id,state,source,policy_version,occurred_at) VALUES(?,?,?,?,?,?,?,?)`, newID("cnh"), p, c, l, "confirmed", source, policy, now); er != nil {
				return nil, er
			}
			if er = e.enrollSubscriptionTx(ctx, tx, p, c, l, now); er != nil {
				return nil, er
			}
			_ = insertOutbox(ctx, tx, p, "subscription.confirmed", map[string]any{"contact_id": c, "list_id": l}, c, now)
			return map[string]any{"confirmed": true}, nil
		})
		if er != nil {
			return nil, asEngineError(er)
		}
		return res, nil
	case "unsubscribe":
		var in struct {
			Token       string `json:"token"`
			ContactID   string `json:"contact_id"`
			List        string `json:"list"`
			ProjectWide bool   `json:"project_wide"`
		}
		if er := decode(op.Input, &in); er != nil {
			return nil, er
		}
		if a.Public {
			var p, c, l string
			er := e.db.QueryRowContext(ctx, `SELECT project_id,contact_id,list_id FROM consent_tokens WHERE token_hash=? AND project_id=? AND kind='unsubscribe'`, tokenHash(in.Token), op.Project).Scan(&p, &c, &l)
			if er != nil {
				return nil, bad("invalid_token", "invalid unsubscribe token")
			}
			in.ContactID = c
			in.List = l
		}
		if in.ContactID == "" {
			return nil, bad("invalid_input", "contact_id required")
		}
		now := e.now().UTC().Format(time.RFC3339Nano)
		q := `UPDATE consent SET state='unsubscribed',unsubscribed_at=?,updated_at=? WHERE project_id=? AND contact_id=?`
		args := []any{now, now, op.Project, in.ContactID}
		if !in.ProjectWide {
			if in.List == "" {
				return nil, bad("invalid_input", "list required")
			}
			q += ` AND list_id=?`
			args = append(args, in.List)
		}
		tx, er := e.db.BeginTx(ctx, nil)
		if er != nil {
			return nil, internal(er)
		}
		defer tx.Rollback()
		r, er := tx.ExecContext(ctx, q, args...)
		if er != nil {
			return nil, internal(er)
		}
		n, _ := r.RowsAffected()
		if n == 0 {
			return nil, notFound("consent not found")
		}
		if _, er = tx.ExecContext(ctx, `INSERT INTO consent_history(id,project_id,contact_id,list_id,state,source,policy_version,occurred_at,detail) SELECT 'cnh_'||lower(hex(randomblob(16))),project_id,contact_id,list_id,'unsubscribed','unsubscribe',policy_version,?,? FROM consent WHERE project_id=? AND contact_id=? AND (? OR list_id=?)`, now, map[bool]string{true: `{"project_wide":true}`, false: `{}`}[in.ProjectWide], op.Project, in.ContactID, boolInt(in.ProjectWide), in.List); er != nil {
			return nil, internal(er)
		}
		if in.ProjectWide {
			var email string
			if tx.QueryRowContext(ctx, `SELECT email FROM contacts WHERE id=? AND project_id=?`, in.ContactID, op.Project).Scan(&email) == nil {
				if _, er = tx.ExecContext(ctx, `INSERT INTO suppressions(project_id,email,reason,created_at) VALUES(?,?,?,?) ON CONFLICT(project_id,email) DO UPDATE SET reason=excluded.reason`, op.Project, email, "unsubscribe_project", now); er != nil {
					return nil, internal(er)
				}
			}
			if _, er = tx.ExecContext(ctx, `UPDATE enrollments SET state='cancelled',updated_at=? WHERE project_id=? AND contact_id=? AND state IN ('active','waiting')`, now, op.Project, in.ContactID); er != nil {
				return nil, internal(er)
			}
		} else {
			if er = e.cancelListEnrollmentsTx(ctx, tx, op.Project, in.ContactID, in.List, now); er != nil {
				return nil, internal(er)
			}
		}
		if er = insertOutbox(ctx, tx, op.Project, "subscription.unsubscribed", map[string]any{"contact_id": in.ContactID, "list_id": in.List, "project_wide": in.ProjectWide}, in.ContactID, now); er != nil {
			return nil, internal(er)
		}
		if er = tx.Commit(); er != nil {
			return nil, internal(er)
		}
		return map[string]any{"unsubscribed": true}, nil
	case "preferences":
		var in struct {
			Token string `json:"token"`
		}
		if er := decode(op.Input, &in); er != nil {
			return nil, er
		}
		var c string
		er := e.db.QueryRowContext(ctx, `SELECT contact_id FROM consent_tokens WHERE token_hash=? AND project_id=? AND (kind='unsubscribe' OR (kind='confirm' AND used_at IS NULL AND expires_at>?)) LIMIT 1`, tokenHash(in.Token), op.Project, e.now().UTC().Format(time.RFC3339Nano)).Scan(&c)
		if er != nil {
			return nil, bad("invalid_token", "invalid preferences token")
		}
		var locale string
		_ = e.db.QueryRowContext(ctx, `SELECT locale FROM contacts WHERE id=? AND project_id=?`, c, op.Project).Scan(&locale)
		rows, er := e.db.QueryContext(ctx, `SELECT l.id,l.name,COALESCE(c.state,'none') FROM lists l LEFT JOIN consent c ON c.project_id=l.project_id AND c.list_id=l.id AND c.contact_id=? WHERE l.project_id=? AND l.retired=0 ORDER BY l.id`, c, op.Project)
		if er != nil {
			return nil, internal(er)
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var id, n, s string
			if er = rows.Scan(&id, &n, &s); er != nil {
				return nil, internal(er)
			}
			items = append(items, map[string]any{"id": id, "name": n, "state": s})
		}
		return map[string]any{"locale": locale, "lists": items}, nil
	case "list":
		if er := validateFilters(op.Filters, "state", "contact_id", "list_id"); er != nil {
			return nil, er
		}
		q := `SELECT c.id,c.contact_id,c.list_id,c.state,c.source,c.policy_version,c.requested_at,c.confirmed_at,c.unsubscribed_at FROM consent c WHERE c.project_id=? AND c.id>?`
		args := []any{op.Project, op.Cursor}
		for _, f := range []string{"state", "contact_id", "list_id"} {
			if v := op.Filters[f]; v != "" {
				q += ` AND c.` + f + `=?`
				args = append(args, v)
			}
		}
		q += ` ORDER BY c.id LIMIT ?`
		args = append(args, op.Limit+1)
		rows, er := e.db.QueryContext(ctx, q, args...)
		if er != nil {
			return nil, internal(er)
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var id, c, l, s, src, pv, r string
			var cf, un sql.NullString
			if er = rows.Scan(&id, &c, &l, &s, &src, &pv, &r, &cf, &un); er != nil {
				return nil, internal(er)
			}
			items = append(items, map[string]any{"id": id, "contact_id": c, "list_id": l, "state": s, "source": src, "policy_version": pv, "requested_at": r, "confirmed_at": cf.String, "unsubscribed_at": un.String})
		}
		return paged(items, op.Limit), nil
	default:
		return nil, notFound("unknown consent action")
	}
}

func (e *Engine) cancelListEnrollments(ctx context.Context, p, c, list, now string) error {
	rows, er := e.db.QueryContext(ctx, `SELECT id,release_id,sequence_id FROM enrollments WHERE project_id=? AND contact_id=? AND state IN ('active','waiting')`, p, c)
	if er != nil {
		return er
	}
	type x struct{ id, r, s string }
	var all []x
	for rows.Next() {
		var v x
		if er = rows.Scan(&v.id, &v.r, &v.s); er != nil {
			rows.Close()
			return er
		}
		all = append(all, v)
	}
	rows.Close()
	for _, v := range all {
		m, er := manifestForRelease(ctx, e.db, p, v.r)
		if er != nil {
			continue
		}
		for _, s := range m.Sequences {
			if s.ID == v.s && s.List == list {
				_, er = e.db.ExecContext(ctx, `UPDATE enrollments SET state='cancelled',updated_at=? WHERE id=?`, now, v.id)
				if er != nil {
					return er
				}
				break
			}
		}
	}
	return nil
}
func (e *Engine) cancelListEnrollmentsTx(ctx context.Context, tx *sql.Tx, p, c, list, now string) error {
	rows, er := tx.QueryContext(ctx, `SELECT id,release_id,sequence_id FROM enrollments WHERE project_id=? AND contact_id=? AND state IN ('active','waiting')`, p, c)
	if er != nil {
		return er
	}
	type x struct{ id, r, s string }
	var all []x
	for rows.Next() {
		var v x
		if er = rows.Scan(&v.id, &v.r, &v.s); er != nil {
			rows.Close()
			return er
		}
		all = append(all, v)
	}
	rows.Close()
	for _, v := range all {
		m, er := manifestForRelease(ctx, tx, p, v.r)
		if er != nil {
			continue
		}
		for _, s := range m.Sequences {
			if s.ID == v.s && s.List == list {
				if _, er = tx.ExecContext(ctx, `UPDATE enrollments SET state='cancelled',updated_at=? WHERE id=?`, now, v.id); er != nil {
					return er
				}
				break
			}
		}
	}
	return nil
}
func genericSubscribe() map[string]any {
	return map[string]any{"accepted": true, "message": "If eligible, a confirmation email will be sent."}
}
func (e *Engine) allowSubscribe(ctx context.Context, p, ip, email string) bool {
	var pending int
	if e.db.QueryRowContext(ctx, `SELECT count(*) FROM consent WHERE project_id=? AND state='pending'`, p).Scan(&pending) != nil || pending >= 10000 {
		return false
	}
	now := e.now().UTC().Truncate(time.Hour).Format(time.RFC3339Nano)
	tx, er := e.db.BeginTx(ctx, nil)
	if er != nil {
		return false
	}
	defer tx.Rollback()
	for _, v := range []struct {
		s, b string
		max  int
	}{{"global", "all", 2000}, {"project", p, 500}, {"ip", ip, 20}, {"email", p + ":" + email, 3}} {
		if v.b == "" {
			continue
		}
		_, er = tx.ExecContext(ctx, `INSERT INTO rate_limits(scope,bucket,window_start,count) VALUES(?,?,?,1) ON CONFLICT(scope,bucket,window_start) DO UPDATE SET count=count+1`, v.s, v.b, now)
		if er != nil {
			return false
		}
		var n int
		if er = tx.QueryRowContext(ctx, `SELECT count FROM rate_limits WHERE scope=? AND bucket=? AND window_start=?`, v.s, v.b, now).Scan(&n); er != nil || n > v.max {
			return false
		}
	}
	return tx.Commit() == nil
}
func confirmationMessage(base, p string, v map[string]string) mail.Message {
	u := strings.TrimRight(base, "/") + "/public/" + url.PathEscape(p) + "/confirm?token=" + url.QueryEscape(v["token"])
	subject, label, prefix := "Confirm your subscription", "Confirm", "Confirm your subscription"
	switch strings.Split(v["locale"], "-")[0] {
	case "it":
		subject, label, prefix = "Conferma la tua iscrizione", "Conferma", "Conferma la tua iscrizione"
	case "tr":
		subject, label, prefix = "Aboneliğinizi onaylayın", "Onayla", "Aboneliğinizi onaylayın"
	}
	id := v["message_id"]
	if id == "" {
		id = newID("confirm") + "@mrkt"
	}
	return mail.Message{ID: id, From: "mrkt <no-reply@localhost>", To: v["email"], Subject: subject, Text: prefix + ": " + u, HTML: `<p>` + prefix + `: <a href="` + u + `">` + label + `</a></p>`}
}
func insertOutbox(ctx context.Context, tx *sql.Tx, p, typ string, payload any, correlation, now string) error {
	raw, _ := json.Marshal(payload)
	_, er := tx.ExecContext(ctx, `INSERT INTO outbox(id,project_id,type,payload,correlation_id,next_at,created_at) VALUES(?,?,?,?,?,?,?)`, newID("evt"), p, typ, string(raw), correlation, now, now)
	return er
}

var _ = fmt.Sprintf
