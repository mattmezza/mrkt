package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"time"
)

func (e *Engine) doInstallation(ctx context.Context, a Authority, op Operation) (any, error) {
	if op.Action == "verify" {
		return e.verifyInstallation(ctx, op)
	}
	var recovery, paused int
	var created, updated string
	if op.Action == "get" || op.Action == "" {
		er := e.db.QueryRowContext(ctx, `SELECT recovery,outbound_paused,created_at,updated_at FROM installation WHERE id=1`).Scan(&recovery, &paused, &created, &updated)
		if er != nil {
			return nil, internal(er)
		}
		return map[string]any{"recovery": recovery == 1, "outbound_paused": paused == 1, "created_at": created, "updated_at": updated}, nil
	}
	if op.Action == "resume" {
		var in struct {
			Reconciled bool   `json:"reconciled"`
			Note       string `json:"note"`
		}
		if er := decode(op.Input, &in); er != nil {
			return nil, er
		}
		if !in.Reconciled || in.Note == "" {
			return nil, bad("reconciliation_required", "reconciled=true and an audit note are required")
		}
		now := e.now().UTC().Format(time.RFC3339Nano)
		tx, er := e.db.BeginTx(ctx, nil)
		if er != nil {
			return nil, internal(er)
		}
		defer tx.Rollback()
		if _, er = tx.ExecContext(ctx, `UPDATE installation SET recovery=0,outbound_paused=0,updated_at=? WHERE id=1`, now); er != nil {
			return nil, internal(er)
		}
		detail, _ := json.Marshal(in)
		if _, er = tx.ExecContext(ctx, `INSERT INTO audit(id,action,detail,created_at) VALUES(?,'recovery.resume',?,?)`, newID("aud"), string(detail), now); er != nil {
			return nil, internal(er)
		}
		if er = tx.Commit(); er != nil {
			return nil, internal(er)
		}
		if er := os.Remove(e.cfg.DBPath + ".recovery-required"); er != nil && !os.IsNotExist(er) {
			return nil, &Error{"recovery_marker_remove_failed", "reconciliation was audited but the recovery marker remains; restart will pause outbound again", 500}
		}
		return map[string]any{"recovery": false, "outbound_paused": false}, nil
	}
	return nil, notFound("unknown installation action")
}
func (e *Engine) doRuntimeOperations(ctx context.Context, a Authority, op Operation) (any, error) {
	if op.Action == "usage" || op.Action == "gc" || op.Action == "retention" {
		return e.doMaintenance(ctx, op)
	}
	if op.Action != "pause" && op.Action != "resume" {
		return nil, notFound("unknown operation")
	}
	paused := op.Action == "pause"
	var in struct {
		Reason string `json:"reason"`
	}
	if len(op.Input) > 0 {
		if er := decode(op.Input, &in); er != nil {
			return nil, er
		}
	}
	if paused && in.Reason == "" {
		return nil, bad("invalid_input", "reason required")
	}
	now := e.now().UTC().Format(time.RFC3339Nano)
	tx, er := e.db.BeginTx(ctx, nil)
	if er != nil {
		return nil, internal(er)
	}
	defer tx.Rollback()
	r, er := tx.ExecContext(ctx, `UPDATE projects SET paused=?,pause_reason=?,updated_at=? WHERE id=?`, boolInt(paused), in.Reason, now, op.Project)
	if er != nil {
		return nil, internal(er)
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return nil, notFound("project not found")
	}
	detail, _ := json.Marshal(map[string]any{"paused": paused, "reason": in.Reason})
	if _, er = tx.ExecContext(ctx, `INSERT INTO audit(id,project_id,action,detail,created_at) VALUES(?,?,?,?,?)`, newID("aud"), op.Project, "project."+op.Action, string(detail), now); er != nil {
		return nil, internal(er)
	}
	if er = insertOutbox(ctx, tx, op.Project, "project."+op.Action, map[string]any{"reason": in.Reason}, op.Project, now); er != nil {
		return nil, internal(er)
	}
	if er = tx.Commit(); er != nil {
		return nil, internal(er)
	}
	return map[string]any{"paused": paused, "reason": in.Reason}, nil
}
func (e *Engine) doMaintenance(ctx context.Context, op Operation) (any, error) {
	switch op.Action {
	case "usage":
		counts := map[string]int64{}
		for _, table := range []string{"contacts", "consent", "events", "enrollments", "broadcasts", "deliveries", "jobs", "outbox", "releases", "release_files"} {
			var n int64
			q := `SELECT count(*) FROM ` + table + ` WHERE project_id=?`
			if er := e.db.QueryRowContext(ctx, q, op.Project).Scan(&n); er == nil {
				counts[table] = n
			}
		}
		sizes := map[string]int64{}
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if st, er := os.Stat(e.cfg.DBPath + suffix); er == nil {
				sizes[suffix] = st.Size()
			}
		}
		return map[string]any{"counts": counts, "sqlite_bytes": sizes, "artifact_usage": "object sizes are available from release inventory; provider bucket totals are not inferred", "measured_at": e.now().UTC()}, nil
	case "gc":
		var in struct {
			DryRun bool `json:"dry_run"`
		}
		if er := decode(op.Input, &in); er != nil {
			return nil, er
		}
		if !in.DryRun {
			return nil, bad("dry_run_required", "artifact GC is conservative and requires dry_run=true")
		}
		var n, size int64
		er := e.db.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(size),0) FROM release_files f WHERE project_id=? AND NOT EXISTS(SELECT 1 FROM public_assets p WHERE p.project_id=f.project_id AND p.hash=f.hash) AND NOT EXISTS(SELECT 1 FROM deliveries d WHERE d.project_id=f.project_id AND d.artifact_hash=f.hash)`, op.Project).Scan(&n, &size)
		if er != nil {
			return nil, internal(er)
		}
		return map[string]any{"dry_run": true, "candidate_references": n, "candidate_bytes": size, "objects_deleted": 0, "protected": "all release, public asset, delivery, and recoverable backup references; no object deletion is enabled"}, nil
	case "retention":
		var in struct {
			EventDays int    `json:"event_days"`
			DryRun    bool   `json:"dry_run"`
			Apply     bool   `json:"apply"`
			Note      string `json:"note"`
		}
		if er := decode(op.Input, &in); er != nil {
			return nil, er
		}
		if in.EventDays < 1 || in.EventDays > 3650 {
			return nil, bad("invalid_retention", "event_days must be between 1 and 3650")
		}
		cut := e.now().UTC().Add(-time.Duration(in.EventDays) * 24 * time.Hour).Format(time.RFC3339Nano)
		var n int64
		_ = e.db.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE project_id=? AND created_at<? AND payload<>'{}' AND NOT EXISTS(SELECT 1 FROM enrollments WHERE enrollments.event_id=events.id AND state NOT IN ('completed','cancelled','failed'))`, op.Project, cut).Scan(&n)
		if !in.Apply {
			return map[string]any{"dry_run": true, "eligible_event_payloads": n, "cutoff": cut, "redacted": 0, "deduplication_tombstones_retained": true}, nil
		}
		if in.Note == "" {
			return nil, bad("retention_note_required", "apply requires an operator audit note")
		}
		now := e.now().UTC().Format(time.RFC3339Nano)
		tx, er := e.db.BeginTx(ctx, nil)
		if er != nil {
			return nil, internal(er)
		}
		defer tx.Rollback()
		r, er := tx.ExecContext(ctx, `UPDATE events SET payload='{}' WHERE id IN (SELECT id FROM events WHERE project_id=? AND created_at<? AND payload<>'{}' AND NOT EXISTS(SELECT 1 FROM enrollments WHERE enrollments.event_id=events.id AND state NOT IN ('completed','cancelled','failed')) LIMIT 1000)`, op.Project, cut)
		if er != nil {
			return nil, internal(er)
		}
		redacted, _ := r.RowsAffected()
		tokens, _ := tx.ExecContext(ctx, `DELETE FROM consent_tokens WHERE project_id=? AND ((used_at IS NOT NULL AND used_at<?) OR expires_at<?)`, op.Project, cut, now)
		tokenN, _ := tokens.RowsAffected()
		_, _ = tx.ExecContext(ctx, `DELETE FROM rate_limits WHERE window_start<?`, cut)
		_, _ = tx.ExecContext(ctx, `DELETE FROM jobs WHERE project_id=? AND state IN ('complete','cancelled','failed') AND updated_at<?`, op.Project, cut)
		detail, _ := json.Marshal(map[string]any{"note": in.Note, "cutoff": cut, "events_redacted": redacted, "tokens_deleted": tokenN})
		if _, er = tx.ExecContext(ctx, `INSERT INTO audit(id,project_id,action,detail,created_at) VALUES(?,?,'retention.apply',?,?)`, newID("aud"), op.Project, string(detail), now); er != nil {
			return nil, internal(er)
		}
		if er = tx.Commit(); er != nil {
			return nil, internal(er)
		}
		return map[string]any{"dry_run": false, "cutoff": cut, "event_payloads_redacted": redacted, "expired_tokens_deleted": tokenN, "deduplication_tombstones_retained": true}, nil
	}
	return nil, notFound("unknown maintenance action")
}

func (e *Engine) doDeliveries(ctx context.Context, a Authority, op Operation) (any, error) {
	return e.listRuntime(ctx, op, "deliveries")
}
func (e *Engine) doEnrollments(ctx context.Context, a Authority, op Operation) (any, error) {
	if op.Action == "plan-migration" || op.Action == "migrate" {
		return e.migrateEnrollments(ctx, op)
	}
	if op.Action == "explain" {
		return e.explainEnrollment(ctx, op)
	}
	if op.Action == "cancel" || op.Action == "pause" || op.Action == "resume" {
		state := map[string]string{"cancel": "cancelled", "pause": "paused", "resume": "active"}[op.Action]
		allowed := map[string]string{"cancel": "active','waiting','paused", "pause": "active','waiting", "resume": "paused"}[op.Action]
		now := e.now().UTC().Format(time.RFC3339Nano)
		tx, er := e.db.BeginTx(ctx, nil)
		if er != nil {
			return nil, internal(er)
		}
		defer tx.Rollback()
		r, er := tx.ExecContext(ctx, `UPDATE enrollments SET state=?,updated_at=? WHERE project_id=? AND id=? AND state IN ('`+allowed+`')`, state, now, op.Project, op.ID)
		if er != nil {
			return nil, internal(er)
		}
		n, _ := r.RowsAffected()
		if n == 0 {
			return nil, conflict("invalid_transition", "enrollment state does not permit "+op.Action)
		}
		detail, _ := json.Marshal(map[string]any{"enrollment_id": op.ID, "state": state})
		if _, er = tx.ExecContext(ctx, `INSERT INTO audit(id,project_id,action,detail,created_at) VALUES(?,?,?,?,?)`, newID("aud"), op.Project, "enrollment."+op.Action, string(detail), now); er != nil {
			return nil, internal(er)
		}
		if er = tx.Commit(); er != nil {
			return nil, internal(er)
		}
		return map[string]any{"ok": true, "state": state}, nil
	}
	return e.listRuntime(ctx, op, "enrollments")
}
func (e *Engine) migrateEnrollments(ctx context.Context, op Operation) (any, error) {
	var in struct {
		SequenceID string `json:"sequence_id"`
		ReleaseID  string `json:"release_id"`
		Expected   int    `json:"expected"`
		Confirm    bool   `json:"confirm"`
	}
	if er := decode(op.Input, &in); er != nil {
		return nil, er
	}
	target, er := manifestForRelease(ctx, e.db, op.Project, in.ReleaseID)
	if er != nil {
		return nil, notFound("target release not found")
	}
	steps := map[string]bool{}
	found := false
	for _, s := range target.Sequences {
		if s.ID == in.SequenceID {
			found = true
			for _, st := range s.Steps {
				steps[st.ID] = true
			}
		}
	}
	if !found {
		return nil, bad("invalid_migration", "sequence absent from target release")
	}
	rows, er := e.db.QueryContext(ctx, `SELECT id,current_step,release_id FROM enrollments WHERE project_id=? AND sequence_id=? AND state IN ('active','waiting','paused') AND release_id<>?`, op.Project, in.SequenceID, in.ReleaseID)
	if er != nil {
		return nil, internal(er)
	}
	defer rows.Close()
	eligible := []string{}
	incompatible := []map[string]string{}
	for rows.Next() {
		var id, step, from string
		if er = rows.Scan(&id, &step, &from); er != nil {
			return nil, internal(er)
		}
		if steps[step] {
			eligible = append(eligible, id)
		} else {
			incompatible = append(incompatible, map[string]string{"id": id, "current_step": step, "release_id": from})
		}
	}
	plan := map[string]any{"sequence_id": in.SequenceID, "target_release": in.ReleaseID, "eligible": eligible, "eligible_count": len(eligible), "incompatible": incompatible, "completed_steps_replayed": false}
	if op.Action == "plan-migration" {
		return plan, nil
	}
	if !in.Confirm || in.Expected != len(eligible) {
		return nil, conflict("migration_precondition", "confirm=true and expected must match eligible_count")
	}
	tx, er := e.db.BeginTx(ctx, nil)
	if er != nil {
		return nil, internal(er)
	}
	defer tx.Rollback()
	updated := 0
	for _, id := range eligible {
		r, er := tx.ExecContext(ctx, `UPDATE enrollments SET release_id=?,updated_at=? WHERE id=? AND project_id=? AND sequence_id=? AND state IN ('active','waiting','paused')`, in.ReleaseID, e.now().UTC().Format(time.RFC3339Nano), id, op.Project, in.SequenceID)
		if er != nil {
			return nil, internal(er)
		}
		n, _ := r.RowsAffected()
		updated += int(n)
	}
	if updated != in.Expected {
		return nil, conflict("migration_changed", "enrollments changed while applying migration")
	}
	if er = tx.Commit(); er != nil {
		return nil, internal(er)
	}
	plan["migrated"] = updated
	return plan, nil
}
func (e *Engine) explainEnrollment(ctx context.Context, op Operation) (any, error) {
	var id, c, r, s, step, state, next, created, updated string
	er := e.db.QueryRowContext(ctx, `SELECT id,contact_id,release_id,sequence_id,current_step,state,COALESCE(next_at,''),created_at,updated_at FROM enrollments WHERE project_id=? AND id=?`, op.Project, op.ID).Scan(&id, &c, &r, &s, &step, &state, &next, &created, &updated)
	if er == sql.ErrNoRows {
		return nil, notFound("enrollment not found")
	}
	if er != nil {
		return nil, internal(er)
	}
	timeline := []map[string]any{}
	rows, er := e.db.QueryContext(ctx, `SELECT id,step_id,message_key,message_id,state,detail,created_at,updated_at FROM deliveries WHERE project_id=? AND enrollment_id=? ORDER BY created_at,id`, op.Project, id)
	if er != nil {
		return nil, internal(er)
	}
	for rows.Next() {
		var did, st, msg, mid, ds, detail, ca, ua string
		if er = rows.Scan(&did, &st, &msg, &mid, &ds, &detail, &ca, &ua); er != nil {
			rows.Close()
			return nil, internal(er)
		}
		timeline = append(timeline, map[string]any{"delivery_id": did, "step": st, "message": msg, "message_id": mid, "state": ds, "detail": detail, "created_at": ca, "updated_at": ua})
	}
	rows.Close()
	var consentState, suppression string
	_ = e.db.QueryRowContext(ctx, `SELECT COALESCE((SELECT state FROM consent c JOIN releases r ON r.id=? WHERE c.project_id=? AND c.contact_id=? AND EXISTS(SELECT 1 FROM json_each(r.manifest,'$.sequences') j WHERE json_extract(j.value,'$.id')=? AND json_extract(j.value,'$.list')=c.list_id) LIMIT 1),'missing'),COALESCE((SELECT reason FROM suppressions x JOIN contacts c ON c.email=x.email AND c.project_id=x.project_id WHERE c.id=? AND x.project_id=?),'')`, r, op.Project, c, s, c, op.Project).Scan(&consentState, &suppression)
	return map[string]any{"id": id, "contact_id": c, "release_id": r, "sequence_id": s, "current_step": step, "state": state, "next_at": next, "created_at": created, "updated_at": updated, "consent_state": consentState, "suppression": suppression, "timeline": timeline, "reason": explainReason(state, consentState, suppression, next)}, nil
}
func explainReason(state, consent, suppression, next string) string {
	if suppression != "" {
		return "suppressed: " + suppression
	}
	if consent != "confirmed" {
		return "required list consent is " + consent
	}
	switch state {
	case "waiting":
		return "delivery job is queued or in progress"
	case "active":
		if next != "" {
			return "next step scheduled for " + next
		}
		return "ready"
	case "completed":
		return "sequence completed"
	case "cancelled":
		return "enrollment cancelled"
	}
	return state
}
func (e *Engine) doBroadcasts(ctx context.Context, a Authority, op Operation) (any, error) {
	return e.listRuntime(ctx, op, "broadcasts")
}
func (e *Engine) listRuntime(ctx context.Context, op Operation, kind string) (any, error) {
	if op.Action != "list" && op.Action != "get" && op.Action != "explain" {
		return nil, notFound("unknown " + kind + " action")
	}
	var q string
	allowed := []string{"state", "release_id"}
	if kind != "broadcasts" {
		allowed = append(allowed, "contact_id")
	}
	if er := validateFilters(op.Filters, allowed...); er != nil {
		return nil, er
	}
	if kind == "deliveries" {
		q = `SELECT id,json_object('id',id,'contact_id',contact_id,'release_id',release_id,'state',state,'message_id',message_id,'detail',detail) FROM deliveries WHERE project_id=?`
	} else if kind == "enrollments" {
		q = `SELECT id,json_object('id',id,'contact_id',contact_id,'release_id',release_id,'sequence_id',sequence_id,'current_step',current_step,'state',state,'next_at',next_at) FROM enrollments WHERE project_id=?`
	} else {
		q = `SELECT id,json_object('id',id,'release_id',release_id,'definition_id',definition_id,'state',state,'audience_frozen_at',audience_frozen_at) FROM broadcasts WHERE project_id=?`
	}
	args := []any{op.Project}
	for _, f := range allowed {
		if v := op.Filters[f]; v != "" {
			q += ` AND ` + f + `=?`
			args = append(args, v)
		}
	}
	if op.Action == "list" {
		q += ` AND id>? ORDER BY id LIMIT ?`
		args = append(args, op.Cursor, op.Limit+1)
	} else {
		q += ` AND id=?`
		args = append(args, op.ID)
	}
	rows, er := e.db.QueryContext(ctx, q, args...)
	if er != nil {
		return nil, internal(er)
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, raw string
		if er = rows.Scan(&id, &raw); er != nil {
			return nil, internal(er)
		}
		v := scanJSON[map[string]any](raw)
		items = append(items, v)
	}
	if op.Action != "list" {
		if len(items) == 0 {
			return nil, notFound(kind + " entry not found")
		}
		return items[0], nil
	}
	return paged(items, op.Limit), nil
}
func (e *Engine) doAsset(ctx context.Context, op Operation) (any, error) {
	var ct string
	var size int64
	er := e.db.QueryRowContext(ctx, `SELECT content_type,size FROM public_assets WHERE project_id=? AND hash=?`, op.Project, op.ID).Scan(&ct, &size)
	if er == sql.ErrNoRows {
		return nil, notFound("asset not found")
	}
	if er != nil {
		return nil, internal(er)
	}
	return map[string]any{"hash": op.ID, "content_type": ct, "size": size}, nil
}
