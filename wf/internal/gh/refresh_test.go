package gh

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

func prBinding(url string, state wf.BindingState) wf.Binding {
	return wf.Binding{Kind: wf.KindPR, Ref: url, State: state, At: time.Now().UTC(), Via: "r1"}
}

func merged(url string) string {
	return `{"number":1,"state":"MERGED","title":"Landed","url":"` + url + `"}`
}

func TestRefreshMixedMergedAndNot(t *testing.T) {
	// The case the completion rule actually has to survive: one PR landed,
	// one is still open, one wf could not reach.
	const (
		a = "https://github.com/acme/widgets/pull/1"
		b = "https://github.com/acme/widgets/pull/2"
		c = "https://github.com/acme/widgets/pull/3"
	)
	s := &fakeSpawner{byNumber: map[string]fakeReply{
		"1": {stdout: merged(a)},
		"2": {stdout: `{"number":2,"state":"OPEN","title":"In flight","url":"` + b + `"}`},
		"3": {stderr: "gh: Not Found (HTTP 404)", err: errors.New("exit status 1")},
	}}

	rec := wf.Record{ID: "t1", Bindings: wf.Bindings{
		prBinding(a, wf.BindingLive),
		prBinding(b, wf.BindingLive),
		prBinding(c, wf.BindingLive),
	}}

	results := Refresh(context.Background(), Client{Spawner: s}, rec.Bindings)
	if len(results) != 3 {
		t.Fatalf("got %d results, want one per pull request binding", len(results))
	}
	if results[0].To != wf.BindingMerged || !results[0].Changed() {
		t.Errorf("first result = %+v, want a merge", results[0])
	}
	if results[1].To != wf.BindingLive || results[1].Changed() {
		t.Errorf("second result = %+v, want it left open", results[1])
	}
	if results[2].Err == nil {
		t.Fatal("third result carries no error; a 404 must not read as an answer")
	}
	if results[2].To != results[2].From {
		t.Errorf("third result moved from %q to %q; a failed lookup keeps the state it had",
			results[2].From, results[2].To)
	}

	Apply(&rec, results)
	states := []wf.BindingState{wf.BindingMerged, wf.BindingLive, wf.BindingLive}
	for i, want := range states {
		if got := rec.Bindings[i].State; got != want {
			t.Errorf("binding %d state = %q, want %q", i, got, want)
		}
	}

	completion := rec.PRCompletion()
	if completion.Complete() {
		t.Error("PRCompletion().Complete() = true with one open and one unverified pull request")
	}
	if completion.Merged != 1 || completion.Open != 2 {
		t.Errorf("completion = %+v, want one merged and two open", completion)
	}
}

func TestApplySkipsFailedLookups(t *testing.T) {
	// A binding that was live and could not be checked stays live: writing
	// "unknown" over it would lose the last thing wf actually saw.
	rec := wf.Record{ID: "t1", Bindings: wf.Bindings{prBinding("u", wf.BindingLive)}}
	Apply(&rec, []Result{{Ref: "u", From: wf.BindingLive, To: wf.BindingUnknown, Err: errors.New("boom")}})
	if got := rec.Bindings[0].State; got != wf.BindingLive {
		t.Errorf("state = %q, want it untouched by a failed lookup", got)
	}
}

func TestApplyRefreshesTheLabel(t *testing.T) {
	rec := wf.Record{ID: "t1", Bindings: wf.Bindings{prBinding("u", wf.BindingLive)}}
	rec.Bindings[0].Label = "old title"
	n := Apply(&rec, []Result{{Ref: "u", From: wf.BindingLive, To: wf.BindingMerged, Title: "new title"}})
	if n != 2 {
		t.Errorf("Apply() = %d changes, want the state and the label", n)
	}
	if rec.Bindings[0].Label != "new title" {
		t.Errorf("label = %q, want GitHub's current title", rec.Bindings[0].Label)
	}
}

func TestRefreshTouchesOnlyPullRequests(t *testing.T) {
	s := &fakeSpawner{stdout: merged("https://github.com/acme/widgets/pull/1")}
	bs := wf.Bindings{
		{Kind: wf.KindWorkspace, Ref: "/tmp/wt", State: wf.BindingLive},
		{Kind: wf.KindDoc, Ref: "Notes/plan.md"},
	}
	if got := Refresh(context.Background(), Client{Spawner: s}, bs); got != nil {
		t.Errorf("Refresh() = %v over a task with no pull requests, want nothing", got)
	}
	if s.calls != 0 {
		t.Errorf("spawned %d time(s) with no pull request to look up", s.calls)
	}
}

func TestRefreshRecordPersistsAndLeavesFailuresAlone(t *testing.T) {
	const url = "https://github.com/acme/widgets/pull/1"
	st := store.New(t.TempDir())
	rec := wf.Record{ID: "t1", Handle: "neck", Created: time.Now().UTC(),
		Bindings: wf.Bindings{prBinding(url, wf.BindingLive)}}
	if err := st.Save(rec); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// A failed refresh writes nothing.
	failing := Client{Spawner: &fakeSpawner{err: errors.New("exec: \"gh\": executable file not found in $PATH")}}
	got, results, err := RefreshRecord(context.Background(), failing, st, "t1")
	if err != nil {
		t.Fatalf("RefreshRecord() error = %v; a missing gh is a report, not a failure", err)
	}
	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("results = %+v, want one failed lookup", results)
	}
	if got.Bindings[0].State != wf.BindingLive {
		t.Errorf("state = %q, want the state it had", got.Bindings[0].State)
	}
	reloaded, err := st.Load("t1")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if reloaded.Bindings[0].State != wf.BindingLive {
		t.Errorf("persisted state = %q, want it unchanged on disk", reloaded.Bindings[0].State)
	}
	if !reloaded.Updated.IsZero() && reloaded.Updated.After(time.Now().Add(time.Hour)) {
		t.Errorf("Updated = %v, impossible", reloaded.Updated)
	}

	// A successful one does.
	ok := Client{Spawner: &fakeSpawner{stdout: merged(url)}}
	got, results, err = RefreshRecord(context.Background(), ok, st, "t1")
	if err != nil {
		t.Fatalf("RefreshRecord() error = %v", err)
	}
	if !results[0].Changed() {
		t.Fatalf("results = %+v, want a change", results)
	}
	if !got.PRCompletion().Complete() {
		t.Error("PRCompletion().Complete() = false after the only pull request merged")
	}
	reloaded, err = st.Load("t1")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if reloaded.Bindings[0].State != wf.BindingMerged {
		t.Errorf("persisted state = %q, want merged", reloaded.Bindings[0].State)
	}
}

func TestRefreshRecordDoesNotBumpUpdatedWhenNothingChanged(t *testing.T) {
	// `wf gc` reads Updated as "when this task last did anything", so a
	// nightly refresh that learned nothing must not reset the retention
	// clock on work that has been finished for months.
	const url = "https://github.com/acme/widgets/pull/1"
	st := store.New(t.TempDir())
	rec := wf.Record{ID: "t1", Created: time.Now().UTC(),
		Bindings: wf.Bindings{{Kind: wf.KindPR, Ref: url, Label: "Landed", State: wf.BindingMerged}}}
	if err := st.Save(rec); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	before, err := st.Load("t1")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if _, _, err := RefreshRecord(context.Background(),
		Client{Spawner: &fakeSpawner{stdout: merged(url)}}, st, "t1"); err != nil {
		t.Fatalf("RefreshRecord() error = %v", err)
	}

	after, err := st.Load("t1")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !after.Updated.Equal(before.Updated) {
		t.Errorf("Updated moved from %v to %v with nothing to record", before.Updated, after.Updated)
	}
}

func TestRefreshRecordNeedsALedger(t *testing.T) {
	if _, _, err := RefreshRecord(context.Background(), Client{}, nil, "t1"); err == nil {
		t.Fatal("RefreshRecord() with no store = nil error")
	}
}
