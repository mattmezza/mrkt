package engine

import (
	"context"
	"errors"
	"github.com/mattmezza/mrkt/internal/security"
	"time"
)

// RotateEncryptionKey is offline maintenance. The caller must hold the database
// volume lock and must not run HTTP/workers concurrently. No key is logged.
func (e *Engine) RotateEncryptionKey(ctx context.Context, newKey []byte) error {
	next, err := security.NewSecretBox(newKey)
	if err != nil {
		return err
	}
	old, err := e.box()
	if err != nil {
		return err
	}
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT project_id,id,config FROM registry LIMIT 10001`)
	if err != nil {
		return err
	}
	type record struct{ project, id, encoded string }
	records := []record{}
	for rows.Next() {
		var r record
		if err = rows.Scan(&r.project, &r.id, &r.encoded); err != nil {
			rows.Close()
			return err
		}
		records = append(records, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(records) > 10000 {
		return errors.New("offline key rotation exceeds 10000 registry records; partitioned migration required")
	}
	for _, r := range records {
		plain, er := old.Open(r.encoded, r.project+":"+r.id)
		if er != nil {
			return er
		}
		encoded, er := next.Seal(plain, r.project+":"+r.id)
		if er != nil {
			return er
		}
		if _, er = tx.ExecContext(ctx, `UPDATE registry SET config=? WHERE project_id=? AND id=?`, encoded, r.project, r.id); er != nil {
			return er
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO audit(id,action,detail,created_at) VALUES(?,'encryption.rotate','{"offline":true}',?)`, newID("aud"), e.now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	e.cfg.EncryptionKey = append([]byte(nil), newKey...)
	return nil
}

// RotateAdministratorToken replaces installation administrators under the same
// offline volume lock. Project credentials are unaffected.
func (e *Engine) RotateAdministratorToken(ctx context.Context, token string) error {
	if len(token) < 32 {
		return errors.New("new administrator token must contain at least 32 random characters")
	}
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := e.now().UTC().Format(time.RFC3339Nano)
	if _, err = tx.ExecContext(ctx, `UPDATE api_tokens SET revoked_at=? WHERE admin=1 AND revoked_at IS NULL`, now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO api_tokens(id,name,token_hash,scopes,admin,created_at) VALUES(?,'installation administrator',?,'["*"]',1,?)`, newID("tok"), tokenHash(token), now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO audit(id,action,detail,created_at) VALUES(?,'administrator.rotate','{"offline":true}',?)`, newID("aud"), now); err != nil {
		return err
	}
	return tx.Commit()
}
