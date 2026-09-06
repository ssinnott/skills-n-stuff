package wf

import (
	"testing"
	"time"
)

func taskWith(meta map[string]any) Task {
	return Task{ID: "01HZ", ShortID: "neck", Title: "Add the parser", Meta: meta}
}

func TestLoadBindingsReadsEveryKeyShape(t *testing.T) {
	started := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	task := taskWith(map[string]any{
		RepoKey:             "/code/app",
		SessionWorkspaceKey: "/wt/neck",
		SessionPathKey:      "/s/2.jsonl",
		SessionIDKey:        "sess-2",
		SessionHistoryKey: `[{"id":"sess-1","path":"/s/1.jsonl","cwd":"/wt/old","started":"` +
			started.Format(time.RFC3339) + `","outcome":"escalated"},` +
			`{"id":"sess-2","path":"/s/2.jsonl","cwd":"/wt/neck","started":"` +
			started.Add(time.Hour).Format(time.RFC3339) + `"}]`,
		PRsKey:    `["https://a/1","https://a/2"]`,
		IssuesKey: `[{"url":"https://a/i1","title":"internal/foo.go:42 nil check"}]`,
		DocKey:    "Research/plan.md",
	})

	bs := LoadBindings(task)

	repo, ok := bs.Current(KindRepo)
	if !ok || repo.Ref != "/code/app" {
		t.Errorf("repo binding = %+v, want /code/app", repo)
	}

	ws, ok := bs.Current(KindWorkspace)
	if !ok || ws.Ref != "/wt/neck" {
		t.Errorf("workspace binding = %+v, want /wt/neck", ws)
	}
	if got := ws.Get(MetaRepo); got != "/code/app" {
		t.Errorf("workspace repo = %q, want the task's repo", got)
	}

	// Two spawns, two session bindings — the current keys name the newest
	// rather than adding a third.
	if n := len(bs.ByKind(KindSession)); n != 2 {
		t.Fatalf("session bindings = %d, want one per run", n)
	}
	session, ok := bs.Current(KindSession)
	if !ok || session.Ref != "/s/2.jsonl" {
		t.Fatalf("current session = %+v, want the newest spawn", session)
	}
	if got := session.Get(MetaSessionID); got != "sess-2" {
		t.Errorf("session id = %q, want sess-2", got)
	}
	if got := session.Get(MetaCwd); got != "/wt/neck" {
		t.Errorf("session cwd = %q, want /wt/neck", got)
	}
	if got := session.Get(MetaRunner); got != "pi" {
		t.Errorf("session runner = %q, want pi as a value", got)
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

// The current session is the one to reattach to; the runs before it are
// recorded but no longer current.
func TestLoadBindingsSupersedesEarlierSessions(t *testing.T) {
	task := taskWith(map[string]any{
		SessionPathKey: "/s/2.jsonl",
		SessionHistoryKey: `[{"id":"sess-1","path":"/s/1.jsonl","started":"2026-03-01T09:00:00Z"},` +
			`{"id":"sess-2","path":"/s/2.jsonl","started":"2026-03-01T10:00:00Z"}]`,
	})

	bs := LoadBindings(task)
	if n := len(bs.Live(KindSession)); n != 1 {
		t.Fatalf("live sessions = %d, want only the current one", n)
	}
	for _, b := range bs.ByKind(KindSession) {
		if b.Ref == "/s/1.jsonl" && b.State != BindingSuperseded {
			t.Errorf("earlier session state = %q, want superseded", b.State)
		}
	}
}

// A run that ended still has an attachable session file — that is the whole
// reason the binding is written at spawn — so ending does not retire it.
func TestLoadBindingsKeepsAnEndedSessionAttachable(t *testing.T) {
	task := taskWith(map[string]any{
		SessionPathKey: "/s/1.jsonl",
		SessionHistoryKey: `[{"id":"sess-1","path":"/s/1.jsonl","started":"2026-03-01T09:00:00Z",` +
			`"ended":"2026-03-01T09:30:00Z","outcome":"escalated"}]`,
	})

	session, ok := LoadBindings(task).Current(KindSession)
	if !ok || session.Ref != "/s/1.jsonl" {
		t.Fatalf("current session = %+v, want the settled run's session", session)
	}
}

// The history is a convenience, never a lifecycle input: garbage in it must
// not cost the current session its binding.
func TestLoadBindingsFallsBackToCurrentKeysWhenHistoryIsGarbage(t *testing.T) {
	task := taskWith(map[string]any{
		SessionPathKey:    "/s/1.jsonl",
		SessionIDKey:      "sess-1",
		SessionHistoryKey: "not json at all",
	})

	bs := LoadBindings(task)
	if n := len(bs.ByKind(KindSession)); n != 1 {
		t.Fatalf("session bindings = %d, want the one the current keys name", n)
	}
	session, _ := bs.Current(KindSession)
	if session.Ref != "/s/1.jsonl" || session.Get(MetaSessionID) != "sess-1" {
		t.Errorf("session = %+v, want it read off the current keys", session)
	}
}

func TestLoadBindingsMalformedValuesReadAsEmpty(t *testing.T) {
	malformed := []map[string]any{
		{PRsKey: "{not an array}"},
		{PRsKey: 42},
		{IssuesKey: `["a bare string"]`},
		{IssuesKey: map[string]any{"url": "https://a/1"}},
		{SessionHistoryKey: `{"path":"/s/1.jsonl"}`},
		{SessionHistoryKey: `[{"id":"no path"}]`},
		{DocKey: 7},
		{RepoKey: []any{"/code/app"}},
		{SessionWorkspaceKey: false},
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
	if _, ok := LoadBindings(Task{}).Current(KindSession); ok {
		t.Error("Current() found a session on a task with no metadata")
	}
}

// One release of tolerant reads: a task bound before the rename still
// resolves, and a task carrying both keys prefers the new one.
func TestLoadBindingsReadsLegacyKeys(t *testing.T) {
	task := taskWith(map[string]any{
		LegacySessionPathKey:      "/s/1.jsonl",
		LegacySessionIDKey:        "sess-1",
		LegacySessionWorkspaceKey: "/wt/old",
		LegacySessionHistoryKey:   `[{"id":"sess-1","path":"/s/1.jsonl","started":"2026-03-01T09:00:00Z"}]`,
		ObsidianNoteKey:           "Research/plan.md",
	})

	bs := LoadBindings(task)
	session, ok := bs.Current(KindSession)
	if !ok || session.Ref != "/s/1.jsonl" || session.Get(MetaSessionID) != "sess-1" {
		t.Errorf("session = %+v, want the pi-namespaced keys still read", session)
	}
	if n := len(bs.ByKind(KindSession)); n != 1 {
		t.Errorf("session bindings = %d, want the legacy history read once", n)
	}
	ws, ok := bs.Current(KindWorkspace)
	if !ok || ws.Ref != "/wt/old" {
		t.Errorf("workspace = %+v, want the legacy workspace key", ws)
	}
	doc, ok := bs.Current(KindDoc)
	if !ok || doc.Ref != "Research/plan.md" {
		t.Errorf("doc = %+v, want the legacy note key", doc)
	}
}

func TestLoadBindingsPrefersNewKeysOverLegacy(t *testing.T) {
	task := taskWith(map[string]any{
		SessionPathKey:            "/s/new.jsonl",
		LegacySessionPathKey:      "/s/old.jsonl",
		SessionWorkspaceKey:       "/wt/new",
		LegacySessionWorkspaceKey: "/wt/old",
		DocKey:                    "New/plan.md",
		ObsidianNoteKey:           "Old/plan.md",
	})

	bs := LoadBindings(task)
	if session, _ := bs.Current(KindSession); session.Ref != "/s/new.jsonl" {
		t.Errorf("session = %q, want the new key to win", session.Ref)
	}
	if ws, _ := bs.Current(KindWorkspace); ws.Ref != "/wt/new" {
		t.Errorf("workspace = %q, want the new key to win", ws.Ref)
	}
	if doc, _ := bs.Current(KindDoc); doc.Ref != "New/plan.md" {
		t.Errorf("doc = %q, want the new key to win", doc.Ref)
	}
}

// Round-trip: what BindSession writes is what LoadBindings reads back.
func TestLoadBindingsReadsWhatBindSessionWrote(t *testing.T) {
	q := newFakeQueue()
	first := SessionBinding{ID: "sess-1", Path: "/s/1.jsonl", Cwd: "/wt/1", Started: time.Now().UTC()}
	second := SessionBinding{ID: "sess-2", Path: "/s/2.jsonl", Cwd: "/wt/2", Started: time.Now().UTC().Add(time.Hour)}

	for _, b := range []SessionBinding{first, second} {
		if err := BindSession(t.Context(), q, "neck", b); err != nil {
			t.Fatal(err)
		}
	}

	bs := LoadBindings(taskWith(q.meta))
	if n := len(bs.ByKind(KindSession)); n != 2 {
		t.Fatalf("session bindings = %d, want one per spawn", n)
	}
	current, ok := bs.Current(KindSession)
	if !ok || current.Ref != second.Path || current.Get(MetaCwd) != second.Cwd {
		t.Errorf("current session = %+v, want the second spawn", current)
	}
}
