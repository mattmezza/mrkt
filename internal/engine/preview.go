package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"sort"

	"github.com/mattmezza/mrkt/internal/manifest"
)

// doPreview reads only project-owned immutable sources; never creates jobs or tokens.
func (e *Engine) doPreview(ctx context.Context, op Operation) (any, error) {
	if e.cfg.Artifacts == nil {
		return nil, &Error{"artifacts_unavailable", "artifact storage unavailable", 503}
	}
	if op.Resource == "deliveries" {
		var hash, mid string
		err := e.db.QueryRowContext(ctx, `SELECT COALESCE(artifact_hash,''),message_id FROM deliveries WHERE project_id=? AND id=?`, op.Project, op.ID).Scan(&hash, &mid)
		if err == sql.ErrNoRows || hash == "" {
			return nil, notFound("frozen delivery not found")
		}
		if err != nil {
			return nil, internal(err)
		}
		rc, err := e.cfg.Artifacts.Get(ctx, op.Project, hash)
		if err != nil {
			return nil, internal(err)
		}
		defer rc.Close()
		b, err := io.ReadAll(io.LimitReader(rc, 3*manifest.MaxRenderSize+1))
		if err != nil || len(b) > 3*manifest.MaxRenderSize {
			return nil, bad("invalid_artifact", "rendered artifact exceeds limit")
		}
		var rendered manifest.Rendered
		if err = json.Unmarshal(b, &rendered); err != nil {
			return nil, internal(err)
		}
		return map[string]any{"subject": rendered.Subject, "html": rendered.HTML, "text": rendered.Text, "message_id": mid, "frozen": true}, nil
	}
	var in struct {
		Message   string         `json:"message"`
		Locale    string         `json:"locale"`
		Variables map[string]any `json:"variables"`
	}
	if err := decode(op.Input, &in); err != nil {
		return nil, err
	}
	m, err := manifestForRelease(ctx, e.db, op.Project, op.ID)
	if err == sql.ErrNoRows {
		return nil, notFound("release not found")
	}
	if err != nil {
		return nil, internal(err)
	}
	if in.Message == "" {
		keys := []string{}
		for key := range m.Messages {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if len(keys) > 0 {
			in.Message = keys[0]
		}
	}
	variants, ok := m.Messages[in.Message]
	if !ok {
		return nil, notFound("message not found")
	}
	available := []string{}
	for locale := range variants {
		available = append(available, locale)
	}
	locale := manifest.ResolveLocale(in.Locale, m.Project.DefaultLocale, available)
	content := variants[locale]
	sources := map[string][]byte{}
	for _, name := range []string{content.Subject, content.HTML, content.Text} {
		for _, f := range m.Files {
			if f.Path != name {
				continue
			}
			rc, er := e.cfg.Artifacts.Get(ctx, op.Project, f.SHA256)
			if er != nil {
				return nil, internal(er)
			}
			b, er := io.ReadAll(io.LimitReader(rc, manifest.MaxFileSize+1))
			rc.Close()
			if er != nil || int64(len(b)) != f.Size {
				return nil, bad("invalid_artifact", "artifact size mismatch")
			}
			sources[name] = b
		}
	}
	vars := map[string]any{"Name": "Synthetic reader", "Email": "reader@example.test", "Locale": locale, "Timezone": "UTC", "Attributes": map[string]any{}, "Event": map[string]any{}, "UnsubscribeURL": "https://example.test/unsubscribe", "ConfirmationURL": "https://example.test/confirm"}
	for k, v := range in.Variables {
		vars[k] = v
	}
	vars["Locale"] = locale
	rendered, err := manifest.Render(content, sources, vars)
	if err != nil {
		return nil, bad("render_failed", err.Error())
	}
	attachments := []manifest.File{}
	for _, name := range content.Attachments {
		for _, f := range m.Files {
			if f.Path == name {
				attachments = append(attachments, f)
			}
		}
	}
	return map[string]any{"subject": rendered.Subject, "html": rendered.HTML, "text": rendered.Text, "locale": locale, "requested_locale": in.Locale, "fallback": in.Locale != "" && locale != in.Locale, "message": in.Message, "attachments": attachments, "external_sends": false}, nil
}
