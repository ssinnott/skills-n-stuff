package gc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/review"
	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

const actor = "wf@laptop"

// fixed is the clock every test sweeps against, so retention is arithmetic
// rather than a race with the wall clock.
var fixed = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// sweeper builds a GC over a temp ledger with every seam faked: no
// filesystem full of worktrees, no live processes, no git repository.
func sweeper(t *testing.T, present map[string]bool, alive map[int]bool) (*GC, *store.FileStore) {
	t.Helper()
	st := store.New(t.TempDir())
	return &GC{
		Store: st,
		Actor: actor,
		Exists: func(path string) bool {
			return present[path]
		},
		Alive: func(pid int) bool { return alive[pid] },
		Now:   func() time.Time { return fixed },
	}, st
}

func save(t *testing.T, st store.Store, rec wf.Record) {
	t.Helper()
	if err := st.Save(rec); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
}

// saveOld writes a record's file directly, keeping the Updated stamp it
// carries. Save always stamps Updated with the wall clock — which is right,
// since it records when the ledger was last written — so a record that has
// sat untouched for months can only be simulated by putting one on disk.
func saveOld(t *testing.T, st *store.FileStore, rec wf.Record) {
	t.Helper()
	if err := os.MkdirAll(st.Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Dir(), rec.ID+".json"), encoded, 0o644); err != nil {
		t.Fatal(err)
	}
}

func find(report Report, k Kind) (Finding, bool) {
	for _, f := range report.Findings {
		if f.Kind == k {
			return f, true
		}
	}
	return Finding{}, false
}

func TestReportingIsTheDefaultAndWritesNothing(t *testing.T) {
	g, st := sweeper(t, nil, nil)
	save(t, st, wf.Record{
		ID: "t1", Handle: "neck", Created: fixed.Add(-90 * 24 * time.Hour),
		Bindings: wf.Bindings{
			{Kind: wf.KindWorkspace, Ref: "/wt/neck", State: wf.BindingLive, Host: actor, At: fixed.Add(-90 * 24 * time.Hour)},
		},
	})
	before, err := st.Load("t1")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	report, err := g.Sweep(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}

	f, ok := find(report, WorkspaceMissing)
	if !ok {
		t.Fatalf("no workspace-missing finding in %+v", report.Findings)
	}
	if f.Repair != RepairMark || f.Done {
		t.Errorf("finding = %+v, want a repair that was reported and not done", f)
	}

	after, err := st.Load("t1")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if after.Bindings[0].State != wf.BindingLive {
		t.Errorf("state = %q, want it untouched by a reporting sweep", after.Bindings[0].State)
	}
	if !after.Updated.Equal(before.Updated) {
		t.Error("Updated moved; a bare `wf gc` must not write to the ledger at all")
	}
}

func TestFixMarksAMissingWorkspace(t *testing.T) {
	g, st := sweeper(t, nil, nil)
	save(t, st, wf.Record{ID: "t1", Created: fixed,
		Bindings: wf.Bindings{
			{Kind: wf.KindWorkspace, Ref: "/wt/gone", State: wf.BindingLive, Host: actor, At: fixed},
			{Kind: wf.KindSession, Ref: "/sessions/gone.jsonl", State: wf.BindingLive, Host: actor, At: fixed},
		},
	})

	report, err := g.Sweep(context.Background(), Options{Fix: true})
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if got := report.Repaired(); got != 2 {
		t.Errorf("Repaired() = %d, want the workspace and the session", got)
	}

	rec, err := st.Load("t1")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, b := range rec.Bindings {
		if b.State != wf.BindingMissing {
			t.Errorf("%s binding state = %q, want missing", b.Kind, b.State)
		}
	}
	if _, ok := find(report, SessionMissing); !ok {
		t.Error("no session-missing finding")
	}
}

func TestAPresentWorkspaceIsNotAFinding(t *testing.T) {
	g, st := sweeper(t, map[string]bool{"/wt/live": true}, nil)
	save(t, st, wf.Record{ID: "t1", Created: fixed,
		Bindings: wf.Bindings{{Kind: wf.KindWorkspace, Ref: "/wt/live", State: wf.BindingLive, Host: actor, At: fixed}}})

	report, err := g.Sweep(context.Background(), Options{Fix: true})
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if len(report.Findings) != 0 {
		t.Errorf("findings = %+v, want none", report.Findings)
	}
}

func TestAnotherHostsBindingIsNeverJudged(t *testing.T) {
	// os.Stat here proves nothing about a checkout on another machine, and
	// marking it missing would be the multi-host clobber the binding design
	// exists to stop.
	g, st := sweeper(t, nil, nil)
	saveOld(t, st, wf.Record{ID: "t1", Created: fixed.Add(-365 * 24 * time.Hour), Updated: fixed.Add(-365 * 24 * time.Hour),
		Bindings: wf.Bindings{
			{Kind: wf.KindWorkspace, Ref: "/wt/desktop", State: wf.BindingLive,
				Host: "wf@desktop", At: fixed.Add(-365 * 24 * time.Hour)},
		}})

	report, err := g.Sweep(context.Background(), Options{Delete: true})
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if len(report.Findings) != 0 {
		t.Errorf("findings = %+v, want none for another host's binding", report.Findings)
	}
	if _, err := st.Load("t1"); err != nil {
		t.Errorf("Load() error = %v; a record another host still owns must survive --delete", err)
	}
}

func TestStaleRecordNeedsBothRetentionAndDelete(t *testing.T) {
	old := fixed.Add(-90 * 24 * time.Hour)
	rec := wf.Record{ID: "t1", Handle: "neck", Created: old, Updated: old,
		Bindings: wf.Bindings{
			{Kind: wf.KindWorkspace, Ref: "/wt/neck", State: wf.BindingDisposed, Host: actor, At: old},
			{Kind: wf.KindPR, Ref: "https://github.com/acme/w/pull/1", State: wf.BindingMerged, At: old},
			{Kind: wf.KindQueue, Ref: "01M1S", State: wf.BindingLive, At: old},
		}}

	t.Run("inside the window it is not stale", func(t *testing.T) {
		g, st := sweeper(t, nil, nil)
		saveOld(t, st, rec)
		report, err := g.Sweep(context.Background(), Options{Before: 365 * 24 * time.Hour, Delete: true})
		if err != nil {
			t.Fatalf("Sweep() error = %v", err)
		}
		if _, ok := find(report, RecordStale); ok {
			t.Error("record inside the retention window reported stale")
		}
		if _, err := st.Load("t1"); err != nil {
			t.Errorf("Load() error = %v, want the record kept", err)
		}
	})

	t.Run("outside the window, reported but not deleted without the flag", func(t *testing.T) {
		g, st := sweeper(t, nil, nil)
		saveOld(t, st, rec)
		report, err := g.Sweep(context.Background(), Options{Before: 30 * 24 * time.Hour})
		if err != nil {
			t.Fatalf("Sweep() error = %v", err)
		}
		f, ok := find(report, RecordStale)
		if !ok {
			t.Fatalf("no record-stale finding in %+v", report.Findings)
		}
		if f.Done {
			t.Error("record deleted without --delete")
		}
		if _, err := st.Load("t1"); err != nil {
			t.Errorf("Load() error = %v, want the record still there", err)
		}
	})

	t.Run("--delete removes the record", func(t *testing.T) {
		g, st := sweeper(t, nil, nil)
		saveOld(t, st, rec)
		report, err := g.Sweep(context.Background(), Options{Before: 30 * 24 * time.Hour, Delete: true})
		if err != nil {
			t.Fatalf("Sweep() error = %v", err)
		}
		f, _ := find(report, RecordStale)
		if !f.Done || f.Err != nil {
			t.Fatalf("finding = %+v, want a completed delete", f)
		}
		if _, err := st.Load("t1"); err == nil {
			t.Error("Load() succeeded; want the record gone")
		}
	})
}

func TestAnUnknownPullRequestBlocksPruning(t *testing.T) {
	// Pruning on unknown is the same evidence-free guess as closing on one,
	// in a place nobody would notice it.
	old := fixed.Add(-90 * 24 * time.Hour)
	g, st := sweeper(t, nil, nil)
	saveOld(t, st, wf.Record{ID: "t1", Created: old, Updated: old,
		Bindings: wf.Bindings{
			{Kind: wf.KindPR, Ref: "https://github.com/acme/w/pull/1", At: old},
		}})

	report, err := g.Sweep(context.Background(), Options{Before: 30 * 24 * time.Hour, Delete: true})
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if _, ok := find(report, RecordStale); ok {
		t.Error("record with an unverified pull request reported stale")
	}
	if _, err := st.Load("t1"); err != nil {
		t.Errorf("Load() error = %v, want the record kept", err)
	}
}

// The line the whole package holds: gc removes ledger rows, never the things
// they point at.
func TestSweepNeverRemovesAnArtifact(t *testing.T) {
	root := t.TempDir()
	old := fixed.Add(-400 * 24 * time.Hour)

	worktree := filepath.Join(root, "worktrees", "neck")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(worktree, "plan.md")
	if err := os.WriteFile(doc, []byte("work"), 0o644); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(root, "worktrees", "nobody-claims-this")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}

	st := store.New(t.TempDir())
	g := &GC{
		Store: st, Actor: actor,
		WorktreeRoot: filepath.Join(root, "worktrees"),
		Exists:       func(path string) bool { _, err := os.Stat(path); return err == nil },
		Alive:        func(int) bool { return false },
		Now:          func() time.Time { return fixed },
	}
	saveOld(t, st, wf.Record{ID: "t1", Created: old, Updated: old,
		Bindings: wf.Bindings{
			// Disposed on paper, still on disk: Dispose is best effort.
			{Kind: wf.KindWorkspace, Ref: worktree, State: wf.BindingDisposed, Host: actor, At: old,
				Meta: map[string]string{wf.MetaBranch: "wf/neck"}},
			{Kind: wf.KindDoc, Ref: doc, At: old},
		}})

	report, err := g.Sweep(context.Background(), Options{Before: 30 * 24 * time.Hour, Delete: true})
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}

	if _, ok := find(report, WorkspaceLingering); !ok {
		t.Errorf("no workspace-lingering finding in %+v", report.Findings)
	}
	if f, ok := find(report, WorkspaceOrphan); !ok {
		t.Error("no workspace-orphan finding for an unclaimed checkout")
	} else if f.Repair != RepairNone {
		t.Errorf("orphan finding = %+v, want no repair — it may hold uncommitted work", f)
	}

	// The record may go; nothing it named may.
	for _, path := range []string{worktree, doc, orphan} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s is gone: %v — gc must never delete an artifact", path, err)
		}
	}
}

func TestDeadPaneInReviewState(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "review.json")
	if err := review.SaveState(statePath, review.State{
		Ref: "neck", PID: 4242, Port: 4980, URL: "http://localhost:4980",
	}); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}

	g := &GC{Store: store.New(t.TempDir()), Actor: actor, ReviewState: statePath,
		Alive: func(int) bool { return false },
		Now:   func() time.Time { return fixed }}

	report, err := g.Sweep(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if _, ok := find(report, PaneDead); !ok {
		t.Fatalf("no pane-dead finding in %+v", report.Findings)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Error("review.json was cleared by a reporting sweep")
	}

	if _, err := g.Sweep(context.Background(), Options{Fix: true}); err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Errorf("review.json survived --fix: %v", err)
	}
}

func TestLivePaneIsNotAFinding(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "review.json")
	if err := review.SaveState(statePath, review.State{Ref: "neck", PID: 7, URL: "http://localhost:1"}); err != nil {
		t.Fatalf("SaveState() error = %v", err)
	}
	g := &GC{Store: store.New(t.TempDir()), Actor: actor, ReviewState: statePath,
		Alive: func(pid int) bool { return pid == 7 },
		Now:   func() time.Time { return fixed }}

	report, err := g.Sweep(context.Background(), Options{Fix: true})
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if len(report.Findings) != 0 {
		t.Errorf("findings = %+v, want none for a running viewer", report.Findings)
	}
}

func TestDeadReviewBindingIsMarked(t *testing.T) {
	g, st := sweeper(t, nil, map[int]bool{})
	save(t, st, wf.Record{ID: "t1", Created: fixed,
		Bindings: wf.Bindings{{
			Kind: wf.KindReview, Ref: "http://localhost:4980", State: wf.BindingLive,
			Host: actor, At: fixed, Meta: map[string]string{wf.MetaPID: "4242"},
		}}})

	report, err := g.Sweep(context.Background(), Options{Fix: true})
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if _, ok := find(report, PaneDead); !ok {
		t.Fatalf("no pane-dead finding in %+v", report.Findings)
	}
	rec, err := st.Load("t1")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if rec.Bindings[0].State != wf.BindingDisposed {
		t.Errorf("state = %q, want disposed", rec.Bindings[0].State)
	}
}

func TestBranchesWithNoTask(t *testing.T) {
	g, st := sweeper(t, nil, nil)
	g.Repo = "/repo"
	g.Branches = func(context.Context, string) ([]string, error) {
		return []string{"main", "wf/neck-add-parser", "wf/orphaned", "feature/mine"}, nil
	}
	save(t, st, wf.Record{ID: "t1", Created: fixed,
		Bindings: wf.Bindings{{
			Kind: wf.KindWorkspace, Ref: "/wt/neck", State: wf.BindingDisposed, Host: actor, At: fixed,
			Meta: map[string]string{wf.MetaBranch: "wf/neck-add-parser"},
		}}})

	report, err := g.Sweep(context.Background(), Options{Delete: true})
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}

	var refs []string
	for _, f := range report.Findings {
		if f.Kind == BranchOrphan {
			refs = append(refs, f.Ref)
			if f.Repair != RepairNone {
				t.Errorf("branch finding = %+v, want report only — a branch is work", f)
			}
		}
	}
	if len(refs) != 1 || refs[0] != "wf/orphaned" {
		// main and feature/mine are nobody's business: only wf's own
		// namespace is claimed here.
		t.Errorf("orphan branches = %v, want only wf/orphaned", refs)
	}
}

func TestBranchLookupFailureCostsOneQueryNotTheSweep(t *testing.T) {
	g, st := sweeper(t, nil, nil)
	g.Repo = "/repo"
	g.Branches = func(context.Context, string) ([]string, error) {
		return nil, os.ErrNotExist
	}
	save(t, st, wf.Record{ID: "t1", Created: fixed,
		Bindings: wf.Bindings{{Kind: wf.KindWorkspace, Ref: "/wt/gone", State: wf.BindingLive, Host: actor, At: fixed}}})

	report, err := g.Sweep(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if _, ok := find(report, WorkspaceMissing); !ok {
		t.Error("the rest of the sweep was lost with the branch listing")
	}
}

func TestUnreadableLedgerFileIsReportedNotDeleted(t *testing.T) {
	dir := t.TempDir()
	st := store.New(dir)
	save(t, st, wf.Record{ID: "good", Created: fixed})
	bad := filepath.Join(dir, "tasks", "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	g := &GC{Store: st, Actor: actor, Now: func() time.Time { return fixed },
		Exists: func(string) bool { return true }, Alive: func(int) bool { return true }}

	report, err := g.Sweep(context.Background(), Options{Delete: true})
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	f, ok := find(report, LedgerUnreadable)
	if !ok {
		t.Fatalf("no ledger-unreadable finding in %+v", report.Findings)
	}
	if f.Repair != RepairNone {
		t.Errorf("finding = %+v, want no repair — wf cannot know what it would discard", f)
	}
	if _, err := os.Stat(bad); err != nil {
		t.Errorf("the unreadable file was removed: %v", err)
	}
	if report.Records != 1 {
		t.Errorf("Records = %d, want the readable sibling still counted", report.Records)
	}
}

func TestSweepNeedsALedger(t *testing.T) {
	if _, err := (&GC{}).Sweep(context.Background(), Options{}); err == nil {
		t.Fatal("Sweep() with no store = nil error")
	}
}
