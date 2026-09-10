package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
)

func (e *Engine) inspectSequences(ctx context.Context, op Operation) (any, error) {
	if op.Action != "list" && op.Action != "get" {
		return nil, notFound("sequence definitions are repository-owned; inspect enrollments for runtime operations")
	}
	m, rid, err := e.activeManifest(ctx, op.Project)
	if err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for _, seq := range m.Sequences {
		if op.Action == "get" && seq.ID != op.ID {
			continue
		}
		if op.Action == "list" && seq.ID <= op.Cursor {
			continue
		}
		b, _ := json.Marshal(seq)
		var item map[string]any
		_ = json.Unmarshal(b, &item)
		item["release_id"] = rid
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i]["id"].(string) < items[j]["id"].(string) })
	if op.Action == "get" {
		if len(items) == 0 {
			return nil, notFound("sequence not found")
		}
		return items[0], nil
	}
	return paged(items, op.Limit), nil
}

func (e *Engine) inspectEvent(ctx context.Context, op Operation) (any, error) {
	var id, key, typ, contact, payload, occurred, created string
	err := e.db.QueryRowContext(ctx, `SELECT id,event_key,type,COALESCE(contact_id,''),payload,occurred_at,created_at FROM events WHERE project_id=? AND id=?`, op.Project, op.ID).Scan(&id, &key, &typ, &contact, &payload, &occurred, &created)
	if err == sql.ErrNoRows {
		return nil, notFound("event not found")
	}
	if err != nil {
		return nil, internal(err)
	}
	return map[string]any{"id": id, "key": key, "type": typ, "contact_id": contact, "payload": scanJSON[any](payload), "occurred_at": occurred, "created_at": created}, nil
}
