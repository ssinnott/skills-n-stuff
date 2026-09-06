package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLedgerFile(t *testing.T, dir, name string, doc map[string]any) {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readLedgerFile(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestMigrateFileRenamesToTheQueueRefAndDropsTheBinding(t *testing.T) {
	dir := t.TempDir()
	writeLedgerFile(t, dir, "twfabc.json", map[string]any{
		"id":      "twfabc",
		"handle":  "neck",
		"title":   "Fix the parser",
		"created": "2025-01-01T00:00:00Z",
		"bindings": []any{
			map[string]any{
				"kind":  "queue",
				"ref":   "01HZTRACKERROW",
				"label": "Fix the parser",
				"state": "live",
				"at":    "2025-01-01T00:00:00Z",
				"meta":  map[string]any{"backend": "kata", "short_id": "abc4"},
			},
			map[string]any{
				"kind": "workspace",
				"ref":  "/wt/neck",
				"at":   "2025-01-01T01:00:00Z",
			},
		},
	})

	out, err := migrateFile(dir, filepath.Join(dir, "twfabc.json"))
	if err != nil {
		t.Fatalf("migrateFile() error = %v", err)
	}
	want := filepath.Join(dir, "01HZTRACKERROW.json")
	if out != want {
		t.Fatalf("migrateFile() = %q, want %q", out, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "twfabc.json")); !os.IsNotExist(err) {
		t.Error("the old file must be gone after a successful migration")
	}

	doc := readLedgerFile(t, want)
	if doc["id"] != "01HZTRACKERROW" {
		t.Errorf("id = %v, want the queue ref", doc["id"])
	}
	if _, ok := doc["handle"]; ok {
		t.Error("handle survived the migration")
	}
	if _, ok := doc["title"]; ok {
		t.Error("title survived the migration")
	}
	bindings, ok := doc["bindings"].([]any)
	if !ok || len(bindings) != 1 {
		t.Fatalf("bindings = %+v, want just the workspace one", doc["bindings"])
	}
	kept := bindings[0].(map[string]any)
	if kept["kind"] != "workspace" {
		t.Errorf("surviving binding = %+v, want the workspace one", kept)
	}
}

func TestMigrateFileLeavesAFileWithNoQueueBindingAlone(t *testing.T) {
	dir := t.TempDir()
	writeLedgerFile(t, dir, "twfnone.json", map[string]any{
		"id":      "twfnone",
		"handle":  "wrist",
		"title":   "Never filed",
		"created": "2025-01-01T00:00:00Z",
	})

	out, err := migrateFile(dir, filepath.Join(dir, "twfnone.json"))
	if err != nil {
		t.Fatalf("migrateFile() error = %v", err)
	}
	if out != "" {
		t.Errorf("migrateFile() = %q, want empty — nothing to rekey to", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "twfnone.json")); err != nil {
		t.Errorf("the unmigratable file must be left in place: %v", err)
	}
}

func TestCmdMigrateLedgerSweepsTheWholeDirectory(t *testing.T) {
	h := newHome(t, filepath.Join(t.TempDir(), "no-such-kata"))
	dir := h.ledger().(interface{ Dir() string }).Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	writeLedgerFile(t, dir, "twf1.json", map[string]any{
		"id": "twf1", "created": "2025-01-01T00:00:00Z",
		"bindings": []any{
			map[string]any{"kind": "queue", "ref": "01HZONE", "at": "2025-01-01T00:00:00Z"},
		},
	})
	writeLedgerFile(t, dir, "twf2.json", map[string]any{
		"id": "twf2", "created": "2025-01-01T00:00:00Z",
	})

	out, err := h.cli(t, "migrate-ledger")
	if err != nil {
		t.Fatalf("wf migrate-ledger error = %v", err)
	}
	for _, want := range []string{"01HZONE", "1 migrated", "1 unmigratable"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want it to name %q", out, want)
		}
	}

	if _, err := os.Stat(filepath.Join(dir, "01HZONE.json")); err != nil {
		t.Errorf("migrated file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "twf2.json")); err != nil {
		t.Errorf("unmigratable file should be left in place: %v", err)
	}
}
