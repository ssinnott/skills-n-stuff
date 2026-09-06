package wf

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// fakeQueue records writes so the binding and apply logic can be tested
// without a tracker.
type fakeQueue struct {
	meta     map[string]any
	comments []string
	closes   []CloseResult
	created  []CreateInput
	closeErr error
}

func newFakeQueue() *fakeQueue { return &fakeQueue{meta: map[string]any{}} }

func (f *fakeQueue) Name() string                                { return "fake" }
func (f *fakeQueue) Ready(context.Context, int) ([]Task, error)  { return nil, nil }
func (f *fakeQueue) Get(context.Context, string) (Task, error)   { return Task{}, nil }
func (f *fakeQueue) Claim(context.Context, string, string) error { return nil }
func (f *fakeQueue) Release(context.Context, string) error       { return nil }

func (f *fakeQueue) Comment(_ context.Context, _, body string) error {
	f.comments = append(f.comments, body)
	return nil
}

func (f *fakeQueue) Close(_ context.Context, _ string, result CloseResult, _ string) error {
	if f.closeErr != nil {
		return f.closeErr
	}
	f.closes = append(f.closes, result)
	return nil
}

func (f *fakeQueue) Create(_ context.Context, in CreateInput) (Task, error) {
	f.created = append(f.created, in)
	return Task{ID: "new-" + slugKey(in.Title), ShortID: "new1", Title: in.Title}, nil
}

func (f *fakeQueue) UnsetMeta(_ context.Context, _, key string) error {
	delete(f.meta, key)
	return nil
}
func (f *fakeQueue) GetMeta(context.Context, string) (map[string]any, error) {
	out := map[string]any{}
	for k, v := range f.meta {
		out[k] = v
	}
	return out, nil
}
func (f *fakeQueue) SetMeta(_ context.Context, _, key, value string, _ SetMetaOptions) error {
	f.meta[key] = value
	return nil
}

func TestBindSessionWritesCurrentAndHistory(t *testing.T) {
	ctx := context.Background()
	q := newFakeQueue()
	started := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)

	b := SessionBinding{
		ID:      "sess-1",
		Path:    "/home/me/.pi/agent/sessions/abc.jsonl",
		Cwd:     "/work/task-1",
		Started: started,
	}
	if err := BindSession(ctx, q, "abc4", b); err != nil {
		t.Fatalf("BindSession() error = %v", err)
	}

	meta, _ := q.GetMeta(ctx, "abc4")
	got, ok := BindingFromMeta(meta)
	if !ok {
		t.Fatal("binding not readable after write")
	}
	if got.Path != b.Path || got.ID != b.ID || got.Cwd != b.Cwd {
		t.Errorf("binding round trip lost fields: %+v", got)
	}
	if len(HistoryFromMeta(meta)) != 1 {
		t.Errorf("history = %d entries, want 1", len(HistoryFromMeta(meta)))
	}
}

func TestBindSessionAppendsHistoryAcrossRetries(t *testing.T) {
	ctx := context.Background()
	q := newFakeQueue()

	for i, path := range []string{"/s/1.jsonl", "/s/2.jsonl", "/s/3.jsonl"} {
		b := SessionBinding{ID: "sess", Path: path, Cwd: "/work"}
		if err := BindSession(ctx, q, "abc4", b); err != nil {
			t.Fatalf("BindSession() %d error = %v", i, err)
		}
	}

	meta, _ := q.GetMeta(ctx, "abc4")
	history := HistoryFromMeta(meta)
	if len(history) != 3 {
		t.Fatalf("history = %d entries, want 3 (a retried task keeps every attempt)", len(history))
	}
	// The current-session keys track the latest run.
	current, _ := BindingFromMeta(meta)
	if current.Path != "/s/3.jsonl" {
		t.Errorf("current session = %q, want the newest", current.Path)
	}
}

func TestFinishSessionStampsLastEntry(t *testing.T) {
	ctx := context.Background()
	q := newFakeQueue()
	_ = BindSession(ctx, q, "abc4", SessionBinding{ID: "s", Path: "/s/1.jsonl", Cwd: "/work"})

	ended := time.Date(2026, 9, 5, 13, 0, 0, 0, time.UTC)
	if err := FinishSession(ctx, q, "abc4", SessionEscalated, ended); err != nil {
		t.Fatalf("FinishSession() error = %v", err)
	}

	meta, _ := q.GetMeta(ctx, "abc4")
	history := HistoryFromMeta(meta)
	if len(history) != 1 {
		t.Fatalf("history = %d entries, want 1", len(history))
	}
	if history[0].Outcome != SessionEscalated {
		t.Errorf("Outcome = %q, want %q", history[0].Outcome, SessionEscalated)
	}
	if history[0].Ended == nil || !history[0].Ended.Equal(ended) {
		t.Errorf("Ended = %v, want %v", history[0].Ended, ended)
	}
}

func TestFinishSessionWithNoHistoryIsNoop(t *testing.T) {
	q := newFakeQueue()
	if err := FinishSession(context.Background(), q, "abc4", SessionDone, time.Now()); err != nil {
		t.Errorf("FinishSession() on an unbound task error = %v, want nil", err)
	}
}

func TestBindingFromMetaRequiresPath(t *testing.T) {
	// The session id alone is not enough: attach resolves the file path.
	if _, ok := BindingFromMeta(map[string]any{SessionIDKey: "sess-1"}); ok {
		t.Error("BindingFromMeta() accepted a binding with no path")
	}
	if _, ok := BindingFromMeta(map[string]any{}); ok {
		t.Error("BindingFromMeta() accepted empty metadata")
	}
}

func TestHistoryFromMetaTolerance(t *testing.T) {
	// History is a convenience, not a lifecycle input: garbage reads as empty.
	for _, value := range []any{nil, "", "not json", `{"not":"an array"}`, 42} {
		if got := HistoryFromMeta(map[string]any{SessionHistoryKey: value}); len(got) != 0 {
			t.Errorf("HistoryFromMeta(%#v) = %v, want empty", value, got)
		}
	}

	// Entries without a path are dropped rather than failing the whole read.
	raw, _ := json.Marshal([]map[string]any{{"path": "/s/1.jsonl"}, {"id": "no-path"}})
	if got := HistoryFromMeta(map[string]any{SessionHistoryKey: string(raw)}); len(got) != 1 {
		t.Errorf("HistoryFromMeta() = %d entries, want 1", len(got))
	}
}

func TestAttachArgsUsesPath(t *testing.T) {
	b := Binding{Kind: KindSession, Ref: "/s/1.jsonl", Meta: map[string]string{MetaSessionID: "sess-1"}}
	got := AttachArgs(b)
	if len(got) != 2 || got[0] != "--session" || got[1] != "/s/1.jsonl" {
		t.Errorf("AttachArgs() = %v, want the session file path", got)
	}
}

func TestSeedPreambleCarriesRefAndVerbs(t *testing.T) {
	got := SeedPreamble("abc4", "Wire the supervisor")

	if !strings.Contains(got, "abc4") || !strings.Contains(got, "Wire the supervisor") {
		t.Error("preamble must name the issue the agent is working on")
	}
	for _, verb := range []string{"DONE", "PR:", "ISSUE:", "DOC:", "NEXT:", "REPO:"} {
		if !strings.Contains(got, verb) {
			t.Errorf("preamble missing the %s verb", verb)
		}
	}
	// The one-writer rule has to be stated, or agents close their own issues.
	if !strings.Contains(got, "Do NOT close it") {
		t.Error("preamble must forbid the agent from closing the issue")
	}
}
