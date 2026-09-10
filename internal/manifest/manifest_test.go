package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func valid() Manifest {
	return Manifest{Version: 1, Project: Project{Name: "Synthetic", DefaultLocale: "en", Locales: []string{"en", "it", "tr"}}, Lists: []List{{ID: "news", Name: "News", Purpose: "marketing", PolicyVersion: "2026-01"}}, Files: []File{{Path: "subject.txt", ContentType: "text/plain"}, {Path: "mail.html", ContentType: "text/html"}, {Path: "mail.txt", ContentType: "text/plain"}}, Messages: map[string]map[string]Content{"welcome": {"en": {Subject: "subject.txt", HTML: "mail.html", Text: "mail.txt"}}}, Sequences: []Sequence{{ID: "welcome", List: "news", Entry: "subscription", Reentry: "once", Steps: []Step{{ID: "send", Type: "send", Message: "welcome", Next: "done"}, {ID: "done", Type: "complete"}}}}}
}
func TestValidationSafety(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"cycle", func(m *Manifest) { m.Sequences[0].Steps[0].Next = "send" }},
		{"traversal", func(m *Manifest) { m.Files[0].Path = "../secret" }},
		{"public_html", func(m *Manifest) { m.Files[1].Public = true }},
		{"unsupported_locale", func(m *Manifest) { m.Project.DefaultLocale = "de" }},
		{"missing_translation", func(m *Manifest) { delete(m.Messages["welcome"], "en") }},
		{"unknown_message", func(m *Manifest) { m.Sequences[0].Steps[0].Message = "missing" }},
		{"unbounded_delay", func(m *Manifest) { m.Sequences[0].Steps[0] = Step{ID: "send", Type: "delay", Delay: "999999h"} }},
	}
	if err := Validate(valid()); err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := valid()
			tt.mutate(&m)
			if Validate(m) == nil {
				t.Fatal("unsafe manifest accepted")
			}
		})
	}
}
func TestParseStrict(t *testing.T) {
	for _, data := range []string{"version: 1\nsecret: ignored", "version: 1\nversion: 2", "version: 1\n---\nversion: 1"} {
		if _, err := Parse([]byte(data)); err == nil {
			t.Fatal("invalid document accepted")
		}
	}
}
func TestRendering(t *testing.T) {
	c := Content{Subject: "s", HTML: "h", Text: "t"}
	sources := map[string][]byte{"s": []byte("Merhaba {{.Name}}\n"), "h": []byte("<p>{{.Name}}</p>"), "t": []byte("Ciao {{.Name}}")}
	r, err := Render(c, sources, map[string]any{"Name": "İpek <script>"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.HTML, "<script>") || !strings.Contains(r.HTML, "&lt;script&gt;") {
		t.Fatal(r.HTML)
	}
	if !strings.Contains(r.Subject, "İpek") {
		t.Fatal(r.Subject)
	}
	if _, err := Render(c, sources, map[string]any{}); err == nil {
		t.Fatal("missing variable accepted")
	}
	if _, err := Render(c, sources, map[string]any{"Name": "x\r\nBcc: victim@example.test"}); err == nil {
		t.Fatal("header injection accepted")
	}
	if _, err := Render(c, sources, map[string]any{"Name": strings.Repeat("x", MaxRenderSize+1)}); err == nil {
		t.Fatal("unbounded output accepted")
	}
	if got := ResolveLocale("it-CH", "en", []string{"en", "it", "tr"}); got != "it" {
		t.Fatal(got)
	}
}
func TestRejectSymlink(t *testing.T) {
	d := t.TempDir()
	if err := os.Symlink(filepath.Join(d, "other"), filepath.Join(d, "mrkt.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadDirectory(d); err == nil {
		t.Fatal("symlink accepted")
	}
}
