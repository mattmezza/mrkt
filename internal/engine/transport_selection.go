package engine

import (
	"context"
	"database/sql"
	"errors"
	"github.com/mattmezza/mrkt/internal/mail"
	"time"
)

// Selected IDs are persisted with delivery intent: retry never switches provider.
func (e *Engine) selectedTransport(ctx context.Context, p, stream, pinnedID string) (string, transportConfig, mail.Sender, error) {
	installation := func() (string, transportConfig, mail.Sender, error) {
		return "__installation__", transportConfig{From: e.cfg.From, RatePerMinute: 60, Stream: stream}, e.cfg.Sender, nil
	}
	if pinnedID == "__installation__" {
		return installation()
	}
	var c transportConfig
	id := pinnedID
	if id != "" {
		if err := e.registryConfig(ctx, p, "transports", id, &c); err != nil {
			return "", c, nil, errors.New("pinned transport unavailable; explicit reconciliation required")
		}
	} else {
		rows, err := e.db.QueryContext(ctx, `SELECT id,config FROM registry WHERE project_id=? AND resource='transports' AND enabled=1 ORDER BY created_at DESC LIMIT 101`, p)
		if err != nil {
			return "", c, nil, err
		}
		for rows.Next() {
			var candidate, encoded string
			if err = rows.Scan(&candidate, &encoded); err != nil {
				rows.Close()
				return "", c, nil, err
			}
			var conf transportConfig
			if err = e.openConfig(p, candidate, encoded, &conf); err != nil {
				rows.Close()
				return "", c, nil, err
			}
			if conf.Stream == stream {
				c = conf
				id = candidate
				break
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", c, nil, err
		}
		if id == "" {
			if stream != "" {
				return "", c, nil, sql.ErrNoRows
			}
			return installation()
		}
	}
	sender, err := mail.NewSMTP(mail.SMTPConfig{Host: c.Host, Port: c.Port, Username: c.Username, Password: c.Password, TLSMode: c.TLSMode, Development: e.cfg.Development, Timeout: 30 * time.Second, MaxEncodedSize: 12 << 20})
	return id, c, sender, err
}
