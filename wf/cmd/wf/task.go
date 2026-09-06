package main

// wf task: the verbs that exist because a task is wf's own object.
//
// `new` is the one that carries the argument. Starting work and filing an
// issue are different acts, and until stage 4 they had to be the same one —
// so a task begins in the ledger, with no queue running and possibly none
// installed, and the tracker row arrives later through `adopt` as a binding
// rather than as the identity.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

const taskUsage = `wf task new "<title>" [--handle H]   create a task, no tracker needed
wf task adopt <ref> --queue <id>     attach a tracker row to a task
wf task list                         every task in the ledger`

func (a *app) cmdTask(ctx context.Context, args []string) (int, error) {
	positional := positionals(args)
	if len(positional) == 0 {
		return 1, errors.New(taskUsage)
	}
	switch positional[0] {
	case "new":
		return a.cmdTaskNew(args, positional)
	case "adopt":
		return a.cmdTaskAdopt(ctx, args, positional)
	case "list":
		return a.cmdTaskList(args)
	default:
		return 1, fmt.Errorf("unknown task verb %q\n\n%s", positional[0], taskUsage)
	}
}

// cmdTaskNew mints a task. Nothing here touches the queue — not even to ask
// whether one is reachable — which is the whole claim: `wf task new "fix the
// parser"` works with no kata running, and with none installed.
func (a *app) cmdTaskNew(args, positional []string) (int, error) {
	if len(positional) < 2 {
		return 1, errors.New(`wf task new "<title>"`)
	}
	title := strings.TrimSpace(strings.Join(positional[1:], " "))
	if title == "" {
		return 1, errors.New("wf task new: the title is empty")
	}

	handle, err := a.mintHandle(flagValue(args, "--handle"))
	if err != nil {
		return 1, err
	}

	rec := wf.Record{
		ID:      wf.NewTaskID(now()),
		Handle:  handle,
		Title:   title,
		Created: now().UTC(),
	}
	if err := a.ledger.Save(rec); err != nil {
		return 1, err
	}

	if hasFlag(args, "--json") {
		return 0, emit("record", a.recordToJSON(rec, wf.Task{}))
	}
	fmt.Printf("%s  %s\n", rec.Handle, rec.Title)
	fmt.Printf("id       %s\n", rec.ID)
	// Not `wf run`: dispatch writes through the tracker, so an unfiled task
	// is refused. Point at the step that actually comes next.
	fmt.Printf("   wf task adopt %s --queue <tracker-id>\n", rec.Handle)
	fmt.Printf("   wf bind %s <note.md>\n", rec.Handle)
	return 0, nil
}

// mintHandle settles on a handle nothing else in the ledger answers to.
//
// A chosen handle that is taken is an error rather than a silent suffix: the
// person typing it meant that word, and handing them `neck-2` because they
// could not see the collision is how two tasks end up looking alike in every
// list they appear in. A generated one just tries again — there is nothing to
// tell anyone about.
func (a *app) mintHandle(chosen string) (string, error) {
	if chosen != "" {
		if err := wf.ValidHandle(chosen); err != nil {
			return "", err
		}
		taken, err := a.handleTaken(chosen)
		if err != nil {
			return "", err
		}
		if taken {
			return "", fmt.Errorf("handle %q is already taken", chosen)
		}
		return chosen, nil
	}

	for attempt := 0; attempt < 8; attempt++ {
		candidate := wf.NewHandle()
		taken, err := a.handleTaken(candidate)
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
	}
	// Eight collisions in a row against a 900k-value alphabet is not a
	// crowded ledger, it is a broken one. Rather than loop, the task keeps
	// its id and goes without a handle: the id always resolves.
	return "", nil
}

// handleTaken asks for an exact match, deliberately not store.Resolve.
// Resolve is tolerant by design — it matches prefixes so a human can
// abbreviate — and a uniqueness check that inherits that tolerance would
// reject "neck" because "necklace" exists.
func (a *app) handleTaken(handle string) (bool, error) {
	recs, err := a.ledger.List()
	if err != nil {
		var skip *store.SkipError
		if !errors.As(err, &skip) {
			return false, err
		}
		// A record that will not parse might be the one holding this
		// handle. Refusing to mint on that basis would make one corrupt
		// file block every new task, so the miss is accepted and named.
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
	}
	for _, rec := range recs {
		if strings.EqualFold(rec.Handle, handle) {
			return true, nil
		}
	}
	return false, nil
}

// cmdTaskAdopt attaches a tracker row to a task that already exists.
//
// It adds a binding; it does not move the record, mint a second one, or
// touch the runs already on it. That is the difference between a tracker row
// being a task's identity and being one of the things bound to it — and it
// is what makes "start now, file it later" leave a history behind rather
// than start one over.
func (a *app) cmdTaskAdopt(ctx context.Context, args, positional []string) (int, error) {
	if len(positional) < 2 {
		return 1, errors.New("wf task adopt <ref> --queue <tracker-id>")
	}
	queueRef := flagValue(args, "--queue")
	if queueRef == "" {
		return 1, errors.New("wf task adopt: --queue <tracker-id> is required")
	}

	rec, err := a.ledger.Resolve(positional[1])
	if err != nil {
		return 1, err
	}

	// The row has to exist before it can be bound: adopting a typo would
	// record a binding that resolves to nothing and read as a filed task
	// forever after.
	row, err := a.queue.Get(ctx, queueRef)
	if err != nil {
		return 1, fmt.Errorf("look up %s in %s: %w", queueRef, a.queue.Name(), err)
	}

	// A row already bound elsewhere is refused rather than duplicated. Two
	// records naming one row is the state that makes every later resolution
	// ambiguous, and it is cheaper to refuse here than to reconcile later.
	if owner, err := a.owningRecord(row.ID); err != nil {
		return 1, err
	} else if owner != "" && owner != rec.ID {
		return 1, fmt.Errorf("%s is already bound to task %s", row.ShortID, owner)
	}

	updated := rec
	err = a.ledger.Update(rec.ID, func(r *wf.Record) error {
		r.Bindings = r.Bindings.Upsert(wf.Binding{
			Kind: wf.KindQueue, Ref: row.ID, Label: row.Title,
			State: wf.BindingLive, At: now().UTC(),
			Meta: queueMeta(a.queue.Name(), row.ShortID),
		})
		updated = *r
		return nil
	})
	if err != nil {
		return 1, err
	}

	if hasFlag(args, "--json") {
		return 0, emit("record", a.recordToJSON(updated, row))
	}
	fmt.Printf("adopted %s ← %s %s\n", updated.Ref(), a.queue.Name(), row.ShortID)
	return 0, nil
}

func queueMeta(backend, shortID string) map[string]string {
	meta := map[string]string{wf.MetaBackend: backend}
	if shortID != "" {
		// Recorded because it cannot be derived: kata builds its short id
		// from the ULID's last four characters, so no prefix rule finds it.
		meta[wf.MetaShortID] = shortID
	}
	return meta
}

// owningRecord returns the wf id of the task already bound to a tracker row,
// or empty when none is.
func (a *app) owningRecord(queueRef string) (string, error) {
	recs, err := a.ledger.List()
	if err != nil {
		var skip *store.SkipError
		if !errors.As(err, &skip) {
			return "", err
		}
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
	}
	for _, rec := range recs {
		for _, b := range rec.Bindings.ByKind(wf.KindQueue) {
			if b.Ref == queueRef {
				return rec.ID, nil
			}
		}
	}
	return "", nil
}

func (a *app) cmdTaskList(args []string) (int, error) {
	recs, err := a.ledger.List()
	if err != nil {
		var skip *store.SkipError
		if !errors.As(err, &skip) {
			return 1, err
		}
		fmt.Fprintf(os.Stderr, "warning: %v\n", err)
	}

	if hasFlag(args, "--json") {
		// "records", never "tasks": `wf ready --json` already emits tasks
		// and they are tracker rows, not wf's own object. One key, one
		// shape, or a client has to guess which it got.
		out := make([]jsonRecord, 0, len(recs))
		for _, rec := range recs {
			out = append(out, a.recordToJSON(rec, wf.Task{}))
		}
		return 0, emit("records", out)
	}
	if len(recs) == 0 {
		fmt.Println("no tasks in the ledger")
		return 0, nil
	}
	for _, rec := range recs {
		filed := "unfiled"
		if ref, ok := rec.QueueRef(); ok {
			filed = ref
			if b, found := rec.Bindings.Current(wf.KindQueue); found && b.Get(wf.MetaShortID) != "" {
				filed = b.Get(wf.MetaShortID)
			}
		}
		fmt.Printf("%-8s %-12s %s%s\n", rec.Ref(), filed, rec.Name(), runsSuffix(rec))
	}
	return 0, nil
}

func runsSuffix(rec wf.Record) string {
	if len(rec.Runs) == 0 {
		return ""
	}
	return fmt.Sprintf("  (%d run(s))", len(rec.Runs))
}

// age renders how long ago something happened, in the one unit that matters
// at that distance. A task list is scanned, not read, so "2h" carries the
// whole answer and "2h13m47s" costs the column it takes.
func age(t time.Time, ref time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := ref.Sub(t)
	switch {
	case d < 0:
		return "just now"
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
