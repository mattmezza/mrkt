package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mattmezza/mrkt/internal/artifact"
	"github.com/mattmezza/mrkt/internal/cli"
	"github.com/mattmezza/mrkt/internal/engine"
	"github.com/mattmezza/mrkt/internal/httpapi"
	"github.com/mattmezza/mrkt/internal/mail"
	"github.com/mattmezza/mrkt/internal/security"
)

var version = "0.1.0-dev"
var commit = "unknown"
var built = "unknown"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "mrkt:", err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "version" {
		fmt.Printf("mrkt %s commit=%s built=%s\n", version, commit, built)
		return nil
	}
	if len(args) > 0 && args[0] == "server" {
		return serve(ctx, args[1:])
	}
	if len(args) == 2 && args[0] == "doctor" && args[1] == "--local" {
		return localDoctor(ctx)
	}
	return cli.Run(ctx, args, os.Stdin, os.Stdout, os.Stderr)
}
func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
func flagEnv(k string) bool { return os.Getenv(k) == "true" }
func secret(k string) (string, error) {
	if p := os.Getenv(k + "_FILE"); p != "" {
		f, e := os.Open(p)
		if e != nil {
			return "", e
		}
		defer f.Close()
		b, e := io.ReadAll(io.LimitReader(f, 4097))
		if e != nil {
			return "", e
		}
		if len(b) > 4096 {
			return "", fmt.Errorf("%s secret file too large", k)
		}
		return strings.TrimSpace(string(b)), nil
	}
	return os.Getenv(k), nil
}
func serve(ctx context.Context, args []string) error {
	initialize := len(args) == 1 && args[0] == "init"
	rotate := len(args) == 1 && args[0] == "rotate-key"
	rotateAdmin := len(args) == 1 && args[0] == "rotate-admin"
	if len(args) > 0 && !initialize && !rotate && !rotateAdmin {
		return errors.New("usage: mrkt server [init|rotate-key|rotate-admin]")
	}
	development := flagEnv("MRKT_DEVELOPMENT")
	publicURL := env("MRKT_PUBLIC_URL", "http://localhost:8080")
	u, err := url.Parse(publicURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("valid MRKT_PUBLIC_URL required")
	}
	if !development && u.Scheme != "https" {
		return errors.New("production MRKT_PUBLIC_URL must use HTTPS")
	}
	keyText, err := secret("MRKT_ENCRYPTION_KEY")
	if err != nil {
		return err
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(keyText, "base64:"))
	if err != nil || len(key) != 32 {
		return errors.New("MRKT_ENCRYPTION_KEY must contain base64-encoded 32 random bytes")
	}
	admin, err := secret("MRKT_ADMIN_TOKEN")
	if err != nil {
		return err
	}
	if initialize && len(admin) < 32 {
		return errors.New("initialization requires MRKT_ADMIN_TOKEN with at least 32 characters (or _FILE)")
	}
	artifacts, err := artifact.NewS3(ctx, artifact.S3Config{Region: env("MRKT_S3_REGION", "us-east-1"), Bucket: os.Getenv("MRKT_S3_BUCKET"), Prefix: env("MRKT_S3_PREFIX", "mrkt/production"), Endpoint: os.Getenv("MRKT_S3_ENDPOINT"), PathStyle: flagEnv("MRKT_S3_PATH_STYLE"), MaxObjectSize: 8 << 20})
	if err != nil {
		return err
	}
	pass, err := secret("MRKT_SMTP_PASSWORD")
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(env("MRKT_SMTP_PORT", "587"))
	if err != nil {
		return errors.New("invalid MRKT_SMTP_PORT")
	}
	sender, err := mail.NewSMTP(mail.SMTPConfig{Host: os.Getenv("MRKT_SMTP_HOST"), Port: port, Username: os.Getenv("MRKT_SMTP_USERNAME"), Password: pass, TLSMode: env("MRKT_SMTP_TLS_MODE", "starttls"), Development: development, MaxEncodedSize: 12 << 20, Timeout: 30 * time.Second})
	if err != nil {
		return err
	}
	cfg := engine.Config{DBPath: env("MRKT_DB_PATH", "/data/mrkt.db"), Initialize: initialize, Recovery: flagEnv("MRKT_RECOVERY"), AdminToken: admin, PublicURL: strings.TrimRight(publicURL, "/"), Development: development, Artifacts: artifacts, Sender: sender, EncryptionKey: key}
	cfg.From = os.Getenv("MRKT_FROM")
	if development {
		cfg.WebhookDevelopmentAllowedHosts = strings.Split(os.Getenv("MRKT_WEBHOOK_DEVELOPMENT_ALLOWED_HOSTS"), ",")
	}
	if cfg.From == "" && development {
		cfg.From = "mrkt <no-reply@example.test>"
	}
	if cfg.From == "" {
		return errors.New("MRKT_FROM verified sender address is required")
	}
	if initialize {
		if err = os.MkdirAll(filepath.Dir(cfg.DBPath), 0700); err != nil {
			return err
		}
	}
	lock, err := os.OpenFile(cfg.DBPath+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("another mrkt process owns this database volume")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	app, err := engine.Open(cfg)
	if err != nil {
		return err
	}
	defer app.Close()
	if rotateAdmin {
		token, err := secret("MRKT_NEW_ADMIN_TOKEN")
		if err != nil {
			return err
		}
		if err = app.RotateAdministratorToken(ctx, token); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, "Installation administrator credential rotated; old administrator sessions are revoked.")
		return nil
	}
	if rotate {
		value, err := secret("MRKT_NEW_ENCRYPTION_KEY")
		if err != nil {
			return err
		}
		newKey, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "base64:"))
		if err != nil || len(newKey) != 32 {
			return errors.New("MRKT_NEW_ENCRYPTION_KEY[_FILE] requires base64-encoded 32 random bytes")
		}
		if err = app.RotateEncryptionKey(ctx, newKey); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, "Registry secrets re-encrypted. Update the installation key before restarting; retain old keys for retained backups.")
		return nil
	}
	if initialize {
		fmt.Fprintln(os.Stdout, "Installation initialized. Keep the admin token and encryption key in your secret manager.")
		return nil
	}
	var challenge httpapi.Challenge
	if development && flagEnv("MRKT_CHALLENGE_BYPASS") {
		challenge = security.DevelopmentBypass{Enabled: true}
	} else {
		turnstile, err := secret("MRKT_TURNSTILE_SECRET")
		if err != nil {
			return err
		}
		challenge = &security.Turnstile{Secret: turnstile, Hostname: os.Getenv("MRKT_TURNSTILE_HOSTNAME"), Action: env("MRKT_TURNSTILE_ACTION", "subscribe")}
	}
	proxies := []net.IPNet{}
	for _, raw := range strings.Split(os.Getenv("MRKT_TRUSTED_PROXIES"), ",") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		_, n, e := net.ParseCIDR(strings.TrimSpace(raw))
		if e != nil {
			return errors.New("invalid MRKT_TRUSTED_PROXIES CIDR")
		}
		proxies = append(proxies, *n)
	}
	sessionKey := sha256.Sum256(append([]byte("mrkt.session.v1:"), key...))
	handler := httpapi.New(httpapi.Config{App: app, Artifacts: artifacts, Development: development, PublicURL: publicURL, SessionKey: sessionKey[:], Challenge: challenge, TrustedProxies: proxies})
	srv := &http.Server{Addr: env("MRKT_LISTEN", ":8080"), Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 16 << 10}
	workerCtx, stopWorker := context.WithCancel(ctx)
	defer stopWorker()
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				if err := app.Tick(workerCtx); err != nil && !errors.Is(err, context.Canceled) {
					log.Printf("worker tick failed: %v", err)
				}
			}
		}
	}()
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe() }()
	log.Printf("mrkt %s listening on %s", version, srv.Addr)
	select {
	case err := <-done:
		stopWorker()
		<-workerDone
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	err = srv.Shutdown(shutdown)
	stopWorker()
	select {
	case <-workerDone:
	case <-shutdown.Done():
		return shutdown.Err()
	}
	return err
}
func localDoctor(ctx context.Context) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://127.0.0.1:8080/healthz", nil)
	c := http.Client{Timeout: 3 * time.Second}
	r, e := c.Do(req)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return fmt.Errorf("health HTTP %d", r.StatusCode)
	}
	return nil
}
