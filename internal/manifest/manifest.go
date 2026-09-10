// Package manifest defines the strict, versioned repository-owned release contract.
package manifest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	html "html/template"
	"io"
	"io/fs"
	"mime"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	text "text/template"
	"time"

	"go.yaml.in/yaml/v3"
)

const MaxFileSize int64 = 8 << 20
const MaxReleaseSize int64 = 32 << 20
const MaxRenderSize = 1 << 20

type Manifest struct {
	Version    int                           `json:"version" yaml:"version"`
	Project    Project                       `json:"project" yaml:"project"`
	Lists      []List                        `json:"lists" yaml:"lists"`
	Files      []File                        `json:"files" yaml:"files"`
	Messages   map[string]map[string]Content `json:"messages" yaml:"messages"`
	Sequences  []Sequence                    `json:"sequences" yaml:"sequences"`
	Broadcasts []Broadcast                   `json:"broadcasts" yaml:"broadcasts"`
}
type Project struct {
	Transport     string   `json:"transport,omitempty" yaml:"transport,omitempty"`
	Domain        string   `json:"domain,omitempty" yaml:"domain,omitempty"`
	Name          string   `json:"name" yaml:"name"`
	DefaultLocale string   `json:"default_locale" yaml:"default_locale"`
	Locales       []string `json:"locales" yaml:"locales"`
}
type List struct {
	ID            string `json:"id" yaml:"id"`
	Name          string `json:"name" yaml:"name"`
	Purpose       string `json:"purpose" yaml:"purpose"`
	PolicyVersion string `json:"policy_version" yaml:"policy_version"`
}
type File struct {
	Path        string `json:"path" yaml:"path"`
	SHA256      string `json:"sha256" yaml:"sha256"`
	ContentType string `json:"content_type" yaml:"content_type"`
	Size        int64  `json:"size" yaml:"size"`
	Public      bool   `json:"public" yaml:"public"`
}
type Content struct {
	Subject     string   `json:"subject" yaml:"subject"`
	HTML        string   `json:"html" yaml:"html"`
	Text        string   `json:"text" yaml:"text"`
	Attachments []string `json:"attachments,omitempty" yaml:"attachments,omitempty"`
}
type Sequence struct {
	Stream  string     `json:"stream,omitempty" yaml:"stream,omitempty"`
	ID      string     `json:"id" yaml:"id"`
	List    string     `json:"list" yaml:"list"`
	Event   string     `json:"event,omitempty" yaml:"event,omitempty"`
	Entry   string     `json:"entry" yaml:"entry"`
	Reentry string     `json:"reentry" yaml:"reentry"`
	Steps   []Step     `json:"steps" yaml:"steps"`
	Exit    *Condition `json:"exit,omitempty" yaml:"exit,omitempty"`
}
type Step struct {
	ID        string     `json:"id" yaml:"id"`
	Type      string     `json:"type" yaml:"type"`
	Message   string     `json:"message,omitempty" yaml:"message,omitempty"`
	Delay     string     `json:"delay,omitempty" yaml:"delay,omitempty"`
	Next      string     `json:"next,omitempty" yaml:"next,omitempty"`
	Else      string     `json:"else,omitempty" yaml:"else,omitempty"`
	Condition *Condition `json:"condition,omitempty" yaml:"condition,omitempty"`
}
type Condition struct {
	Field string `json:"field" yaml:"field"`
	Op    string `json:"op" yaml:"op"`
	Value any    `json:"value" yaml:"value"`
}
type Broadcast struct {
	Stream  string `json:"stream,omitempty" yaml:"stream,omitempty"`
	ID      string `json:"id" yaml:"id"`
	Event   string `json:"event" yaml:"event"`
	List    string `json:"list" yaml:"list"`
	Message string `json:"message" yaml:"message"`
}
type Rendered struct{ Subject, HTML, Text string }
type ValidationError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }
func invalid(field, msg string) error    { return &ValidationError{field, "invalid_manifest", msg} }

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,79}$`)
var localePattern = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)
var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func Parse(b []byte) (Manifest, error) {
	var m Manifest
	if len(b) > 1<<20 {
		return m, invalid("manifest", "exceeds 1 MiB")
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return m, invalid("manifest", err.Error())
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return m, invalid("manifest", "exactly one YAML document required")
	}
	return m, Validate(m)
}
func SafePath(p string) bool {
	return p != "" && fs.ValidPath(p) && !strings.ContainsAny(p, "\\\x00:\r\n") && p != "." && path.Clean(p) == p
}
func Validate(m Manifest) error {
	for _, id := range []string{m.Project.Transport, m.Project.Domain} {
		if id != "" && !identifier.MatchString(id) {
			return invalid("project", "invalid provisioned registry reference")
		}
	}
	for _, seq := range m.Sequences {
		if seq.Stream != "" && !identifier.MatchString(seq.Stream) {
			return invalid("sequences.stream", "invalid stream")
		}
	}
	for _, broadcast := range m.Broadcasts {
		if broadcast.Stream != "" && !identifier.MatchString(broadcast.Stream) {
			return invalid("broadcasts.stream", "invalid stream")
		}
	}
	if m.Version != 1 {
		return invalid("version", "must be 1")
	}
	if m.Project.Name == "" || len(m.Project.Name) > 120 {
		return invalid("project.name", "required, maximum 120 characters")
	}
	if len(m.Project.Locales) == 0 || len(m.Project.Locales) > 30 || !slices.Contains(m.Project.Locales, m.Project.DefaultLocale) {
		return invalid("project.locales", "must contain default locale and at most 30 locales")
	}
	for i, l := range m.Project.Locales {
		if !localePattern.MatchString(l) || slices.Contains(m.Project.Locales[:i], l) {
			return invalid("project.locales", "invalid or duplicate locale")
		}
	}
	if len(m.Files) > 256 || len(m.Messages) > 100 || len(m.Sequences) > 100 || len(m.Broadcasts) > 100 || len(m.Lists) > 100 {
		return invalid("manifest", "resource count exceeds limit")
	}
	files := map[string]File{}
	var total int64
	for _, f := range m.Files {
		if !SafePath(f.Path) {
			return invalid("files.path", "unsafe path")
		}
		if _, ok := files[f.Path]; ok {
			return invalid("files.path", "duplicate")
		}
		if f.Size < 0 || f.Size > MaxFileSize {
			return invalid("files.size", "out of bounds")
		}
		total += f.Size
		if f.SHA256 != "" && !hashPattern.MatchString(f.SHA256) {
			return invalid("files.sha256", "expected lowercase SHA256")
		}
		mt, _, err := mime.ParseMediaType(f.ContentType)
		if err != nil {
			return invalid("files.content_type", "valid MIME type required")
		}
		if f.Public && !slices.Contains([]string{"image/png", "image/jpeg", "image/gif", "image/webp"}, mt) {
			return invalid("files.public", "public assets must be PNG, JPEG, GIF or WebP")
		}
		files[f.Path] = f
	}
	if total > MaxReleaseSize {
		return invalid("files", "release exceeds 32 MiB")
	}
	lists := map[string]bool{}
	for _, l := range m.Lists {
		if !identifier.MatchString(l.ID) || lists[l.ID] || l.Name == "" || l.Purpose == "" || l.PolicyVersion == "" {
			return invalid("lists", "unique ID, name, purpose and policy_version required")
		}
		lists[l.ID] = true
	}
	for id, translations := range m.Messages {
		if !identifier.MatchString(id) {
			return invalid("messages", "invalid ID")
		}
		if _, ok := translations[m.Project.DefaultLocale]; !ok {
			return invalid("messages."+id, "default locale translation required")
		}
		for loc, c := range translations {
			if !slices.Contains(m.Project.Locales, loc) {
				return invalid("messages."+id, "unsupported locale")
			}
			for _, p := range []string{c.Subject, c.HTML, c.Text} {
				if _, ok := files[p]; !ok {
					return invalid("messages."+id, "source missing from inventory: "+p)
				}
			}
			for _, p := range c.Attachments {
				f, ok := files[p]
				if !ok || f.Public {
					return invalid("messages."+id, "attachment missing or public")
				}
			}
			if len(c.Attachments) > 10 {
				return invalid("messages."+id, "at most ten attachments")
			}
		}
	}
	seqIDs := map[string]bool{}
	for _, s := range m.Sequences {
		if !identifier.MatchString(s.ID) || seqIDs[s.ID] || !lists[s.List] {
			return invalid("sequences", "unique ID and existing list required")
		}
		seqIDs[s.ID] = true
		if s.Entry != "subscription" && s.Entry != "event" {
			return invalid("sequences.entry", "must be subscription or event")
		}
		if s.Entry == "event" && !identifier.MatchString(s.Event) {
			return invalid("sequences.event", "event name required")
		}
		if s.Reentry != "once" && s.Reentry != "event" {
			return invalid("sequences.reentry", "must be once or event")
		}
		if s.Entry == "subscription" && s.Reentry != "once" {
			return invalid("sequences.reentry", "subscriptions require once")
		}
		if len(s.Steps) == 0 || len(s.Steps) > 100 {
			return invalid("sequences.steps", "requires 1–100 steps")
		}
		steps := map[string]Step{}
		for _, st := range s.Steps {
			if !identifier.MatchString(st.ID) {
				return invalid("steps.id", "invalid")
			}
			if _, ok := steps[st.ID]; ok {
				return invalid("steps.id", "duplicate")
			}
			steps[st.ID] = st
		}
		for _, st := range s.Steps {
			switch st.Type {
			case "send":
				if _, ok := m.Messages[st.Message]; !ok {
					return invalid("steps.message", "unknown message")
				}
			case "delay":
				d, err := time.ParseDuration(st.Delay)
				if err != nil || d < 0 || d > 366*24*time.Hour {
					return invalid("steps.delay", "requires Go duration between 0 and 366 days")
				}
			case "condition":
				if st.Condition == nil || st.Next == "" || st.Else == "" {
					return invalid("steps.condition", "condition and both branches required")
				}
				if err := validateCondition(st.Condition); err != nil {
					return err
				}
			case "complete":
				if st.Next != "" || st.Else != "" {
					return invalid("steps.complete", "cannot have successor")
				}
			default:
				return invalid("steps.type", "unknown step type")
			}
			for _, next := range []string{st.Next, st.Else} {
				if next != "" {
					if _, ok := steps[next]; !ok {
						return invalid("steps.next", "unknown step")
					}
				}
			}
		}
		colors := map[string]int{}
		var visit func(string) error
		visit = func(id string) error {
			if colors[id] == 1 {
				return invalid("steps", "cycles forbidden")
			}
			if colors[id] == 2 {
				return nil
			}
			colors[id] = 1
			st := steps[id]
			for _, next := range []string{st.Next, st.Else} {
				if next != "" {
					if err := visit(next); err != nil {
						return err
					}
				}
			}
			colors[id] = 2
			return nil
		}
		for id := range steps {
			if err := visit(id); err != nil {
				return err
			}
		}
		if s.Exit != nil {
			if err := validateCondition(s.Exit); err != nil {
				return err
			}
		}
	}
	ids := map[string]bool{}
	for _, b := range m.Broadcasts {
		if !identifier.MatchString(b.ID) || ids[b.ID] || !identifier.MatchString(b.Event) || !lists[b.List] {
			return invalid("broadcasts", "unique ID, event and existing list required")
		}
		ids[b.ID] = true
		if _, ok := m.Messages[b.Message]; !ok {
			return invalid("broadcasts.message", "unknown message")
		}
	}
	return nil
}
func validateCondition(c *Condition) error {
	if !regexp.MustCompile(`^(Attributes|Event)\.[A-Za-z0-9_.]{1,80}$|^(Locale|Email)$`).MatchString(c.Field) {
		return invalid("condition.field", "use Attributes.*, Event.*, Locale or Email")
	}
	if !slices.Contains([]string{"eq", "ne", "gt", "gte", "lt", "lte", "exists"}, c.Op) {
		return invalid("condition.op", "unsupported operator")
	}
	switch c.Value.(type) {
	case nil, string, bool, int, int64, float64:
	default:
		return invalid("condition.value", "must be scalar")
	}
	return nil
}
func Digest(m Manifest) string {
	b, _ := json.Marshal(m)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func ResolveLocale(requested, def string, supported []string) string {
	for requested != "" {
		if slices.Contains(supported, requested) {
			return requested
		}
		i := strings.LastIndexByte(requested, '-')
		if i < 0 {
			break
		}
		requested = requested[:i]
	}
	return def
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > MaxRenderSize {
		return 0, errors.New("render exceeds 1 MiB")
	}
	return b.Buffer.Write(p)
}
func Render(c Content, sources map[string][]byte, vars map[string]any) (Rendered, error) {
	var r Rendered
	if err := validateVariables(vars); err != nil {
		return r, err
	}
	for _, part := range []struct {
		path string
		html bool
		out  *string
	}{{c.Subject, false, &r.Subject}, {c.HTML, true, &r.HTML}, {c.Text, false, &r.Text}} {
		source, ok := sources[part.path]
		if !ok {
			return r, fmt.Errorf("missing template %s", part.path)
		}
		if len(source) > MaxRenderSize {
			return r, errors.New("template too large")
		}
		var b limitedBuffer
		if part.html {
			t, err := html.New(part.path).Funcs(html.FuncMap(renderFunctions())).Option("missingkey=error").Parse(string(source))
			if err != nil {
				return r, err
			}
			if err = validateRenderTree(t.Tree.Root); err != nil {
				return r, err
			}
			if err = t.Execute(&b, vars); err != nil {
				return r, err
			}
		} else {
			t, err := text.New(part.path).Funcs(text.FuncMap(renderFunctions())).Option("missingkey=error").Parse(string(source))
			if err != nil {
				return r, err
			}
			if err = validateRenderTree(t.Tree.Root); err != nil {
				return r, err
			}
			if err = t.Execute(&b, vars); err != nil {
				return r, err
			}
		}
		*part.out = b.String()
	}
	r.Subject = strings.TrimSpace(r.Subject)
	if strings.ContainsAny(r.Subject, "\r\n") || len(r.Subject) > 998 {
		return r, errors.New("invalid subject header")
	}
	var err error
	r.HTML, err = inlineEmailCSS(r.HTML)
	if err != nil {
		return r, err
	}
	return r, nil
}

func ReadDirectory(dir string) (Manifest, map[string][]byte, error) {
	var m Manifest
	root, err := os.OpenRoot(dir)
	if err != nil {
		return m, nil, err
	}
	defer root.Close()
	read := func(p string) ([]byte, error) {
		if !SafePath(p) {
			return nil, errors.New("unsafe path")
		}
		prefix := ""
		for _, segment := range strings.Split(p, "/") {
			prefix = path.Join(prefix, segment)
			i, e := root.Lstat(prefix)
			if e != nil {
				return nil, e
			}
			if i.Mode()&os.ModeSymlink != 0 {
				return nil, errors.New("symlinks forbidden")
			}
		}
		f, e := root.Open(p)
		if e != nil {
			return nil, e
		}
		defer f.Close()
		i, e := f.Stat()
		if e != nil {
			return nil, e
		}
		if !i.Mode().IsRegular() || i.Size() > MaxFileSize {
			return nil, errors.New("not a bounded regular file")
		}
		return io.ReadAll(io.LimitReader(f, MaxFileSize+1))
	}
	raw, err := read("mrkt.yaml")
	if err != nil {
		return m, nil, err
	}
	m, err = Parse(raw)
	if err != nil {
		return m, nil, err
	}
	sources := map[string][]byte{}
	var total int64
	for i, f := range m.Files {
		data, e := read(filepath.ToSlash(f.Path))
		if e != nil {
			return m, nil, fmt.Errorf("%s: %w", f.Path, e)
		}
		h := sha256.Sum256(data)
		digest := hex.EncodeToString(h[:])
		if f.SHA256 != "" && f.SHA256 != digest {
			return m, nil, fmt.Errorf("%s: digest mismatch", f.Path)
		}
		m.Files[i].SHA256 = digest
		m.Files[i].Size = int64(len(data))
		total += int64(len(data))
		if total > MaxReleaseSize {
			return m, nil, errors.New("release exceeds 32 MiB")
		}
		sources[f.Path] = data
	}
	return m, sources, Validate(m)
}
