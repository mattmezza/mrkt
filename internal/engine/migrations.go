package engine

import (
	"context"
	"database/sql"
	"fmt"
)

var migrations = []string{`
CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY);
CREATE TABLE installation(id INTEGER PRIMARY KEY CHECK(id=1), recovery INTEGER NOT NULL, outbound_paused INTEGER NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE projects(id TEXT PRIMARY KEY, name TEXT NOT NULL, active_release_id TEXT, paused INTEGER NOT NULL DEFAULT 0, pause_reason TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE api_tokens(id TEXT PRIMARY KEY, project_id TEXT REFERENCES projects(id) ON DELETE CASCADE, name TEXT NOT NULL, token_hash TEXT NOT NULL UNIQUE, scopes TEXT NOT NULL, admin INTEGER NOT NULL DEFAULT 0, created_at TEXT NOT NULL, revoked_at TEXT);
CREATE TABLE contacts(id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE, external_id TEXT, email TEXT NOT NULL, name TEXT NOT NULL DEFAULT '', locale TEXT NOT NULL DEFAULT '', timezone TEXT NOT NULL DEFAULT '', attributes TEXT NOT NULL DEFAULT '{}', created_at TEXT NOT NULL, updated_at TEXT NOT NULL, deleted_at TEXT, UNIQUE(project_id,email), UNIQUE(project_id,external_id));
CREATE INDEX contacts_project_id ON contacts(project_id,id);
CREATE TABLE lists(project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE, id TEXT NOT NULL, name TEXT NOT NULL, purpose TEXT NOT NULL, policy_version TEXT NOT NULL, release_id TEXT NOT NULL, PRIMARY KEY(project_id,id));
CREATE TABLE consent(id TEXT PRIMARY KEY, project_id TEXT NOT NULL, contact_id TEXT NOT NULL, list_id TEXT NOT NULL, state TEXT NOT NULL CHECK(state IN ('pending','confirmed','unsubscribed')), source TEXT NOT NULL, policy_version TEXT NOT NULL, requested_at TEXT NOT NULL, confirmed_at TEXT, unsubscribed_at TEXT, updated_at TEXT NOT NULL, UNIQUE(project_id,contact_id,list_id), FOREIGN KEY(contact_id) REFERENCES contacts(id) ON DELETE CASCADE);
CREATE INDEX consent_eligibility ON consent(project_id,list_id,state,contact_id);
CREATE TABLE suppressions(project_id TEXT NOT NULL, email TEXT NOT NULL, reason TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(project_id,email));
CREATE TABLE consent_tokens(token_hash TEXT PRIMARY KEY, project_id TEXT NOT NULL, contact_id TEXT NOT NULL, list_id TEXT NOT NULL, kind TEXT NOT NULL, expires_at TEXT NOT NULL, used_at TEXT, created_at TEXT NOT NULL);
CREATE TABLE releases(id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE, digest TEXT NOT NULL, manifest TEXT NOT NULL, status TEXT NOT NULL, created_at TEXT NOT NULL, activated_at TEXT, UNIQUE(project_id,digest));
CREATE INDEX releases_project_created ON releases(project_id,created_at,id);
CREATE TABLE release_files(release_id TEXT NOT NULL REFERENCES releases(id) ON DELETE CASCADE, project_id TEXT NOT NULL, path TEXT NOT NULL, hash TEXT NOT NULL, size INTEGER NOT NULL, content_type TEXT NOT NULL, public INTEGER NOT NULL, PRIMARY KEY(release_id,path));
CREATE TABLE public_assets(project_id TEXT NOT NULL, hash TEXT NOT NULL, name TEXT NOT NULL, content_type TEXT NOT NULL, size INTEGER NOT NULL, release_id TEXT NOT NULL, PRIMARY KEY(project_id,hash,name));
CREATE TABLE events(id TEXT PRIMARY KEY, project_id TEXT NOT NULL, event_key TEXT NOT NULL, type TEXT NOT NULL, contact_id TEXT, payload TEXT NOT NULL, occurred_at TEXT NOT NULL, created_at TEXT NOT NULL, UNIQUE(project_id,event_key));
CREATE TABLE enrollments(id TEXT PRIMARY KEY, project_id TEXT NOT NULL, contact_id TEXT NOT NULL, release_id TEXT NOT NULL, sequence_id TEXT NOT NULL, event_id TEXT, current_step TEXT NOT NULL, state TEXT NOT NULL, next_at TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, UNIQUE(project_id,sequence_id,contact_id,event_id));
CREATE INDEX enrollments_due ON enrollments(state,next_at);
CREATE TABLE broadcasts(id TEXT PRIMARY KEY, project_id TEXT NOT NULL, release_id TEXT NOT NULL, definition_id TEXT NOT NULL, event_id TEXT, state TEXT NOT NULL, audience_frozen_at TEXT NOT NULL, created_at TEXT NOT NULL, UNIQUE(project_id,definition_id,event_id));
CREATE TABLE jobs(id TEXT PRIMARY KEY, project_id TEXT NOT NULL, kind TEXT NOT NULL, payload TEXT NOT NULL, run_at TEXT NOT NULL, state TEXT NOT NULL, lease_owner TEXT, lease_until TEXT, attempts INTEGER NOT NULL DEFAULT 0, last_error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE INDEX jobs_due ON jobs(state,run_at);
CREATE TABLE deliveries(id TEXT PRIMARY KEY, project_id TEXT NOT NULL, contact_id TEXT NOT NULL, release_id TEXT NOT NULL, enrollment_id TEXT, broadcast_id TEXT, step_id TEXT NOT NULL, message_key TEXT NOT NULL, message_id TEXT NOT NULL UNIQUE, state TEXT NOT NULL, artifact_hash TEXT, detail TEXT NOT NULL DEFAULT '', attempt_id TEXT, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, UNIQUE(enrollment_id,step_id), UNIQUE(broadcast_id,contact_id));
CREATE TABLE outbox(id TEXT PRIMARY KEY, project_id TEXT NOT NULL, type TEXT NOT NULL, payload TEXT NOT NULL, correlation_id TEXT NOT NULL, causation_id TEXT NOT NULL DEFAULT '', state TEXT NOT NULL DEFAULT 'pending', attempts INTEGER NOT NULL DEFAULT 0, next_at TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE registry(id TEXT PRIMARY KEY, project_id TEXT NOT NULL, resource TEXT NOT NULL, name TEXT NOT NULL, config TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, UNIQUE(project_id,resource,name));
CREATE TABLE idempotency(project_id TEXT NOT NULL, resource TEXT NOT NULL, action TEXT NOT NULL, key TEXT NOT NULL, fingerprint TEXT NOT NULL, response TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY(project_id,resource,action,key));
CREATE TABLE audit(id TEXT PRIMARY KEY, project_id TEXT, action TEXT NOT NULL, detail TEXT NOT NULL, created_at TEXT NOT NULL);
`, `CREATE TABLE rate_limits(scope TEXT NOT NULL, bucket TEXT NOT NULL, window_start TEXT NOT NULL, count INTEGER NOT NULL, PRIMARY KEY(scope,bucket,window_start));`,
	`ALTER TABLE lists ADD COLUMN retired INTEGER NOT NULL DEFAULT 0;
CREATE TABLE webhook_deliveries(id TEXT PRIMARY KEY,project_id TEXT NOT NULL,event_id TEXT NOT NULL,endpoint_id TEXT NOT NULL,payload TEXT NOT NULL,state TEXT NOT NULL DEFAULT 'pending',attempts INTEGER NOT NULL DEFAULT 0,next_at TEXT NOT NULL,lease_until TEXT,attempt_id TEXT,last_status INTEGER,last_error TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,UNIQUE(event_id,endpoint_id));
CREATE TABLE provider_feedback(project_id TEXT NOT NULL,event_id TEXT NOT NULL,fingerprint TEXT NOT NULL,kind TEXT NOT NULL,created_at TEXT NOT NULL,PRIMARY KEY(project_id,event_id));`,
	`CREATE TABLE consent_history(id TEXT PRIMARY KEY,project_id TEXT NOT NULL,contact_id TEXT NOT NULL,list_id TEXT NOT NULL,state TEXT NOT NULL,source TEXT NOT NULL,policy_version TEXT NOT NULL,occurred_at TEXT NOT NULL,detail TEXT NOT NULL DEFAULT '{}');CREATE INDEX consent_history_contact ON consent_history(project_id,contact_id,occurred_at);`,
	`CREATE TABLE webhook_attempts(id TEXT PRIMARY KEY,project_id TEXT NOT NULL,delivery_id TEXT NOT NULL,event_id TEXT NOT NULL,status INTEGER,error TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL);CREATE INDEX webhook_attempts_delivery ON webhook_attempts(project_id,delivery_id,created_at);`,
	`ALTER TABLE deliveries ADD COLUMN transport_id TEXT NOT NULL DEFAULT '';`,
	`ALTER TABLE events ADD COLUMN request_fingerprint TEXT NOT NULL DEFAULT '';`,
	`ALTER TABLE enrollments ADD COLUMN entry_key TEXT NOT NULL DEFAULT '';UPDATE enrollments SET entry_key=CASE WHEN event_id IS NULL THEN 'once' ELSE event_id END;CREATE UNIQUE INDEX enrollments_entry_dedupe ON enrollments(project_id,sequence_id,contact_id,entry_key);`,
	`ALTER TABLE enrollments ADD COLUMN completion_emitted INTEGER NOT NULL DEFAULT 0;`,
	`ALTER TABLE broadcasts ADD COLUMN completion_emitted INTEGER NOT NULL DEFAULT 0;`}

func (e *Engine) migrate(ctx context.Context) error {
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	for i, m := range migrations {
		version := i + 1
		var found int
		er := tx.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version=?`, version).Scan(&found)
		if er != nil {
			return er
		}
		if found > 0 {
			continue
		}
		if _, er = tx.ExecContext(ctx, m); er != nil {
			return fmt.Errorf("migration %d: %w", version, er)
		}
		if _, er = tx.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES(?)`, version); er != nil {
			return er
		}
	}
	return tx.Commit()
}

func withTx[T any](ctx context.Context, db *sql.DB, fn func(*sql.Tx) (T, error)) (T, error) {
	var zero T
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return zero, e
	}
	v, e := fn(tx)
	if e != nil {
		tx.Rollback()
		return zero, e
	}
	if e = tx.Commit(); e != nil {
		return zero, e
	}
	return v, nil
}
