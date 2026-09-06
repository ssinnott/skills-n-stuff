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
		ws("/w/old", 20, BindingSuperseded, "run-3"),
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

// The re-run bug this whole model exists to fix: a second run takes over
// without destroying what the first left on disk for a human to inspect.
func TestSupersedeKeepsTheNewRunAndDemotesTheOld(t *testing.T) {
	bs := Bindings{
		ws("/w/escalated", 10, BindingLive, "run-1"),
		ws("/w/retry", 20, BindingLive, "run-2"),
	}
	bs = bs.Supersede(KindWorkspace, "run-2")

	if bs[0].State != BindingSuperseded {
		t.Errorf("run-1 workspace state = %q, want superseded", bs[0].State)
	}
	if bs[0].Ref != "/w/escalated" {
		t.Error("superseding must not drop the older binding — it is the evidence")
	}
	if bs[1].State != BindingLive {
		t.Errorf("run-2 workspace state = %q, want live", bs[1].State)
	}
}

func TestSupersedeLeavesOtherKindsAlone(t *testing.T) {
	bs := Bindings{
		ws("/w/one", 10, BindingLive, "run-1"),
		{Kind: KindPR, Ref: "https://example/pr/1", At: at(10), State: BindingLive, Via: "run-1"},
	}
	bs = bs.Supersede(KindWorkspace, "run-2")
	if bs[1].State != BindingLive {
		t.Errorf("PR state = %q, want untouched live", bs[1].State)
	}
}

// Re-recording a PR is a state refresh, not a duplicate row.
func TestUpsertReplacesSameKindAndRef(t *testing.T) {
	bs := Bindings{{Kind: KindPR, Ref: "u/1", State: BindingLive, At: at(10)}}
	bs = bs.Upsert(Binding{Kind: KindPR, Ref: "u/1", State: BindingMerged, At: at(20)})

	if len(bs) != 1 {
		t.Fatalf("len = %d, want 1 — same kind and ref is one binding", len(bs))
	}
	if bs[0].State != BindingMerged {
		t.Errorf("state = %q, want merged", bs[0].State)
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

func TestRefsPreservesOrderAndIncludesDead(t *testing.T) {
	bs := Bindings{
		{Kind: KindPR, Ref: "u/1", At: at(10), State: BindingMerged},
		{Kind: KindPR, Ref: "u/2", At: at(20), State: BindingLive},
	}
	got := bs.Refs(KindPR)
	if len(got) != 2 || got[0] != "u/1" || got[1] != "u/2" {
		t.Fatalf("Refs = %v, want [u/1 u/2] — a merged PR is still evidence", got)
	}
}

func TestGetToleratesNilMeta(t *testing.T) {
	if got := (Binding{}).Get(MetaBranch); got != "" {
		t.Errorf("Get on nil Meta = %q, want empty", got)
	}
}

func TestRecordLatestRunAndProduced(t *testing.T) {
	ended := at(15)
	rec := Record{
		ID: "01ABC",
		Runs: []Run{
			{ID: "run-1", Workflow: "plan-to-pr", Started: at(10), Ended: &ended, Outcome: SessionEscalated},
			{ID: "run-2", Workflow: "plan-to-pr", Started: at(20)},
		},
		Bindings: Bindings{
			ws("/w/one", 10, BindingSuperseded, "run-1"),
			ws("/w/two", 20, BindingLive, "run-2"),
			{Kind: KindPR, Ref: "u/1", At: at(21), Via: "run-2"},
		},
	}

	latest, ok := rec.LatestRun()
	if !ok || latest.ID != "run-2" {
		t.Fatalf("LatestRun = %q, want run-2", latest.ID)
	}
	if latest.Done() {
		t.Error("run-2 has not ended; Done should be false")
	}
	if first, _ := rec.Run("run-1"); !first.Done() {
		t.Error("run-1 ended; Done should be true")
	}
	if got := len(rec.Produced("run-2")); got != 2 {
		t.Errorf("Produced(run-2) = %d, want 2", got)
	}
}

func TestQueueRef(t *testing.T) {
	rec := Record{Bindings: Bindings{
		{Kind: KindQueue, Ref: "01M1S", At: at(10), State: BindingLive,
			Meta: map[string]string{MetaBackend: "kata"}},
	}}
	ref, ok := rec.QueueRef()
	if !ok || ref != "01M1S" {
		t.Fatalf("QueueRef = %q (ok=%v), want 01M1S", ref, ok)
	}

	// A task that has not been filed is the case the whole standalone
	// identity decision exists for.
	if _, ok := (Record{ID: "01ABC"}).QueueRef(); ok {
		t.Error("an unfiled task must report no queue ref, not an empty one")
	}
}
