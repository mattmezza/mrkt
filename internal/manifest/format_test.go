package manifest

import (
	"strings"
	"testing"
)

func TestLocalizedFormattingAndInlineCSS(t *testing.T) {
	c := Content{Subject: "s", HTML: "h", Text: "t"}
	sources := map[string][]byte{"s": []byte(`{{number 1234.5 "it" 2}}`), "h": []byte(`<style>p{color:red}</style><p>{{.Name}}</p>`), "t": []byte(`{{date "2026-03-29T01:30:00Z" "it" "Europe/Zurich"}}`)}
	r, err := Render(c, sources, map[string]any{"Name": "<script>"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Subject != "1.234,50" || !strings.Contains(r.Text, "03:30 CEST") || !strings.Contains(r.HTML, `style="color:red"`) || !strings.Contains(r.HTML, "&lt;script&gt;") {
		t.Fatalf("unexpected rendering %#v", r)
	}
	for _, html := range []string{`<script>alert(1)</script>`, `<link href="http://localhost/a">`, `<style>@import url(http://localhost);</style>`, `<p onclick="alert(1)">x</p>`} {
		if _, err = inlineEmailCSS(html); err == nil {
			t.Fatalf("unsafe email accepted %s", html)
		}
	}
}

func TestRenderComplexityWithoutOutput(t *testing.T) {
	c := Content{Subject: "s", HTML: "h", Text: "t"}
	for _, source := range []string{`{{range .Items}}{{range $.Items}}{{end}}{{end}}`, `{{define "loop"}}{{template "loop" .}}{{end}}{{template "loop" .}}`} {
		_, err := Render(c, map[string][]byte{"s": []byte("subject"), "h": []byte(source), "t": []byte("text")}, map[string]any{"Items": []any{1, 2}})
		if err == nil {
			t.Fatal("unbounded empty-output template accepted")
		}
	}
}
