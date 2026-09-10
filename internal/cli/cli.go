package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mattmezza/mrkt/internal/client"
	"github.com/mattmezza/mrkt/internal/manifest"
	"github.com/mattmezza/mrkt/internal/mcpserver"
	"github.com/mattmezza/mrkt/presets"
	"go.yaml.in/yaml/v3"
)

func Run(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) error {
	if len(args) == 0 {
		usage(errOut)
		return errors.New("command required")
	}
	switch args[0] {
	case "init":
		return runInit(args[1:], out)
	case "sequence":
		return runSequence(args[1:], out)
	case "validate", "preview", "plan", "deploy":
		return runManifest(ctx, args[0], args[1:], out)
	case "mcp":
		if len(args) == 2 && args[1] == "serve" {
			c, err := apiClient()
			if err != nil {
				return err
			}
			return mcpserver.Serve(ctx, c)
		}
		return errors.New("usage: mrkt mcp serve")
	case "api":
		return runAPI(ctx, args[1:], in, out)
	case "projects", "tokens", "releases", "events", "contacts", "sequences", "broadcasts", "domains", "transports", "webhooks":
		return runNamed(ctx, args[0], args[1:], in, out)
	case "explain":
		return runExplain(ctx, args[1:], out)
	case "doctor":
		return runDoctor(ctx, args[1:], out)
	case "help", "-h", "--help":
		usage(out)
		return nil
	case "completion":
		return runCompletion(args[1:], out)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}
func runCompletion(args []string,w io.Writer)error{if len(args)!=1{return errors.New("usage: mrkt completion <bash|zsh>")};commands:="init sequence validate preview plan deploy projects tokens releases events contacts sequences broadcasts explain domains transports webhooks doctor api mcp completion";switch args[0]{case "bash":fmt.Fprintf(w,"_mrkt_complete() { COMPREPLY=( $(compgen -W '%s' -- \"${COMP_WORDS[1]}\") ); }\ncomplete -F _mrkt_complete mrkt\n",commands);case "zsh":fmt.Fprintf(w,"#compdef mrkt\n_arguments '1:command:(%s)' '*::argument:->args'\n",commands);default:return errors.New("completion shell must be bash or zsh")};return nil}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: mrkt <init|sequence add|validate|preview|plan|deploy|projects|tokens|releases|events|contacts|sequences|broadcasts|explain|domains|transports|webhooks|doctor|api|mcp serve>")
}
func apiClient() (*client.Client, error) {
	return client.New(os.Getenv("MRKT_URL"), os.Getenv("MRKT_TOKEN"))
}

type environmentsFile struct {
	Environments map[string]struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	} `json:"environments"`
}

func apiClientFor(env string) (*client.Client, error) {
	if env == "" {
		return apiClient()
	}
	p := os.Getenv("MRKT_CONFIG")
	if p == "" {
		d, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		p = filepath.Join(d, "mrkt", "config.json")
	}
	info, err := os.Stat(p)
	if err != nil {
		return nil, fmt.Errorf("read environment %q: %w", env, err)
	}
	if info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("credential file %s must have mode 0600", p)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var cfg environmentsFile
	if err = json.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	e, ok := cfg.Environments[env]
	if !ok {
		return nil, fmt.Errorf("environment %q not found", env)
	}
	return client.New(e.URL, e.Token)
}

func runInit(args []string, out io.Writer) error {
	f := flag.NewFlagSet("init", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	preset := f.String("preset", "welcome", "workflow preset")
	dir := f.String("dir", ".", "target directory")
	if err := f.Parse(args); err != nil {
		return err
	}
	files, err := presets.Files(*preset)
	if err != nil {
		return err
	}
	for name, b := range files {
		dst := filepath.Join(*dir, name)
		if _, err := os.Stat(dst); err == nil {
			return fmt.Errorf("refusing to overwrite %s", dst)
		}
		if err = os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return err
		}
		if err = os.WriteFile(dst, b, 0644); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "initialized %s with %s preset\n", *dir, *preset)
	return nil
}

func runSequence(args []string, out io.Writer) error {
	if len(args) < 2 || args[0] != "add" {
		return errors.New("usage: mrkt sequence add <id> [--dir .]")
	}
	f := flag.NewFlagSet("sequence add", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	dir := f.String("dir", ".", "project directory")
	if err := f.Parse(args[2:]); err != nil {
		return err
	}
	id := args[1]
	if id == "" || strings.ContainsAny(id, "/\\ \t\n") {
		return errors.New("sequence id must be a simple identifier")
	}
	p := filepath.Join(*dir, "mrkt.yaml")
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	m, err := manifest.Parse(b)
	if err != nil {
		return err
	}
	for _, s := range m.Sequences {
		if s.ID == id {
			return fmt.Errorf("sequence %s already exists", id)
		}
	}
	if len(m.Lists) == 0 || len(m.Messages) == 0 {
		return errors.New("a list and message are required before adding a sequence")
	}
	message := ""
	for name := range m.Messages {
		if message == "" || name < message {
			message = name
		}
	}
	m.Sequences = append(m.Sequences, manifest.Sequence{ID: id, List: m.Lists[0].ID, Entry: "subscription", Reentry: "once", Steps: []manifest.Step{{ID: "send", Type: "send", Message: message, Next: "done"}, {ID: "done", Type: "complete"}}})
	if err = manifest.Validate(m); err != nil {
		return err
	}
	b, err = yaml.Marshal(m)
	if err != nil {
		return err
	}
	if err = os.WriteFile(p, b, 0644); err != nil {
		return err
	}
	fmt.Fprintf(out, "added sequence %s\n", id)
	return nil
}

func readProject(dir string) (manifest.Manifest, map[string][]byte, error) {
	return manifest.ReadDirectory(dir)
}
func runManifest(ctx context.Context, cmd string, args []string, out io.Writer) error {
	f := flag.NewFlagSet(cmd, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	dir := f.String("dir", ".", "project directory")
	expected := f.String("expected-release", "", "optimistic release precondition")
	destructive := f.Bool("allow-destructive", false, "permit destructive changes")
	locale := f.String("locale", "", "preview locale")
	if err := f.Parse(args); err != nil {
		return err
	}
	m, sources, err := readProject(*dir)
	if err != nil {
		return err
	}
	if err = manifest.Validate(m); err != nil {
		return err
	}
	if cmd == "validate" {
		fmt.Fprintf(out, "valid %s (%s)\n", m.Project.Name, manifest.Digest(m))
		return nil
	}
	if cmd == "preview" {
		return preview(m, sources, *locale, out)
	}
	c, err := apiClient()
	if err != nil {
		return err
	}
	if cmd == "deploy" {
		for _, f := range m.Files {
			b, ok := sources[f.Path]
			if !ok {
				return fmt.Errorf("missing file %s", f.Path)
			}
			if err = c.PutArtifact(ctx, m.Project.Name, f.SHA256, f.ContentType, int64(len(b)), strings.NewReader(string(b))); err != nil {
				return err
			}
		}
	}
	body := map[string]any{"manifest": m, "expected_release": *expected, "allow_destructive": *destructive}
	raw, err := c.Do(ctx, client.Operation{Project: m.Project.Name, Resource: "releases", ID: "_", Action: cmd, Input: body})
	if err != nil {
		return err
	}
	return pretty(out, raw)
}

func preview(m manifest.Manifest, sources map[string][]byte, requested string, out io.Writer) error {
	locales := append([]string(nil), m.Project.Locales...)
	if requested != "" {
		locales = []string{manifest.ResolveLocale(requested, m.Project.DefaultLocale, m.Project.Locales)}
	}
	names := make([]string, 0, len(m.Messages))
	for n := range m.Messages {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		for _, l := range locales {
			c, ok := m.Messages[n][l]
			if !ok {
				resolved := manifest.ResolveLocale(l, m.Project.DefaultLocale, m.Project.Locales)
				c, ok = m.Messages[n][resolved]
			}
			if !ok {
				continue
			}
			r, err := manifest.Render(c, sources, map[string]any{"Name": "Preview", "Email": "preview@example.test", "Locale": l, "UnsubscribeURL": "https://example.test/unsubscribe", "ConfirmationURL": "https://example.test/confirm", "Attributes": map[string]any{}, "Event": map[string]any{}})
			if err != nil {
				return fmt.Errorf("%s/%s: %w", n, l, err)
			}
			fmt.Fprintf(out, "--- %s [%s] ---\nSubject: %s\n\n%s\n\n%s\n", n, l, r.Subject, r.Text, r.HTML)
		}
	}
	return nil
}

func runAPI(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	if len(args) < 1 {
		return errors.New("usage: mrkt api <resource> [action]")
	}
	f := flag.NewFlagSet("api", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	project := f.String("project", "", "project id")
	id := f.String("id", "", "resource id")
	key := f.String("idempotency-key", "", "idempotency key")
	limit := f.Int("limit", 50, "page size")
	cursor := f.String("cursor", "", "page cursor")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	var input any
	if b, err := io.ReadAll(in); err != nil {
		return err
	} else if strings.TrimSpace(string(b)) != "" {
		if err = json.Unmarshal(b, &input); err != nil {
			return fmt.Errorf("invalid JSON input: %w", err)
		}
	}
	action := ""
	if f.NArg() > 0 {
		action = f.Arg(0)
	}
	c, err := apiClient()
	if err != nil {
		return err
	}
	raw, err := c.Do(ctx, client.Operation{Project: *project, Resource: args[0], ID: *id, Action: action, Key: *key, Input: input, Limit: *limit, Cursor: *cursor})
	if err != nil {
		return err
	}
	return pretty(out, raw)
}

func runNamed(ctx context.Context, resource string, args []string, in io.Reader, out io.Writer) error {
	verb := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		verb = args[0]
		args = args[1:]
	}
	f := flag.NewFlagSet(resource, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	project := f.String("project", "", "project id")
	id := f.String("id", "", "resource id")
	env := f.String("env", "", "named credential environment")
	key := f.String("idempotency-key", "", "retry key")
	limit := f.Int("limit", 50, "page size")
	cursor := f.String("cursor", "", "page cursor")
	format := f.String("output", "json", "json or table")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() > 0 && *id == "" {
		*id = f.Arg(0)
	}
	var input any
	b, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(b)) != "" {
		if err = json.Unmarshal(b, &input); err != nil {
			return fmt.Errorf("invalid JSON input: %w", err)
		}
	}
	c, err := apiClientFor(*env)
	if err != nil {
		return err
	}
	op := client.Operation{Project: *project, Resource: resource, ID: *id, Key: *key, Limit: *limit, Cursor: *cursor}
	switch verb {
	case "list":
		op.ID = ""
	case "get":
		if op.ID == "" {
			return errors.New("get requires --id or positional id")
		}
	case "create":
		op.ID = ""
		op.Input = input
		if input == nil {
			op.Input = map[string]any{}
		}
	case "delete":
		if op.ID == "" {
			return errors.New("delete requires --id or positional id")
		}
		return c.Delete(ctx, *project, resource, *id, *key)
	case "emit":
		if resource != "events" {
			return errors.New("emit is only valid for events")
		}
		op.ID = ""
		op.Input = input
		if input == nil {
			return errors.New("emit requires JSON on stdin")
		}
	default:
		op.Action = verb
		if op.ID == "" {
			op.ID = "_"
		}
		op.Input = input
		if input == nil {
			op.Input = map[string]any{}
		}
	}
	raw, err := c.Do(ctx, op)
	if err != nil {
		return err
	}
	return renderOutput(out, raw, *format)
}

func runExplain(ctx context.Context, args []string, out io.Writer) error {
	if len(args) < 2 {
		return errors.New("usage: mrkt explain <resource> <id> --project PROJECT")
	}
	joined := append([]string{"explain", args[1]}, args[2:]...)
	return runNamed(ctx, args[0], joined, strings.NewReader("{}"), out)
}
func runDoctor(ctx context.Context, args []string, out io.Writer) error {
	f := flag.NewFlagSet("doctor", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	env := f.String("env", "", "named environment")
	if err := f.Parse(args); err != nil {
		return err
	}
	c, err := apiClientFor(*env)
	if err != nil {
		return err
	}
	raw, err := c.Do(ctx, client.Operation{Resource: "installation"})
	if err != nil {
		return err
	}
	fmt.Fprintln(out, "connection and authentication succeeded")
	return pretty(out, raw)
}
func renderOutput(w io.Writer, raw []byte, format string) error {
	if format == "json" {
		return pretty(w, raw)
	}
	if format != "table" {
		return fmt.Errorf("output must be json or table")
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	if obj, ok := v.(map[string]any); ok {
		if items, ok := obj["items"].([]any); ok {
			for _, item := range items {
				b, _ := json.Marshal(item)
				fmt.Fprintln(w, string(b))
			}
			return nil
		}
	}
	b, _ := json.Marshal(v)
	fmt.Fprintln(w, string(b))
	return nil
}
func pretty(w io.Writer, b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	e := json.NewEncoder(w)
	e.SetIndent("", "  ")
	return e.Encode(v)
}
