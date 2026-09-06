package wf

import (
	"testing"
)

func taskWith(meta map[string]any) Task {
	return Task{ID: "01HZ", ShortID: "neck", Title: "Add the parser", Meta: meta}
}

func TestLoadBindingsReadsEveryKeyShape(t *testing.T) {
	task := taskWith(map[string]any{
		RepoKey:   "/code/app",
		PRsKey:    `["https://a/1","https://a/2"]`,
		IssuesKey: `[{"url":"https://a/i1","title":"internal/foo.go:42 nil check"}]`,
		DocKey:    "Research/plan.md",
	})

	bs := LoadBindings(task)

	repo, ok := bs.Current(KindRepo)
	if !ok || repo.Ref != "/code/app" {
		t.Errorf("repo binding = %+v, want /code/app", repo)
	}

	prs := bs.Refs(KindPR)
	if len(prs) != 2 || prs[0] != "https://a/1" || prs[1] != "https://a/2" {
		t.Errorf("PR refs = %v, want both in report order", prs)
	}

	issues := bs.ByKind(KindIssue)
	if len(issues) != 1 || issues[0].Ref != "https://a/i1" {
		t.Fatalf("issue bindings = %+v, want the one filed issue", issues)
	}
	if issues[0].Label != "internal/foo.go:42 nil check" {
		t.Errorf("issue label = %q, want the filed title", issues[0].Label)
	}

	doc, ok := bs.Current(KindDoc)
	if !ok || doc.Ref != "Research/plan.md" {
		t.Fatalf("doc binding = %+v", doc)
	}
	if got := doc.Get(MetaStore); got != "vault" {
		t.Errorf("doc store = %q, want vault as a value", got)
	}
}

// A session or a workspace is machine-local and never lives on the tracker
// at all, so LoadBindings has no key name for either and cannot produce one
// no matter what a task's metadata holds — nothing here is the sync-back
// the design refuses.
func TestLoadBindingsNeverProducesMachineLocalKinds(t *testing.T) {
	task := taskWith(map[string]any{
		RepoKey: "/code/app",
		DocKey:  "Research/plan.md",
	})
	bs := LoadBindings(task)
	if len(bs.ByKind(KindSession)) != 0 {
		t.Error("LoadBindings() produced a session binding — sessions live only in the ledger")
	}
	if len(bs.ByKind(KindWorkspace)) != 0 {
		t.Error("LoadBindings() produced a workspace binding — workspaces live only in the ledger")
	}
}

func TestLoadBindingsMalformedValuesReadAsEmpty(t *testing.T) {
	malformed := []map[string]any{
		{PRsKey: "{not an array}"},
		{PRsKey: 42},
		{IssuesKey: `["a bare string"]`},
		{IssuesKey: map[string]any{"url": "https://a/1"}},
		{DocKey: 7},
		{RepoKey: []any{"/code/app"}},
	}

	for _, meta := range malformed {
		if got := LoadBindings(taskWith(meta)); len(got) != 0 {
			t.Errorf("LoadBindings(%#v) = %+v, want no bindings", meta, got)
		}
	}
}

func TestLoadBindingsNoMetadata(t *testing.T) {
	if got := LoadBindings(Task{ID: "01HZ"}); len(got) != 0 {
		t.Errorf("LoadBindings() on a bare task = %+v, want none", got)
	}
	if got := LoadBindings(taskWith(map[string]any{})); len(got) != 0 {
		t.Errorf("LoadBindings() on empty metadata = %+v, want none", got)
	}
}
