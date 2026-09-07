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
// lock. A nil store means no ledger, a supported configuration: the run
// goes ahead the same either way.
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

// previousBranch is the branch the task's most recent checkout was on,
// whatever became of that checkout, so the next run can pick it up. Empty
// when the ledger has none.
func (s *Supervisor) previousBranch(task wf.Task) string {
	if s.Store == nil || task.ID == "" {
		return ""
	}
	rec, err := s.Store.Load(task.ID)
	if err != nil {
		return ""
	}
	last, ok := rec.Bindings.Last(wf.KindWorkspace)
	if !ok {
		return ""
	}
	return last.Get(wf.MetaBranch)
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
			Started: started,
		})
	})
}

// recordWorkspace binds the checkout this run works in. A run that
// continues in a checkout an earlier run kept takes it over: the binding
// is now this run's. A run whose checkout merely lands on a path an earlier,
// disposed checkout once had gets a binding of its own, so the earlier run
// keeps its history. Which checkout is current is a matter of timestamp and
// state, answered at read time by Bindings.Current. The branch comes off
// the workspace itself rather than being re-derived from the title.
func (s *Supervisor) recordWorkspace(task wf.Task, runID string, space wf.Workspace, at time.Time) {
	b := wf.Binding{Kind: wf.KindWorkspace, Ref: space.Path(), State: wf.BindingLive, At: at, Via: runID}
	if branch := space.Branch(); branch != "" {
		b.Meta = map[string]string{wf.MetaBranch: branch}
	}
	s.record(task, "record workspace", func(rec *wf.Record) {
		for i := range rec.Bindings {
			existing := &rec.Bindings[i]
			if existing.Kind == wf.KindWorkspace && existing.Ref == b.Ref && existing.IsLive() {
				*existing = b
				return
			}
		}
		rec.Bindings = append(rec.Bindings, b)
	})
}

// recordSession binds the agent session this run spawned. Older sessions
// stay live: a previous run's session file still exists and `wf attach`
// still opens it.
func (s *Supervisor) recordSession(task wf.Task, runID, sessionID, path string, at time.Time) {
	b := wf.Binding{Kind: wf.KindSession, Ref: path, State: wf.BindingLive, At: at, Via: runID}
	if sessionID != "" {
		b.Meta = map[string]string{wf.MetaSessionID: sessionID}
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
