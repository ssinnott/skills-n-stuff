package wf

import (
	"testing"
	"time"
)

func at(min int) time.Time {
	return time.Date(2026, 1, 1, 0, min, 0, 0, time.UTC)
}

func ws(ref string, min int, state BindingState, via string) Binding {
	return Binding{Kind: KindWorkspace, Ref: ref, At: at(min), State: state, Via: via}
}

func TestCurrentPicksNewestLive(t *testing.T) {
	bs := Bindings{
		ws("/w/one", 10, BindingLive, "run-1"),
		ws("/w/two", 30, BindingLive, "run-2"),
		ws("/w/three", 20, BindingLive, "run-3"),
	}
	got, ok := bs.Current(KindWorkspace)
	if !ok || got.Ref != "/w/two" {
		t.Fatalf("Current = %q (ok=%v), want /w/two", got.Ref, ok)
	}
}

// A disposed checkout must not be handed to a caller as the one to look
// at, even when it is the most recent.
func TestCurrentSkipsDeadBindings(t *testing.T) {
	bs := Bindings{
		ws("/w/kept", 10, BindingLive, "run-1"),
		ws("/w/gone", 30, BindingDisposed, "run-2"),
		ws("/w/old", 20, BindingMissing, "run-3"),
	}
	got, ok := bs.Current(KindWorkspace)
	if !ok || got.Ref != "/w/kept" {
		t.Fatalf("Current = %q (ok=%v), want /w/kept", got.Ref, ok)
	}
}

// An unchecked binding is unproven, not absent: hiding it would make a
// freshly recorded worktree invisible until something stat'd it.
func TestUnknownStateCountsAsLive(t *testing.T) {
	bs := Bindings{ws("/w/fresh", 10, BindingUnknown, "run-1")}
	if got, ok := bs.Current(KindWorkspace); !ok || got.Ref != "/w/fresh" {
		t.Fatalf("Current = %q (ok=%v), want /w/fresh", got.Ref, ok)
	}
}

func TestCurrentAbsentKind(t *testing.T) {
	bs := Bindings{ws("/w/one", 10, BindingLive, "run-1")}
	if _, ok := bs.Current(KindPR); ok {
		t.Fatal("Current(KindPR) reported a binding that was never recorded")
	}
}

// The re-run case: a second run's checkout takes over as current without
// destroying what the first left on disk for a human to inspect. Which one
// is current is a matter of timestamp, answered at read time.
func TestRerunCheckoutIsCurrentWhileTheFirstStays(t *testing.T) {
	bs := Bindings{
		ws("/w/escalated", 10, BindingLive, "run-1"),
		ws("/w/retry", 20, BindingLive, "run-2"),
	}
	got, ok := bs.Current(KindWorkspace)
	if !ok || got.Ref != "/w/retry" {
		t.Fatalf("Current = %q, want the re-run's checkout", got.Ref)
	}
	if len(bs.Live(KindWorkspace)) != 2 {
		t.Error("the older checkout must stay recorded and live — it is the evidence")
	}
}

// Last answers "what did the most recent run leave", disposed or not: the
// branch a later run should pick up is on a checkout that is usually gone.
func TestLastIgnoresState(t *testing.T) {
	bs := Bindings{
		ws("/w/one", 10, BindingLive, "run-1"),
		ws("/w/two", 20, BindingDisposed, "run-2"),
	}
	got, ok := bs.Last(KindWorkspace)
	if !ok || got.Ref != "/w/two" {
		t.Fatalf("Last = %q, want /w/two even though it is disposed", got.Ref)
	}
	if _, ok := bs.Last(KindSession); ok {
		t.Error("Last reported a kind that was never recorded")
	}
}

// Re-recording a binding is a state refresh, not a duplicate row.
func TestUpsertReplacesSameKindAndRef(t *testing.T) {
	bs := Bindings{{Kind: KindWorkspace, Ref: "/w/one", State: BindingLive, At: at(10)}}
	bs = bs.Upsert(Binding{Kind: KindWorkspace, Ref: "/w/one", State: BindingDisposed, At: at(20)})

	if len(bs) != 1 {
		t.Fatalf("len = %d, want 1 — same kind and ref is one binding", len(bs))
	}
	if bs[0].State != BindingDisposed {
		t.Errorf("state = %q, want disposed", bs[0].State)
	}
}

func TestUpsertAppendsDistinctRef(t *testing.T) {
	bs := Bindings{{Kind: KindPR, Ref: "u/1", At: at(10)}}
	bs = bs.Upsert(Binding{Kind: KindPR, Ref: "u/2", At: at(20)})
	if len(bs) != 2 {
		t.Fatalf("len = %d, want 2", len(bs))
	}
}

// Same ref under a different kind is a different thing entirely: a repo
// path and a workspace path can coincide.
func TestUpsertDistinguishesKind(t *testing.T) {
	bs := Bindings{{Kind: KindRepo, Ref: "/code/app", At: at(10)}}
	bs = bs.Upsert(Binding{Kind: KindWorkspace, Ref: "/code/app", At: at(20)})
	if len(bs) != 2 {
		t.Fatalf("len = %d, want 2 — kind is part of identity", len(bs))
	}
}

func TestFromGroupsByRun(t *testing.T) {
	bs := Bindings{
		ws("/w/one", 10, BindingLive, "run-1"),
		{Kind: KindPR, Ref: "u/1", At: at(11), Via: "run-1"},
		{Kind: KindDoc, Ref: "d.md", At: at(20), Via: "run-2"},
		{Kind: KindPR, Ref: "u/adopted", At: at(5)},
	}
	if got := len(bs.From("run-1")); got != 2 {
		t.Errorf("From(run-1) = %d bindings, want 2", got)
	}
	if got := len(bs.From("run-2")); got != 1 {
		t.Errorf("From(run-2) = %d bindings, want 1", got)
	}
	if got := len(bs.From("")); got != 1 {
		t.Errorf("From(\"\") = %d, want 1 — a binding with no run is the task's own", got)
	}
}

func TestGetToleratesNilMeta(t *testing.T) {
	if got := (Binding{}).Get(MetaBranch); got != "" {
		t.Errorf("Get on nil Meta = %q, want empty", got)
	}
}

func TestRecordRunsAndWhatEachProduced(t *testing.T) {
	ended := at(15)
	rec := Record{
		ID: "01ABC",
		Runs: []Run{
			{ID: "run-1", Workflow: "repro", Started: at(10), Ended: &ended, Outcome: SessionEscalated},
			{ID: "run-2", Workflow: "plan-to-pr", Started: at(20)},
		},
		Bindings: Bindings{
			ws("/w/one", 10, BindingDisposed, "run-1"),
			ws("/w/two", 20, BindingLive, "run-2"),
			{Kind: KindSession, Ref: "s2.jsonl", At: at(21), Via: "run-2"},
		},
	}

	if !rec.Runs[0].Done() {
		t.Error("run-1 ended; Done should be true")
	}
	if rec.Runs[1].Done() {
		t.Error("run-2 has not ended; Done should be false")
	}
	if got := len(rec.Bindings.From("run-2")); got != 2 {
		t.Errorf("From(run-2) = %d, want 2", got)
	}
}

// Bindings recovered from flat metadata all carry a zero timestamp, so they
// all tie. Pinning last-wins because callers who need report order must use
// Live instead, and that choice is only safe if this one is predictable.
func TestCurrentTieBreaksToLastRecorded(t *testing.T) {
	bs := Bindings{
		{Kind: KindPR, Ref: "pr/first", State: BindingLive},
		{Kind: KindPR, Ref: "pr/second", State: BindingLive},
		{Kind: KindPR, Ref: "pr/third", State: BindingLive},
	}
	got, ok := bs.Current(KindPR)
	if !ok || got.Ref != "pr/third" {
		t.Fatalf("Current on untimestamped bindings = %q, want pr/third", got.Ref)
	}
	// And the ordering a report-order caller relies on is unchanged.
	if live := bs.Live(KindPR); live[0].Ref != "pr/first" {
		t.Errorf("Live[0] = %q, want pr/first — report order must survive", live[0].Ref)
	}
}
