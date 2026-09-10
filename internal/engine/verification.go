package engine

import (
	"context"
	"os"
)

func (e *Engine) verifyInstallation(ctx context.Context, op Operation) (any, error) {
	var integrity string
	if err := e.db.QueryRowContext(ctx, `PRAGMA quick_check(1)`).Scan(&integrity); err != nil {
		return nil, internal(err)
	}
	foreign, err := e.db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return nil, internal(err)
	}
	violations := 0
	for foreign.Next() {
		violations++
		if violations >= 1000 {
			break
		}
	}
	err = foreign.Err()
	foreign.Close()
	if err != nil {
		return nil, internal(err)
	}
	rows, err := e.db.QueryContext(ctx, `SELECT project_id,hash FROM (SELECT project_id,hash FROM release_files UNION SELECT project_id,digest AS hash FROM releases UNION SELECT project_id,artifact_hash AS hash FROM deliveries WHERE artifact_hash IS NOT NULL) WHERE project_id||'/'||hash>? ORDER BY project_id,hash LIMIT 201`, op.Cursor)
	if err != nil {
		return nil, internal(err)
	}
	type ref struct{ project, hash string }
	refs := []ref{}
	for rows.Next() {
		var r ref
		if err = rows.Scan(&r.project, &r.hash); err != nil {
			rows.Close()
			return nil, internal(err)
		}
		refs = append(refs, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, internal(err)
	}
	next := ""
	if len(refs) > 200 {
		refs = refs[:200]
		last := refs[len(refs)-1]
		next = last.project + "/" + last.hash
	}
	missing := []map[string]string{}
	for _, r := range refs {
		if e.cfg.Artifacts == nil {
			missing = append(missing, map[string]string{"project": r.project, "hash": r.hash, "error": "artifact backend unavailable"})
			continue
		}
		if _, er := e.cfg.Artifacts.Stat(ctx, r.project, r.hash); er != nil {
			missing = append(missing, map[string]string{"project": r.project, "hash": r.hash, "error": "object inaccessible"})
		}
	}
	var uncertain, queued int
	if err = e.db.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE state IN ('uncertain','dispatching')`).Scan(&uncertain); err != nil {
		return nil, internal(err)
	}
	if err = e.db.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE state='pending'`).Scan(&queued); err != nil {
		return nil, internal(err)
	}
	sizes := map[string]int64{}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if info, er := os.Stat(e.cfg.DBPath + suffix); er == nil {
			sizes["database"+suffix] = info.Size()
		}
	}
	return map[string]any{"integrity": integrity, "foreign_key_violations": violations, "checked_artifacts": len(refs), "missing_artifacts": missing, "next_cursor": next, "artifact_scan_complete": next == "", "uncertain_or_dispatching_jobs": uncertain, "pending_jobs": queued, "local_bytes": sizes, "safe_to_resume": false, "reconciliation_required": true, "backup_health": "inspect independent Litestream metrics; local integrity does not establish backup freshness"}, nil
}
