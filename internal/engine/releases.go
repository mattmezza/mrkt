package engine

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mattmezza/mrkt/internal/manifest"
)

type deployInput struct {
	Manifest         manifest.Manifest `json:"manifest"`
	ExpectedRelease  string            `json:"expected_release"`
	AllowDestructive bool              `json:"allow_destructive"`
}

func (e *Engine) doReleases(ctx context.Context, a Authority, op Operation) (any, error) {
	switch op.Action {
	case "plan", "deploy":
		var in deployInput
		if er := decode(op.Input, &in); er != nil {
			return nil, er
		}
		if er := manifest.Validate(in.Manifest); er != nil {
			return nil, bad("invalid_manifest", er.Error())
		}
		digest := manifest.Digest(in.Manifest)
		current, er := e.activeRelease(ctx, op.Project)
		if er != nil {
			return nil, er
		}
		destructive, changes := e.releaseChanges(ctx, op.Project, in.Manifest)
		if destructive && !in.AllowDestructive && op.Action == "deploy" {
			return nil, conflict("destructive_change", "release removes live list definitions; set allow_destructive")
		}
		plan := map[string]any{"digest": digest, "current_release": current, "expected_release": in.ExpectedRelease, "destructive": destructive, "changes": changes}
		if op.Action == "plan" {
			return plan, nil
		}
		var activeDigest string
		_ = e.db.QueryRowContext(ctx, `SELECT r.digest FROM projects p JOIN releases r ON r.id=p.active_release_id WHERE p.id=?`, op.Project).Scan(&activeDigest)
		if activeDigest == digest {
			return map[string]any{"id": current, "digest": digest, "active": true, "unchanged": true}, nil
		}
		if current != in.ExpectedRelease {
			return nil, conflict("stale_release", "active release changed")
		}
		if e.cfg.Artifacts == nil {
			return nil, &Error{"artifacts_unavailable", "artifact store is not configured", 503}
		}
		if er := e.validateReleaseRuntimeRefs(ctx, op.Project, in.Manifest); er != nil {
			return nil, er
		}
		for _, f := range in.Manifest.Files {
			info, er := e.cfg.Artifacts.Stat(ctx, op.Project, f.SHA256)
			if er != nil {
				return nil, bad("missing_artifact", fmt.Sprintf("%s: %v", f.Path, er))
			}
			if info.Size != f.Size || info.ContentType != f.ContentType {
				return nil, bad("artifact_mismatch", "artifact metadata mismatch for "+f.Path)
			}
		}
		raw, _ := json.Marshal(in.Manifest)
		if er := e.cfg.Artifacts.Put(ctx, op.Project, digest, "application/vnd.mrkt.manifest+json", int64(len(raw)), bytes.NewReader(raw)); er != nil {
			return nil, &Error{"artifact_upload_failed", er.Error(), 503}
		}
		rid := newID("rel")
		now := e.now().UTC().Format(time.RFC3339Nano)
		result, er := withTx(ctx, e.db, func(tx *sql.Tx) (any, error) {
			var active sql.NullString
			if er := tx.QueryRowContext(ctx, `SELECT active_release_id FROM projects WHERE id=?`, op.Project).Scan(&active); er == sql.ErrNoRows {
				return nil, notFound("project not found")
			}
			if er != nil {
				return nil, er
			}
			if active.String != in.ExpectedRelease {
				return nil, conflict("stale_release", "active release changed")
			}
			var existing string
			er := tx.QueryRowContext(ctx, `SELECT id FROM releases WHERE project_id=? AND digest=?`, op.Project, digest).Scan(&existing)
			if er == nil {
				rid = existing
			} else if er != sql.ErrNoRows {
				return nil, er
			} else {
				if _, er = tx.ExecContext(ctx, `INSERT INTO releases(id,project_id,digest,manifest,status,created_at,activated_at) VALUES(?,?,?,?,?,?,?)`, rid, op.Project, digest, string(raw), "active", now, now); er != nil {
					return nil, er
				}
				for _, f := range in.Manifest.Files {
					if _, er = tx.ExecContext(ctx, `INSERT INTO release_files(release_id,project_id,path,hash,size,content_type,public) VALUES(?,?,?,?,?,?,?)`, rid, op.Project, f.Path, f.SHA256, f.Size, f.ContentType, boolInt(f.Public)); er != nil {
						return nil, er
					}
				}
			}
			if _, er = tx.ExecContext(ctx, `UPDATE releases SET status='inactive' WHERE project_id=? AND id<>?`, op.Project, rid); er != nil {
				return nil, er
			}
			if _, er = tx.ExecContext(ctx, `UPDATE releases SET status='active',activated_at=? WHERE id=?`, now, rid); er != nil {
				return nil, er
			}
			if _, er = tx.ExecContext(ctx, `UPDATE projects SET name=?,active_release_id=?,updated_at=? WHERE id=?`, in.Manifest.Project.Name, rid, now, op.Project); er != nil {
				return nil, er
			}
			if _, er = tx.ExecContext(ctx, `UPDATE lists SET retired=1 WHERE project_id=?`, op.Project); er != nil {
				return nil, er
			}
			for _, l := range in.Manifest.Lists {
				var oldPolicy, oldPurpose string
				oldErr := tx.QueryRowContext(ctx, `SELECT policy_version,purpose FROM lists WHERE project_id=? AND id=?`, op.Project, l.ID).Scan(&oldPolicy, &oldPurpose)
				if oldErr == nil && (oldPolicy != l.PolicyVersion || oldPurpose != l.Purpose) {
					if !in.AllowDestructive {
						return nil, conflict("reconsent_required", "changed list purpose or policy requires allow_destructive and fresh consent")
					}
					if _, qe := tx.ExecContext(ctx, `DELETE FROM consent_tokens WHERE project_id=? AND list_id=? AND kind='confirm' AND used_at IS NULL`, op.Project, l.ID); qe != nil {
						return nil, qe
					}
					rows, qe := tx.QueryContext(ctx, `SELECT contact_id FROM consent WHERE project_id=? AND list_id=? AND state='confirmed'`, op.Project, l.ID)
					if qe != nil {
						return nil, qe
					}
					var contacts []string
					for rows.Next() {
						var c string
						if qe = rows.Scan(&c); qe != nil {
							rows.Close()
							return nil, qe
						}
						contacts = append(contacts, c)
					}
					rows.Close()
					for _, c := range contacts {
						if _, qe = tx.ExecContext(ctx, `UPDATE consent SET state='pending',policy_version=?,confirmed_at=NULL,updated_at=? WHERE project_id=? AND contact_id=? AND list_id=?`, l.PolicyVersion, now, op.Project, c, l.ID); qe != nil {
							return nil, qe
						}
						if _, qe = tx.ExecContext(ctx, `INSERT INTO consent_history(id,project_id,contact_id,list_id,state,source,policy_version,occurred_at,detail) VALUES(?,?,?,?,?,?,?,?,?)`, newID("cnh"), op.Project, c, l.ID, "pending", "release_policy_change", l.PolicyVersion, now, `{"reconsent_required":true}`); qe != nil {
							return nil, qe
						}
					}
				} else if oldErr != nil && oldErr != sql.ErrNoRows {
					return nil, oldErr
				}
				if _, er = tx.ExecContext(ctx, `INSERT INTO lists(project_id,id,name,purpose,policy_version,release_id,retired) VALUES(?,?,?,?,?,?,0) ON CONFLICT(project_id,id) DO UPDATE SET name=excluded.name,purpose=excluded.purpose,policy_version=excluded.policy_version,release_id=excluded.release_id,retired=0`, op.Project, l.ID, l.Name, l.Purpose, l.PolicyVersion, rid); er != nil {
					return nil, er
				}
			}
			for _, f := range in.Manifest.Files {
				if f.Public {
					if _, er = tx.ExecContext(ctx, `INSERT OR IGNORE INTO public_assets(project_id,hash,name,content_type,size,release_id) VALUES(?,?,?,?,?,?)`, op.Project, f.SHA256, f.Path, f.ContentType, f.Size, rid); er != nil {
						return nil, er
					}
				}
			}
			return map[string]any{"id": rid, "digest": digest, "active": true}, nil
		})
		if er != nil {
			return nil, asEngineError(er)
		}
		return result, nil
	case "rollback":
		var in struct {
			ReleaseID       string `json:"release_id"`
			ExpectedRelease string `json:"expected_release"`
		}
		if er := decode(op.Input, &in); er != nil {
			return nil, er
		}
		now := e.now().UTC().Format(time.RFC3339Nano)
		r, er := e.db.ExecContext(ctx, `UPDATE projects SET active_release_id=?,updated_at=? WHERE id=? AND COALESCE(active_release_id,'')=? AND EXISTS(SELECT 1 FROM releases WHERE id=? AND project_id=?)`, in.ReleaseID, now, op.Project, in.ExpectedRelease, in.ReleaseID, op.Project)
		if er != nil {
			return nil, internal(er)
		}
		n, _ := r.RowsAffected()
		if n == 0 {
			return nil, conflict("stale_or_invalid_release", "release precondition failed")
		}
		_, _ = e.db.ExecContext(ctx, `UPDATE releases SET status=CASE WHEN id=? THEN 'active' ELSE 'inactive' END WHERE project_id=?`, in.ReleaseID, op.Project)
		return map[string]any{"id": in.ReleaseID, "active": true}, nil
	case "list":
		rows, er := e.db.QueryContext(ctx, `SELECT id,digest,status,created_at,activated_at FROM releases WHERE project_id=? AND id>? ORDER BY id LIMIT ?`, op.Project, op.Cursor, op.Limit+1)
		if er != nil {
			return nil, internal(er)
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var id, d, s, c string
			var at sql.NullString
			if er = rows.Scan(&id, &d, &s, &c, &at); er != nil {
				return nil, internal(er)
			}
			items = append(items, map[string]any{"id": id, "digest": d, "status": s, "created_at": c, "activated_at": at.String})
		}
		return paged(items, op.Limit), nil
	case "get":
		var id, d, s, c, raw string
		var at sql.NullString
		er := e.db.QueryRowContext(ctx, `SELECT id,digest,status,created_at,activated_at,manifest FROM releases WHERE project_id=? AND id=?`, op.Project, op.ID).Scan(&id, &d, &s, &c, &at, &raw)
		if er == sql.ErrNoRows {
			return nil, notFound("release not found")
		}
		if er != nil {
			return nil, internal(er)
		}
		return map[string]any{"id": id, "digest": d, "status": s, "created_at": c, "activated_at": at.String, "manifest": scanJSON[any](raw)}, nil
	default:
		return nil, notFound("unknown releases action")
	}
}
func (e *Engine) validateReleaseRuntimeRefs(ctx context.Context, p string, m manifest.Manifest) error {
	if m.Project.Transport != "" {
		var n int
		_ = e.db.QueryRowContext(ctx, `SELECT count(*) FROM registry WHERE id=? AND project_id=? AND resource='transports' AND enabled=1`, m.Project.Transport, p).Scan(&n)
		if n != 1 {
			return bad("invalid_transport_ref", "project transport is absent, disabled, or belongs to another project")
		}
	}
	if m.Project.Domain != "" {
		var c domainConfig
		if er := e.registryConfig(ctx, p, "domains", m.Project.Domain, &c); er != nil {
			return bad("invalid_domain_ref", "project domain is absent, disabled, or belongs to another project")
		}
		if !e.cfg.Development && !c.Verified {
			return bad("unverified_domain", "project domain ownership is not verified")
		}
	}
	streams := map[string]bool{}
	for _, s := range m.Sequences {
		if s.Stream != "" {
			streams[s.Stream] = true
		}
	}
	for _, b := range m.Broadcasts {
		if b.Stream != "" {
			streams[b.Stream] = true
		}
	}
	for stream := range streams {
		rows, er := e.db.QueryContext(ctx, `SELECT id,config FROM registry WHERE project_id=? AND resource='transports' AND enabled=1`, p)
		if er != nil {
			return internal(er)
		}
		found := false
		for rows.Next() {
			var id, raw string
			if er = rows.Scan(&id, &raw); er != nil {
				rows.Close()
				return internal(er)
			}
			var c transportConfig
			if e.openConfig(p, id, raw, &c) == nil && c.Stream == stream {
				found = true
			}
		}
		rows.Close()
		if !found {
			return bad("invalid_stream", "no enabled transport configured for stream "+stream)
		}
	}
	return nil
}
func (e *Engine) activeRelease(ctx context.Context, p string) (string, error) {
	var v sql.NullString
	er := e.db.QueryRowContext(ctx, `SELECT active_release_id FROM projects WHERE id=?`, p).Scan(&v)
	if er == sql.ErrNoRows {
		return "", notFound("project not found")
	}
	if er != nil {
		return "", internal(er)
	}
	return v.String, nil
}
func (e *Engine) releaseChanges(ctx context.Context, p string, m manifest.Manifest) (bool, []string) {
	type oldList struct{ policy, purpose string }
	old := map[string]oldList{}
	rows, er := e.db.QueryContext(ctx, `SELECT id,policy_version,purpose FROM lists WHERE project_id=? AND retired=0`, p)
	if er == nil {
		defer rows.Close()
		for rows.Next() {
			var id string
			var policy, purpose string
			_ = rows.Scan(&id, &policy, &purpose)
			old[id] = oldList{policy, purpose}
		}
	}
	changes := []string{}
	for _, l := range m.Lists {
		v, exists := old[l.ID]
		if !exists {
			changes = append(changes, "add list "+l.ID)
		} else if v.policy != l.PolicyVersion || v.purpose != l.Purpose {
			changes = append(changes, "require reconsent for list "+l.ID)
		}
		delete(old, l.ID)
	}
	destructive := len(old) > 0
	for _, c := range changes {
		if len(c) >= 17 && c[:17] == "require reconsent" {
			destructive = true
		}
	}
	for id := range old {
		changes = append(changes, "remove list "+id)
	}
	return destructive, changes
}
func asEngineError(e error) error {
	var ee *Error
	if errors.As(e, &ee) {
		return ee
	}
	return internal(e)
}
