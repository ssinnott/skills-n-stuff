package supervisor

import (
	"errors"
	"testing"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// What the ledger has to get right, against the fakes: a run exists before
// the agent produces anything, a later run picks up where the last one
// left off without destroying what it left, and everything a run made
// points back at it.
//
// The real-git versions of the same properties live in realgit_test.go,
// and the real-kata ones in integration_test.go. These run everywhere.

// withLedger gives a supervisor a real FileStore over a temp directory —
// real because the thing under test is what lands on disk, and a fake store
// would only prove the supervisor calls it.
func withLedger(t *testing.T, s *Supervisor) store.Store {
	t.Helper()
	ledger := store.New(t.TempDir())
	s.Store = ledger
	return ledger
}

// loadRecord finds a task's record by the tracker's own id — the only id
// there is, and the key the ledger files it under.
func loadRecord(t *testing.T, ledger store.Store, id string) wf.Record {
	t.Helper()
	rec, err := ledger.Load(id)
	if err != nil {
		t.Fatalf("no ledger record for %s: %v", id, err)
	}
	return rec
}

func TestRunIsRecordedBeforeTheAgentProducesAnything(t *testing.T) {
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Slow work"})
	release := make(chan struct{})
	r := &fakeRunner{transcript: "PR: https://a/1 — x\nDONE Landed it and covered it.\n", release: release}
	s := newSupervisor(q, r, &fakeProvider{}, basicFlows(t))
	ledger := withLedger(t, s)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := run(s, "01HZ"); err != nil {
			t.Errorf("RunOnce() error = %v", err)
		}
	}()

	// While the agent is still working the run must already be on the
	// record, unsettled — a run written down at completion is precisely the
	// run that never gets written down.
	deadline := time.After(2 * time.Second)
	for {
		rec, err := ledger.Load("01HZ")
		if err == nil && len(rec.Runs) == 1 {
			run := rec.Runs[0]
			if run.Started.IsZero() {
				t.Error("a spawned run must carry when it started")
			}
			if run.Done() {
				t.Error("a run still in flight must not be settled")
			}
			if run.Workflow != "work" {
				t.Errorf("run workflow = %q, want the recipe it runs under", run.Workflow)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("the run was not recorded while it was in flight")
		case <-time.After(5 * time.Millisecond):
		}
	}

	close(release)
	<-done

	rec := loadRecord(t, ledger, "01HZ")
	if len(rec.Runs) != 1 || !rec.Runs[0].Done() {
		t.Fatalf("runs = %+v, want one settled run", rec.Runs)
	}
	if rec.Runs[0].Outcome != wf.SessionDone {
		t.Errorf("outcome = %q, want done", rec.Runs[0].Outcome)
	}
}

func TestCrashedAgentStillLeavesARecordedRun(t *testing.T) {
	// The runs most worth inspecting are the ones that died. This one
	// produced nothing at all, so the record is the only thing that knows
	// it happened.
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Doomed work"})
	r := &fakeRunner{transcript: "boom\n", err: errors.New("process died")}
	p := &fakeProvider{}
	s := newSupervisor(q, r, p, basicFlows(t))
	ledger := withLedger(t, s)

	if _, err := run(s, "01HZ"); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	rec := loadRecord(t, ledger, "01HZ")
	if len(rec.Runs) != 1 {
		t.Fatalf("runs = %+v, want the crashed run recorded", rec.Runs)
	}
	run := rec.Runs[0]
	if !run.Done() || run.Outcome != wf.SessionFailed {
		t.Errorf("run = %+v, want settled as failed", run)
	}

	// Its session is attachable and its checkout is still live, which is the
	// whole reason a human would come looking.
	session, ok := rec.Bindings.Current(wf.KindSession)
	if !ok || session.Via != run.ID {
		t.Fatalf("session binding = %+v, want one from this run", session)
	}
	space, ok := rec.Bindings.Current(wf.KindWorkspace)
	if !ok || space.State != wf.BindingLive {
		t.Fatalf("workspace binding = %+v, want it live", space)
	}
	if !p.spaces[0].kept {
		t.Error("a crashed run must keep its checkout")
	}
}

func TestRerunContinuesInTheKeptCheckout(t *testing.T) {
	// An escalated run keeps its checkout because the state in it is what
	// a human, or the next run, picks up from. The re-run is handed that
	// branch and continues in that checkout; when it completes, the
	// checkout is disposed and the record says which run did so.
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Twice-run work"})
	r := &fakeRunner{transcript: "I could not decide and stopped.\n"}
	p := &fakeProvider{}
	s := newSupervisor(q, r, p, basicFlows(t))
	ledger := withLedger(t, s)

	first, err := run(s, "01HZ")
	if err != nil {
		t.Fatalf("first RunOnce() error = %v", err)
	}
	if !first.Applied.Escalated {
		t.Fatalf("Applied = %+v, want the first run escalated", first.Applied)
	}
	if p.spaces[0].disposed {
		t.Fatal("the escalated checkout was destroyed")
	}

	r.transcript = "PR: https://a/2 — The fix\nDONE Resolved the ambiguity and landed it.\n"
	second, err := run(s, "01HZ")
	if err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}
	if !second.Applied.Completed {
		t.Fatalf("Applied = %+v, want the re-run completed", second.Applied)
	}

	rec := loadRecord(t, ledger, "01HZ")
	if len(rec.Runs) != 2 {
		t.Fatalf("runs = %+v, want two", rec.Runs)
	}
	if p.hints[1] != p.spaces[0].branch {
		t.Errorf("re-run was handed %q, want the kept checkout's branch %q", p.hints[1], p.spaces[0].branch)
	}
	if len(p.spaces) != 1 {
		t.Fatalf("checkouts = %d, want the re-run to continue in the kept one", len(p.spaces))
	}

	// One checkout, now the second run's: the binding is keyed by path, so
	// continuing in it hands it to the run that finished the work.
	spaces := rec.Bindings.ByKind(wf.KindWorkspace)
	if len(spaces) != 1 {
		t.Fatalf("workspace bindings = %+v, want the one continued checkout", spaces)
	}
	if spaces[0].Via != rec.Runs[1].ID {
		t.Errorf("checkout Via = %q, want the re-run %q", spaces[0].Via, rec.Runs[1].ID)
	}
	if spaces[0].State != wf.BindingDisposed || !p.spaces[0].disposed {
		t.Errorf("checkout state = %q, want disposed once the re-run completed", spaces[0].State)
	}
	// Both sessions stay: each is a real transcript worth attaching to.
	if len(rec.Bindings.ByKind(wf.KindSession)) != 2 {
		t.Errorf("session bindings = %+v, want one per run", rec.Bindings.ByKind(wf.KindSession))
	}
}

func TestRerunAfterCompletionGetsItsOwnCheckoutOnTheSameBranch(t *testing.T) {
	// A completed run disposed its checkout but left the branch. The next
	// run, under whatever workflow, gets a new checkout of that branch —
	// and the first checkout stays recorded as disposed, under its run.
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Two steps"})
	r := &fakeRunner{transcript: "PR: https://a/1 — Step one\nDONE Opened the PR.\n"}
	p := &fakeProvider{}
	s := newSupervisor(q, r, p, basicFlows(t))
	ledger := withLedger(t, s)

	if _, err := run(s, "01HZ"); err != nil {
		t.Fatalf("first RunOnce() error = %v", err)
	}
	if _, err := run(s, "01HZ"); err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}

	rec := loadRecord(t, ledger, "01HZ")
	spaces := rec.Bindings.ByKind(wf.KindWorkspace)
	if len(spaces) != 2 {
		t.Fatalf("workspace bindings = %+v, want one per run", spaces)
	}
	if spaces[0].Ref == spaces[1].Ref {
		t.Error("the two runs must not share a checkout binding")
	}
	if spaces[0].Get(wf.MetaBranch) != spaces[1].Get(wf.MetaBranch) {
		t.Errorf("branches = %q and %q, want the second run on the first's branch",
			spaces[0].Get(wf.MetaBranch), spaces[1].Get(wf.MetaBranch))
	}
	for i, b := range spaces {
		if b.Via != rec.Runs[i].ID {
			t.Errorf("checkout %d Via = %q, want run %q", i, b.Via, rec.Runs[i].ID)
		}
		if b.State != wf.BindingDisposed {
			t.Errorf("checkout %d state = %q, want disposed", i, b.State)
		}
	}
}

func TestRerunTakesOverAsCurrentWhileTheFirstStays(t *testing.T) {
	// Two runs that both escalate: the second continued in the first's
	// kept checkout, so there is one live checkout and it is the current
	// one, reachable from the run that last worked in it.
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Stuck twice"})
	r := &fakeRunner{transcript: "I could not decide and stopped.\n"}
	p := &fakeProvider{}
	s := newSupervisor(q, r, p, basicFlows(t))
	ledger := withLedger(t, s)

	if _, err := run(s, "01HZ"); err != nil {
		t.Fatalf("first RunOnce() error = %v", err)
	}
	if _, err := run(s, "01HZ"); err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}

	rec := loadRecord(t, ledger, "01HZ")
	current, ok := rec.Bindings.Current(wf.KindWorkspace)
	if !ok || current.Via != rec.Runs[1].ID {
		t.Fatalf("Current(workspace) = %+v, want the second run's", current)
	}
	if current.State != wf.BindingLive || p.spaces[0].disposed {
		t.Error("the checkout must still be live after a second escalation")
	}
}

func TestEveryLocalBindingARunProducedCarriesTheRun(t *testing.T) {
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Productive work"})
	r := &fakeRunner{transcript: "" +
		"REPO: /code/app\n" +
		"PR: https://example.test/pull/412 — Add the parser\n" +
		"ISSUE: https://example.test/issues/9 — Flaky test\n" +
		"DOC: notes/plan.md — The plan\n" +
		"NEXT: fix the flaky test\n" +
		"DONE Landed the parser and filed the flake separately.\n"}
	s := newSupervisor(q, r, &fakeProvider{}, basicFlows(t))
	ledger := withLedger(t, s)

	result, err := run(s, "01HZ")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !result.Applied.Completed {
		t.Fatalf("Applied = %+v, want completed", result.Applied)
	}

	rec := loadRecord(t, ledger, "01HZ")
	produced := rec.Bindings.From(rec.Runs[0].ID)
	kinds := map[wf.Kind]wf.Binding{}
	for _, b := range produced {
		kinds[b.Kind] = b
	}
	// Only what is machine-local goes on the run: a workspace and a
	// session. PRs, issues, documents, repos and the NEXT follow-on are all
	// shareable and have no place in the ledger at all.
	for _, want := range []wf.Kind{wf.KindWorkspace, wf.KindSession} {
		if _, ok := kinds[want]; !ok {
			t.Errorf("no %s binding tagged with the run: %+v", want, produced)
		}
	}
	for _, unwanted := range []wf.Kind{wf.KindPR, wf.KindIssue, wf.KindDoc, wf.KindRepo} {
		if b, ok := kinds[unwanted]; ok {
			t.Errorf("%s is a shareable fact and must not be in the ledger: %+v", unwanted, b)
		}
	}

	// The tracker carries every one of those facts as flat metadata — their
	// only home — and a client reads them back through LoadBindings.
	if q.meta("01HZ", wf.RepoKey) != "/code/app" {
		t.Errorf("tracker repo metadata = %v, want it published", q.meta("01HZ", wf.RepoKey))
	}
	if q.meta("01HZ", wf.PRsKey) == nil {
		t.Error("tracker PR metadata was dropped")
	}
	if q.meta("01HZ", wf.IssuesKey) == nil {
		t.Error("tracker issue metadata was dropped")
	}
	if len(q.created) != 1 || q.created[0].RelatedTo != "01HZ" {
		t.Errorf("follow-on = %+v, want one related to the parent", q.created)
	}

	published := wf.LoadBindings(wf.Task{ID: "01HZ", Meta: q.tasks["01HZ"].Meta})
	if pr, ok := published.Current(wf.KindPR); !ok || pr.Ref != "https://example.test/pull/412" {
		t.Errorf("published pr binding = %+v", pr)
	}
}

func TestWorkspaceBindingRecordsTheBranch(t *testing.T) {
	// The branch is recorded by the run that created it rather than
	// re-derived from the task's title, which is what made a rename orphan
	// the branch a run had already cut.
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Branchy work"})
	r := &fakeRunner{transcript: "PR: https://a/1 — x\nDONE Landed it and covered it.\n"}
	p := &fakeProvider{}
	s := newSupervisor(q, r, p, basicFlows(t))
	ledger := withLedger(t, s)

	if _, err := run(s, "01HZ"); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	rec := loadRecord(t, ledger, "01HZ")
	spaces := rec.Bindings.ByKind(wf.KindWorkspace)
	if len(spaces) != 1 {
		t.Fatalf("workspace bindings = %+v", spaces)
	}
	space := spaces[0]
	if got := space.Get(wf.MetaBranch); got != "wf/abc4" {
		t.Errorf("branch = %q, want the one the checkout is actually on", got)
	}
	// A completed run disposes the checkout, and the record says so rather
	// than leaving `wf review` to find out by stat-ing the directory.
	if space.State != wf.BindingDisposed {
		t.Errorf("state = %q, want disposed", space.State)
	}

	session, ok := rec.Bindings.Current(wf.KindSession)
	if !ok {
		t.Fatal("no session binding")
	}
	if session.Get(wf.MetaSessionID) == "" {
		t.Error("session binding lost its id")
	}

	// A PR is shareable and resolves from any machine, so it has no place
	// in the ledger at all — it lives on the tracker, published by Apply.
	if _, ok := rec.Bindings.Current(wf.KindPR); ok {
		t.Error("a PR binding must not be in the ledger — it is a shareable fact")
	}
	pr, ok := wf.LoadBindings(wf.Task{ID: "01HZ", Meta: q.tasks["01HZ"].Meta}).Current(wf.KindPR)
	if !ok || pr.Ref != "https://a/1" {
		t.Fatalf("published pr binding = %+v", pr)
	}
}

func TestLedgerBindingsCarryRealTimestamps(t *testing.T) {
	// Bindings recovered from flat metadata all share a zero time, so every
	// one of them ties and Current falls back on "last recorded". A binding
	// written here never has to.
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Timed work"})
	r := &fakeRunner{transcript: "PR: https://a/1 — x\nDONE Landed it and covered it.\n"}
	s := newSupervisor(q, r, &fakeProvider{}, basicFlows(t))
	ledger := withLedger(t, s)

	before := time.Now().UTC().Add(-time.Second)
	if _, err := run(s, "01HZ"); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	after := time.Now().UTC().Add(time.Second)

	rec := loadRecord(t, ledger, "01HZ")
	if len(rec.Bindings) == 0 {
		t.Fatal("no bindings recorded")
	}
	for _, b := range rec.Bindings {
		if b.At.IsZero() {
			t.Errorf("%s binding %q has no timestamp", b.Kind, b.Ref)
			continue
		}
		if b.At.Before(before) || b.At.After(after) {
			t.Errorf("%s binding %q recorded at %v, outside the run", b.Kind, b.Ref, b.At)
		}
	}
}

func TestARenamedTaskDoesNotDisturbItsLedgerRecord(t *testing.T) {
	// The record is keyed by the tracker's id, which does not move when a
	// human renames the task — only kata's own row does, and wf never
	// mirrors a title into the ledger.
	task := wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Original title"}
	q := newQueue(task)
	r := &fakeRunner{transcript: "I stopped.\n"}
	s := newSupervisor(q, r, &fakeProvider{}, basicFlows(t))
	ledger := withLedger(t, s)

	if _, err := run(s, "01HZ"); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	q.tasks["01HZ"].Title = "Renamed in the tracker"
	if _, err := run(s, "01HZ"); err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}

	rec := loadRecord(t, ledger, "01HZ")
	if len(rec.Runs) != 2 {
		t.Fatalf("runs = %+v, want both dispatches on the one record", rec.Runs)
	}

	// And the branch the run cut is on its binding, so a rename cannot
	// orphan it.
	for _, b := range rec.Bindings.ByKind(wf.KindWorkspace) {
		if b.Get(wf.MetaBranch) == "" {
			t.Errorf("workspace binding %q lost its branch", b.Ref)
		}
	}
}

func TestDispatchRunsWithNoLedgerAtAll(t *testing.T) {
	// Nothing in a run may take a lifecycle decision from the ledger, so
	// having none must change nothing about how a run settles.
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Unrecorded work"})
	r := &fakeRunner{transcript: "PR: https://a/1 — x\nDONE Landed it and covered it.\n"}
	s := newSupervisor(q, r, &fakeProvider{}, basicFlows(t))
	s.Store = nil

	result, err := run(s, "01HZ")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !result.Applied.Completed {
		t.Errorf("Applied = %+v, want completed", result.Applied)
	}
}

func TestRunIDsAreUniqueWithinASecond(t *testing.T) {
	now := time.Now()
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := newRunID(now)
		if seen[id] {
			t.Fatalf("run id %q was minted twice from one instant", id)
		}
		seen[id] = true
	}
}
