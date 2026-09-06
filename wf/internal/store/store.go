// Package store is wf's local task ledger.
//
// A binding points at something that lives on one machine — a checkout, a
// session file, a browser origin — and is exactly as durable as the thing it
// points at. That is why the ledger is local and the tracker is not: a
// worktree path is meaningless on another host, so co-locating the record
// with the artifact is correctness rather than convenience. The tracker keeps
// title, priority and open/closed; wf keeps provenance and everything
// machine-local. See DESIGN-task.md.
//
// This is review.json grown up. That file is already a local per-ref binding
// ledger written beside wf's config, and it set the tolerance this package
// inherits: a missing ledger is an empty store, never an error, because a
// lost ledger costs history and convenience but never work. Nothing in the
// run loop may take a lifecycle decision from it alone.
//
// The interface is narrow on purpose. DESIGN-task.md defers SQLite as a
// derived index rather than as the record, and that deferral is only real if
// swapping the implementation costs nothing at the call sites — so callers
// depend on Store, never on FileStore.
package store

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ssinnott/skills-n-stuff/wf/internal/wf"
)

// Store is the whole of what a ledger has to do. Deliberately five verbs and
// no transaction: anything wider would leak the file layout into callers and
// make the SQLite escape hatch a rewrite instead of a swap.
type Store interface {
	// Load returns one record by wf id. A record that is not there is a
	// *NotFoundError — an answer, since work with no ledger entry is
	// ordinary — rather than a failure.
	Load(id string) (wf.Record, error)
	// Save writes a record whole, stamping Updated. The id is the
	// caller's; this package never mints one.
	Save(rec wf.Record) error
	// Update is read-modify-write on one record, which Load plus Save
	// cannot be: the gap between them is a lost update, and recording a
	// run's bindings is inherently load-mutate-save. fn receives the
	// record to mutate in place; returning an error from it abandons the
	// write and hands that error back unwrapped, so a caller can signal
	// "nothing to do" with a sentinel of its own.
	//
	// It is an upsert. A record that is not there yet arrives as a zero
	// Record with its ID and Created filled in, because the alternative —
	// load, notice the absence, save — is the same race in a different
	// shape. A caller that means "only if it exists" tests the record it
	// was handed and returns an error.
	//
	// What it guarantees: within this process, no two Updates and no
	// Update and Save interleave on the same id. What it does not: any
	// ordering against another process. See the lock comment in file.go —
	// two `wf` invocations share no mutex, and the honest failure there is
	// a lost update, never a corrupt record.
	Update(id string, fn func(*wf.Record) error) error
	// List returns every readable record. One that cannot be read is
	// skipped and named in a *SkipError, so losing a single record never
	// costs the others — see that type for why the error rides along with
	// the results instead of replacing them.
	List() ([]wf.Record, error)
	// Delete removes a record. Removing one that is already gone is not an
	// error: deleting twice must not fail the second time.
	Delete(id string) error
	// Resolve finds a record by wf id, handle, or the ref of any queue
	// binding — the ref forms a human actually types. Ambiguity is an
	// error naming the candidates, never a pick.
	Resolve(ref string) (wf.Record, error)
}

// ErrNotFound is what a missing record unwraps to, so callers test with
// errors.Is instead of matching on message text.
var ErrNotFound = errors.New("task not found")

// ErrInvalidID rejects an id that cannot safely name a file. The store mints
// no ids, but it does refuse to let one walk out of its own directory.
var ErrInvalidID = errors.New("invalid task id")

// NotFoundError names the ref that found nothing, which is the part a human
// reading the message needs.
type NotFoundError struct{ Ref string }

func (e *NotFoundError) Error() string { return fmt.Sprintf("task %q: %s", e.Ref, ErrNotFound) }

// Unwrap makes errors.Is(err, ErrNotFound) work.
func (e *NotFoundError) Unwrap() error { return ErrNotFound }

// AmbiguousError reports a ref that names more than one task. It carries the
// candidates rather than just a count because "which did you mean" is
// unanswerable without them, and the alternative — picking one — silently
// attaches a run to the wrong piece of work.
type AmbiguousError struct {
	Ref string
	// Candidates identify the matches in listed order, each as an id with
	// its handle when it has one.
	Candidates []string
}

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("task %q is ambiguous: matches %s", e.Ref, strings.Join(e.Candidates, ", "))
}

// SkippedFile is one ledger entry that could not be read, and why.
type SkippedFile struct {
	Path string
	Err  error
}

// SkipError reports the entries List could not read. It is returned
// alongside the records that did read, which is unusual for Go and
// deliberate: the requirement is that one corrupt file must not cost the
// others, so discarding good records in order to report a bad one would
// defeat the point. A caller that wants tolerance uses the records and
// ignores the error; a caller that wants to complain has the filenames.
type SkipError struct{ Skipped []SkippedFile }

func (e *SkipError) Error() string {
	parts := make([]string, 0, len(e.Skipped))
	for _, s := range e.Skipped {
		parts = append(parts, fmt.Sprintf("%s: %v", s.Path, s.Err))
	}
	return fmt.Sprintf("skipped %d unreadable task file(s): %s", len(e.Skipped), strings.Join(parts, "; "))
}

// Paths lists the files that were skipped, for a caller that wants to name
// them without formatting the whole error.
func (e *SkipError) Paths() []string {
	out := make([]string, 0, len(e.Skipped))
	for _, s := range e.Skipped {
		out = append(out, s.Path)
	}
	return out
}
