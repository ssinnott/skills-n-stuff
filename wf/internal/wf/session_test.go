package wf

import (
	"context"
	"strings"
	"testing"
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
