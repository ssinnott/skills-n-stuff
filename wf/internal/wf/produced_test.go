package wf

import (
	"context"
	"testing"
	"time"
)

// The ledger half of Apply's dual write: what a run produced, as typed
// bindings tagged with the run. The tracker half is tested next to it in
// apply_test.go, and both have to keep happening.

func kinds(bs Bindings) map[Kind]Binding {
	out := map[Kind]Binding{}
	for _, b := range bs {
		out[b.Kind] = b
	}
	return out
}

func TestApplyReturnsBindingsTaggedWithTheRun(t *testing.T) {
	q := newFakeQueue()
	transcript := "" +
		"REPO: /code/app\n" +
		"PR: https://example.test/pull/412 — Add the parser\n" +
		"ISSUE: https://example.test/issues/9 — Flaky test\n" +
		"DOC: notes/plan.md — The plan\n" +
		"DONE Landed the parser and wrote the plan up alongside it.\n"

	at := time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC)
	got, err := Apply(context.Background(), q, task(), ParseOutcomes(transcript),
		ApplyOptions{Transcript: transcript, Run: "run-1", Now: at})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !got.Closed {
		t.Fatalf("Apply() = %+v, want closed", got)
	}

	byKind := kinds(got.Bindings)
	for _, want := range []Kind{KindRepo, KindPR, KindIssue, KindDoc} {
		if _, ok := byKind[want]; !ok {
			t.Fatalf("no %s binding: %+v", want, got.Bindings)
		}
	}

	pr := byKind[KindPR]
	if pr.Ref != "https://example.test/pull/412" || pr.Label != "Add the parser" {
		t.Errorf("pr binding = %+v", pr)
	}
	if pr.Via != "run-1" {
		t.Errorf("pr Via = %q, want the run that opened it", pr.Via)
	}
	if pr.State != BindingLive {
		t.Errorf("pr state = %q, want live — wf saw it open and has not looked since", pr.State)
	}
	if pr.StateLabel() != "open" {
		t.Errorf("pr renders as %q, want open", pr.StateLabel())
	}
	if !pr.At.Equal(at) {
		t.Errorf("pr At = %v, want the pinned time", pr.At)
	}

	// The repo is context the run named, not something it produced, so it
	// reads as the task's own and renders above the runs rather than inside
	// one.
	if repo := byKind[KindRepo]; repo.Via != "" || repo.Ref != "/code/app" {
		t.Errorf("repo binding = %+v, want the task's own", repo)
	}

	// An unbound document is still wherever the agent wrote it, so nothing
	// claims it lives in a vault.
	if doc := byKind[KindDoc]; doc.Ref != "notes/plan.md" || doc.Get(MetaStore) != "" {
		t.Errorf("doc binding = %+v, want the reported path and no store", doc)
	}

	// And the tracker still carries all of it as flat metadata: publication
	// is one way, unchanged, and what kata's own surfaces render.
	if q.meta[RepoKey] != "/code/app" {
		t.Errorf("tracker repo metadata = %v", q.meta[RepoKey])
	}
	if len(PRsFromMeta(q.meta)) != 1 {
		t.Errorf("tracker PR metadata = %v", q.meta[PRsKey])
	}
	if len(IssuesFromMeta(q.meta)) != 1 {
		t.Errorf("tracker issue metadata = %v", q.meta[IssuesKey])
	}
}

func TestEscalatedRunStillReportsWhatItProduced(t *testing.T) {
	// An escalated run that still opened a PR left that trace, and losing it
	// would lose exactly the runs worth going back to.
	cases := map[string]string{
		"no DONE at all": "PR: https://example.test/pull/9 — Half a fix\n",
		"DONE with no evidence": "REPO: /code/app\n" +
			"ISSUE: https://example.test/issues/3 — Filed instead\n" +
			"DONE\n",
	}

	for name, transcript := range cases {
		t.Run(name, func(t *testing.T) {
			q := newFakeQueue()
			got, err := Apply(context.Background(), q, task(), ParseOutcomes(transcript),
				ApplyOptions{Transcript: transcript, Run: "run-1"})
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			if !got.Escalated {
				t.Fatalf("Apply() = %+v, want escalated", got)
			}
			if len(got.Bindings) == 0 {
				t.Fatal("an escalated run must still report what it produced")
			}
			for _, b := range got.Bindings {
				if b.Kind == KindRepo {
					continue
				}
				if b.Via != "run-1" {
					t.Errorf("%s binding Via = %q, want the run", b.Kind, b.Via)
				}
			}
		})
	}
}

func TestDocBindingNamesTheBoundLocation(t *testing.T) {
	// A binding into a worktree that gets disposed on the next line is a
	// link to nothing, so it names where the document actually ended up —
	// and only then claims the vault.
	bound := []BoundDoc{{Source: "plan.md", VaultPath: "Research/plan.md"}}
	outcomes := ParseOutcomes("DOC: plan.md — The plan\nDOC: scratch.md — Left in place\n")

	bs := runBindings(outcomes, bound, "run-1", time.Now().UTC())
	docs := bs.ByKind(KindDoc)
	if len(docs) != 2 {
		t.Fatalf("doc bindings = %+v, want both", docs)
	}
	if docs[0].Ref != "Research/plan.md" || docs[0].Get(MetaStore) != "vault" {
		t.Errorf("bound doc = %+v, want the vault path and the vault store", docs[0])
	}
	if docs[1].Ref != "scratch.md" || docs[1].Get(MetaStore) != "" {
		t.Errorf("unbound doc = %+v, want the reported path and no store", docs[1])
	}
}

func TestFollowOnBecomesATaskBinding(t *testing.T) {
	// The parent/child edge is a binding rather than a metadata string,
	// which is what lets a chain render as a chain.
	q := newFakeQueue()
	transcript := "PR: https://example.test/pull/1 — The change\n" +
		"NEXT: fix the flaky test in the parser suite\n" +
		"DONE Landed the change and split the flake out into its own task.\n"

	got, err := Apply(context.Background(), q, task(), ParseOutcomes(transcript),
		ApplyOptions{Transcript: transcript, Run: "run-1"})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	next := got.Bindings.ByKind(KindTask)
	if len(next) != 1 {
		t.Fatalf("task bindings = %+v, want the follow-on", next)
	}
	if next[0].Get(MetaRelation) != RelationNext {
		t.Errorf("relation = %q, want %q", next[0].Get(MetaRelation), RelationNext)
	}
	if next[0].Via != "run-1" {
		t.Errorf("follow-on Via = %q, want the run that proposed it", next[0].Via)
	}
	if next[0].Label != "fix the flaky test in the parser suite" {
		t.Errorf("follow-on label = %q", next[0].Label)
	}
}

func TestBindingsWithNoRunReadAsTheTasksOwn(t *testing.T) {
	// Empty Via is the honest record for work nothing dispatched — an
	// adopted PR, a note bound by hand — rather than a missing one.
	bs := runBindings(ParseOutcomes("PR: https://example.test/pull/1 — By hand\n"), nil, "", time.Now().UTC())
	if len(bs) != 1 || bs[0].Via != "" {
		t.Fatalf("bindings = %+v, want one carrying no run", bs)
	}
	if len(bs.From("")) != 1 {
		t.Error("a binding with no run must be findable as the task's own")
	}
}

func TestMachineLocalKindsAreTheOnesThatNeedAHost(t *testing.T) {
	local := map[Kind]bool{KindWorkspace: true, KindSession: true}
	for _, k := range []Kind{KindRepo, KindWorkspace, KindSession, KindPR, KindDoc, KindIssue, KindTask} {
		if got := k.MachineLocal(); got != local[k] {
			t.Errorf("%s.MachineLocal() = %v, want %v", k, got, local[k])
		}
	}
}
