package engine

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeCSVCell(t *testing.T) {
	for _, v := range []string{"=cmd", " +sum", "-1", "@x", "\tvalue", "  \rvalue"} {
		if got := safeCSVCell(v); !strings.HasPrefix(got, "'") {
			t.Errorf("%q not protected: %q", v, got)
		}
	}
	if got := safeCSVCell(" ordinary"); got != " ordinary" {
		t.Errorf("ordinary changed: %q", got)
	}
}

func TestCSVPreviewCountsAndValidation(t *testing.T) {
	rows, problems, err := parseContactsCSV("email,locale,timezone,attributes\nok@example.test,en,Europe/Zurich,\"{\"\"n\"\":1}\"\nbad,it,UTC,{}\nother@example.test,INVALID,Nowhere,{}\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || len(problems) != 2 {
		t.Fatalf("rows=%d problems=%d", len(rows), len(problems))
	}
}

func TestCSVExportCursorColumnsAndFormulaSafety(t *testing.T) {
	e, err := Open(Config{DBPath: filepath.Join(t.TempDir(), "mrkt.db"), Initialize: true, AdminToken: "csv-test-admin"})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	ctx := context.Background()
	a := Authority{Admin: true}
	if _, err = e.Do(ctx, a, Operation{Resource: "projects", Action: "create", Input: raw(map[string]any{"id": "csv", "name": "CSV"})}); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"=one", "+two", "ordinary"} {
		_, err := e.Do(ctx, a, Operation{Project: "csv", Resource: "contacts", Action: "create", Input: raw(map[string]any{"email": string(rune('a'+i)) + "@example.test", "name": name})})
		if err != nil {
			t.Fatal(err)
		}
	}
	v, err := e.Do(ctx, a, Operation{Project: "csv", Resource: "contacts", Action: "export", Limit: 2, Input: raw(map[string]any{})})
	if err != nil {
		t.Fatal(err)
	}
	m := objectMap(v)
	if m["next_cursor"] == "" {
		t.Fatal("missing next cursor")
	}
	v, err = e.Do(ctx, a, Operation{Project: "csv", Resource: "contacts", Action: "export", Limit: 10, Input: raw(map[string]any{})})
	if err != nil {
		t.Fatal(err)
	}
	m = objectMap(v)
	csvText := m["csv"].(string)
	if !strings.Contains(csvText, "'=one") || !strings.Contains(csvText, "'+two") {
		t.Fatalf("unsafe export: %s", csvText)
	}
	if len(m["columns"].([]any)) != 6 {
		t.Fatalf("columns=%v", m["columns"])
	}
}

func objectMap(v any) map[string]any { return scanJSON[map[string]any](string(mustJSON(v))) }
func mustJSON(v any) []byte          { b, _ := json.Marshal(v); return b }
