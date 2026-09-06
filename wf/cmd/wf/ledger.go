package main

// Resolving a ref the way a human types one, and reading the task behind it.
//
// kata's ULID is the task's only id now — wf mints nothing. A ref is
// whatever `kata show <ref>` accepts (the ULID or kata's own short id), so
// resolving one is a single call to the queue; the ULID it returns is also
// the key the ledger files its record under, so there is no second lookup
// and nothing to reconcile between two id spaces.

import (
	"context"
	"errors"

	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// resolved is one task: the tracker row kata returned, and wf's own ledger
// record for it — always present, empty when the ledger has never seen this
// task.
type resolved struct {
	Task   wf.Task
	Record wf.Record
}

// resolve finds the task a ref names and the ledger record beside it.
//
// The queue is asked first and is the only place asked: kata already accepts
// its own short ids as well as the ULID, so every ref form a human types
// resolves there. The ledger is then loaded by the ULID kata returned — a
// miss is not an error, since a task the ledger has never recorded a run or
// a local binding for is an ordinary, empty one.
func (a *app) resolve(ctx context.Context, ref string) (resolved, error) {
	task, err := a.queue.Get(ctx, ref)
	if err != nil {
		return resolved{}, err
	}
	rec, err := a.ledger.Load(task.ID)
	if err != nil {
		var notFound *store.NotFoundError
		if errors.As(err, &notFound) {
			return resolved{Task: task, Record: wf.Record{ID: task.ID}}, nil
		}
		return resolved{}, err
	}
	return resolved{Task: task, Record: rec}, nil
}
