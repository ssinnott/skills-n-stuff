package main

// Resolving a ref the way a human types one, and reading the task behind it.
//
// Stage 4 gives wf two id spaces: its own — a minted id and a short handle —
// and the tracker's. A ref can be any of the four, so every command that
// takes one comes through here rather than each deciding for itself which
// space to look in first.
//
// The ledger is asked first and the queue second. That is not a preference
// for local data; it is the only order that answers correctly, because a
// task wf minted has no tracker row to ask about, while a task the ledger
// has never seen is still fully described by its metadata. So: ledger hit
// wins, ledger miss falls through to the tracker, and a tracker row with no
// ledger record is turned into a record on the spot so that everything
// downstream renders one shape.

import (
	"context"
	"errors"
	"fmt"

	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// resolved is one task in whichever forms are actually available. Record is
// always populated; Task is the tracker row when there is one and this
// machine can reach it.
type resolved struct {
	Record wf.Record
	Task   wf.Task
	// Filed reports whether Task holds a real tracker row. A task with a
	// queue binding on a host where kata is unreachable is filed but not
	// loaded, and callers that render the tracker's own facts — priority,
	// labels, the lease — have to tell those apart.
	Filed bool
}

// resolve finds the task a ref names, across both id spaces.
//
// A ref that resolves in neither place reports the ledger's error rather
// than the tracker's, because the ledger is where a ref a human invented
// would have had to be recorded, and "no such task" from kata for a wf
// handle is a confusing thing to print.
func (a *app) resolve(ctx context.Context, ref string) (resolved, error) {
	rec, err := a.ledger.Resolve(ref)
	if err == nil {
		out := resolved{Record: rec}
		if queueRef, ok := rec.QueueRef(); ok {
			out.Filed = true
			// A tracker that is down or absent must not make a recorded
			// task unreadable: everything wf itself knows is in the record.
			if task, err := a.queue.Get(ctx, queueRef); err == nil {
				out.Task = task
			}
		}
		return out, nil
	}
	// Ambiguity is an answer, not a miss — falling through to the tracker
	// would silently resolve one of the candidates the ledger refused to
	// choose between.
	var ambiguous *store.AmbiguousError
	if errors.As(err, &ambiguous) {
		return resolved{}, err
	}

	task, queueErr := a.queue.Get(ctx, ref)
	if queueErr != nil {
		return resolved{}, fmt.Errorf("%w (and the queue: %v)", err, queueErr)
	}
	return resolved{Record: a.recordFromTask(task), Task: task, Filed: true}, nil
}

// recordFromTask projects a tracker row into a record, for a task the ledger
// never saw — one bound by an earlier release, or filed on another host.
//
// The bindings come out of the metadata reader, so they carry no run and no
// host: flat keys have nowhere to put either. That is the honest shape of
// what is known, and it is why the whole set lands as the task's own rather
// than under a fabricated run.
func (a *app) recordFromTask(task wf.Task) wf.Record {
	rec := wf.Record{
		ID:       task.ID,
		Handle:   task.ShortID,
		Title:    task.Title,
		Bindings: wf.LoadBindings(task),
	}
	row := wf.Binding{
		Kind: wf.KindQueue, Ref: task.ID, Label: task.Title,
		State: wf.BindingLive,
		Meta:  map[string]string{wf.MetaBackend: a.queue.Name()},
	}
	if task.ShortID != "" {
		row.Meta[wf.MetaShortID] = task.ShortID
	}
	rec.Bindings = append(wf.Bindings{row}, rec.Bindings...)
	return rec
}

// queueRef is the tracker ref to hand a queue verb for this task, and
// whether the task has one at all. Everything that talks to the tracker —
// dispatch, comments, state — needs the second half: a task wf minted and
// nobody filed has no row for any of them to act on, and saying so beats
// sending kata a wf handle it will not recognize.
func (r resolved) queueRef() (string, bool) { return r.Record.QueueRef() }

// needsQueue is the error every tracker-backed verb gives a task that has no
// row yet. It names the way out, because "adopt it" is not guessable.
func needsQueue(rec wf.Record) error {
	return fmt.Errorf("%s has no tracker row — file one and run: wf task adopt %s --queue <tracker-id>",
		rec.Ref(), rec.Ref())
}

// dispatchRef turns a ref a human typed into the tracker ref the dispatch
// loop works against.
//
// The translation exists because those are two different id spaces and the
// loop only speaks one of them: it leases, claims, sets state and applies
// outcomes through the Queue, so a task with no row has nothing for any of
// those verbs to act on. That is a real limit rather than an oversight —
// closing it means a Queue adapter over the ledger, which is what that seam
// was cut for and is not what this stage builds. Until then the error says
// so and names the fix.
func (a *app) dispatchRef(ctx context.Context, ref string) (string, error) {
	if ref == "" {
		// Queue-driven dispatch: nobody named anything, and the loop takes
		// the top of the ready queue exactly as it always has.
		return "", nil
	}
	found, err := a.resolve(ctx, ref)
	if err != nil {
		return "", err
	}
	queueRef, ok := found.queueRef()
	if !ok {
		return "", needsQueue(found.Record)
	}
	return queueRef, nil
}

// sessionFromMeta is the last place to look for a session: the tracker's own
// metadata, read directly.
//
// It is not redundant with what a.resolve already did. That path reads
// bindings off the task `kata show` returns, and this one asks `kata meta
// get` — two different responses from a backend whose wire format is
// undocumented, which is exactly the class of surprise DESIGN.md names as
// its standing risk. `wf attach` is the command where being wrong costs a
// human the session of a run that died, so it asks twice.
func (a *app) sessionFromMeta(ctx context.Context, found resolved) (wf.Binding, bool) {
	ref, filed := found.queueRef()
	if !filed {
		return wf.Binding{}, false
	}
	meta, err := a.queue.GetMeta(ctx, ref)
	if err != nil {
		return wf.Binding{}, false
	}
	// Only the metadata is in hand here, so the task is assembled around
	// it — LoadBindings reads nothing else.
	return wf.LoadBindings(wf.Task{ID: ref, Meta: meta}).Current(wf.KindSession)
}

// requireTask insists on a loaded tracker row, for the verbs that write to
// one. It splits the two ways that can fail, because they need different
// answers from the person reading them: a task nobody filed needs adopting,
// and a task the tracker cannot answer for right now needs kata running.
func (a *app) requireTask(found resolved) (wf.Task, error) {
	if !found.Filed {
		return wf.Task{}, needsQueue(found.Record)
	}
	if found.Task.ID == "" {
		ref, _ := found.queueRef()
		return wf.Task{}, fmt.Errorf("cannot reach %s in %s to read %s", ref, a.queue.Name(), found.Record.Ref())
	}
	return found.Task, nil
}
