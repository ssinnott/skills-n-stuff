package supervisor

// Recording a run in wf's own ledger.
//
// The dispatch loop publishes to the tracker exactly as it always has, and
// additionally writes what a run produced here as typed bindings. That dual
// write is the design's posture rather than a transitional state: the
// tracker is authoritative for *work state* — title, priority, open or
// closed, queue order — and every one of those stays a read against it. The
// ledger is authoritative for *bindings*, because a binding points at
// something that lives on one machine and is exactly as durable as the thing
// it points at. The two are disjoint by construction, so neither overwrites
// the other and publication never syncs back.
//
// Nothing in here may fail a run. Every binding is a reference to something
// independently verifiable, so a lost or unwritable ledger costs history and
// convenience but never work — and taking a lifecycle decision from it alone
// is what the design forbids outright. Failures are logged and the run
// carries on.

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
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

// taskRef pairs a queue task with the wf id its ledger record is filed
// under.
//
// It exists because that id is no longer free to compute. Finding the record
// a tracker row belongs to is a scan of the ledger, and a dispatch records
// five or six times, so the answer is resolved once when the run starts and
// carried through rather than asked again per write.
type taskRef struct {
	task wf.Task
	// id is empty only when there is no ledger to file under.
	id string
}

// ledgerRef finds the record this tracker row already belongs to, or mints
// the identity it is about to get.
//
// This is the inversion stage 4 is for: the record is no longer *keyed* by
// the tracker id, it is *found* by it — through the queue binding identify
// writes below, which is the same binding `wf task adopt` adds by hand. A row
// wf has never seen gets a fresh wf id, so the first dispatch of a queued
// task and `wf task new` produce records of exactly the same shape.
func (s *Supervisor) ledgerRef(task wf.Task) taskRef {
	if s.Store == nil {
		return taskRef{task: task}
	}
	if rec, err := s.Store.Resolve(task.ID); err == nil && namesRow(rec, task.ID) {
		return taskRef{task: task, id: rec.ID}
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		// Ambiguity or an unreadable ledger. Minting is still the right
		// move — nothing in the loop may take a lifecycle decision from the
		// ledger — but it is worth saying out loud, because the only way to
		// get here is a ledger that already holds two records for one row.
		s.logf("%s: resolve ledger record: %v", task.ShortID, err)
	}
	return taskRef{task: task, id: wf.NewTaskID(time.Now())}
}

// namesRow checks that a resolved record really is this tracker row's,
// rather than something Resolve's abbreviation pass reached for. Resolve is
// built for refs a human types and is deliberately tolerant; attaching a run
// to the wrong task is not a tolerable outcome, so the loose passes are
// filtered back out here.
func namesRow(rec wf.Record, id string) bool {
	if rec.ID == id {
		return true
	}
	for _, b := range rec.Bindings.ByKind(wf.KindQueue) {
		if b.Ref == id {
			return true
		}
	}
	return false
}

// record applies fn to the task's ledger record under the store's per-id
// lock, so a concurrent writer's work is not lost across the read and the
// write. A nil store means no ledger, which is a supported configuration:
// the run loop runs the same either way.
func (s *Supervisor) record(ref taskRef, what string, fn func(*wf.Record)) {
	if s.Store == nil || ref.id == "" {
		return
	}
	err := s.Store.Update(ref.id, func(rec *wf.Record) error {
		s.identify(rec, ref.task)
		fn(rec)
		return nil
	})
	if err != nil {
		s.logf("%s: %s: %v", ref.task.ShortID, what, err)
	}
}

// identify makes sure the record knows which task it is: a handle for
// display and a queue binding naming the tracker row.
//
// The label is refreshed and the timestamp is not. A title moves when a
// human renames the task; the row it names does not, and the note this
// record renders into lives in a synced vault where an unchanged task has to
// re-render to the same bytes.
func (s *Supervisor) identify(rec *wf.Record, task wf.Task) {
	if rec.Handle == "" {
		rec.Handle = task.ShortID
	}
	for i := range rec.Bindings {
		b := &rec.Bindings[i]
		if b.Kind == wf.KindQueue && b.Ref == task.ID {
			b.Label = task.Title
			return
		}
	}

	// The tracker row is a binding, not the identity. A task exists whether
	// or not a row does, and Via is empty because no run produced this one:
	// filing the work and doing it are different acts.
	b := wf.Binding{
		Kind: wf.KindQueue, Ref: task.ID, Label: task.Title,
		State: wf.BindingLive, At: time.Now().UTC(),
		Meta: map[string]string{wf.MetaBackend: s.Queue.Name()},
	}
	if task.ShortID != "" {
		// Recorded because it cannot be derived: kata builds its short id
		// from the ULID's last four characters, so no prefix rule finds it.
		b.Meta[wf.MetaShortID] = task.ShortID
	}
	rec.Bindings = append(rec.Bindings, b)
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
func (s *Supervisor) beginRun(ref taskRef, flow workflow.Workflow, runID string, started time.Time) {
	s.record(ref, "record run", func(rec *wf.Record) {
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
func (s *Supervisor) recordWorkspace(ref taskRef, runID string, space wf.Workspace, at time.Time) {
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

	s.record(ref, "record workspace", func(rec *wf.Record) {
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
func (s *Supervisor) recordSession(ref taskRef, runID string, session wf.SessionBinding) {
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

	s.record(ref, "record session", func(rec *wf.Record) {
		rec.Bindings = rec.Bindings.Upsert(b)
	})
}

// endRun settles the run and files everything it produced. The bindings come
// from Apply, which is what decides what a run made; they arrive already
// tagged with the run in Via, because provenance is the edge the flat
// metadata could not express.
func (s *Supervisor) endRun(ref taskRef, runID string, outcome wf.SessionOutcome, produced wf.Bindings, at time.Time) {
	s.record(ref, "settle run", func(rec *wf.Record) {
		for i := range rec.Runs {
			if rec.Runs[i].ID != runID {
				continue
			}
			ended := at
			rec.Runs[i].Ended = &ended
			rec.Runs[i].Outcome = outcome
		}
		for _, b := range produced {
			// Apply builds these without knowing which machine it is on,
			// so the host is stamped here rather than there. It matters
			// for a DOC: path no workflow bound into the vault: that file
			// is still sitting in a worktree about to be disposed, and
			// without a host it reads on another device as a path that
			// should be there.
			if b.Host == "" && b.MachineLocal() {
				b.Host = s.actor()
			}
			rec.Bindings = rec.Bindings.Upsert(b)
		}
	})
}

// disposedWorkspace records that this run's checkout was torn down on
// purpose, which is different from a checkout that vanished behind wf's back
// and different again from one that is still there. `wf review` learns this
// by stat-ing the directory; the record is what lets it be a query instead.
func (s *Supervisor) disposedWorkspace(ref taskRef, runID string, space wf.Workspace) {
	if space == nil {
		return
	}
	path := space.Path()
	s.record(ref, "record disposal", func(rec *wf.Record) {
		for i := range rec.Bindings {
			b := &rec.Bindings[i]
			if b.Kind == wf.KindWorkspace && b.Ref == path && b.Via == runID {
				b.State = wf.BindingDisposed
			}
		}
	})
}
