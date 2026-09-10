package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"time"

	"github.com/mattmezza/mrkt/internal/security"
)

func (e *Engine) tickOutbox(ctx context.Context) error {
	var paused int
	if err := e.db.QueryRowContext(ctx, `SELECT outbound_paused FROM installation WHERE id=1`).Scan(&paused); err != nil {
		return err
	}
	if paused != 0 {
		return nil
	}
	now := e.now().UTC().Format(time.RFC3339Nano)
	// Fan out each committed event exactly once per endpoint, freezing exact envelope bytes.
	_, err := withTx(ctx, e.db, func(tx *sql.Tx) (bool, error) {
		rs, err := tx.QueryContext(ctx, `SELECT id,project_id,type,payload,correlation_id,causation_id,created_at FROM outbox WHERE state='pending' ORDER BY created_at LIMIT 100`)
		if err != nil {
			return false, err
		}
		type event struct{ id, p, typ, payload, correlation, causation, created string }
		events := []event{}
		for rs.Next() {
			var v event
			if err = rs.Scan(&v.id, &v.p, &v.typ, &v.payload, &v.correlation, &v.causation, &v.created); err != nil {
				rs.Close()
				return false, err
			}
			events = append(events, v)
		}
		rs.Close()
		for _, v := range events {
			endpoints, err := tx.QueryContext(ctx, `SELECT id,config FROM registry WHERE project_id=? AND resource='webhooks' AND enabled=1`, v.p)
			if err != nil {
				return false, err
			}
			type endpoint struct{ id, raw string }
			eps := []endpoint{}
			for endpoints.Next() {
				var ep endpoint
				if err = endpoints.Scan(&ep.id, &ep.raw); err != nil {
					endpoints.Close()
					return false, err
				}
				eps = append(eps, ep)
			}
			endpoints.Close()
			payload, err := json.Marshal(map[string]any{"version": "1", "id": v.id, "project_id": v.p, "type": v.typ, "occurred_at": v.created, "correlation_id": v.correlation, "causation_id": v.causation, "data": json.RawMessage(v.payload)})
			if err != nil {
				return false, err
			}
			for _, ep := range eps {
				var c webhookConfig
				if err = e.openConfig(v.p, ep.id, ep.raw, &c); err != nil {
					return false, err
				}
				if !slices.Contains(c.Events, "*") && !slices.Contains(c.Events, v.typ) {
					continue
				}
				if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO webhook_deliveries(id,project_id,event_id,endpoint_id,payload,next_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, newID("whd"), v.p, v.id, ep.id, string(payload), now, now, now); err != nil {
					return false, err
				}
			}
			if _, err = tx.ExecContext(ctx, `UPDATE outbox SET state='distributed' WHERE id=?`, v.id); err != nil {
				return false, err
			}
		}
		return true, nil
	})
	if err != nil {
		return err
	}
	// HTTP is at least once: lease expiry permits retry with same event ID and new attempt ID.
	for count := 0; count < 10; count++ {
		type delivery struct {
			id, p, event, endpoint, payload, attempt string
			attempts                                 int
		}
		d, err := withTx(ctx, e.db, func(tx *sql.Tx) (delivery, error) {
			var d delivery
			err := tx.QueryRowContext(ctx, `SELECT d.id,d.project_id,d.event_id,d.endpoint_id,d.payload,d.attempts FROM webhook_deliveries d JOIN projects p ON p.id=d.project_id WHERE p.paused=0 AND (d.state='pending' OR (d.state='sending' AND d.lease_until<?)) AND d.next_at<=? ORDER BY d.next_at LIMIT 1`, now, now).Scan(&d.id, &d.p, &d.event, &d.endpoint, &d.payload, &d.attempts)
			if err != nil {
				return d, err
			}
			d.attempt = newID("attempt")
			_, err = tx.ExecContext(ctx, `UPDATE webhook_deliveries SET state='sending',lease_until=?,attempt_id=?,attempts=attempts+1,updated_at=? WHERE id=?`, e.now().Add(time.Minute).UTC().Format(time.RFC3339Nano), d.attempt, now, d.id)
			return d, err
		})
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		var c webhookConfig
		err = e.registryConfig(ctx, d.p, "webhooks", d.endpoint, &c)
		status := 0
		if err == nil {
			var stopped int
			err = e.db.QueryRowContext(ctx, `SELECT i.outbound_paused+p.paused FROM installation i,projects p WHERE i.id=1 AND p.id=?`, d.p).Scan(&stopped)
			if err == nil && stopped != 0 {
				_, err = e.db.ExecContext(ctx, `UPDATE webhook_deliveries SET state='pending' WHERE id=? AND attempt_id=?`, d.id, d.attempt)
				return err
			}
			if err == nil {
				client := security.WebhookClient{AllowHTTPDevelopment: e.cfg.Development && c.Development, DevelopmentAllowedHosts: e.cfg.WebhookDevelopmentAllowedHosts}
				status, err = client.Post(ctx, c.URL, []byte(d.payload), []byte(c.Secret), d.event, d.attempt)
			}
		}
		state := "pending"
		detail := ""
		if err != nil {
			detail = err.Error()
		}
		if status >= 200 && status < 300 && err == nil {
			state = "delivered"
		}
		if d.attempts >= 9 && state != "delivered" {
			state = "dead_letter"
		}
		if status >= 400 && status < 500 && status != 408 && status != 429 {
			state = "dead_letter"
		}
		delay := time.Duration(1<<min(d.attempts, 12)) * time.Minute
		if _, err = e.db.ExecContext(ctx, `INSERT INTO webhook_attempts(id,project_id,delivery_id,event_id,status,error,created_at) VALUES(?,?,?,?,?,?,?)`, d.attempt, d.p, d.id, d.event, status, detail, now); err != nil {
			return err
		}
		if _, err = e.db.ExecContext(ctx, `UPDATE webhook_deliveries SET state=?,last_status=?,last_error=?,next_at=?,lease_until=NULL,updated_at=? WHERE id=? AND attempt_id=?`, state, status, detail, e.now().Add(delay).UTC().Format(time.RFC3339Nano), now, d.id, d.attempt); err != nil {
			return err
		}
	}
	return nil
}
func (e *Engine) webhookDeliveries(ctx context.Context, a Authority, op Operation) (any, error) {
	if op.Action == "replay" {
		now := e.now().UTC().Format(time.RFC3339Nano)
		r, err := e.db.ExecContext(ctx, `UPDATE webhook_deliveries SET state='pending',attempts=0,next_at=?,updated_at=? WHERE project_id=? AND id=? AND state<>'sending'`, now, now, op.Project, op.ID)
		if err != nil {
			return nil, internal(err)
		}
		return affected(r, "delivery missing or currently leased")
	}
	if op.Action != "list" && op.Action != "get" {
		return nil, notFound("unknown webhook delivery action")
	}
	q := `SELECT id,event_id,endpoint_id,state,attempts,last_status,last_error,created_at FROM webhook_deliveries WHERE project_id=?`
	args := []any{op.Project}
	if op.Action == "get" {
		q += ` AND id=?`
		args = append(args, op.ID)
	} else {
		q += ` AND id>? ORDER BY id LIMIT ?`
		args = append(args, op.Cursor, op.Limit+1)
	}
	rs, err := e.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, internal(err)
	}
	defer rs.Close()
	items := []map[string]any{}
	for rs.Next() {
		var id, event, endpoint, state, detail, created string
		var attempts int
		var status sql.NullInt64
		if err = rs.Scan(&id, &event, &endpoint, &state, &attempts, &status, &detail, &created); err != nil {
			return nil, internal(err)
		}
		items = append(items, map[string]any{"id": id, "event_id": event, "endpoint_id": endpoint, "state": state, "attempts": attempts, "last_status": status.Int64, "last_error": detail, "created_at": created})
	}
	if op.Action == "get" {
		if len(items) == 0 {
			return nil, notFound("delivery not found")
		}
		rs.Close()
		history, err := e.db.QueryContext(ctx, `SELECT id,event_id,status,error,created_at FROM webhook_attempts WHERE project_id=? AND delivery_id=? ORDER BY created_at DESC LIMIT 100`, op.Project, op.ID)
		if err != nil {
			return nil, internal(err)
		}
		defer history.Close()
		attempts := []map[string]any{}
		for history.Next() {
			var id, event, detail, created string
			var status int
			if err = history.Scan(&id, &event, &status, &detail, &created); err != nil {
				return nil, internal(err)
			}
			attempts = append(attempts, map[string]any{"id": id, "event_id": event, "status": status, "error": detail, "created_at": created})
		}
		items[0]["history"] = attempts
		return items[0], nil
	}
	return paged(items, op.Limit), nil
}
