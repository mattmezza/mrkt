package engine

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/mattmezza/mrkt/internal/security"
)

// feedbackInput keeps exact received bytes intact for signature verification.
type feedbackInput struct {
	Body      []byte `json:"body"`
	Timestamp int64  `json:"timestamp"`
	Signature string `json:"signature"`
}
type normalizedFeedback struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	MessageID  string `json:"message_id"`
	Email      string `json:"email"`
	OccurredAt string `json:"occurred_at"`
}

func (e *Engine) doFeedback(ctx context.Context, op Operation) (any, error) {
	var request feedbackInput
	if err := decode(op.Input, &request); err != nil {
		return nil, err
	}
	if len(request.Body) > 1<<20 {
		return nil, bad("invalid_feedback", "body exceeds 1 MiB")
	}
	var transport transportConfig
	if err := e.registryConfig(ctx, op.Project, "transports", op.ID, &transport); err != nil {
		return nil, notFound("transport not found")
	}
	events := []normalizedFeedback{}
	if transport.Provider == "ses" {
		verified, err := (security.SNSVerifier{TopicARN: transport.SNSTopicARN, Now: e.now, MaxAge: 7 * 24 * time.Hour}).Verify(ctx, request.Body)
		if err != nil {
			return nil, unauthorized("invalid SNS notification")
		}
		var ses struct {
			NotificationType string `json:"notificationType"`
			EventType        string `json:"eventType"`
			Mail             struct {
				MessageID     string `json:"messageId"`
				CommonHeaders struct {
					MessageID string `json:"messageId"`
				} `json:"commonHeaders"`
				Headers []struct{ Name, Value string } `json:"headers"`
			} `json:"mail"`
			Bounce struct {
				BounceType string `json:"bounceType"`
				Timestamp  string `json:"timestamp"`
				Recipients []struct {
					Email string `json:"emailAddress"`
				} `json:"bouncedRecipients"`
			} `json:"bounce"`
			Complaint struct {
				Timestamp  string `json:"timestamp"`
				Recipients []struct {
					Email string `json:"emailAddress"`
				} `json:"complainedRecipients"`
			} `json:"complaint"`
			Delivery struct {
				Timestamp  string   `json:"timestamp"`
				Recipients []string `json:"recipients"`
			} `json:"delivery"`
		}
		if json.Unmarshal([]byte(verified.Message), &ses) != nil {
			return nil, bad("invalid_feedback", "invalid SES message")
		}
		typ := ses.NotificationType
		if typ == "" {
			typ = ses.EventType
		}
		messageID := strings.Trim(ses.Mail.CommonHeaders.MessageID, "<>")
		for _, h := range ses.Mail.Headers {
			if strings.EqualFold(h.Name, "Message-ID") {
				messageID = strings.Trim(h.Value, "<>")
			}
		}
		// SES provider IDs differ from our RFC Message-ID. Correlate the preserved original header only.
		switch typ {
		case "Bounce":
			if ses.Bounce.BounceType != "Permanent" {
				return map[string]any{"accepted": true, "ignored": "transient bounce; provider handles retry"}, nil
			}
			for _, v := range ses.Bounce.Recipients {
				events = append(events, normalizedFeedback{verified.MessageID + ":" + v.Email, "bounce", messageID, v.Email, ses.Bounce.Timestamp})
			}
		case "Complaint":
			for _, v := range ses.Complaint.Recipients {
				events = append(events, normalizedFeedback{verified.MessageID + ":" + v.Email, "complaint", messageID, v.Email, ses.Complaint.Timestamp})
			}
		case "Delivery":
			for _, email := range ses.Delivery.Recipients {
				events = append(events, normalizedFeedback{verified.MessageID + ":" + email, "delivered", messageID, email, ses.Delivery.Timestamp})
			}
		default:
			return nil, bad("unsupported_feedback", "unsupported SES event type")
		}
	} else {
		if !security.VerifyWebhook([]byte(transport.FeedbackSecret), request.Body, request.Timestamp, strings.TrimPrefix(request.Signature, "v1="), e.now(), 10*time.Minute) {
			return nil, unauthorized("invalid MTA feedback signature")
		}
		var event normalizedFeedback
		if err := decode(json.RawMessage(request.Body), &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if len(events) == 0 || len(events) > 100 {
		return nil, bad("invalid_feedback", "requires 1–100 recipients")
	}
	result, err := withTx(ctx, e.db, func(tx *sql.Tx) (any, error) {
		applied := 0
		newComplaint, newBounce := false, false
		for _, event := range events {
			if event.ID == "" || len(event.ID) > 300 || event.MessageID == "" {
				return nil, bad("invalid_feedback", "event ID and original Message-ID required")
			}
			if event.Type != "bounce" && event.Type != "complaint" && event.Type != "delivered" {
				return nil, bad("invalid_feedback", "type must be bounce, complaint, delivered")
			}
			if _, err := time.Parse(time.RFC3339Nano, event.OccurredAt); err != nil {
				return nil, bad("invalid_feedback", "valid occurred_at required")
			}
			email, err := normalizeEmail(event.Email)
			if err != nil {
				return nil, err
			}
			var did, cid string
			err = tx.QueryRowContext(ctx, `SELECT d.id,d.contact_id FROM deliveries d JOIN contacts c ON c.id=d.contact_id AND c.project_id=d.project_id WHERE d.project_id=? AND d.message_id=? AND c.email=?`, op.Project, strings.Trim(event.MessageID, "<>"), email).Scan(&did, &cid)
			if err == sql.ErrNoRows {
				return nil, bad("unknown_message", "no matching message in this project")
			}
			if err != nil {
				return nil, err
			}
			raw, _ := json.Marshal(event)
			sum := sha256.Sum256(raw)
			fp := hex.EncodeToString(sum[:])
			var old string
			err = tx.QueryRowContext(ctx, `SELECT fingerprint FROM provider_feedback WHERE project_id=? AND event_id=?`, op.Project, event.ID).Scan(&old)
			if err == nil {
				if old != fp {
					return nil, conflict("idempotency_conflict", "feedback ID reused with different data")
				}
				continue
			}
			if err != sql.ErrNoRows {
				return nil, err
			}
			now := e.now().UTC().Format(time.RFC3339Nano)
			if _, err = tx.ExecContext(ctx, `INSERT INTO provider_feedback(project_id,event_id,fingerprint,kind,created_at) VALUES(?,?,?,?,?)`, op.Project, event.ID, fp, event.Type, now); err != nil {
				return nil, err
			}
			if event.Type == "bounce" || event.Type == "complaint" {
				_, err = tx.ExecContext(ctx, `INSERT INTO suppressions(project_id,email,reason,created_at) VALUES(?,?,?,?) ON CONFLICT(project_id,email) DO UPDATE SET reason=CASE WHEN reason='complaint' THEN reason ELSE excluded.reason END`, op.Project, email, event.Type, now)
				if err != nil {
					return nil, err
				}
				_, err = tx.ExecContext(ctx, `UPDATE enrollments SET state='cancelled',updated_at=? WHERE project_id=? AND contact_id=? AND state IN ('active','waiting','paused')`, now, op.Project, cid)
				if err != nil {
					return nil, err
				}
			}
			_, err = tx.ExecContext(ctx, `UPDATE deliveries SET state=CASE WHEN state='complaint' THEN state WHEN state='bounce' AND ?='delivered' THEN state ELSE ? END,updated_at=? WHERE project_id=? AND id=?`, event.Type, event.Type, now, op.Project, did)
			if err != nil {
				return nil, err
			}
			if err = insertOutbox(ctx, tx, op.Project, "delivery."+event.Type, map[string]any{"delivery_id": did, "contact_id": cid, "occurred_at": event.OccurredAt}, event.MessageID, now); err != nil {
				return nil, err
			}
			applied++
			newComplaint = newComplaint || event.Type == "complaint"
			newBounce = newBounce || event.Type == "bounce"
		}
		var sent, bounces, complaints int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM deliveries WHERE project_id=?`, op.Project).Scan(&sent); err != nil {
			return nil, err
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM suppressions WHERE project_id=? AND reason='bounce'`, op.Project).Scan(&bounces); err != nil {
			return nil, err
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM suppressions WHERE project_id=? AND reason='complaint'`, op.Project).Scan(&complaints); err != nil {
			return nil, err
		}
		if newComplaint || newBounce && sent >= 20 && float64(bounces)/float64(sent) >= .05 {
			now := e.now().UTC().Format(time.RFC3339Nano)
			reason := "automatic pause: complaint received or bounce rate at least 5% after 20 attempts"
			r, err := tx.ExecContext(ctx, `UPDATE projects SET paused=1,pause_reason=?,updated_at=? WHERE id=? AND paused=0`, reason, now, op.Project)
			if err != nil {
				return nil, err
			}
			n, err := r.RowsAffected()
			if err != nil {
				return nil, err
			}
			if n == 0 {
				return map[string]any{"accepted": true, "applied": applied}, nil
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO audit(id,project_id,action,detail,created_at) VALUES(?,?,'feedback.pause',?,?)`, newID("aud"), op.Project, reason, now); err != nil {
				return nil, err
			}
			if err := insertOutbox(ctx, tx, op.Project, "operations.paused", map[string]string{"reason": reason}, op.ID, now); err != nil {
				return nil, err
			}
		}
		return map[string]any{"accepted": true, "applied": applied}, nil
	})
	if err != nil {
		return nil, asEngineError(err)
	}
	return result, nil
}
