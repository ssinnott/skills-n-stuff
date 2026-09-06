package supervisor

// Recording a run in wf's own ledger.
//
// A fact lives in exactly one place. The tracker is authoritative for work
// state — title, priority, open or closed, queue order — and for every
// shareable fact a run makes: repo, PRs, filed issues, produced documents.
// Apply publishes those, and LoadBindings reads them back from the same
// place. The ledger holds what the tracker cannot: the run itself, and the
// bindings that only resolve on this machine — a workspace checkout, an
// agent session. Nothing here is also written to the tracker, and nothing
// the tracker holds is copied back into here.
//
// Nothing in here may fail a run. Every binding is a reference to something
// independently verifiable, so a lost or unwritable ledger costs history and
// convenience but never work — and taking a lifecycle decision from it alone
// is what the design forbids outright. Failures are logged and the run
// carries on.

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
	"github.com/ssinnott/skills-n-stuff/wf/internal/workflow"
)

// newRunID mints an id for one dispatch: a sortable UTC stamp, so a record
// read with jq lists its runs in order, plus random bytes so two dispatches
// in the same second do not collide.
//
// Deliberately not a ULID and not whatever wf ends up minting for tasks — a
// run id only has to be unique within one task's record and stable enough to
// point a binding at.
func newRunID(now time.Time) string {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		// The randomness is a collision guard, not a security property, so
		// the clock is a good enough fallback: two dispatches of one task
		// in the same nanosecond is not a case worth failing over.
		binary.BigEndian.PutUint32(suffix[:], uint32(now.UnixNano()))
	}
	return fmt.Sprintf("r%s-%x", now.UTC().Format("20060102T150405"), suffix)
}

// record applies fn to the task's ledger record under the store's per-id
// lock, so a concurrent writer's work is not lost across the read and the
// write. A nil store means no ledger, which is a supported configuration:
// the run loop runs the same either way. The record is keyed by the task's
// own id — kata's ULID — which is the only id space there is now: there is
// no ledger scan to find it and nothing to mint.
func (s *Supervisor) record(task wf.Task, what string, fn func(*wf.Record)) {
	if s.Store == nil || task.ID == "" {
		return
	}
	err := s.Store.Update(task.ID, func(rec *wf.Record) error {
		fn(rec)
		return nil
	})
	if err != nil {
		s.logf("%s: %s: %v", task.ShortID, what, err)
	}
}

// localBinding builds a binding whose referent lives on one machine,
// stamping the actor that owns it. A worktree path or a session file is
// meaningless on another host, so a laptop and a desktop working one queue
// need every such record to say whose it is rather than silently claiming to
// be the answer everywhere.
func (s *Supervisor) localBinding(kind wf.Kind, ref, runID string, at time.Time) wf.Binding {
	b := wf.Binding{Kind: kind, Ref: ref, State: wf.BindingLive, At: at, Via: runID}
	if b.MachineLocal() {
		b.Host = s.actor()
	}
	return b
}

// beginRun records the run before the agent has produced anything, for the
// same reason the session binding is written at spawn: the runs most worth
// inspecting are the ones that died, and a run recorded on completion is
// precisely the run that never gets recorded.
//
// The workflow, profile and model live on the run rather than the task
// because a task dispatched twice under two recipes has a history, not an
// overwritten field — which is also what makes "was the bigger model worth
// it here" a question with two rows to compare rather than one.
func (s *Supervisor) beginRun(task wf.Task, flow workflow.Workflow, runID string, started time.Time) {
	s.record(task, "record run", func(rec *wf.Record) {
		rec.Runs = append(rec.Runs, wf.Run{
			ID:       runID,
			Workflow: flow.Name,
			Profile:  flow.Profile,
			// The resolved model, not the workflow's blank: what ran is
			// the fact worth keeping.
			Model:   s.Config.ResolveModel(flow.Model),
			Host:    s.actor(),
			Started: started,
		})
	})
}

// recordWorkspace binds the checkout this run created, and retires whatever
// the previous run left.
//
// Superseding rather than overwriting is the concrete bug this whole shape
// exists to fix, though not in the direction the design first claimed. A
// re-run did not clobber the previous checkout: WorktreeName is derived from
// the task, so it computed the same directory, and Create refused an
// existing one — meaning a task could not be re-run *at all* while the
// checkout an escalated run had deliberately been kept was still on disk.
// Now the run gets its own -N checkout and the old binding stays recorded
// and findable; it simply stops being current.
//
// The branch comes off the workspace itself rather than being re-derived
// from the task's title, which is what made a renamed task orphan its own
// branch.
func (s *Supervisor) recordWorkspace(task wf.Task, runID string, space wf.Workspace, at time.Time) {
	b := s.localBinding(wf.KindWorkspace, space.Path(), runID, at)
	meta := map[string]string{}
	if branch := space.Branch(); branch != "" {
		meta[wf.MetaBranch] = branch
	}
	if repo := space.Repo(); repo != "" {
		meta[wf.MetaRepo] = repo
	}
	if len(meta) > 0 {
		b.Meta = meta
	}

	s.record(task, "record workspace", func(rec *wf.Record) {
		rec.Bindings = rec.Bindings.Supersede(wf.KindWorkspace, runID).Upsert(b)
	})
}

// recordSession binds the agent session this run spawned.
//
// Older sessions are deliberately *not* superseded. A previous run's session
// file still exists and `wf attach` still opens it, and unlike a checkout
// there is nothing about a second session that makes the first one stop
// being a real place to look. The reason the metadata reader has to retire
// them is that bindings recovered from flat keys all share a zero timestamp,
// so every one of them ties and only a recorded state can break it. Bindings
// written here carry the time they happened, so Current answers correctly
// without anyone asserting a lifecycle that did not occur.
func (s *Supervisor) recordSession(task wf.Task, runID string, session wf.SessionBinding) {
	b := s.localBinding(wf.KindSession, session.Path, runID, session.Started)
	// The runner is a value on the binding, never half of a key name: a
	// second runner is then a new value rather than a parallel set of keys
	// with a parallel set of readers.
	b.Meta = map[string]string{wf.MetaRunner: s.Runner.Name()}
	if session.ID != "" {
		b.Meta[wf.MetaSessionID] = session.ID
	}
	if session.Cwd != "" {
		b.Meta[wf.MetaCwd] = session.Cwd
	}

	s.record(task, "record session", func(rec *wf.Record) {
		rec.Bindings = rec.Bindings.Upsert(b)
	})
}

// endRun settles the run: it stamps when it ended and how. What the run
// produced is published to the tracker by Apply and read back from there —
// PRs, issues, documents and repos are shareable facts and have no ledger
// binding to settle here. recordWorkspace and recordSession already wrote
// this run's machine-local bindings at the point each was created.
func (s *Supervisor) endRun(task wf.Task, runID string, outcome wf.SessionOutcome, at time.Time) {
	s.record(task, "settle run", func(rec *wf.Record) {
		for i := range rec.Runs {
			if rec.Runs[i].ID != runID {
				continue
			}
			ended := at
			rec.Runs[i].Ended = &ended
			rec.Runs[i].Outcome = outcome
		}
	})
}

// disposedWorkspace records that this run's checkout was torn down on
// purpose, which is different from a checkout that vanished behind wf's back
// and different again from one that is still there. `wf review` learns this
// by stat-ing the directory; the record is what lets it be a query instead.
func (s *Supervisor) disposedWorkspace(task wf.Task, runID string, space wf.Workspace) {
	if space == nil {
		return
	}
	path := space.Path()
	s.record(task, "record disposal", func(rec *wf.Record) {
		for i := range rec.Bindings {
			b := &rec.Bindings[i]
			if b.Kind == wf.KindWorkspace && b.Ref == path && b.Via == runID {
				b.State = wf.BindingDisposed
			}
		}
	})
}
