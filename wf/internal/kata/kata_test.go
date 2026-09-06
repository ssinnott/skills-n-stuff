package kata

import (
	"encoding/json"
	"testing"
)

// These tests pin the tolerance of the wire-format layer, not kata's actual
// shape — which is undocumented and therefore inferred. They exist so that
// when the first live run reveals the real field names, changing
// NormalizeIssue is provably the only edit needed.

func decode(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("bad test fixture: %v", err)
	}
	return v
}

func TestExtractIssuesEnvelopes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"bare array", `[{"id":"a"},{"id":"b"}]`, 2},
		{"issues wrapper", `{"issues":[{"id":"a"}]}`, 1},
		{"items wrapper", `{"items":[{"id":"a"}]}`, 1},
		{"data wrapper", `{"data":[{"id":"a"}]}`, 1},
		{"single issue key", `{"issue":{"id":"a"}}`, 1},
		{"bare object with id", `{"id":"a","title":"t"}`, 1},
		{"empty array", `[]`, 0},
		{"unrelated object", `{"ok":true}`, 0},
		{"null", `null`, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractIssues(decode(t, tt.in))
			if len(got) != tt.want {
				t.Errorf("ExtractIssues() = %d issues, want %d", len(got), tt.want)
			}
		})
	}
}

func TestNormalizeIssuePrefersDurableID(t *testing.T) {
	raw := decode(t, `{
		"ulid": "01HZNQ7VFPK1XGD8R5MABCD4EX",
		"id": "should-not-win",
		"short_id": "cd4ex",
		"title": "Wire the supervisor",
		"priority": 1,
		"labels": ["agent", "queue"],
		"owner": "wf-1",
		"metadata": {"wf.state": "running"}
	}`).(map[string]any)

	got := NormalizeIssue(raw)

	if got.ID != "01HZNQ7VFPK1XGD8R5MABCD4EX" {
		t.Errorf("ID = %q, want the ULID", got.ID)
	}
	if got.ShortID != "cd4ex" {
		t.Errorf("ShortID = %q, want %q", got.ShortID, "cd4ex")
	}
	if got.Priority != 1 {
		t.Errorf("Priority = %d, want 1", got.Priority)
	}
	if len(got.Labels) != 2 {
		t.Errorf("Labels = %v, want two", got.Labels)
	}
	if got.Meta["wf.state"] != "running" {
		t.Errorf("Meta not carried through: %v", got.Meta)
	}
}

func TestNormalizeIssueFallbacks(t *testing.T) {
	// Alternate field names, and a missing short id derived from the ULID.
	raw := decode(t, `{
		"id": "01HZNQ7VFPK1XGD8R5MABCD4EX",
		"summary": "Alternate title field",
		"description": "body text",
		"tags": ["x"]
	}`).(map[string]any)

	got := NormalizeIssue(raw)

	if got.Title != "Alternate title field" {
		t.Errorf("Title = %q", got.Title)
	}
	if got.Body != "body text" {
		t.Errorf("Body = %q", got.Body)
	}
	if got.ShortID != "d4ex" {
		t.Errorf("ShortID = %q, want the derived %q", got.ShortID, "d4ex")
	}
	if got.Priority != 2 {
		t.Errorf("Priority = %d, want the default 2", got.Priority)
	}
	if got.Owner != "" {
		t.Errorf("Owner = %q, want empty", got.Owner)
	}
}

func TestNormalizeIssueEmpty(t *testing.T) {
	// A shape we do not recognize must degrade, not panic.
	got := NormalizeIssue(map[string]any{})
	if got.ID != "" || got.Priority != 2 || got.Meta == nil {
		t.Errorf("unexpected zero-value normalization: %+v", got)
	}
}

func TestMetaObjectUnwraps(t *testing.T) {
	nested := MetaObject(decode(t, `{"metadata":{"a":"1"}}`))
	if nested["a"] != "1" {
		t.Errorf("MetaObject() did not unwrap: %v", nested)
	}

	flat := MetaObject(decode(t, `{"a":"1"}`))
	if flat["a"] != "1" {
		t.Errorf("MetaObject() lost a flat object: %v", flat)
	}

	if got := MetaObject(nil); got == nil || len(got) != 0 {
		t.Errorf("MetaObject(nil) = %v, want an empty map", got)
	}
}
