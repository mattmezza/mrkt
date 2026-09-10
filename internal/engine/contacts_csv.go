package engine

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"time"
)

const maxCSVBytes = 10 << 20
const importBatchSize = 1000

var csvLocale = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)

type csvInput struct {
	CSV          string `json:"csv"`
	List         string `json:"list"`
	Source       string `json:"source"`
	ConsentState string `json:"consent_state"`
}
type csvRow struct {
	Email, Name, ExternalID, Locale, Timezone string
	Attributes                                map[string]any
}

func (e *Engine) doContactsCSV(ctx context.Context, op Operation) (any, error) {
	if op.Action == "export" {
		return e.exportContacts(ctx, op)
	}
	var in csvInput
	if er := decode(op.Input, &in); er != nil {
		return nil, er
	}
	rows, problems, er := parseContactsCSV(in.CSV)
	if er != nil {
		return nil, er
	}
	preview := map[string]any{"rows": len(rows) + len(problems), "valid": len(rows), "invalid": len(problems), "problems": problems}
	if op.Action == "preview-import" {
		return preview, nil
	}
	if in.ConsentState != "" && in.ConsentState != "pending" {
		return nil, bad("consent_mapping", "imports may only create pending consent; confirmation requires double opt-in")
	}
	if len(problems) > 0 {
		return nil, bad("invalid_csv", "fix preview errors before import")
	}
	now := e.now().UTC().Format(time.RFC3339Nano)
	policy := ""
	if in.ConsentState == "pending" {
		if in.List == "" {
			return nil, bad("consent_mapping", "list is required for pending consent")
		}
		if er = e.db.QueryRowContext(ctx, `SELECT policy_version FROM lists WHERE project_id=? AND id=? AND retired=0`, op.Project, in.List).Scan(&policy); er != nil {
			return nil, bad("consent_mapping", "active list not found")
		}
	}
	imported := 0
	for start := 0; start < len(rows); start += importBatchSize {
		end := start + importBatchSize
		if end > len(rows) {
			end = len(rows)
		}
		tx, er := e.db.BeginTx(ctx, nil)
		if er != nil {
			return nil, internal(er)
		}
		for _, r := range rows[start:end] {
			attrs, _ := json.Marshal(r.Attributes)
			id := newID("con")
			_, er = tx.ExecContext(ctx, `INSERT INTO contacts(id,project_id,external_id,email,name,locale,timezone,attributes,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(project_id,email) DO UPDATE SET name=excluded.name,locale=excluded.locale,timezone=excluded.timezone,attributes=excluded.attributes,updated_at=excluded.updated_at`, id, op.Project, nullable(r.ExternalID), r.Email, r.Name, r.Locale, r.Timezone, string(attrs), now, now)
			if er != nil {
				return nil, internal(er)
			}
			if in.ConsentState == "pending" {
				if er = tx.QueryRowContext(ctx, `SELECT id FROM contacts WHERE project_id=? AND email=?`, op.Project, r.Email).Scan(&id); er != nil {
					return nil, internal(er)
				}
				_, er = tx.ExecContext(ctx, `INSERT INTO consent(id,project_id,contact_id,list_id,state,source,policy_version,requested_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(project_id,contact_id,list_id) DO NOTHING`, newID("cns"), op.Project, id, in.List, "pending", in.Source, policy, now, now)
				if er != nil {
					return nil, internal(er)
				}
			}
			imported++
		}
		if er = tx.Commit(); er != nil {
			return nil, internal(er)
		}
	}
	preview["imported"] = imported
	preview["confirmed_consent_granted"] = false
	return preview, nil
}
func parseContactsCSV(raw string) ([]csvRow, []map[string]any, error) {
	if len(raw) > maxCSVBytes {
		return nil, nil, bad("csv_too_large", "CSV exceeds 10 MiB")
	}
	r := csv.NewReader(strings.NewReader(raw))
	r.FieldsPerRecord = -1
	header, er := r.Read()
	if er != nil {
		return nil, nil, bad("invalid_csv", er.Error())
	}
	allowed := map[string]bool{"email": true, "name": true, "external_id": true, "locale": true, "timezone": true, "attributes": true}
	idx := map[string]int{}
	for i, h := range header {
		h = strings.TrimSpace(h)
		if !allowed[h] || idx[h] > 0 || h == "" {
			return nil, nil, bad("invalid_csv", "unknown or duplicate header: "+h)
		}
		idx[h] = i + 1
	}
	if idx["email"] == 0 {
		return nil, nil, bad("invalid_csv", "email header required")
	}
	out := []csvRow{}
	problems := []map[string]any{}
	for line := 2; line <= 100002; line++ {
		rec, er := r.Read()
		if er == io.EOF {
			break
		}
		if er != nil {
			return nil, nil, bad("invalid_csv", er.Error())
		}
		get := func(k string) string {
			i := idx[k] - 1
			if i < 0 || i >= len(rec) {
				return ""
			}
			return strings.TrimSpace(rec[i])
		}
		email, emailErr := normalizeEmail(get("email"))
		if emailErr != nil {
			problems = append(problems, map[string]any{"line": line, "field": "email", "message": "invalid email"})
			continue
		}
		attrs := map[string]any{}
		if v := get("attributes"); v != "" {
			if json.Unmarshal([]byte(v), &attrs) != nil {
				problems = append(problems, map[string]any{"line": line, "field": "attributes", "message": "JSON object required"})
				continue
			}
		}
		locale := get("locale")
		if locale != "" && !csvLocale.MatchString(locale) {
			problems = append(problems, map[string]any{"line": line, "field": "locale", "message": "invalid locale"})
			continue
		}
		tz := get("timezone")
		if tz != "" {
			if _, err := time.LoadLocation(tz); err != nil {
				problems = append(problems, map[string]any{"line": line, "field": "timezone", "message": "invalid IANA timezone"})
				continue
			}
		}
		out = append(out, csvRow{email, get("name"), get("external_id"), locale, tz, attrs})
	}
	if len(out)+len(problems) > 100000 {
		return nil, nil, bad("csv_too_large", "CSV exceeds 100000 records")
	}
	return out, problems, nil
}
func (e *Engine) exportContacts(ctx context.Context, op Operation) (any, error) {
	rows, er := e.db.QueryContext(ctx, `SELECT id,email,name,COALESCE(external_id,''),locale,timezone,attributes FROM contacts WHERE project_id=? AND deleted_at IS NULL AND id>? ORDER BY id LIMIT ?`, op.Project, op.Cursor, op.Limit+1)
	if er != nil {
		return nil, internal(er)
	}
	defer rows.Close()
	var b strings.Builder
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"email", "name", "external_id", "locale", "timezone", "attributes"})
	n := 0
	next := ""
	for rows.Next() {
		var id string
		var vals [6]string
		if er = rows.Scan(&id, &vals[0], &vals[1], &vals[2], &vals[3], &vals[4], &vals[5]); er != nil {
			return nil, internal(er)
		}
		if n == op.Limit {
			next = id
			break
		}
		for i := range vals {
			vals[i] = safeCSVCell(vals[i])
		}
		_ = w.Write(vals[:])
		n++
	}
	w.Flush()
	return map[string]any{"csv": b.String(), "rows": n, "columns": []string{"email", "name", "external_id", "locale", "timezone", "attributes"}, "next_cursor": next, "generated_at": e.now().UTC().Format(time.RFC3339Nano)}, nil
}
func safeCSVCell(v string) string {
	trimmed := strings.TrimLeft(v, " \t\r")
	prefix := v[:len(v)-len(trimmed)]
	if strings.ContainsAny(prefix, "\t\r") || (trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0]))) {
		return "'" + v
	}
	return v
}
