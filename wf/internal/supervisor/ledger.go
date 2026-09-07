package supervisor

// Recording a run in wf's own ledger: the run itself, and the bindings that
// only resolve on this machine. See DESIGN-slim.md.
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
func newRunID(now time.Time) string {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		// Collision guard, not security; the clock is a fine fallback.
		binary.BigEndian.PutUint32(suffix[:], uint32(now.UnixNano()))
	}
	return fmt.Sprintf("r%s-%x", now.UTC().Format("20060102T150405"), suffix)
}

// record applies fn to the task's ledger record under the store's per-id
// lock. A nil store means no ledger, a supported configuration: the run loop
// runs the same either way.
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
// stamping the actor that owns it: a worktree path or session file is
// meaningless on another host.
func (s *Supervisor) localBinding(kind wf.Kind, ref, runID string, at time.Time) wf.Binding {
	b := wf.Binding{Kind: kind, Ref: ref, State: wf.BindingLive, At: at, Via: runID}
	if b.MachineLocal() {
		b.Host = s.actor()
	}
	return b
}

// beginRun records the run before the agent has produced anything: the runs
// most worth inspecting are the ones that died, and a run recorded on
// completion is precisely the run that never gets recorded.
func (s *Supervisor) beginRun(task wf.Task, flow workflow.Workflow, runID string, started time.Time) {
	s.record(task, "record run", func(rec *wf.Record) {
		rec.Runs = append(rec.Runs, wf.Run{
			ID:       runID,
			Workflow: flow.Name,
			Profile:  flow.Profile,
			// The resolved model, not the workflow's blank.
			Model:   s.Config.ResolveModel(flow.Model),
			Host:    s.actor(),
			Started: started,
		})
	})
}

// recordWorkspace binds the checkout this run created, and supersedes rather
// than overwrites whatever the previous run left: the old binding stays
// recorded and findable, it just stops being current. The branch comes off
// the workspace itself rather than being re-derived from the task's title.
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

// recordSession binds the agent session this run spawned. Older sessions are
// deliberately *not* superseded: a previous run's session file still exists
// and `wf attach` still opens it, unlike a checkout, nothing about a second
// session makes the first stop being a real place to look.
func (s *Supervisor) recordSession(task wf.Task, runID string, session wf.SessionBinding) {
	b := s.localBinding(wf.KindSession, session.Path, runID, session.Started)
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

// endRun settles the run: it stamps when it ended and how. Shareable facts
// the run produced are published to the tracker by Apply, not settled here.
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
// purpose, distinct from one that vanished behind wf's back.
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
