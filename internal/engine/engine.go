package engine

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mattmezza/mrkt/internal/artifact"
	"github.com/mattmezza/mrkt/internal/mail"
	_ "modernc.org/sqlite"
)

type Config struct {
	DBPath                         string
	Initialize                     bool
	Recovery                       bool
	AdminToken                     string
	PublicURL                      string
	From                           string
	Development                    bool
	Artifacts                      artifact.Store
	Sender                         mail.Sender
	EncryptionKey                  []byte
	WebhookDevelopmentAllowedHosts []string
	Now                            func() time.Time
}

type Authority struct {
	Project string   `json:"project,omitempty"`
	Admin   bool     `json:"admin"`
	Public  bool     `json:"-"`
	Scopes  []string `json:"scopes,omitempty"`
}
type Operation struct {
	Project  string
	Resource string
	Action   string
	ID       string
	Key      string
	Input    json.RawMessage
	Limit    int
	Cursor   string
	Filters  map[string]string
}
type Error struct {
	Code, Message string
	Status        int
}

func (e *Error) Error() string { return e.Message }

type Engine struct {
	db     *sql.DB
	cfg    Config
	now    func() time.Time
	tickMu sync.Mutex
}

func Open(cfg Config) (*Engine, error) {
	if cfg.DBPath == "" {
		return nil, fmt.Errorf("engine: DBPath is required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if _, markerErr := os.Stat(cfg.DBPath + ".recovery-required"); markerErr == nil {
		cfg.Recovery = true
	} else if !errors.Is(markerErr, os.ErrNotExist) {
		return nil, fmt.Errorf("engine: recovery marker: %w", markerErr)
	}
	_, statErr := os.Stat(cfg.DBPath)
	isNew := errors.Is(statErr, os.ErrNotExist)
	if errors.Is(statErr, os.ErrNotExist) && !cfg.Initialize {
		return nil, fmt.Errorf("engine: database does not exist; explicit initialization required")
	}
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("engine: stat database: %w", statErr)
	}
	if isNew && cfg.Initialize && cfg.AdminToken == "" {
		return nil, fmt.Errorf("engine: AdminToken required for initialization")
	}
	if cfg.Initialize {
		if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0700); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	e := &Engine{db: db, cfg: cfg, now: cfg.Now}
	if err = e.configure(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if err = e.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if cfg.Initialize {
		var initialized int
		if err = db.QueryRow(`SELECT count(*) FROM installation`).Scan(&initialized); err != nil {
			db.Close()
			return nil, err
		}
		if initialized == 0 {
			if cfg.AdminToken == "" {
				db.Close()
				return nil, fmt.Errorf("engine: AdminToken required for initialization")
			}
			now := cfg.Now().UTC().Format(time.RFC3339Nano)
			tx, txErr := db.Begin()
			if txErr != nil {
				db.Close()
				return nil, txErr
			}
			_, err = tx.Exec(`INSERT INTO installation(id,recovery,outbound_paused,created_at,updated_at) VALUES(1,?,?,?,?)`, boolInt(cfg.Recovery), boolInt(cfg.Recovery), now, now)
			if err == nil {
				_, err = tx.Exec(`INSERT INTO api_tokens(id,project_id,name,token_hash,scopes,admin,created_at) VALUES(?,?,?,?,?,?,?)`, newID("tok"), nil, "initial admin", tokenHash(cfg.AdminToken), `["*"]`, 1, now)
			}
			if err == nil {
				err = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
			if err != nil {
				db.Close()
				return nil, err
			}
		}
	}
	var installed int
	if err = db.QueryRow(`SELECT count(*) FROM installation`).Scan(&installed); err != nil || installed != 1 {
		db.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("engine: database is not an initialized installation")
	}
	if cfg.Recovery {
		_, err = db.Exec(`UPDATE installation SET recovery=1,outbound_paused=1,updated_at=? WHERE id=1`, cfg.Now().UTC().Format(time.RFC3339Nano))
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return e, nil
}

func (e *Engine) configure(ctx context.Context) error {
	for _, q := range []string{`PRAGMA journal_mode=WAL`, `PRAGMA foreign_keys=ON`, `PRAGMA busy_timeout=5000`, `PRAGMA synchronous=NORMAL`} {
		if _, err := e.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("engine: sqlite configuration: %w", err)
		}
	}
	return nil
}
func (e *Engine) Close() error { return e.db.Close() }

func (e *Engine) Authenticate(ctx context.Context, token string) (Authority, error) {
	if token == "" {
		return Authority{}, unauthorized("missing API token")
	}
	want := tokenHash(token)
	var p, s string
	var admin int
	err := e.db.QueryRowContext(ctx, `SELECT COALESCE(project_id,''),scopes,admin FROM api_tokens WHERE token_hash=? AND revoked_at IS NULL`, want).Scan(&p, &s, &admin)
	if err == sql.ErrNoRows {
		return Authority{}, unauthorized("invalid API token")
	}
	if err != nil {
		return Authority{}, internal(err)
	}
	var scopes []string
	_ = json.Unmarshal([]byte(s), &scopes)
	return Authority{Project: p, Admin: admin == 1, Scopes: scopes}, nil
}

func (e *Engine) Do(ctx context.Context, a Authority, op Operation) (any, error) {
	if op.Key == "" || op.Action == "list" || op.Action == "get" || op.Action == "preview" || op.Action == "plan" || op.Action == "explain" || op.Action == "simulate" {
		return e.do(ctx, a, op)
	}
	project := op.Project
	if project == "" {
		project = a.Project
	}
	if er := e.authorizeOperation(a, op, project); er != nil {
		return nil, er
	}
	if (op.Resource == "tokens" || op.Resource == "webhooks" || op.Resource == "transports") && (op.Action == "create" || op.Action == "rotate") {
		return nil, bad("idempotency_unsupported", "secret creation does not support replayable idempotency keys")
	}
	var canonical any
	if len(op.Input) > 0 && json.Unmarshal(op.Input, &canonical) == nil {
		op.Input, _ = json.Marshal(canonical)
	}
	sum := sha256.Sum256(append([]byte(project+"\x00"+op.Resource+"\x00"+op.Action+"\x00"+op.ID+"\x00"), op.Input...))
	fingerprint := hex.EncodeToString(sum[:])
	var storedFP, response string
	er := e.db.QueryRowContext(ctx, `SELECT fingerprint,response FROM idempotency WHERE project_id=? AND resource=? AND action=? AND key=?`, project, op.Resource, op.Action, op.Key).Scan(&storedFP, &response)
	if er == nil {
		if storedFP != fingerprint {
			return nil, conflict("idempotency_conflict", "idempotency key was used with different input")
		}
		if response == "__pending__" {
			return nil, conflict("idempotency_in_progress", "idempotent operation is in progress or its outcome requires reconciliation")
		}
		var v any
		if json.Unmarshal([]byte(response), &v) != nil {
			return nil, internal(fmt.Errorf("invalid stored idempotency response"))
		}
		return v, nil
	}
	if er != sql.ErrNoRows {
		return nil, internal(er)
	}
	_, insertErr := e.db.ExecContext(ctx, `INSERT INTO idempotency(project_id,resource,action,key,fingerprint,response,created_at) VALUES(?,?,?,?,?,'__pending__',?)`, project, op.Resource, op.Action, op.Key, fingerprint, e.now().UTC().Format(time.RFC3339Nano))
	if isConstraint(insertErr) {
		return nil, conflict("idempotency_in_progress", "idempotency key was claimed concurrently")
	}
	if insertErr != nil {
		return nil, internal(insertErr)
	}
	v, er := e.do(ctx, a, op)
	if er != nil {
		var ee *Error
		if errors.As(er, &ee) && ee.Status < 500 {
			_, _ = e.db.ExecContext(ctx, `DELETE FROM idempotency WHERE project_id=? AND resource=? AND action=? AND key=? AND response='__pending__'`, project, op.Resource, op.Action, op.Key)
		}
		return nil, er
	}
	raw, _ := json.Marshal(v)
	if _, er = e.db.ExecContext(ctx, `UPDATE idempotency SET response=? WHERE project_id=? AND resource=? AND action=? AND key=? AND fingerprint=? AND response='__pending__'`, string(raw), project, op.Resource, op.Action, op.Key, fingerprint); er != nil {
		return nil, internal(er)
	}
	return v, nil
}

func (e *Engine) authorizeOperation(a Authority, op Operation, project string) error {
	if a.Public {
		if project != "" && (a.Project == "" || a.Project == project) && ((op.Resource == "consent" && (op.Action == "subscribe" || op.Action == "confirm" || op.Action == "unsubscribe" || op.Action == "preferences")) || (op.Resource == "feedback" && op.Action == "ingest")) {
			return nil
		}
		return forbidden()
	}
	if op.Resource == "installation" {
		if !a.Admin {
			return forbidden()
		}
		return nil
	}
	if op.Resource == "projects" {
		if a.Admin {
			return nil
		}
		if project == a.Project && (op.Action == "get" || op.Action == "list") && hasAnyScope(a.Scopes, "read", "config", "operate", "send") {
			return nil
		}
		return forbidden()
	}
	if project == "" {
		return bad("project_required", "project is required")
	}
	if !a.Admin && (a.Project != project || !hasScope(a.Scopes, requiredScope(op))) {
		return forbidden()
	}
	return nil
}

func (e *Engine) do(ctx context.Context, a Authority, op Operation) (any, error) {
	if op.Limit < 0 || op.Limit > 200 {
		return nil, bad("invalid_limit", "limit must be between 0 and 200")
	}
	if op.Limit == 0 {
		op.Limit = 50
	}
	if op.Project == "" {
		op.Project = a.Project
	}
	if a.Public {
		if op.Project == "" {
			return nil, forbidden()
		}
		if op.Resource == "consent" && (op.Action == "subscribe" || op.Action == "confirm" || op.Action == "unsubscribe" || op.Action == "preferences") {
			return e.doConsent(ctx, a, op)
		}
		if op.Resource == "assets" && op.Action == "get" {
			return e.doAsset(ctx, op)
		}
		if op.Resource == "feedback" && op.Action == "ingest" {
			return e.doFeedback(ctx, op)
		}
		return nil, forbidden()
	}
	if op.Resource == "installation" {
		if !a.Admin {
			return nil, forbidden()
		}
		return e.doInstallation(ctx, a, op)
	}
	if op.Resource == "projects" {
		return e.doProjects(ctx, a, op)
	}
	if op.Project == "" {
		return nil, bad("project_required", "project is required")
	}
	if !a.Admin && a.Project != op.Project {
		return nil, forbidden()
	}
	if !a.Admin && !hasScope(a.Scopes, requiredScope(op)) {
		return nil, forbidden()
	}
	switch op.Resource {
	case "tokens":
		return e.doTokens(ctx, a, op)
	case "contacts":
		return e.doContacts(ctx, a, op)
	case "lists":
		return e.doLists(ctx, a, op)
	case "consent":
		return e.doConsent(ctx, a, op)
	case "releases":
		if op.Action == "preview" {
			return e.doPreview(ctx, op)
		}
		if op.Action == "simulate" {
			return e.doSimulation(ctx, op)
		}
		return e.doReleases(ctx, a, op)
	case "events":
		if op.Action == "get" {
			return e.inspectEvent(ctx, op)
		}
		return e.doEvents(ctx, a, op)
	case "sequences":
		return e.inspectSequences(ctx, op)
	case "enrollments":
		if op.Action == "simulate" {
			return e.doSimulation(ctx, op)
		}
		return e.doEnrollments(ctx, a, op)
	case "broadcasts":
		return e.doBroadcasts(ctx, a, op)
	case "deliveries":
		if op.Action == "preview" {
			return e.doPreview(ctx, op)
		}
		return e.doDeliveries(ctx, a, op)
	case "operations":
		return e.doRuntimeOperations(ctx, a, op)
	case "webhooks", "webhook-deliveries", "domains", "transports":
		return e.doRegistry(ctx, a, op)
	case "feedback":
		return e.doFeedback(ctx, op)
	default:
		return nil, notFound("unknown resource")
	}
}

func hasScope(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == "*" || s == want {
			return true
		}
	}
	return false
}
func requiredScope(op Operation) string {
	if op.Action == "list" || op.Action == "get" || op.Action == "explain" || op.Action == "preview" || op.Action == "simulate" {
		return "read"
	}
	if op.Resource == "tokens" || op.Resource == "releases" || op.Resource == "domains" || op.Resource == "transports" || op.Resource == "webhooks" {
		return "config"
	}
	if op.Resource == "deliveries" || op.Resource == "broadcasts" || op.Action == "resume" {
		return "send"
	}
	return "operate"
}
func tokenHash(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func newID(prefix string) string {
	b := make([]byte, 18)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return prefix + "_" + base64.RawURLEncoding.EncodeToString(b)
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func bad(c, m string) *Error       { return &Error{c, m, 400} }
func unauthorized(m string) *Error { return &Error{"unauthorized", m, 401} }
func forbidden() *Error            { return &Error{"forbidden", "insufficient authority", 403} }
func notFound(m string) *Error     { return &Error{"not_found", m, 404} }
func conflict(c, m string) *Error  { return &Error{c, m, 409} }
func internal(e error) *Error      { return &Error{"internal", e.Error(), 500} }
func decode(input json.RawMessage, dst any) error {
	d := json.NewDecoder(strings.NewReader(string(input)))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return bad("invalid_input", err.Error())
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return bad("invalid_input", "exactly one JSON value required")
	}
	return nil
}
func scanJSON[T any](raw string) T { var v T; _ = json.Unmarshal([]byte(raw), &v); return v }
func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}
