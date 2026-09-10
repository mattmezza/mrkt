package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestInitValidatePreviewAndSequence(t *testing.T) {
	d := t.TempDir()
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"init", "--dir", d}, bytes.NewReader(nil), &out, &out); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"validate", "--dir", d}, bytes.NewReader(nil), &out, &out); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"preview", "--dir", d, "--locale", "it-CH"}, bytes.NewReader(nil), &out, &out); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"sequence", "add", "second", "--dir", d}, bytes.NewReader(nil), &out, &out); err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), []string{"validate", "--dir", d}, bytes.NewReader(nil), &out, &out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(d, "welcome.tr.html")); err != nil {
		t.Fatal(err)
	}
}

func TestSyntheticConsumersValidate(t *testing.T) {
	for _, dir := range []string{"../../examples/flowrent/consumer", "../../examples/cheerful/consumer"} {
		if _, _, err := readProject(dir); err != nil {
			t.Errorf("%s: %v", dir, err)
		}
	}
}

func TestNamedLocalizedPresets(t *testing.T) {
	for _, name := range []string{"coming-soon", "welcome", "newsletter", "launch"} {
		d := t.TempDir()
		if err := Run(context.Background(), []string{"init", "--preset", name, "--dir", d}, bytes.NewReader(nil), io.Discard, io.Discard); err != nil {
			t.Fatal(err)
		}
		m, _, err := readProject(d)
		if err != nil {
			t.Fatal(err)
		}
		if m.Project.Name != name || len(m.Project.Locales) != 3 {
			t.Fatalf("unexpected %s preset", name)
		}
	}
}

func TestShellCompletion(t *testing.T){for _,shell:=range []string{"bash","zsh"}{var out bytes.Buffer;if err:=Run(context.Background(),[]string{"completion",shell},bytes.NewReader(nil),&out,&out);err!=nil{t.Fatal(err)};if !strings.Contains(out.String(),"projects"){t.Fatalf("%s completion missing commands",shell)}}}
