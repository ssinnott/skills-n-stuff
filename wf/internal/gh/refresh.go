package gh

// Refreshing the pull request bindings on a task.
//
// The shape here is load, ask, then write: the lookups happen outside the
// ledger's per-id lock and only the resulting states go back in under it.
// Holding a file lock across a network round trip would make one slow
// GitHub call block every other writer on that task, and the store's Update
// is explicitly the place read-modify-write is safe — so the pattern is
// "collect answers, then apply them by (kind, ref)", which also means a
// binding another writer added in the meantime is left alone rather than
// clobbered by a stale copy of the record.

import (
	"context"
	"errors"
	"fmt"

	"github.com/ssinnott/skills-n-stuff/wf/internal/store"
	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// Result is one binding's refresh attempt.
type Result struct {
	// Ref is the pull request url, which is also what identifies the
	// binding to write back to.
	Ref string
	// From is the state the binding had; To is the state it has now. On a
	// failed lookup they are equal, because a binding whose state cannot
	// be refreshed keeps the state it had.
	From, To wf.BindingState
	// Title is what GitHub calls the PR, refreshed onto the binding's
	// label. Empty when the lookup failed.
	Title string
	// Err is why the lookup produced nothing. Never fatal: one PR wf
	// cannot reach must not stop it asking about the others.
	Err error
}

// Changed reports whether this refresh actually moved the binding.
func (r Result) Changed() bool { return r.Err == nil && r.To != r.From }

// Refresh asks about every pull request binding on bs, in recorded order.
//
// Every binding produces a Result, including the ones that failed, because
// "wf could not tell" is a thing a human running this needs to see. Nothing
// here mutates bs: applying is Apply's job, and separating them is what lets
// the lookups run outside the ledger lock.
func Refresh(ctx context.Context, c Client, bs wf.Bindings) []Result {
	prs := bs.ByKind(wf.KindPR)
	if len(prs) == 0 {
		return nil
	}
	out := make([]Result, 0, len(prs))
	for _, b := range prs {
		r := Result{Ref: b.Ref, From: b.State, To: b.State}
		status, err := c.Status(ctx, b.Ref)
		if err != nil {
			r.Err = err
			out = append(out, r)
			continue
		}
		r.To = status.State
		r.Title = status.Title
		out = append(out, r)
	}
	return out
}

// Apply writes successful lookups onto a record, matching by kind and ref,
// and reports how many bindings it actually altered.
//
// Failed lookups are skipped rather than written as BindingUnknown: a
// binding that was live and could not be checked is still the last thing wf
// actually saw, and overwriting it with "unknown" would lose evidence in
// exchange for nothing.
func Apply(rec *wf.Record, results []Result) int {
	changed := 0
	for _, r := range results {
		if r.Err != nil {
			continue
		}
		for i := range rec.Bindings {
			b := &rec.Bindings[i]
			if b.Kind != wf.KindPR || b.Ref != r.Ref {
				continue
			}
			if b.State != r.To {
				b.State = r.To
				changed++
			}
			// The title is GitHub's, and it moves when someone edits the
			// PR. The label is display text, so taking the fresh one costs
			// nothing and keeps `wf show` from naming a PR by whatever it
			// was called on the day a run opened it.
			if r.Title != "" && b.Label != r.Title {
				b.Label = r.Title
				changed++
			}
		}
	}
	return changed
}

// errUnchanged aborts the write when a refresh found nothing new. A nightly
// sweep over a ledger of finished tasks would otherwise rewrite every record
// it touched — restamping Updated, and rewriting files whose content did not
// change — to record that nothing happened. Store.Update documents exactly
// this use for a caller's own sentinel.
var errUnchanged = errors.New("no pull request state changed")

// RefreshRecord refreshes one task's pull requests and persists the result.
//
// It returns the record as it stands after the write, so a caller can report
// what the task now looks like — including whether every PR has merged —
// without a second load racing the one this just did.
func RefreshRecord(ctx context.Context, c Client, st store.Store, id string) (wf.Record, []Result, error) {
	if st == nil {
		return wf.Record{}, nil, errors.New("refresh: no ledger configured")
	}
	rec, err := st.Load(id)
	if err != nil {
		return wf.Record{}, nil, err
	}

	results := Refresh(ctx, c, rec.Bindings)
	if len(results) == 0 {
		return rec, nil, nil
	}

	// Re-read under the lock rather than saving the copy loaded above: a
	// dispatch that opened a second PR while gh was being asked about the
	// first would otherwise be erased by this write.
	var updated wf.Record
	err = st.Update(id, func(r *wf.Record) error {
		n := Apply(r, results)
		updated = *r
		if n == 0 {
			return errUnchanged
		}
		return nil
	})
	if err != nil && !errors.Is(err, errUnchanged) {
		return rec, results, fmt.Errorf("record pull request state on %s: %w", id, err)
	}
	return updated, results, nil
}
