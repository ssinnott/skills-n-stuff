// Package gc reports, and under --delete drops, ledger bindings whose
// referent no longer exists on this host. git owns worktrees and branches;
// wf review owns its pane.
package gc

import (
	"context"
	"errors"
	"os"

	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

type Kind string   // what a finding is about
type Repair string // what --delete does about a finding

const (
	WorkspaceMissing Kind   = "workspace-missing"
	SessionMissing   Kind   = "session-missing"
	RecordStale      Kind   = "record-stale"
	LedgerUnreadable Kind   = "ledger-unreadable"
	RepairNone       Repair = ""       // reported, never acted on
	RepairMark       Repair = "mark"   // a binding's state is corrected
	RepairDelete     Repair = "delete" // the whole record is removed
)

// Finding is one thing the sweep noticed; Report is a whole sweep.
type Finding struct {
	Kind        Kind
	Task        string // the record this belongs to, if any (the tracker's own id)
	Ref, Detail string
	Repair      Repair
	Done        bool  // whether this sweep carried the repair out
	Err         error // a repair attempted and failed
}

type Report struct {
	Findings []Finding
	Records  int // ledger entries examined
}

func (r Report) Repaired() int { // findings this sweep acted on
	n := 0
	for _, f := range r.Findings {
		if f.Done {
			n++
		}
	}
	return n
}

// Options: without Delete a sweep only reports.
type Options struct{ Delete bool }

// GC sweeps a ledger for dead machine-local bindings.
type GC struct {
	Store  store.Store
	Exists func(path string) bool // test seam over os.Stat
}

func (g *GC) exists(path string) bool {
	if g.Exists != nil {
		return g.Exists(path)
	}
	_, err := os.Stat(path)
	return err == nil
}

// Sweep examines every record and, under Delete, repairs it.
func (g *GC) Sweep(ctx context.Context, opts Options) (Report, error) {
	if g.Store == nil {
		return Report{}, errors.New("gc: no ledger configured")
	}
	var report Report
	recs, err := g.Store.List()
	if err != nil {
		var skip *store.SkipError
		if !errors.As(err, &skip) {
			return report, err
		}
		for _, s := range skip.Skipped {
			report.Findings = append(report.Findings,
				Finding{Kind: LedgerUnreadable, Ref: s.Path, Detail: s.Err.Error()})
		}
	}
	report.Records = len(recs)
	for _, rec := range recs {
		report.Findings = append(report.Findings, g.sweepRecord(rec, opts)...)
	}
	return report, nil
}

// sweepRecord marks dead workspace/session bindings, and reports the
// record droppable once nothing is still live.
func (g *GC) sweepRecord(rec wf.Record, opts Options) []Finding {
	var findings []Finding
	var missing []wf.Binding
	settled := true
	for _, b := range rec.Bindings {
		if b.Kind != wf.KindWorkspace && b.Kind != wf.KindSession {
			continue
		}
		state := b.State
		if b.IsLive() && !g.exists(b.Ref) {
			kind, detail := WorkspaceMissing, "recorded live, directory is gone"
			if b.Kind == wf.KindSession {
				kind, detail = SessionMissing, "session file is gone; wf attach would fail"
			}
			findings = append(findings, Finding{Kind: kind, Task: rec.ID,
				Ref: b.Ref, Detail: detail, Repair: RepairMark})
			missing, state = append(missing, b), wf.BindingMissing
		}
		if state != wf.BindingMissing && state != wf.BindingDisposed {
			settled = false
		}
	}
	drop := settled
	if drop {
		findings = append(findings, Finding{Kind: RecordStale, Task: rec.ID,
			Ref: rec.ID, Detail: "nothing live left on this host", Repair: RepairDelete})
	}
	switch {
	case !opts.Delete:
	case drop: // marks count as done: removing the record achieves the same thing
		err := g.Store.Delete(rec.ID)
		markDone(findings, RepairDelete, err)
		markDone(findings, RepairMark, err)
	case len(missing) > 0:
		err := g.Store.Update(rec.ID, func(r *wf.Record) error {
			for _, m := range missing {
				for i := range r.Bindings {
					if r.Bindings[i].Kind == m.Kind && r.Bindings[i].Ref == m.Ref {
						r.Bindings[i].State = wf.BindingMissing
					}
				}
			}
			return nil
		})
		markDone(findings, RepairMark, err)
	}
	return findings
}

// markDone records whether repair succeeded, across every finding for it.
func markDone(findings []Finding, repair Repair, err error) {
	for i := range findings {
		if findings[i].Repair == repair {
			findings[i].Done, findings[i].Err = err == nil, err
		}
	}
}
