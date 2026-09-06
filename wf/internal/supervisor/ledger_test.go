package supervisor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// What the ledger has to get right, against the fakes: a run exists before
// the agent produces anything, a re-run takes over without destroying its
// predecessor, and everything a run made points back at it.
//
// The real-git, real-kata versions of the same three properties live in
// integration_test.go. These are the ones that run everywhere.

// withLedger gives a supervisor a real FileStore over a temp directory —
// real because the thing under test is what lands on disk, and a fake store
// would only prove the supervisor calls it.
func withLedger(t *testing.T, s *Supervisor) store.Store {
	t.Helper()
	ledger := store.New(t.TempDir())
	s.Store = ledger
	return ledger
}

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
	s := newSupervisor(q, r, &fakeProvider{}, nil)
	ledger := withLedger(t, s)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := s.RunOnce(context.Background(), ""); err != nil {
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
			if run.Host != "wf-test" {
				t.Errorf("run host = %q, want the actor", run.Host)
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
	s := newSupervisor(q, r, p, nil)
	ledger := withLedger(t, s)

	result, err := s.RunOnce(context.Background(), "")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	rec := loadRecord(t, ledger, "01HZ")
	if len(rec.Runs) != 1 {
		t.Fatalf("runs = %+v, want the crashed run recorded", rec.Runs)
	}
	run := rec.Runs[0]
	if run.ID != result.Run {
		t.Errorf("run id = %q, want the dispatched %q", run.ID, result.Run)
	}
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

func TestRerunSupersedesTheCheckoutWithoutDestroyingIt(t *testing.T) {
	// The concrete bug the whole design exists to fix: a re-run used to
	// overwrite the single workspace key, taking with it the checkout an
	// escalated run had been deliberately kept on disk for a human to open.
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Twice-run work"})
	r := &fakeRunner{transcript: "I could not decide and stopped.\n"}
	p := &fakeProvider{}
	s := newSupervisor(q, r, p, nil)
	ledger := withLedger(t, s)

	first, err := s.RunOnce(context.Background(), "")
	if err != nil {
		t.Fatalf("first RunOnce() error = %v", err)
	}
	if !first.Applied.Escalated {
		t.Fatalf("Applied = %+v, want the first run escalated", first.Applied)
	}

	// The second dispatch names the task, the way `wf run --ref` does: an
	// escalated task is flagged for a human, so the queue will not offer it
	// up on its own.
	r.transcript = "PR: https://a/2 — The fix\nDONE Resolved the ambiguity and landed it.\n"
	second, err := s.RunOnce(context.Background(), "01HZ")
	if err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}
	if first.Run == second.Run {
		t.Fatal("a second dispatch must be its own run")
	}

	rec := loadRecord(t, ledger, "01HZ")
	if len(rec.Runs) != 2 {
		t.Fatalf("runs = %+v, want two", rec.Runs)
	}

	spaces := rec.Bindings.ByKind(wf.KindWorkspace)
	if len(spaces) != 2 {
		t.Fatalf("workspace bindings = %+v, want both checkouts recorded", spaces)
	}

	byRun := map[string]wf.Binding{}
	for _, b := range spaces {
		byRun[b.Via] = b
	}
	old, ok := byRun[first.Run]
	if !ok {
		t.Fatalf("the first run's checkout is gone: %+v", spaces)
	}
	if old.State != wf.BindingSuperseded {
		t.Errorf("first checkout state = %q, want superseded", old.State)
	}
	if old.Ref != p.spaces[0].path {
		t.Errorf("first checkout ref = %q, want %q — it must stay findable", old.Ref, p.spaces[0].path)
	}

	fresh, ok := byRun[second.Run]
	if !ok {
		t.Fatalf("the second run's checkout was not recorded: %+v", spaces)
	}
	if fresh.Ref == old.Ref {
		t.Error("the two runs must not share one checkout binding")
	}
	// The second run closed cleanly, so it tore its own checkout down and
	// the record says so. Nothing is live afterwards, which is the honest
	// answer rather than a missing one: the first checkout is still there,
	// it is simply no longer what anyone should be looking at.
	if fresh.State != wf.BindingDisposed {
		t.Errorf("second checkout state = %q, want disposed", fresh.State)
	}
	if !p.spaces[1].disposed {
		t.Error("a clean re-run should dispose its own checkout")
	}

	// The point of all of it: the escalated run's checkout is still on disk.
	if p.spaces[0].disposed {
		t.Error("the escalated run's checkout was destroyed by the re-run")
	}
}

func TestRerunTakesOverAsCurrentWhileTheFirstStays(t *testing.T) {
	// Two runs that both escalate leave two live checkouts, and "the
	// worktree" has to mean the newer one without the older having been
	// deleted to make that true.
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Stuck twice"})
	r := &fakeRunner{transcript: "I could not decide and stopped.\n"}
	p := &fakeProvider{}
	s := newSupervisor(q, r, p, nil)
	ledger := withLedger(t, s)

	first, err := s.RunOnce(context.Background(), "")
	if err != nil {
		t.Fatalf("first RunOnce() error = %v", err)
	}
	second, err := s.RunOnce(context.Background(), "01HZ")
	if err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}

	rec := loadRecord(t, ledger, "01HZ")
	current, ok := rec.Bindings.Current(wf.KindWorkspace)
	if !ok || current.Via != second.Run {
		t.Fatalf("Current(workspace) = %+v, want the second run's", current)
	}
	if len(rec.Bindings.Live(wf.KindWorkspace)) != 1 {
		t.Errorf("live workspaces = %+v, want exactly the current one",
			rec.Bindings.Live(wf.KindWorkspace))
	}

	// The first run's checkout is still recorded, still on disk, and still
	// reachable through the run that made it.
	produced := rec.Produced(first.Run).ByKind(wf.KindWorkspace)
	if len(produced) != 1 || produced[0].State != wf.BindingSuperseded {
		t.Fatalf("first run's workspace = %+v, want it kept and superseded", produced)
	}
	if p.spaces[0].disposed {
		t.Error("the first escalated checkout was destroyed")
	}
}

func TestEveryBindingARunProducedCarriesTheRun(t *testing.T) {
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Productive work"})
	r := &fakeRunner{transcript: "" +
		"REPO: /code/app\n" +
		"PR: https://example.test/pull/412 — Add the parser\n" +
		"ISSUE: https://example.test/issues/9 — Flaky test\n" +
		"DOC: notes/plan.md — The plan\n" +
		"NEXT: fix the flaky test\n" +
		"DONE Landed the parser and filed the flake separately.\n"}
	s := newSupervisor(q, r, &fakeProvider{}, nil)
	ledger := withLedger(t, s)

	result, err := s.RunOnce(context.Background(), "")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !result.Applied.Closed {
		t.Fatalf("Applied = %+v, want closed", result.Applied)
	}

	rec := loadRecord(t, ledger, "01HZ")
	produced := rec.Produced(result.Run)
	kinds := map[wf.Kind]wf.Binding{}
	for _, b := range produced {
		kinds[b.Kind] = b
	}
	for _, want := range []wf.Kind{wf.KindWorkspace, wf.KindSession, wf.KindPR, wf.KindIssue, wf.KindDoc, wf.KindTask} {
		if _, ok := kinds[want]; !ok {
			t.Errorf("no %s binding tagged with the run: %+v", want, produced)
		}
	}
	if pr := kinds[wf.KindPR]; pr.Ref != "https://example.test/pull/412" || pr.Label != "Add the parser" {
		t.Errorf("pr binding = %+v", pr)
	}
	if next := kinds[wf.KindTask]; next.Get(wf.MetaRelation) != wf.RelationNext {
		t.Errorf("follow-on binding = %+v, want relation next", next)
	}

	// The repo is context the run named rather than something it produced,
	// so it reads as the task's own and renders above the runs.
	repo, ok := rec.Bindings.Current(wf.KindRepo)
	if !ok || repo.Ref != "/code/app" {
		t.Fatalf("repo binding = %+v", repo)
	}
	if repo.Via != "" {
		t.Errorf("repo binding Via = %q, want the task's own", repo.Via)
	}

	// The tracker still carries every one of those facts as flat metadata.
	// Publication is one way and unchanged; the ledger is additional, not a
	// replacement.
	if q.meta("01HZ", wf.RepoKey) != "/code/app" {
		t.Errorf("tracker repo metadata = %v, want it still published", q.meta("01HZ", wf.RepoKey))
	}
	if q.meta("01HZ", wf.PRsKey) == nil {
		t.Error("tracker PR metadata was dropped")
	}
	if q.meta("01HZ", wf.IssuesKey) == nil {
		t.Error("tracker issue metadata was dropped")
	}
}

func TestWorkspaceBindingRecordsTheBranchAndTheHost(t *testing.T) {
	// The branch is recorded by the run that created it rather than
	// re-derived from the task's title, which is what made a rename orphan
	// the branch a run had already cut.
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Branchy work"})
	r := &fakeRunner{transcript: "PR: https://a/1 — x\nDONE Landed it and covered it.\n"}
	p := &fakeProvider{}
	s := newSupervisor(q, r, p, nil)
	ledger := withLedger(t, s)

	if _, err := s.RunOnce(context.Background(), ""); err != nil {
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
	if got := space.Get(wf.MetaRepo); got != "/repo" {
		t.Errorf("repo = %q", got)
	}
	if space.Host != "wf-test" {
		t.Errorf("host = %q — a checkout path is meaningless without one", space.Host)
	}
	// A clean run disposes the checkout, and the record says so rather than
	// leaving `wf review` to find out by stat-ing the directory.
	if space.State != wf.BindingDisposed {
		t.Errorf("state = %q, want disposed", space.State)
	}

	session, ok := rec.Bindings.Current(wf.KindSession)
	if !ok {
		t.Fatal("no session binding")
	}
	if session.Host != "wf-test" {
		t.Errorf("session host = %q", session.Host)
	}
	if got := session.Get(wf.MetaRunner); got != "fake" {
		t.Errorf("runner = %q, want the runner that actually ran", got)
	}

	// A PR URL resolves from any machine, so it carries no host at all.
	pr, ok := rec.Bindings.Current(wf.KindPR)
	if !ok {
		t.Fatal("no pr binding")
	}
	if pr.Host != "" {
		t.Errorf("pr host = %q, want none — a URL is not machine-local", pr.Host)
	}
}

func TestLedgerBindingsCarryRealTimestamps(t *testing.T) {
	// Bindings recovered from flat metadata all share a zero time, so every
	// one of them ties and Current falls back on "last recorded". A binding
	// written here never has to.
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Timed work"})
	r := &fakeRunner{transcript: "PR: https://a/1 — x\nDONE Landed it and covered it.\n"}
	s := newSupervisor(q, r, &fakeProvider{}, nil)
	ledger := withLedger(t, s)

	before := time.Now().UTC().Add(-time.Second)
	if _, err := s.RunOnce(context.Background(), ""); err != nil {
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

func TestQueueBindingNamesTheTrackerRow(t *testing.T) {
	// The tracker row is a binding, not the identity — which is what lets a
	// task exist before one does.
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Filed work"})
	r := &fakeRunner{transcript: "PR: https://a/1 — x\nDONE Landed it and covered it.\n"}
	s := newSupervisor(q, r, &fakeProvider{}, nil)
	ledger := withLedger(t, s)

	if _, err := s.RunOnce(context.Background(), ""); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	rec := loadRecord(t, ledger, "01HZ")
	if rec.Handle != "abc4" {
		t.Errorf("handle = %q", rec.Handle)
	}
	row, ok := rec.Bindings.Current(wf.KindQueue)
	if !ok {
		t.Fatal("no queue binding")
	}
	if row.Ref != "01HZ" || row.Label != "Filed work" {
		t.Errorf("queue binding = %+v", row)
	}
	if row.Via != "" {
		t.Errorf("queue binding Via = %q — filing work and doing it are different acts", row.Via)
	}
	if got := row.Get(wf.MetaBackend); got != "fake" {
		t.Errorf("backend = %q, want the queue's own name as a value", got)
	}
	if got := row.Get(wf.MetaShortID); got != "abc4" {
		t.Errorf("short id = %q — it cannot be derived, so it has to be recorded", got)
	}

	// The tracker id resolves the record, which is what a human types.
	found, err := ledger.Resolve("abc4")
	if err != nil {
		t.Fatalf("Resolve(abc4) error = %v", err)
	}
	if found.ID != "01HZ" {
		t.Errorf("Resolve(abc4) = %q", found.ID)
	}
}

func TestARenamedTaskRefreshesItsRowWithoutMovingTheRecord(t *testing.T) {
	// A title moves when a human renames the task; the row it names does
	// not. The record renders into a synced vault, so an unchanged task has
	// to re-render to the same bytes — which means the timestamp holds still
	// even when the label does not.
	task := wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Original title"}
	q := newQueue(task)
	r := &fakeRunner{transcript: "I stopped.\n"}
	s := newSupervisor(q, r, &fakeProvider{}, nil)
	ledger := withLedger(t, s)

	if _, err := s.RunOnce(context.Background(), ""); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	before, _ := loadRecord(t, ledger, "01HZ").Bindings.Current(wf.KindQueue)

	q.tasks["01HZ"].Title = "Renamed in the tracker"
	if _, err := s.RunOnce(context.Background(), "01HZ"); err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}

	rec := loadRecord(t, ledger, "01HZ")
	rows := rec.Bindings.ByKind(wf.KindQueue)
	if len(rows) != 1 {
		t.Fatalf("queue bindings = %+v, want one row, not one per run", rows)
	}
	if rows[0].Label != "Renamed in the tracker" {
		t.Errorf("label = %q, want the new title", rows[0].Label)
	}
	if !rows[0].At.Equal(before.At) {
		t.Errorf("At moved from %v to %v on a rename", before.At, rows[0].At)
	}

	// And the branch each run cut is still on its own binding, so a rename
	// cannot orphan either of them.
	for _, b := range rec.Bindings.ByKind(wf.KindWorkspace) {
		if b.Get(wf.MetaBranch) == "" {
			t.Errorf("workspace binding %q lost its branch", b.Ref)
		}
	}
}

func TestDispatchRunsWithNoLedgerAtAll(t *testing.T) {
	// Nothing in the loop may take a lifecycle decision from the ledger, so
	// having none must change nothing about how a run settles.
	q := newQueue(wf.Task{ID: "01HZ", ShortID: "abc4", Title: "Unrecorded work"})
	r := &fakeRunner{transcript: "PR: https://a/1 — x\nDONE Landed it and covered it.\n"}
	s := newSupervisor(q, r, &fakeProvider{}, nil)
	s.Store = nil

	result, err := s.RunOnce(context.Background(), "")
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if !result.Applied.Closed {
		t.Errorf("Applied = %+v, want closed", result.Applied)
	}
	if result.Run == "" {
		t.Error("a run still has an id even when nothing records it")
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
