// Package gc sweeps wf's ledger for bindings whose referents are gone.
//
// This is the thing bindings-with-lifecycle actually buy. "Which worktrees
// are dead", "which review panes are stale", "which branches belong to no
// task" used to be guesses over a directory listing; against a ledger they
// are queries, and the answers are checkable rather than plausible.
//
// Three rules shape everything here.
//
// **Reporting is the default and writing is not.** A bare `wf gc` opens no
// file for writing: it says what it found and stops. Repairs need --fix and
// deletions need --delete, because a sweep that quietly rewrote the ledger
// would be a tool nobody could safely run to *look*.
//
// **Deleting a record never deletes the artifact it points at.** A ledger
// entry is a reference plus a lifecycle, never content, so dropping one costs
// history and never work. A leftover checkout, an unmerged branch, a produced
// document: gc reports them and leaves them exactly where they are. Anything
// else would make retention a destructive operation over things wf does not
// own.
//
// **A binding recorded by another host is never judged here.** A worktree
// path is meaningless on another machine, so os.Stat on this one proves
// nothing about it — marking it missing would be the multi-host clobber the
// whole binding design exists to stop, in a new place. Those bindings are
// left alone, and a record still holding one is never prunable, because this
// host cannot know whether the work is finished.
package gc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/review"
	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// Kind names what a finding is about.
type Kind string

const (
	// WorkspaceMissing is a checkout recorded live whose directory is
	// gone — removed by hand, or by a `git worktree prune` wf never saw.
	WorkspaceMissing Kind = "workspace-missing"
	// WorkspaceLingering is a checkout wf recorded as disposed that is
	// still on disk: Dispose is best effort, and this is what its failures
	// look like afterwards.
	WorkspaceLingering Kind = "workspace-lingering"
	// WorkspaceOrphan is a directory under the worktree root that no
	// record claims. Reported, never removed: it is somebody's checkout,
	// possibly with uncommitted work in it.
	WorkspaceOrphan Kind = "workspace-orphan"
	// SessionMissing is a session file recorded live that is no longer
	// there, so `wf attach` would fail on it.
	SessionMissing Kind = "session-missing"
	// PaneDead is a review viewer recorded as running whose process is
	// not.
	PaneDead Kind = "pane-dead"
	// BranchOrphan is a wf-created branch no task's ledger mentions.
	// Reported only — a branch is work, and deleting one is deleting an
	// artifact.
	BranchOrphan Kind = "branch-orphan"
	// RecordStale is a whole ledger entry that points at nothing live and
	// has not moved inside the retention window.
	RecordStale Kind = "record-stale"
	// LedgerUnreadable is a task file that would not parse. It is a
	// finding rather than a failure for the reason List returns its
	// records anyway: one corrupt file must not cost its siblings.
	LedgerUnreadable Kind = "ledger-unreadable"
)

// Repair is what a sweep would do about a finding, if allowed.
type Repair string

const (
	// RepairNone is a finding wf reports and never acts on.
	RepairNone Repair = ""
	// RepairMark corrects a binding's recorded state — live to missing, a
	// dead pane to disposed. It removes nothing.
	RepairMark Repair = "mark"
	// RepairDelete removes a ledger record. Never the artifact.
	RepairDelete Repair = "delete"
)

// Finding is one thing the sweep noticed.
type Finding struct {
	Kind Kind
	// Task is the ledger id the finding belongs to, empty for findings
	// that belong to no record — an orphan directory, a stale review.json.
	Task string
	// Handle is the record's short name, for display.
	Handle string
	// Ref is the referent: a path, a branch, a url.
	Ref string
	// Detail is the human-facing "why".
	Detail string
	// Repair is what could be done; Done says whether this sweep did it.
	Repair Repair
	Done   bool
	// Err is a repair that was attempted and failed. A sweep reports these
	// and carries on: one unwritable record must not stop the others.
	Err error
}

// Report is a whole sweep.
type Report struct {
	Findings []Finding
	// Records is how many ledger entries were examined.
	Records int
}

// Count returns how many findings of a kind the sweep produced.
func (r Report) Count(k Kind) int {
	n := 0
	for _, f := range r.Findings {
		if f.Kind == k {
			n++
		}
	}
	return n
}

// Repaired counts the findings this sweep actually acted on.
func (r Report) Repaired() int {
	n := 0
	for _, f := range r.Findings {
		if f.Done {
			n++
		}
	}
	return n
}

// Options are one sweep's policy.
type Options struct {
	// Before is the retention window: a record is only prunable once
	// nothing on it has moved for this long. DESIGN-task.md names this as
	// the answer over a fixed cap, because bindings for a task closed six
	// months ago cost nothing to keep and something to read past — which is
	// a question about age, not about count.
	Before time.Duration
	// Fix allows state repairs: a live binding whose referent is gone
	// becomes missing, a dead pane's record is cleared. Removes nothing.
	Fix bool
	// Delete allows stale records to be removed, and implies Fix. It never
	// touches an artifact.
	Delete bool
}

// DefaultBefore is the retention window when none is given. Thirty days is
// long enough that a task someone is still going back to is never a
// candidate, and short enough that the window means something.
const DefaultBefore = 30 * 24 * time.Hour

// branchPrefix is the namespace wf creates branches under, matching
// workspace.Provider's default. Only these are ever reported as orphans: a
// branch outside it is a human's, and calling somebody's work garbage because
// wf has no record of it would be the guess this package replaces.
const branchPrefix = "wf/"

// GC sweeps a ledger. The seams — Exists, Alive, Branches, Now — are here so
// the whole sweep is testable without a filesystem full of worktrees, live
// processes or a git repository, which is the same reason internal/review
// puts difit behind a Spawner.
type GC struct {
	Store store.Store
	// Actor is this host's identity, matched against a binding's Host. A
	// binding stamped by another actor is never judged here.
	Actor string
	// ReviewState is the path to review.json, or empty to skip it.
	ReviewState string
	// WorktreeRoot is scanned for checkouts no record claims. Empty skips.
	WorktreeRoot string
	// Repo is the repository whose wf branches are cross-checked against
	// the ledger. Empty skips.
	Repo string

	// Exists reports whether a path is there. Defaults to os.Stat.
	Exists func(path string) bool
	// Alive reports whether a pid is running. Defaults to a signal-0 probe.
	Alive func(pid int) bool
	// Branches lists local branches in a repository. Defaults to git.
	Branches func(ctx context.Context, repo string) ([]string, error)
	// Now pins the clock, so retention is testable.
	Now func() time.Time
}

func (g *GC) exists(path string) bool {
	if g.Exists != nil {
		return g.Exists(path)
	}
	_, err := os.Stat(path)
	return err == nil
}

func (g *GC) alive(pid int) bool {
	if g.Alive != nil {
		return g.Alive(pid)
	}
	return processAlive(pid)
}

func (g *GC) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now().UTC()
}

// mine reports whether this host is entitled to judge a binding's referent.
// An unstamped binding predates host stamping or is portable; either way
// there is nobody else to attribute it to.
func (g *GC) mine(b wf.Binding) bool {
	return b.Host == "" || b.Host == g.Actor
}

// mark is one binding state repair, addressed the way the ledger addresses
// bindings: by kind and ref, so it re-finds its target under the lock rather
// than carrying an index into a record that may have moved.
type mark struct {
	kind  wf.Kind
	ref   string
	state wf.BindingState
}

// Sweep examines the ledger and, when the options allow, repairs it.
//
// It returns what it found even when some repairs failed: the point of a
// sweep is the report, and an unwritable record is one more thing to say
// rather than a reason to say nothing.
func (g *GC) Sweep(ctx context.Context, opts Options) (Report, error) {
	if g.Store == nil {
		return Report{}, errors.New("gc: no ledger configured")
	}
	if opts.Before <= 0 {
		opts.Before = DefaultBefore
	}
	if opts.Delete {
		// Deleting a record whose bindings still claim to be live would
		// report one story and act on another.
		opts.Fix = true
	}
	cutoff := g.now().Add(-opts.Before)

	var report Report

	recs, err := g.Store.List()
	if err != nil {
		var skip *store.SkipError
		if !errors.As(err, &skip) {
			return report, err
		}
		for _, s := range skip.Skipped {
			report.Findings = append(report.Findings, Finding{
				Kind: LedgerUnreadable, Ref: s.Path,
				// Deliberately not deletable, even under --delete: an
				// unreadable file is the one case where wf cannot know
				// what it would be throwing away.
				Detail: s.Err.Error(),
			})
		}
	}
	report.Records = len(recs)

	// Every workspace path and branch the ledger knows about, gathered
	// across all records — including other hosts' — because a checkout this
	// machine cannot see is still not an orphan.
	claimedPaths := map[string]bool{}
	claimedBranches := map[string]bool{}

	for _, rec := range recs {
		for _, b := range rec.Bindings.ByKind(wf.KindWorkspace) {
			claimedPaths[filepath.Clean(b.Ref)] = true
			if br := b.Get(wf.MetaBranch); br != "" {
				claimedBranches[br] = true
			}
		}
		report.Findings = append(report.Findings, g.sweepRecord(rec, opts, cutoff)...)
	}

	report.Findings = append(report.Findings, g.sweepPane(opts)...)
	report.Findings = append(report.Findings, g.sweepWorktreeRoot(claimedPaths)...)
	report.Findings = append(report.Findings, g.sweepBranches(ctx, claimedBranches)...)

	return report, nil
}

// sweepRecord examines one ledger entry and, when allowed, repairs it.
func (g *GC) sweepRecord(rec wf.Record, opts Options, cutoff time.Time) []Finding {
	var (
		findings []Finding
		marks    []mark
	)
	add := func(f Finding) {
		f.Task, f.Handle = rec.ID, rec.Handle
		findings = append(findings, f)
	}

	for _, b := range rec.Bindings {
		if !g.mine(b) {
			continue
		}
		switch b.Kind {
		case wf.KindWorkspace:
			switch {
			case b.IsLive() && !g.exists(b.Ref):
				add(Finding{Kind: WorkspaceMissing, Ref: b.Ref,
					Detail: "recorded " + label(b) + ", directory is gone", Repair: RepairMark})
				marks = append(marks, mark{b.Kind, b.Ref, wf.BindingMissing})
			case b.State == wf.BindingDisposed && g.exists(b.Ref):
				// Dispose removes the worktree and its branch on a best
				// effort basis, so this is what a failed teardown looks
				// like weeks later. Nothing is removed for it: the
				// directory may hold work nobody committed.
				add(Finding{Kind: WorkspaceLingering, Ref: b.Ref,
					Detail: "recorded disposed, still on disk"})
			}
		case wf.KindSession:
			if b.IsLive() && !g.exists(b.Ref) {
				add(Finding{Kind: SessionMissing, Ref: b.Ref,
					Detail: "session file is gone; wf attach would fail", Repair: RepairMark})
				marks = append(marks, mark{b.Kind, b.Ref, wf.BindingMissing})
			}
		case wf.KindReview:
			pid, _ := strconv.Atoi(b.Get(wf.MetaPID))
			if b.IsLive() && !g.alive(pid) {
				add(Finding{Kind: PaneDead, Ref: b.Ref,
					Detail: fmt.Sprintf("viewer pid %d is not running", pid), Repair: RepairMark})
				marks = append(marks, mark{b.Kind, b.Ref, wf.BindingDisposed})
			}
		}
	}

	// Prunability is judged against the record as it stands *after* this
	// sweep's repairs, because "the checkout is gone and the ledger now says
	// so" is the state a human running --delete is looking at.
	projected := rec
	projected.Bindings = applyMarks(cloneBindings(rec.Bindings), marks)

	stale := false
	if reason, ok := g.prunable(projected, cutoff); ok {
		stale = true
		add(Finding{Kind: RecordStale, Ref: rec.ID, Detail: reason, Repair: RepairDelete})
	}

	switch {
	case stale && opts.Delete:
		// The record goes whole, so its individual repairs would be writes
		// to a file about to be removed. They count as done because what a
		// mark is for — the ledger no longer claiming a referent that is
		// gone — is exactly what removing the record achieves.
		err := g.Store.Delete(rec.ID)
		markDone(findings, RepairDelete, err)
		markDone(findings, RepairMark, err)
	case opts.Fix && len(marks) > 0:
		err := g.Store.Update(rec.ID, func(r *wf.Record) error {
			r.Bindings = applyMarks(r.Bindings, marks)
			return nil
		})
		markDone(findings, RepairMark, err)
	}

	return findings
}

// markDone records the outcome of one repair across the findings that asked
// for it. A failure is attached rather than returned: the sweep reports
// everything it found, and a record it could not write is one of those
// things.
func markDone(findings []Finding, repair Repair, err error) {
	for i := range findings {
		if findings[i].Repair != repair {
			continue
		}
		findings[i].Done = err == nil
		findings[i].Err = err
	}
}

func cloneBindings(bs wf.Bindings) wf.Bindings {
	out := make(wf.Bindings, len(bs))
	copy(out, bs)
	return out
}

// applyMarks sets the recorded state of every binding a mark names.
func applyMarks(bs wf.Bindings, marks []mark) wf.Bindings {
	for _, m := range marks {
		for i := range bs {
			if bs[i].Kind == m.kind && bs[i].Ref == m.ref {
				bs[i].State = m.state
			}
		}
	}
	return bs
}

// prunable reports whether a record's ledger entry can go, and why.
//
// The rule is deliberately strict, because the cost of keeping a record is a
// few hundred bytes and the cost of dropping one is provenance nobody can
// reconstruct. Everything machine-local must be finished, every pull request
// must have settled one way or the other, nothing may be stamped by another
// host, and the whole thing must be older than the retention window.
//
// A pull request in an unknown state is what most often blocks this, and that
// is correct: `wf pr refresh` is what turns unknown into an answer, and
// pruning on unknown would be the evidence-free guess in a place where nobody
// would ever notice it.
//
// The queue binding is deliberately not consulted. A tracker row outlives the
// ledger entry by design — the tracker owns work state, the ledger owns
// bindings — so an open row is not a reason to keep machine-local records
// that point at nothing.
func (g *GC) prunable(rec wf.Record, cutoff time.Time) (string, bool) {
	for _, b := range rec.Bindings {
		if !g.mine(b) {
			if b.IsLive() {
				// Another machine says this is live and this one cannot
				// check. Not ours to collect.
				return "", false
			}
			continue
		}
		switch b.Kind {
		case wf.KindWorkspace, wf.KindSession, wf.KindReview:
			if b.IsLive() {
				return "", false
			}
		case wf.KindPR:
			if b.State != wf.BindingMerged && b.State != wf.BindingClosed {
				return "", false
			}
		}
	}

	last := lastActivity(rec)
	if last.IsZero() || !last.Before(cutoff) {
		return "", false
	}
	return fmt.Sprintf("nothing live; last activity %s", last.UTC().Format(time.RFC3339)), true
}

// lastActivity is the newest timestamp the *work* carries: when the task was
// filed, when each binding was recorded, when each run started and ended.
//
// Record.Updated is deliberately not among them, though it is the obvious
// candidate. It moves on every write, and a gc repair is a write — so
// counting it would mean `wf gc --fix` pushed every record it touched a full
// retention window into the future, and a sweep that marked a dead checkout
// missing could never then collect the record for it. Retention is about
// when the work last moved, not when the ledger was last edited.
//
// A record carrying no timestamps at all yields the zero time, which prunable
// reads as "no evidence of age" and refuses to collect.
func lastActivity(rec wf.Record) time.Time {
	newest := rec.Created
	bump := func(t time.Time) {
		if t.After(newest) {
			newest = t
		}
	}
	for _, b := range rec.Bindings {
		bump(b.At)
	}
	for _, run := range rec.Runs {
		bump(run.Started)
		if run.Ended != nil {
			bump(*run.Ended)
		}
	}
	return newest
}

// sweepPane checks the single live review viewer review.json records.
//
// This file is the one binding ledger wf kept honestly before there was a
// ledger, and it is still where a running difit is recorded — no KindReview
// binding is written today — so a sweep that only read task records would
// miss the dead pane that actually happens.
func (g *GC) sweepPane(opts Options) []Finding {
	if g.ReviewState == "" {
		return nil
	}
	state, err := review.LoadState(g.ReviewState)
	if err != nil {
		return []Finding{{Kind: LedgerUnreadable, Ref: g.ReviewState, Detail: err.Error()}}
	}
	if state.PID == 0 || g.alive(state.PID) {
		return nil
	}

	f := Finding{
		Kind: PaneDead, Ref: state.URL,
		Detail: fmt.Sprintf("difit pid %d for %s is not running", state.PID, state.Ref),
		// Clearing the record of a process that is already dead removes
		// nothing: the viewer is gone, and the remembered ports it also
		// holds are a convenience the next `wf review` rebuilds.
		Repair: RepairMark,
	}
	if state.Ref != "" {
		f.Handle = state.Ref
	}
	if opts.Fix {
		err := review.ClearState(g.ReviewState)
		f.Done, f.Err = err == nil, err
	}
	return []Finding{f}
}

// sweepWorktreeRoot reports checkouts under the worktree root that no record
// claims. They are never removed: an unclaimed directory is the one place
// uncommitted work hides, and "wf has no record of it" is not evidence that
// nobody wants it.
func (g *GC) sweepWorktreeRoot(claimed map[string]bool) []Finding {
	if g.WorktreeRoot == "" {
		return nil
	}
	entries, err := os.ReadDir(g.WorktreeRoot)
	if err != nil {
		// A root that does not exist yet is not a problem to report.
		return nil
	}
	var out []Finding
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path := filepath.Join(g.WorktreeRoot, e.Name())
		if claimed[filepath.Clean(path)] {
			continue
		}
		out = append(out, Finding{Kind: WorkspaceOrphan, Ref: path,
			Detail: "checkout under the worktree root that no task claims"})
	}
	return out
}

// sweepBranches reports wf-created branches no task's ledger mentions.
//
// Report only, in both directions. A branch is work — possibly the only copy
// of it — so deleting one is deleting an artifact, which this package never
// does. And the ledger is not authoritative about branches: one created
// before stage 3 recorded them has no binding to match, which is a reason to
// name the branch rather than to act on it.
func (g *GC) sweepBranches(ctx context.Context, claimed map[string]bool) []Finding {
	if g.Repo == "" {
		return nil
	}
	list := g.Branches
	if list == nil {
		list = GitBranches
	}
	branches, err := list(ctx, g.Repo)
	if err != nil {
		// Not being able to enumerate branches costs one query, not the
		// sweep.
		return nil
	}
	var out []Finding
	for _, b := range branches {
		if !strings.HasPrefix(b, branchPrefix) || claimed[b] {
			continue
		}
		out = append(out, Finding{Kind: BranchOrphan, Ref: b,
			Detail: "wf branch with no workspace binding in the ledger"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

// GitBranches lists local branch names in repo.
func GitBranches(ctx context.Context, repo string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "for-each-ref", "--format=%(refname:short)", "refs/heads")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list branches in %s: %w", repo, err)
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

// processAlive probes a pid without disturbing it. Signal 0 is the liveness
// check — the same one internal/review uses before killing a viewer — and a
// permission error counts as alive, because a process wf may not signal is
// still a process.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, os.ErrPermission)
}

// label renders a binding's state for a finding's detail, saying "unchecked"
// rather than nothing when no state was ever recorded.
func label(b wf.Binding) string {
	if s := b.StateLabel(); s != "" {
		return s
	}
	return "unchecked"
}
