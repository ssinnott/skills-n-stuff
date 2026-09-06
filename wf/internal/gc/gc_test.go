package gc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

const actor = "wf@laptop"

// sweeper builds a GC over a temp ledger with the filesystem faked out.
func sweeper(t *testing.T, present map[string]bool) (*GC, store.Store) {
	t.Helper()
	st := store.New(t.TempDir())
	return &GC{
		Store:  st,
		Actor:  actor,
		Exists: func(path string) bool { return present[path] },
	}, st
}

func save(t *testing.T, st store.Store, rec wf.Record) {
	t.Helper()
	if err := st.Save(rec); err != nil {
		t.Fatalf("Save() error = %v", err)
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

func TestALiveWorkspaceIsNotReported(t *testing.T) {
	g, st := sweeper(t, map[string]bool{"/wt/live": true})
	save(t, st, wf.Record{ID: "t1", Created: time.Now(),
		Bindings: wf.Bindings{
			{Kind: wf.KindWorkspace, Ref: "/wt/live", State: wf.BindingLive, Host: actor, At: time.Now()},
		}})

	report, err := g.Sweep(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if len(report.Findings) != 0 {
		t.Errorf("findings = %+v, want none for a live workspace", report.Findings)
	}
}

func TestAMissingWorkspaceIsReportedNotWritten(t *testing.T) {
	g, st := sweeper(t, nil)
	save(t, st, wf.Record{ID: "t1", Handle: "neck", Created: time.Now(),
		Bindings: wf.Bindings{
			{Kind: wf.KindWorkspace, Ref: "/wt/gone", State: wf.BindingLive, Host: actor, At: time.Now()},
		}})
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
		t.Errorf("finding = %+v, want a repair reported and not done", f)
	}

	after, err := st.Load("t1")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if after.Bindings[0].State != wf.BindingLive {
		t.Errorf("state = %q, want it untouched by a bare `wf gc`", after.Bindings[0].State)
	}
	if !after.Updated.Equal(before.Updated) {
		t.Error("Updated moved; a bare `wf gc` must not write to the ledger at all")
	}
}

func TestAMissingSessionIsReported(t *testing.T) {
	g, st := sweeper(t, nil)
	save(t, st, wf.Record{ID: "t1", Created: time.Now(),
		Bindings: wf.Bindings{
			{Kind: wf.KindSession, Ref: "/sessions/gone.jsonl", State: wf.BindingLive, Host: actor, At: time.Now()},
		}})

	report, err := g.Sweep(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if _, ok := find(report, SessionMissing); !ok {
		t.Fatalf("no session-missing finding in %+v", report.Findings)
	}
}

func TestDeleteMarksAMissingWorkspaceWithoutDroppingTheRecord(t *testing.T) {
	g, st := sweeper(t, map[string]bool{"/wt/still-here": true})
	save(t, st, wf.Record{ID: "t1", Created: time.Now(),
		Bindings: wf.Bindings{
			// A second, still-live workspace keeps the record itself from
			// being droppable, so this isolates marking from pruning.
			{Kind: wf.KindWorkspace, Ref: "/wt/gone", State: wf.BindingLive, Host: actor, At: time.Now()},
			{Kind: wf.KindWorkspace, Ref: "/wt/still-here", State: wf.BindingLive, Host: actor, At: time.Now()},
		}})

	report, err := g.Sweep(context.Background(), Options{Delete: true})
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	f, ok := find(report, WorkspaceMissing)
	if !ok || !f.Done {
		t.Fatalf("finding = %+v, want workspace-missing marked done", f)
	}
	if _, ok := find(report, RecordStale); ok {
		t.Error("record-stale reported; the still-live workspace should block it")
	}

	rec, err := st.Load("t1")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, b := range rec.Bindings {
		want := wf.BindingLive
		if b.Ref == "/wt/gone" {
			want = wf.BindingMissing
		}
		if b.State != want {
			t.Errorf("%s state = %q, want %q", b.Ref, b.State, want)
		}
	}
}

func TestForeignHostBindingIsNeverMarkedAndBlocksDeletion(t *testing.T) {
	// os.Stat here proves nothing about a checkout on another machine, and
	// marking it missing would be the multi-host clobber the binding design
	// exists to stop.
	g, st := sweeper(t, nil)
	save(t, st, wf.Record{ID: "t1", Created: time.Now(),
		Bindings: wf.Bindings{
			{Kind: wf.KindWorkspace, Ref: "/wt/desktop", State: wf.BindingLive,
				Host: "wf@desktop", At: time.Now()},
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

func TestDeleteDropsARecordWithNothingLocalLeft(t *testing.T) {
	g, st := sweeper(t, nil)
	save(t, st, wf.Record{ID: "t1", Handle: "neck", Created: time.Now(),
		Bindings: wf.Bindings{
			{Kind: wf.KindWorkspace, Ref: "/wt/neck", State: wf.BindingDisposed, Host: actor, At: time.Now()},
			{Kind: wf.KindSession, Ref: "/sessions/neck.jsonl", State: wf.BindingDisposed, Host: actor, At: time.Now()},
			{Kind: wf.KindPR, Ref: "https://github.com/acme/w/pull/1", State: wf.BindingMerged, At: time.Now()},
		}})

	report, err := g.Sweep(context.Background(), Options{Delete: true})
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	f, ok := find(report, RecordStale)
	if !ok || !f.Done || f.Err != nil {
		t.Fatalf("finding = %+v, want record-stale marked done", f)
	}
	if _, err := st.Load("t1"); err == nil {
		t.Error("Load() succeeded; want the record gone")
	}
}

func TestBareRunNeverDeletes(t *testing.T) {
	g, st := sweeper(t, nil)
	save(t, st, wf.Record{ID: "t1", Created: time.Now(),
		Bindings: wf.Bindings{
			{Kind: wf.KindWorkspace, Ref: "/wt/gone", State: wf.BindingDisposed, Host: actor, At: time.Now()},
		}})

	report, err := g.Sweep(context.Background(), Options{})
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
}

func TestSweepNeverRemovesAnArtifact(t *testing.T) {
	root := t.TempDir()
	worktree := filepath.Join(root, "neck")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}

	g, st := sweeper(t, map[string]bool{worktree: true})
	save(t, st, wf.Record{ID: "t1", Created: time.Now(),
		Bindings: wf.Bindings{
			// Disposed on paper, still on disk: Dispose is best effort. gc
			// never removes it, only the ledger row that names it.
			{Kind: wf.KindWorkspace, Ref: worktree, State: wf.BindingDisposed, Host: actor, At: time.Now()},
		}})

	if _, err := g.Sweep(context.Background(), Options{Delete: true}); err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Errorf("worktree is gone: %v — gc must never delete an artifact", err)
	}
}

func TestUnreadableLedgerFileIsReportedNotDeleted(t *testing.T) {
	dir := t.TempDir()
	st := store.New(dir)
	save(t, st, wf.Record{ID: "good", Created: time.Now()})
	bad := filepath.Join(dir, "tasks", "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	g := &GC{Store: st, Actor: actor, Exists: func(string) bool { return true }}

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
