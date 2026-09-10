package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	netmail "net/mail"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/mattmezza/mrkt/internal/mail"
	"github.com/mattmezza/mrkt/internal/security"
)

type webhookConfig struct {
	URL            string   `json:"url"`
	Secret         string   `json:"secret,omitempty"`
	PreviousSecret string   `json:"previous_secret,omitempty"`
	Events         []string `json:"events"`
	Development    bool     `json:"development,omitempty"`
}
type transportConfig struct {
	Stream         string `json:"stream,omitempty"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Username       string `json:"username"`
	Password       string `json:"password,omitempty"`
	TLSMode        string `json:"tls_mode"`
	From           string `json:"from"`
	Provider       string `json:"provider"`
	SNSTopicARN    string `json:"sns_topic_arn,omitempty"`
	FeedbackSecret string `json:"feedback_secret,omitempty"`
	AllowPrivate   bool   `json:"allow_private"`
	RatePerMinute  int    `json:"rate_per_minute"`
}
type domainConfig struct {
	Domain        string         `json:"domain"`
	Verification  string         `json:"verification"`
	Verified      bool           `json:"verified"`
	DKIMSelectors []string       `json:"dkim_selectors"`
	ReturnPath    string         `json:"return_path,omitempty"`
	Checks        map[string]any `json:"checks,omitempty"`
}

func (e *Engine) box() (*security.SecretBox, error) {
	return security.NewSecretBox(e.cfg.EncryptionKey)
}
func (e *Engine) sealConfig(p, id string, c any) (string, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	b, err := e.box()
	if err != nil {
		return "", err
	}
	return b.Seal(raw, p+":"+id)
}
func (e *Engine) openConfig(p, id, encoded string, dst any) error {
	b, err := e.box()
	if err != nil {
		return err
	}
	raw, err := b.Open(encoded, p+":"+id)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}
func (e *Engine) registryConfig(ctx context.Context, p, resource, id string, dst any) error {
	var raw string
	err := e.db.QueryRowContext(ctx, `SELECT config FROM registry WHERE project_id=? AND resource=? AND id=? AND enabled=1`, p, resource, id).Scan(&raw)
	if err != nil {
		return err
	}
	return e.openConfig(p, id, raw, dst)
}

func (e *Engine) doRegistry(ctx context.Context, a Authority, op Operation) (any, error) {
	if op.Resource == "webhook-deliveries" {
		return e.webhookDeliveries(ctx, a, op)
	}
	if op.Resource == "transports" && op.Action != "list" && op.Action != "get" && !a.Admin {
		return nil, forbidden()
	}
	if op.Action == "check" && op.Resource == "domains" {
		return e.checkDomain(ctx, op)
	}
	if op.Action == "check" && op.Resource == "transports" {
		var c transportConfig
		if err := e.registryConfig(ctx, op.Project, op.Resource, op.ID, &c); err != nil {
			return nil, notFound("transport not found")
		}
		if err := mail.ProbeSMTP(ctx, mail.SMTPConfig{Host: c.Host, Port: c.Port, Username: c.Username, Password: c.Password, TLSMode: c.TLSMode, Development: e.cfg.Development, Timeout: 15 * time.Second}); err != nil {
			return nil, &Error{"smtp_check_failed", "SMTP TLS/authentication/NOOP probe failed; check endpoint and credentials", 503}
		}
		return map[string]any{"submission_ready": true, "mail_sent": false, "inbox_delivery_verified": false, "checked_at": e.now().UTC().Format(time.RFC3339Nano)}, nil
	}
	if op.Action == "rotate" && op.Resource == "webhooks" {
		var c webhookConfig
		if err := e.registryConfig(ctx, op.Project, op.Resource, op.ID, &c); err != nil {
			return nil, notFound("webhook not found")
		}
		c.PreviousSecret = ""
		c.Secret = randomToken()
		raw, err := e.sealConfig(op.Project, op.ID, c)
		if err != nil {
			return nil, internal(err)
		}
		_, err = e.db.ExecContext(ctx, `UPDATE registry SET config=?,updated_at=? WHERE project_id=? AND id=?`, raw, e.now().UTC().Format(time.RFC3339Nano), op.Project, op.ID)
		if err != nil {
			return nil, internal(err)
		}
		return map[string]any{"id": op.ID, "secret": c.Secret, "previous_key_retained": false, "rotation": "new attempts use the new key immediately; consumers should retain the old key for their verification overlap"}, nil
	}
	if op.Action == "create" {
		var in struct {
			Name   string          `json:"name"`
			Config json.RawMessage `json:"config"`
		}
		if err := decode(op.Input, &in); err != nil {
			return nil, err
		}
		if in.Name == "" || len(in.Name) > 120 {
			return nil, bad("invalid_input", "name required, maximum 120 characters")
		}
		id := newID("reg")
		var conf any
		var reveal string
		switch op.Resource {
		case "webhooks":
			var c webhookConfig
			if err := decode(in.Config, &c); err != nil {
				return nil, err
			}
			if c.Development && (!a.Admin || !e.cfg.Development) {
				return nil, forbidden()
			}
			u, err := url.Parse(c.URL)
			if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Scheme != "https" && !(e.cfg.Development && c.Development && u.Scheme == "http") {
				return nil, bad("invalid_url", "HTTPS webhook URL required")
			}
			devAllowed := e.cfg.Development && c.Development && a.Admin && slices.Contains(e.cfg.WebhookDevelopmentAllowedHosts, u.Host)
			if ip := net.ParseIP(u.Hostname()); !devAllowed && ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified()) {
				return nil, bad("ssrf_rejected", "public webhook destination required")
			}
			if c.Secret == "" {
				c.Secret = randomToken()
			}
			if len(c.Secret) < 32 || len(c.Secret) > 256 {
				return nil, bad("invalid_secret", "webhook secret requires 32–256 characters")
			}
			if len(c.Events) == 0 {
				c.Events = []string{"*"}
			}
			c.PreviousSecret = ""
			conf = c
			reveal = c.Secret
		case "transports":
			var c transportConfig
			if err := decode(in.Config, &c); err != nil {
				return nil, err
			}
			if len(c.Stream) > 80 || strings.ContainsAny(c.Stream, " /\\\r\n\t") {
				return nil, bad("invalid_stream", "stream must be a short identifier")
			}
			if c.Provider == "" {
				c.Provider = "generic"
			}
			if !slices.Contains([]string{"generic", "ses"}, c.Provider) {
				return nil, bad("invalid_provider", "provider must be generic or ses")
			}
			addr, err := netmail.ParseAddress(c.From)
			if err != nil || strings.ContainsAny(c.From, "\r\n") {
				return nil, bad("invalid_sender", "valid from address required")
			}
			domain := strings.Split(addr.Address, "@")[1]
			if !e.cfg.Development {
				verified, err := e.verifiedDomain(ctx, op.Project, domain)
				if err != nil {
					return nil, internal(err)
				}
				if !verified {
					return nil, bad("unverified_domain", "verify the sender domain before provisioning transport")
				}
			}
			if !c.AllowPrivate {
				ips, err := net.DefaultResolver.LookupIP(ctx, "ip", c.Host)
				if err != nil {
					return nil, bad("smtp_dns", "SMTP hostname could not be resolved")
				}
				for _, ip := range ips {
					if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
						return nil, bad("private_smtp", "administrator must explicitly set allow_private")
					}
				}
			}
			if c.RatePerMinute == 0 {
				c.RatePerMinute = 60
			}
			if c.RatePerMinute < 1 || c.RatePerMinute > 10000 {
				return nil, bad("invalid_rate", "rate_per_minute must be 1–10000")
			}
			if _, err := mail.NewSMTP(mail.SMTPConfig{Host: c.Host, Port: c.Port, Username: c.Username, Password: c.Password, TLSMode: c.TLSMode, Development: e.cfg.Development}); err != nil {
				return nil, bad("invalid_transport", err.Error())
			}
			if c.Provider == "ses" && !strings.HasPrefix(c.SNSTopicARN, "arn:aws:sns:") {
				return nil, bad("feedback_required", "SES requires the independently provisioned SNS topic ARN")
			}
			if c.FeedbackSecret == "" {
				c.FeedbackSecret = randomToken()
			}
			reveal = c.FeedbackSecret
			conf = c
		case "domains":
			var inDomain struct {
				Domain        string   `json:"domain"`
				DKIMSelectors []string `json:"dkim_selectors"`
				ReturnPath    string   `json:"return_path"`
			}
			if err := decode(in.Config, &inDomain); err != nil {
				return nil, err
			}
			domain := strings.ToLower(strings.TrimSpace(inDomain.Domain))
			if strings.ContainsAny(domain, "/:@ \r\n") || !strings.Contains(domain, ".") || len(domain) > 253 {
				return nil, bad("invalid_domain", "DNS domain required")
			}
			conf = domainConfig{Domain: domain, Verification: "mrkt-verification=" + randomToken(), DKIMSelectors: inDomain.DKIMSelectors, ReturnPath: inDomain.ReturnPath}
		default:
			return nil, notFound("unknown registry resource")
		}
		raw, err := e.sealConfig(op.Project, id, conf)
		if err != nil {
			return nil, internal(err)
		}
		now := e.now().UTC().Format(time.RFC3339Nano)
		_, err = e.db.ExecContext(ctx, `INSERT INTO registry(id,project_id,resource,name,config,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, id, op.Project, op.Resource, in.Name, raw, now, now)
		if err != nil {
			if isConstraint(err) {
				return nil, conflict("already_exists", "name already exists")
			}
			return nil, internal(err)
		}
		result := map[string]any{"id": id, "name": in.Name}
		if reveal != "" {
			result["secret"] = reveal
		}
		if op.Resource == "domains" {
			c := conf.(domainConfig)
			result["verification_record"] = map[string]string{"type": "TXT", "name": "_mrkt." + c.Domain, "value": c.Verification}
		}
		return result, nil
	}
	if op.Action == "delete" {
		r, err := e.db.ExecContext(ctx, `UPDATE registry SET enabled=0,updated_at=? WHERE project_id=? AND resource=? AND id=?`, e.now().UTC().Format(time.RFC3339Nano), op.Project, op.Resource, op.ID)
		if err != nil {
			return nil, internal(err)
		}
		return affected(r, "record not found")
	}
	if op.Action != "list" && op.Action != "get" {
		return nil, notFound("unknown registry action")
	}
	q := `SELECT id,name,config,enabled,created_at FROM registry WHERE project_id=? AND resource=?`
	args := []any{op.Project, op.Resource}
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
		var id, name, raw, created string
		var enabled int
		if err = rs.Scan(&id, &name, &raw, &enabled, &created); err != nil {
			return nil, internal(err)
		}
		c := map[string]any{}
		if err = e.openConfig(op.Project, id, raw, &c); err != nil {
			return nil, internal(err)
		}
		for _, key := range []string{"secret", "previous_secret", "password", "feedback_secret"} {
			if _, ok := c[key]; ok {
				delete(c, key)
				c[key+"_configured"] = true
			}
		}
		item := map[string]any{"id": id, "name": name, "config": c, "enabled": enabled == 1, "created_at": created}
		if op.Resource == "transports" {
			item["delivery_semantics"] = "accepted by next SMTP server"
			item["feedback"] = "authenticated MTA callback required; generic SMTP does not supply complaints automatically"
		}
		items = append(items, item)
	}
	if op.Action == "get" {
		if len(items) == 0 {
			return nil, notFound("record not found")
		}
		return items[0], nil
	}
	return paged(items, op.Limit), nil
}
func (e *Engine) verifiedDomain(ctx context.Context, p, domain string) (bool, error) {
	rs, err := e.db.QueryContext(ctx, `SELECT id,config FROM registry WHERE project_id=? AND resource='domains' AND enabled=1`, p)
	if err != nil {
		return false, err
	}
	defer rs.Close()
	for rs.Next() {
		var id, raw string
		if err = rs.Scan(&id, &raw); err != nil {
			return false, err
		}
		var c domainConfig
		if err = e.openConfig(p, id, raw, &c); err != nil {
			return false, err
		}
		if c.Domain == domain && c.Verified {
			return true, nil
		}
	}
	return false, rs.Err()
}
func (e *Engine) checkDomain(ctx context.Context, op Operation) (any, error) {
	var c domainConfig
	if err := e.registryConfig(ctx, op.Project, "domains", op.ID, &c); err != nil {
		return nil, notFound("domain not found")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	lookup := func(name string) []string {
		txt, err := net.DefaultResolver.LookupTXT(ctx, name)
		if err != nil {
			return nil
		}
		return txt
	}
	txt := lookup("_mrkt." + c.Domain)
	c.Verified = slices.Contains(txt, c.Verification)
	spf := lookup(c.Domain)
	dmarc := lookup("_dmarc." + c.Domain)
	spfRecords := []string{}
	for _, v := range spf {
		if strings.HasPrefix(v, "v=spf1 ") {
			spfRecords = append(spfRecords, v)
		}
	}
	dkim := map[string]any{}
	for _, selector := range c.DKIMSelectors {
		if strings.ContainsAny(selector, "/ @\r\n") {
			continue
		}
		name := selector + "._domainkey." + c.Domain
		txt := lookup(name)
		cn, _ := net.DefaultResolver.LookupCNAME(ctx, name)
		dkim[selector] = map[string]any{"txt": txt, "cname": cn}
	}
	c.Checks = map[string]any{"ownership_verified": c.Verified, "spf_records": spfRecords, "spf_single_record": len(spfRecords) == 1, "dmarc_records": dmarc, "dkim": dkim, "checked_at": e.now().UTC().Format(time.RFC3339Nano), "guidance": "SPF must authorize your actual provider/MTA; DKIM records come from that signing system. DMARC alignment and DNS presence do not guarantee inbox placement."}
	raw, err := e.sealConfig(op.Project, op.ID, c)
	if err != nil {
		return nil, internal(err)
	}
	_, err = e.db.ExecContext(ctx, `UPDATE registry SET config=?,updated_at=? WHERE project_id=? AND id=?`, raw, e.now().UTC().Format(time.RFC3339Nano), op.Project, op.ID)
	if err != nil {
		return nil, internal(err)
	}
	return c.Checks, nil
}
func (e *Engine) senderForProject(ctx context.Context, p string) (mail.Sender, string, error) {
	var id, raw string
	err := e.db.QueryRowContext(ctx, `SELECT id,config FROM registry WHERE project_id=? AND resource='transports' AND enabled=1 ORDER BY created_at DESC LIMIT 1`, p).Scan(&id, &raw)
	if err == sql.ErrNoRows {
		return e.cfg.Sender, e.cfg.From, nil
	}
	if err != nil {
		return nil, "", err
	}
	var c transportConfig
	if err = e.openConfig(p, id, raw, &c); err != nil {
		return nil, "", err
	}
	sender, err := mail.NewSMTP(mail.SMTPConfig{Host: c.Host, Port: c.Port, Username: c.Username, Password: c.Password, TLSMode: c.TLSMode, Development: e.cfg.Development, Timeout: 30 * time.Second, MaxEncodedSize: 12 << 20})
	return sender, c.From, err
}

var _ = fmt.Sprintf
