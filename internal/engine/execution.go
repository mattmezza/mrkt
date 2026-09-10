package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mattmezza/mrkt/internal/mail"
	"github.com/mattmezza/mrkt/internal/manifest"
)

func (e *Engine) activeManifest(ctx context.Context, p string) (manifest.Manifest, string, error) {
	var raw, rid string
	er := e.db.QueryRowContext(ctx, `SELECT r.manifest,r.id FROM projects p JOIN releases r ON r.id=p.active_release_id WHERE p.id=?`, p).Scan(&raw, &rid)
	if er == sql.ErrNoRows {
		return manifest.Manifest{}, "", notFound("active release not found")
	}
	if er != nil {
		return manifest.Manifest{}, "", internal(er)
	}
	var m manifest.Manifest
	if er = json.Unmarshal([]byte(raw), &m); er != nil {
		return m, "", internal(er)
	}
	return m, rid, nil
}
func manifestForRelease(ctx context.Context, q rowQuerier, p, rid string) (manifest.Manifest, error) {
	var raw string
	er := q.QueryRowContext(ctx, `SELECT manifest FROM releases WHERE id=? AND project_id=?`, rid, p).Scan(&raw)
	if er != nil {
		return manifest.Manifest{}, er
	}
	var m manifest.Manifest
	er = json.Unmarshal([]byte(raw), &m)
	return m, er
}

type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (e *Engine) enrollSubscriptionTx(ctx context.Context, tx *sql.Tx, p, c, l, now string) error {
	m, rid, er := func() (manifest.Manifest, string, error) {
		var raw, r string
		er := tx.QueryRowContext(ctx, `SELECT r.manifest,r.id FROM projects p JOIN releases r ON r.id=p.active_release_id WHERE p.id=?`, p).Scan(&raw, &r)
		if er != nil {
			return manifest.Manifest{}, "", er
		}
		var m manifest.Manifest
		er = json.Unmarshal([]byte(raw), &m)
		return m, r, er
	}()
	if er != nil {
		return er
	}
	for _, s := range m.Sequences {
		if s.Entry != "subscription" || s.List != l {
			continue
		}
		id := newID("enr")
		_, er = tx.ExecContext(ctx, `INSERT OR IGNORE INTO enrollments(id,project_id,contact_id,release_id,sequence_id,current_step,state,next_at,created_at,updated_at,entry_key) VALUES(?,?,?,?,?,?, 'active',?,?,?,'once')`, id, p, c, rid, s.ID, s.Steps[0].ID, now, now, now)
		if er != nil {
			return er
		}
	}
	return nil
}

func (e *Engine) doEvents(ctx context.Context, a Authority, op Operation) (any, error) {
	if op.Action == "list" {
		if er := validateFilters(op.Filters, "contact_id"); er != nil {
			return nil, er
		}
		q := `SELECT id,event_key,type,contact_id,payload,occurred_at,created_at FROM events WHERE project_id=? AND id>?`
		args := []any{op.Project, op.Cursor}
		if v := op.Filters["contact_id"]; v != "" {
			q += ` AND contact_id=?`
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
			var id, k, t, pay, o, c string
			var cid sql.NullString
			if er = rows.Scan(&id, &k, &t, &cid, &pay, &o, &c); er != nil {
				return nil, internal(er)
			}
			items = append(items, map[string]any{"id": id, "key": k, "type": t, "contact_id": cid.String, "payload": scanJSON[any](pay), "occurred_at": o, "created_at": c})
		}
		return paged(items, op.Limit), nil
	}
	if op.Action != "create" {
		return nil, notFound("unknown events action")
	}
	var in struct {
		Key        string         `json:"key"`
		Type       string         `json:"type"`
		ContactID  string         `json:"contact_id"`
		Payload    map[string]any `json:"payload"`
		OccurredAt string         `json:"occurred_at"`
	}
	if er := decode(op.Input, &in); er != nil {
		return nil, er
	}
	if in.Key == "" || in.Type == "" {
		return nil, bad("invalid_input", "key and type required")
	}
	now := e.now().UTC()
	occurred := now
	if in.OccurredAt != "" {
		var er error
		occurred, er = time.Parse(time.RFC3339Nano, in.OccurredAt)
		if er != nil {
			return nil, bad("invalid_input", "invalid occurred_at")
		}
	}
	raw, _ := json.Marshal(in.Payload)
	fingerprintInput, _ := json.Marshal(struct {
		Type       string         `json:"type"`
		ContactID  string         `json:"contact_id,omitempty"`
		Payload    map[string]any `json:"payload"`
		OccurredAt string         `json:"occurred_at,omitempty"`
	}{in.Type, in.ContactID, in.Payload, in.OccurredAt})
	fpSum := sha256.Sum256(fingerprintInput)
	fingerprint := hex.EncodeToString(fpSum[:])
	id := newID("event")
	res, er := withTx(ctx, e.db, func(tx *sql.Tx) (any, error) {
		if in.ContactID != "" {
			var owned int
			if er := tx.QueryRowContext(ctx, `SELECT count(*) FROM contacts WHERE id=? AND project_id=? AND deleted_at IS NULL`, in.ContactID, op.Project).Scan(&owned); er != nil {
				return nil, er
			}
			if owned != 1 {
				return nil, bad("invalid_contact", "contact does not belong to project")
			}
		}
		_, er := tx.ExecContext(ctx, `INSERT INTO events(id,project_id,event_key,type,contact_id,payload,occurred_at,created_at,request_fingerprint) VALUES(?,?,?,?,?,?,?,?,?)`, id, op.Project, in.Key, in.Type, nullable(in.ContactID), string(raw), occurred.UTC().Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), fingerprint)
		if isConstraint(er) {
			var old, oldFingerprint string
			if er = tx.QueryRowContext(ctx, `SELECT id,request_fingerprint FROM events WHERE project_id=? AND event_key=?`, op.Project, in.Key).Scan(&old, &oldFingerprint); er != nil {
				return nil, er
			}
			if oldFingerprint == "" {
				return nil, conflict("event_key_retired", "event key predates request fingerprints or its request was retained only as a tombstone")
			}
			if oldFingerprint != fingerprint {
				return nil, conflict("event_key_conflict", "event key was already used with different type, contact, payload, or explicit occurrence time")
			}
			return map[string]any{"id": old, "duplicate": true}, nil
		}
		if er != nil {
			return nil, er
		}
		m, rid, er := func() (manifest.Manifest, string, error) {
			var raw, r string
			er := tx.QueryRowContext(ctx, `SELECT r.manifest,r.id FROM projects p JOIN releases r ON r.id=p.active_release_id WHERE p.id=?`, op.Project).Scan(&raw, &r)
			if er != nil {
				return manifest.Manifest{}, "", er
			}
			var m manifest.Manifest
			er = json.Unmarshal([]byte(raw), &m)
			return m, r, er
		}()
		if er != nil {
			return nil, er
		}
		if in.ContactID != "" {
			for _, s := range m.Sequences {
				if s.Entry == "event" && s.Event == in.Type {
					eid := newID("enr")
					entryKey := id
					if s.Reentry == "once" {
						entryKey = "once"
					}
					_, er = tx.ExecContext(ctx, `INSERT OR IGNORE INTO enrollments(id,project_id,contact_id,release_id,sequence_id,event_id,current_step,state,next_at,created_at,updated_at,entry_key) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, eid, op.Project, in.ContactID, rid, s.ID, id, s.Steps[0].ID, "active", now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), entryKey)
					if er != nil {
						return nil, er
					}
				}
			}
		}
		for _, b := range m.Broadcasts {
			if b.Event == in.Type {
				bid := newID("brd")
				_, er = tx.ExecContext(ctx, `INSERT OR IGNORE INTO broadcasts(id,project_id,release_id,definition_id,event_id,state,audience_frozen_at,created_at) VALUES(?,?,?,?,?,'queued',?,?)`, bid, op.Project, rid, b.ID, id, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
				if er != nil {
					return nil, er
				}
				rows, er := tx.QueryContext(ctx, `SELECT c.contact_id FROM consent c JOIN contacts x ON x.id=c.contact_id LEFT JOIN suppressions s ON s.project_id=c.project_id AND s.email=x.email WHERE c.project_id=? AND c.list_id=? AND c.state='confirmed' AND x.deleted_at IS NULL AND s.email IS NULL`, op.Project, b.List)
				if er != nil {
					return nil, er
				}
				for rows.Next() {
					var cid string
					if er = rows.Scan(&cid); er != nil {
						rows.Close()
						return nil, er
					}
					pay, _ := json.Marshal(map[string]string{"broadcast_id": bid, "contact_id": cid, "message": b.Message, "release_id": rid})
					_, er = tx.ExecContext(ctx, `INSERT INTO jobs(id,project_id,kind,payload,run_at,state,created_at,updated_at) VALUES(?,?,?,?,?,'pending',?,?)`, newID("job"), op.Project, "broadcast", string(pay), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
					if er != nil {
						rows.Close()
						return nil, er
					}
				}
				rows.Close()
			}
		}
		return map[string]any{"id": id, "duplicate": false}, nil
	})
	if er != nil {
		return nil, asEngineError(er)
	}
	return res, nil
}

func (e *Engine) Tick(ctx context.Context) error {
	e.tickMu.Lock()
	defer e.tickMu.Unlock()
	if err := e.tickOutbox(ctx); err != nil {
		return err
	}
	var globallyPaused int
	if er := e.db.QueryRowContext(ctx, `SELECT CASE WHEN recovery=1 OR outbound_paused=1 THEN 1 ELSE 0 END FROM installation WHERE id=1`).Scan(&globallyPaused); er != nil {
		return er
	}
	if globallyPaused == 1 {
		return nil
	}
	now := e.now().UTC()
	if err := e.scheduleDueEnrollments(ctx, now); err != nil {
		return err
	}
	_, _ = e.db.ExecContext(ctx, `UPDATE jobs SET state='uncertain',last_error='worker lease expired after dispatch may have begun',updated_at=? WHERE state='dispatching' AND lease_until<?`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	for i := 0; i < 50; i++ {
		job, er := e.claimJob(ctx, now)
		if er == sql.ErrNoRows {
			return nil
		}
		if er != nil {
			return er
		}
		if er = e.runJob(ctx, job); er != nil {
			return er
		}
	}
	return nil
}

type claimedJob struct{ id, p, kind, payload string }

func (e *Engine) claimJob(ctx context.Context, now time.Time) (claimedJob, error) {
	return withTx(ctx, e.db, func(tx *sql.Tx) (claimedJob, error) {
		var j claimedJob
		er := tx.QueryRowContext(ctx, `SELECT id,project_id,kind,payload FROM jobs WHERE state='pending' AND run_at<=? ORDER BY run_at,id LIMIT 1`, now.Format(time.RFC3339Nano)).Scan(&j.id, &j.p, &j.kind, &j.payload)
		if er != nil {
			return j, er
		}
		r, er := tx.ExecContext(ctx, `UPDATE jobs SET state='dispatching',lease_owner=?,lease_until=?,attempts=attempts+1,updated_at=? WHERE id=? AND state='pending'`, newID("worker"), now.Add(2*time.Minute).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), j.id)
		if er != nil {
			return j, er
		}
		n, _ := r.RowsAffected()
		if n != 1 {
			return j, sql.ErrNoRows
		}
		return j, nil
	})
}
func (e *Engine) runJob(ctx context.Context, j claimedJob) error {
	switch j.kind {
	case "confirmation":
		var p map[string]string
		_ = json.Unmarshal([]byte(j.payload), &p)
		var eligible int
		_ = e.db.QueryRowContext(ctx, `SELECT count(*) FROM consent_tokens t JOIN contacts c ON c.id=t.contact_id AND c.project_id=t.project_id JOIN consent n ON n.project_id=t.project_id AND n.contact_id=t.contact_id AND n.list_id=t.list_id JOIN projects p ON p.id=t.project_id JOIN installation i ON i.id=1 LEFT JOIN suppressions s ON s.project_id=c.project_id AND s.email=c.email WHERE t.project_id=? AND t.token_hash=? AND t.kind='confirm' AND t.used_at IS NULL AND t.expires_at>? AND n.state='pending' AND c.deleted_at IS NULL AND s.email IS NULL AND p.paused=0 AND i.recovery=0 AND i.outbound_paused=0`, j.p, tokenHash(p["token"]), e.now().UTC().Format(time.RFC3339Nano)).Scan(&eligible)
		if eligible == 0 {
			return e.finishJob(ctx, j.id, "cancelled", "confirmation no longer eligible")
		}
		pinned := p["transport_id"]
		if pinned == "" {
			if m, _, mErr := e.activeManifest(ctx, j.p); mErr == nil {
				pinned = m.Project.Transport
			}
		}
		transportID, cfg, sender, senderErr := e.selectedTransport(ctx, j.p, "", pinned)
		if senderErr != nil || sender == nil {
			return e.finishJob(ctx, j.id, "failed", "sender unavailable")
		}
		if p["transport_id"] == "" {
			p["transport_id"] = transportID
			if raw, marshalErr := json.Marshal(p); marshalErr == nil {
				_, _ = e.db.ExecContext(ctx, `UPDATE jobs SET payload=?,updated_at=? WHERE id=?`, string(raw), e.now().UTC().Format(time.RFC3339Nano), j.id)
			}
		}
		if !e.reserveSelectedTransport(ctx, j.p, transportID, cfg.RatePerMinute) {
			return e.retryJob(ctx, j.id, "transport rate limit")
		}
		msg := confirmationMessage(e.cfg.PublicURL, j.p, p)
		msg.From = cfg.From
		res, er := sender.Send(ctx, msg)
		state := res.State
		if er != nil && state == "" {
			state = mail.StateUncertain
		}
		if state == mail.StateAccepted {
			return e.finishJob(ctx, j.id, "complete", res.Detail)
		}
		if state == mail.StateTransient {
			return e.retryJob(ctx, j.id, res.Detail)
		}
		return e.finishJob(ctx, j.id, map[bool]string{true: "uncertain", false: "failed"}[state == mail.StateUncertain], res.Detail)
	case "broadcast":
		var p map[string]string
		_ = json.Unmarshal([]byte(j.payload), &p)
		return e.dispatch(ctx, j, p["contact_id"], p["release_id"], "", p["broadcast_id"], "broadcast", p["message"])
	case "sequence":
		var p map[string]string
		_ = json.Unmarshal([]byte(j.payload), &p)
		er := e.dispatch(ctx, j, p["contact_id"], p["release_id"], p["enrollment_id"], "", p["step_id"], p["message"])
		if er != nil {
			return er
		}
		var state string
		_ = e.db.QueryRowContext(ctx, `SELECT state FROM deliveries WHERE enrollment_id=? AND step_id=?`, p["enrollment_id"], p["step_id"]).Scan(&state)
		if state == mail.StateAccepted {
			_, er = e.db.ExecContext(ctx, `UPDATE enrollments SET current_step=?,state=CASE WHEN ?='' THEN 'completed' ELSE 'active' END,next_at=?,updated_at=? WHERE id=? AND project_id=?`, p["next"], p["next"], e.now().UTC().Format(time.RFC3339Nano), e.now().UTC().Format(time.RFC3339Nano), p["enrollment_id"], j.p)
		}
		return er
	default:
		return e.advanceEnrollmentJob(ctx, j)
	}
}

func (e *Engine) scheduleDueEnrollments(ctx context.Context, now time.Time) error {
	rows, er := e.db.QueryContext(ctx, `SELECT id,project_id,contact_id,release_id,sequence_id,current_step,COALESCE(event_id,'') FROM enrollments WHERE state='active' AND next_at<=? ORDER BY next_at,id LIMIT 50`, now.Format(time.RFC3339Nano))
	if er != nil {
		return er
	}
	type due struct{ id, p, c, r, s, step, event string }
	var all []due
	for rows.Next() {
		var d due
		if er = rows.Scan(&d.id, &d.p, &d.c, &d.r, &d.s, &d.step, &d.event); er != nil {
			rows.Close()
			return er
		}
		all = append(all, d)
	}
	rows.Close()
	for _, d := range all {
		m, er := manifestForRelease(ctx, e.db, d.p, d.r)
		if er != nil {
			return er
		}
		var seq *manifest.Sequence
		for i := range m.Sequences {
			if m.Sequences[i].ID == d.s {
				seq = &m.Sequences[i]
				break
			}
		}
		if seq == nil {
			_, _ = e.db.ExecContext(ctx, `UPDATE enrollments SET state='failed',updated_at=? WHERE id=?`, now.Format(time.RFC3339Nano), d.id)
			continue
		}
		if seq.Exit != nil && evaluateCondition(ctx, e.db, d.p, d.c, d.event, seq.Exit) {
			_, er = e.db.ExecContext(ctx, `UPDATE enrollments SET state='completed',next_at=NULL,updated_at=? WHERE id=?`, now.Format(time.RFC3339Nano), d.id)
			if er != nil {
				return er
			}
			continue
		}
		var st *manifest.Step
		for i := range seq.Steps {
			if seq.Steps[i].ID == d.step {
				st = &seq.Steps[i]
				break
			}
		}
		if st == nil {
			_, _ = e.db.ExecContext(ctx, `UPDATE enrollments SET state='failed',updated_at=? WHERE id=?`, now.Format(time.RFC3339Nano), d.id)
			continue
		}
		switch st.Type {
		case "complete":
			_, er = e.db.ExecContext(ctx, `UPDATE enrollments SET state='completed',next_at=NULL,updated_at=? WHERE id=? AND state='active'`, now.Format(time.RFC3339Nano), d.id)
		case "delay":
			delay, _ := time.ParseDuration(st.Delay)
			_, er = e.db.ExecContext(ctx, `UPDATE enrollments SET current_step=?,state=CASE WHEN ?='' THEN 'completed' ELSE 'active' END,next_at=?,updated_at=? WHERE id=? AND state='active'`, st.Next, st.Next, now.Add(delay).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), d.id)
		case "condition":
			next := st.Else
			if evaluateCondition(ctx, e.db, d.p, d.c, d.event, st.Condition) {
				next = st.Next
			}
			_, er = e.db.ExecContext(ctx, `UPDATE enrollments SET current_step=?,state=CASE WHEN ?='' THEN 'completed' ELSE 'active' END,next_at=?,updated_at=? WHERE id=? AND state='active'`, next, next, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), d.id)
		case "send":
			payload, _ := json.Marshal(map[string]string{"contact_id": d.c, "release_id": d.r, "enrollment_id": d.id, "step_id": st.ID, "message": st.Message, "next": st.Next})
			tx, e2 := e.db.BeginTx(ctx, nil)
			if e2 != nil {
				return e2
			}
			r, e2 := tx.ExecContext(ctx, `UPDATE enrollments SET state='waiting',updated_at=? WHERE id=? AND state='active'`, now.Format(time.RFC3339Nano), d.id)
			if e2 == nil {
				n, _ := r.RowsAffected()
				if n == 1 {
					_, e2 = tx.ExecContext(ctx, `INSERT INTO jobs(id,project_id,kind,payload,run_at,state,created_at,updated_at) VALUES(?,?,?,?,?,'pending',?,?)`, newID("job"), d.p, "sequence", string(payload), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
				}
			}
			if e2 != nil {
				tx.Rollback()
				return e2
			}
			er = tx.Commit()
		}
		if er != nil {
			return er
		}
	}
	return nil
}
func evaluateCondition(ctx context.Context, q rowQuerier, p, c, eventID string, cond *manifest.Condition) bool {
	if cond == nil {
		return false
	}
	var email, locale, attrs string
	if q.QueryRowContext(ctx, `SELECT email,locale,attributes FROM contacts WHERE project_id=? AND id=?`, p, c).Scan(&email, &locale, &attrs) != nil {
		return false
	}
	event := map[string]any{}
	if eventID != "" {
		var payload string
		if q.QueryRowContext(ctx, `SELECT payload FROM events WHERE id=? AND project_id=?`, eventID, p).Scan(&payload) == nil {
			event = scanJSON[map[string]any](payload)
		}
	}
	return manifest.Evaluate(cond, map[string]any{"Email": email, "Locale": locale, "Attributes": scanJSON[map[string]any](attrs), "Event": event})
}

func (e *Engine) advanceEnrollmentJob(ctx context.Context, j claimedJob) error {
	return e.finishJob(ctx, j.id, "failed", "unknown job kind")
}
func (e *Engine) finishJob(ctx context.Context, id, state, detail string) error {
	_, er := e.db.ExecContext(ctx, `UPDATE jobs SET state=?,last_error=?,lease_owner=NULL,lease_until=NULL,updated_at=? WHERE id=?`, state, detail, e.now().UTC().Format(time.RFC3339Nano), id)
	return er
}
func (e *Engine) retryJob(ctx context.Context, id, detail string) error {
	_, er := e.db.ExecContext(ctx, `UPDATE jobs SET state='pending',run_at=?,last_error=?,lease_owner=NULL,lease_until=NULL,updated_at=? WHERE id=?`, e.now().UTC().Add(time.Minute).Format(time.RFC3339Nano), detail, e.now().UTC().Format(time.RFC3339Nano), id)
	return er
}

func (e *Engine) dispatch(ctx context.Context, j claimedJob, contactID, releaseID, enrollmentID, broadcastID, stepID, messageKey string) error {
	var paused, recovery, projectPaused int
	var email, name, locale, attrs string
	er := e.db.QueryRowContext(ctx, `SELECT i.outbound_paused,i.recovery,p.paused,c.email,c.name,c.locale,c.attributes FROM installation i,contacts c JOIN projects p ON p.id=c.project_id WHERE i.id=1 AND c.id=? AND c.project_id=? AND c.deleted_at IS NULL`, contactID, j.p).Scan(&paused, &recovery, &projectPaused, &email, &name, &locale, &attrs)
	if er != nil {
		return e.finishJob(ctx, j.id, "cancelled", "contact unavailable")
	}
	if paused == 1 || recovery == 1 || projectPaused == 1 {
		return e.retryJob(ctx, j.id, "outbound paused")
	}
	m, er := manifestForRelease(ctx, e.db, j.p, releaseID)
	if er != nil {
		return e.finishJob(ctx, j.id, "failed", er.Error())
	}
	requiredList := e.deliveryList(ctx, m, enrollmentID, broadcastID)
	eventVars, eventID := e.deliveryEvent(ctx, j.p, enrollmentID, broadcastID)
	if enrollmentID != "" {
		var sid string
		_ = e.db.QueryRowContext(ctx, `SELECT sequence_id FROM enrollments WHERE id=?`, enrollmentID).Scan(&sid)
		for _, seq := range m.Sequences {
			if seq.ID == sid && seq.Exit != nil && evaluateCondition(ctx, e.db, j.p, contactID, eventID, seq.Exit) {
				_, _ = e.db.ExecContext(ctx, `UPDATE enrollments SET state='completed',next_at=NULL,updated_at=? WHERE id=?`, e.now().UTC().Format(time.RFC3339Nano), enrollmentID)
				return e.finishJob(ctx, j.id, "cancelled", "sequence exit condition matched")
			}
		}
	}
	var eligible int
	_ = e.db.QueryRowContext(ctx, `SELECT count(*) FROM consent c LEFT JOIN suppressions s ON s.project_id=c.project_id AND s.email=? WHERE c.project_id=? AND c.contact_id=? AND c.list_id=? AND c.state='confirmed' AND s.email IS NULL`, email, j.p, contactID, requiredList).Scan(&eligible)
	if eligible == 0 {
		return e.finishJob(ctx, j.id, "cancelled", "not eligible")
	}
	loc := manifest.ResolveLocale(locale, m.Project.DefaultLocale, m.Project.Locales)
	content, ok := m.Messages[messageKey][loc]
	if !ok {
		candidate := loc
		for candidate != "" {
			if c, found := m.Messages[messageKey][candidate]; found {
				content, ok, loc = c, true, candidate
				break
			}
			i := strings.LastIndexByte(candidate, '-')
			if i < 0 {
				break
			}
			candidate = candidate[:i]
		}
		if !ok {
			content, ok = m.Messages[messageKey][m.Project.DefaultLocale]
			loc = m.Project.DefaultLocale
		}
	}
	if !ok {
		return e.finishJob(ctx, j.id, "failed", "message locale unavailable")
	}
	sources := map[string][]byte{}
	files := map[string]manifest.File{}
	for _, f := range m.Files {
		files[f.Path] = f
	}
	needed := append([]string{content.Subject, content.HTML, content.Text}, content.Attachments...)
	for _, path := range needed {
		f := files[path]
		r, er := e.cfg.Artifacts.Get(ctx, j.p, f.SHA256)
		if er != nil {
			return e.finishJob(ctx, j.id, "failed", er.Error())
		}
		b, er := io.ReadAll(io.LimitReader(r, f.Size+1))
		r.Close()
		if er != nil || int64(len(b)) != f.Size {
			return e.finishJob(ctx, j.id, "failed", "artifact read mismatch")
		}
		sources[path] = b
	}
	unsubToken := randomToken()
	now := e.now().UTC().Format(time.RFC3339Nano)
	_, _ = e.db.ExecContext(ctx, `INSERT INTO consent_tokens(token_hash,project_id,contact_id,list_id,kind,expires_at,created_at) SELECT ?,project_id,contact_id,list_id,'unsubscribe',?,? FROM consent WHERE project_id=? AND contact_id=? AND list_id=? AND state='confirmed' LIMIT 1`, tokenHash(unsubToken), "9999-12-31T00:00:00Z", now, j.p, contactID, requiredList)
	unsub := strings.TrimRight(e.cfg.PublicURL, "/") + "/public/" + j.p + "/unsubscribe?token=" + unsubToken
	if name == "" {
		name = strings.Split(email, "@")[0]
	}
	rendered, er := manifest.Render(content, sources, map[string]any{"Name": name, "Email": email, "Locale": loc, "UnsubscribeURL": unsub, "ConfirmationURL": "", "Attributes": scanJSON[map[string]any](attrs), "Event": eventVars})
	if er != nil {
		return e.finishJob(ctx, j.id, "failed", er.Error())
	}
	frozen, _ := json.Marshal(rendered)
	sum := sha256.Sum256(frozen)
	hash := hex.EncodeToString(sum[:])
	if er = e.cfg.Artifacts.Put(ctx, j.p, hash, "application/json", int64(len(frozen)), bytes.NewReader(frozen)); er != nil {
		return e.finishJob(ctx, j.id, "failed", er.Error())
	}
	// This is the final dispatch boundary. The single SQLite writer serializes this
	// eligibility check with unsubscribe and feedback updates before intent creation.
	if enrollmentID != "" {
		var sid string
		_ = e.db.QueryRowContext(ctx, `SELECT sequence_id FROM enrollments WHERE id=?`, enrollmentID).Scan(&sid)
		for _, seq := range m.Sequences {
			if seq.ID == sid && seq.Exit != nil && evaluateCondition(ctx, e.db, j.p, contactID, eventID, seq.Exit) {
				_, _ = e.db.ExecContext(ctx, `UPDATE enrollments SET state='completed',next_at=NULL,updated_at=? WHERE id=?`, e.now().UTC().Format(time.RFC3339Nano), enrollmentID)
				return e.finishJob(ctx, j.id, "cancelled", "sequence exit condition matched at dispatch boundary")
			}
		}
	}
	stream := e.deliveryStream(ctx, m, enrollmentID, broadcastID)
	pinned := m.Project.Transport
	if old := e.existingDeliveryTransport(ctx, enrollmentID, broadcastID, contactID, stepID); old != "" {
		pinned = old
	}
	transportID, transportCfg, sender, senderErr := e.selectedTransport(ctx, j.p, stream, pinned)
	if senderErr != nil || sender == nil {
		return e.finishJob(ctx, j.id, "failed", "selected transport unavailable")
	}
	if !e.reserveSelectedTransport(ctx, j.p, transportID, transportCfg.RatePerMinute) {
		return e.retryJob(ctx, j.id, "transport rate limit")
	}
	var exit *manifest.Condition
	if enrollmentID != "" {
		var sid string
		_ = e.db.QueryRowContext(ctx, `SELECT sequence_id FROM enrollments WHERE id=?`, enrollmentID).Scan(&sid)
		for _, seq := range m.Sequences {
			if seq.ID == sid {
				exit = seq.Exit
				break
			}
		}
	}
	did, messageID, existingHash, reservation, er := e.reserveDelivery(ctx, j.p, email, contactID, requiredList, releaseID, enrollmentID, broadcastID, stepID, messageKey, hash, transportID, eventID, exit, now)
	if er != nil {
		return er
	}
	if reservation == "ineligible" {
		return e.finishJob(ctx, j.id, "cancelled", "not eligible at dispatch boundary")
	}
	if reservation == "exit" {
		if enrollmentID != "" {
			_, _ = e.db.ExecContext(ctx, `UPDATE enrollments SET state='completed',next_at=NULL,updated_at=? WHERE id=?`, e.now().UTC().Format(time.RFC3339Nano), enrollmentID)
		}
		return e.finishJob(ctx, j.id, "cancelled", "sequence exit condition matched at intent boundary")
	}
	if reservation == "existing" {
		return e.finishJob(ctx, j.id, "complete", "already dispatched")
	}
	if existingHash != "" {
		rc, getErr := e.cfg.Artifacts.Get(ctx, j.p, existingHash)
		if getErr != nil {
			return e.finishJob(ctx, j.id, "failed", getErr.Error())
		}
		frozenBytes, _ := io.ReadAll(io.LimitReader(rc, manifest.MaxRenderSize+1))
		rc.Close()
		if json.Unmarshal(frozenBytes, &rendered) != nil {
			return e.finishJob(ctx, j.id, "failed", "invalid frozen message")
		}
		hash = existingHash
	}
	msg := mail.Message{ID: messageID, From: "mrkt <no-reply@localhost>", To: email, Subject: strings.TrimSpace(rendered.Subject), HTML: rendered.HTML, Text: rendered.Text, UnsubscribeURL: unsub}
	for _, path := range content.Attachments {
		f := files[path]
		msg.Attachments = append(msg.Attachments, mail.Attachment{Name: path, ContentType: f.ContentType, Data: sources[path]})
	}
	from := transportCfg.From
	msg.From = from
	res, sendErr := sender.Send(ctx, msg)
	state := res.State
	if sendErr != nil && state == "" {
		state = mail.StateUncertain
	}
	if state == "" {
		state = mail.StateUncertain
	}
	_, _ = e.db.ExecContext(ctx, `UPDATE deliveries SET state=?,detail=?,updated_at=? WHERE id=?`, state, res.Detail, e.now().UTC().Format(time.RFC3339Nano), did)
	if state == mail.StateTransient {
		e.maybeOpenCircuit(ctx, j.p)
	}
	if state == mail.StateAccepted {
		return e.finishJob(ctx, j.id, "complete", res.Detail)
	}
	if state == mail.StateTransient {
		return e.retryJob(ctx, j.id, res.Detail)
	}
	return e.finishJob(ctx, j.id, map[bool]string{true: "uncertain", false: "failed"}[state == mail.StateUncertain], res.Detail)
}

func (e *Engine) reserveTransportSend(ctx context.Context, p string) bool {
	var id, raw string
	er := e.db.QueryRowContext(ctx, `SELECT id,config FROM registry WHERE project_id=? AND resource='transports' AND enabled=1 ORDER BY created_at DESC LIMIT 1`, p).Scan(&id, &raw)
	if er == sql.ErrNoRows {
		return true
	}
	if er != nil {
		return false
	}
	var c transportConfig
	if e.openConfig(p, id, raw, &c) != nil {
		return false
	}
	return e.reserveSelectedTransport(ctx, p, id, c.RatePerMinute)
}
func (e *Engine) reserveSelectedTransport(ctx context.Context, p, id string, limit int) bool {
	if limit <= 0 {
		limit = 60
	}
	window := e.now().UTC().Truncate(time.Minute).Format(time.RFC3339Nano)
	tx, er := e.db.BeginTx(ctx, nil)
	if er != nil {
		return false
	}
	defer tx.Rollback()
	_, er = tx.ExecContext(ctx, `INSERT INTO rate_limits(scope,bucket,window_start,count) VALUES('smtp',?,?,1) ON CONFLICT(scope,bucket,window_start) DO UPDATE SET count=count+1`, p+":"+id, window)
	if er != nil {
		return false
	}
	var n int
	if tx.QueryRowContext(ctx, `SELECT count FROM rate_limits WHERE scope='smtp' AND bucket=? AND window_start=?`, p+":"+id, window).Scan(&n) != nil || n > limit {
		return false
	}
	return tx.Commit() == nil
}
func (e *Engine) maybeOpenCircuit(ctx context.Context, p string) {
	cut := e.now().UTC().Add(-10 * time.Minute).Format(time.RFC3339Nano)
	var n int
	_ = e.db.QueryRowContext(ctx, `SELECT count(*) FROM deliveries WHERE project_id=? AND state='transient' AND updated_at>=?`, p, cut).Scan(&n)
	if n >= 5 {
		now := e.now().UTC().Format(time.RFC3339Nano)
		r, _ := e.db.ExecContext(ctx, `UPDATE projects SET paused=1,pause_reason='transport transient circuit open',updated_at=? WHERE id=? AND paused=0`, now, p)
		if affected, _ := r.RowsAffected(); affected > 0 {
			_, _ = e.db.ExecContext(ctx, `INSERT INTO audit(id,project_id,action,detail,created_at) VALUES(?,?, 'transport.circuit_open',?,?)`, newID("aud"), p, `{"transient_threshold":5,"window_minutes":10}`, now)
		}
	}
}

func (e *Engine) reserveDelivery(ctx context.Context, p, email, contact, list, release, enrollment, broadcast, step, message, hash, transportID, eventID string, exit *manifest.Condition, now string) (did, messageID, existingHash, result string, err error) {
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", "", "", err
	}
	defer tx.Rollback()
	if exit != nil && evaluateCondition(ctx, tx, p, contact, eventID, exit) {
		return "", "", "", "exit", tx.Commit()
	}
	var eligible int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM consent c JOIN contacts x ON x.id=c.contact_id AND x.project_id=c.project_id JOIN lists l ON l.project_id=c.project_id AND l.id=c.list_id JOIN projects p ON p.id=c.project_id JOIN installation i ON i.id=1 LEFT JOIN suppressions s ON s.project_id=c.project_id AND s.email=? WHERE c.project_id=? AND c.contact_id=? AND c.list_id=? AND c.state='confirmed' AND c.policy_version=l.policy_version AND l.retired=0 AND x.deleted_at IS NULL AND s.email IS NULL AND p.paused=0 AND i.outbound_paused=0`, email, p, contact, list).Scan(&eligible); err != nil {
		return
	}
	if eligible == 0 {
		return "", "", "", "ineligible", tx.Commit()
	}
	var state string
	q := `SELECT id,message_id,state,COALESCE(artifact_hash,'') FROM deliveries WHERE enrollment_id=? AND step_id=?`
	args := []any{enrollment, step}
	if broadcast != "" {
		q = `SELECT id,message_id,state,COALESCE(artifact_hash,'') FROM deliveries WHERE broadcast_id=? AND contact_id=?`
		args = []any{broadcast, contact}
	}
	find := tx.QueryRowContext(ctx, q, args...).Scan(&did, &messageID, &state, &existingHash)
	if find == nil {
		if state != mail.StateTransient {
			return did, messageID, existingHash, "existing", tx.Commit()
		}
		var r sql.Result
		r, err = tx.ExecContext(ctx, `UPDATE deliveries SET state='dispatching',attempt_id=?,updated_at=? WHERE id=? AND state=?`, newID("attempt"), now, did, mail.StateTransient)
		if err != nil {
			return
		}
		n, _ := r.RowsAffected()
		if n != 1 {
			return "", "", "", "", conflict("dispatch_owned", "retry ownership lost")
		}
		result = "retry"
	} else if find == sql.ErrNoRows {
		did = newID("del")
		messageID = did + "@mrkt"
		_, err = tx.ExecContext(ctx, `INSERT INTO deliveries(id,project_id,contact_id,release_id,enrollment_id,broadcast_id,step_id,message_key,message_id,state,artifact_hash,attempt_id,created_at,updated_at,transport_id) VALUES(?,?,?,?,?,?,?,?,?,'dispatching',?,?,?,?,?)`, did, p, contact, release, nullable(enrollment), nullable(broadcast), step, message, messageID, hash, newID("attempt"), now, now, transportID)
		result = "new"
	} else {
		err = find
	}
	if err == nil {
		err = tx.Commit()
	}
	return
}

func (e *Engine) existingDeliveryTransport(ctx context.Context, enrollment, broadcast, contact, step string) string {
	var id string
	if enrollment != "" {
		_ = e.db.QueryRowContext(ctx, `SELECT transport_id FROM deliveries WHERE enrollment_id=? AND step_id=?`, enrollment, step).Scan(&id)
	} else if broadcast != "" {
		_ = e.db.QueryRowContext(ctx, `SELECT transport_id FROM deliveries WHERE broadcast_id=? AND contact_id=?`, broadcast, contact).Scan(&id)
	}
	return id
}
func (e *Engine) deliveryStream(ctx context.Context, m manifest.Manifest, enrollment, broadcast string) string {
	if enrollment != "" {
		var sid string
		_ = e.db.QueryRowContext(ctx, `SELECT sequence_id FROM enrollments WHERE id=?`, enrollment).Scan(&sid)
		for _, s := range m.Sequences {
			if s.ID == sid {
				return s.Stream
			}
		}
	}
	if broadcast != "" {
		var bid string
		_ = e.db.QueryRowContext(ctx, `SELECT definition_id FROM broadcasts WHERE id=?`, broadcast).Scan(&bid)
		for _, b := range m.Broadcasts {
			if b.ID == bid {
				return b.Stream
			}
		}
	}
	return ""
}

func (e *Engine) deliveryList(ctx context.Context, m manifest.Manifest, enrollmentID, broadcastID string) string {
	if enrollmentID != "" {
		var sid string
		_ = e.db.QueryRowContext(ctx, `SELECT sequence_id FROM enrollments WHERE id=?`, enrollmentID).Scan(&sid)
		for _, s := range m.Sequences {
			if s.ID == sid {
				return s.List
			}
		}
	}
	if broadcastID != "" {
		var id string
		_ = e.db.QueryRowContext(ctx, `SELECT definition_id FROM broadcasts WHERE id=?`, broadcastID).Scan(&id)
		for _, b := range m.Broadcasts {
			if b.ID == id {
				return b.List
			}
		}
	}
	return ""
}
func (e *Engine) deliveryEvent(ctx context.Context, p, enrollmentID, broadcastID string) (map[string]any, string) {
	var id string
	if enrollmentID != "" {
		_ = e.db.QueryRowContext(ctx, `SELECT COALESCE(event_id,'') FROM enrollments WHERE id=? AND project_id=?`, enrollmentID, p).Scan(&id)
	} else if broadcastID != "" {
		_ = e.db.QueryRowContext(ctx, `SELECT COALESCE(event_id,'') FROM broadcasts WHERE id=? AND project_id=?`, broadcastID, p).Scan(&id)
	}
	if id == "" {
		return map[string]any{}, ""
	}
	var raw string
	if e.db.QueryRowContext(ctx, `SELECT payload FROM events WHERE id=? AND project_id=?`, id, p).Scan(&raw) != nil {
		return map[string]any{}, id
	}
	return scanJSON[map[string]any](raw), id
}

var _ = fmt.Sprintf
